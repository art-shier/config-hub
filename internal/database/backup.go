package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

const (
	backupTempPattern       = ".confighub-backup-*.tmp"
	backupStepPages         = 128
	backupStepRetryWindow   = 5 * time.Second
	backupStepRetryInterval = 25 * time.Millisecond
)

type sqliteBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

type backupStepper interface {
	Step(int32) (bool, error)
}

type sqliteCodedError interface {
	error
	Code() int
}

var publishBackup = renameNoReplace

// Backup creates a consistent SQLite backup and atomically publishes it at
// destination. An existing destination is never overwritten.
func Backup(ctx context.Context, source *sql.DB, destination string) error {
	if ctx == nil {
		return errors.New("backup context is nil")
	}
	if source == nil {
		return errors.New("backup source is nil")
	}
	if destination == "" {
		return errors.New("backup destination is empty")
	}

	finalPath, err := filepath.Abs(destination)
	if err != nil {
		return errors.New("resolve backup destination")
	}
	if _, err := os.Lstat(finalPath); err == nil {
		return errors.New("backup destination already exists")
	} else if !os.IsNotExist(err) {
		return errors.New("inspect backup destination")
	}
	directory := filepath.Dir(finalPath)
	if err := prepareBackupDirectory(directory); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, backupTempPattern)
	if err != nil {
		return errors.New("create temporary backup")
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return errors.New("close temporary backup")
	}
	published := false
	defer func() {
		if !published {
			for _, path := range []string{temporaryPath, temporaryPath + "-wal", temporaryPath + "-shm", temporaryPath + "-journal"} {
				_ = os.Remove(path)
			}
		}
	}()

	if err := copyOnline(ctx, source, temporaryPath); err != nil {
		return err
	}
	if err := normalizeAndVerifyBackup(ctx, temporaryPath); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return errors.New("restrict backup permissions")
	}
	if err := syncFile(temporaryPath); err != nil {
		return err
	}
	if err := syncDirectory(directory); err != nil {
		return err
	}
	if err := publishBackup(temporaryPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("backup destination already exists")
		}
		return errors.New("publish backup")
	}
	published = true
	if err := syncDirectory(directory); err != nil {
		return errors.New("sync published backup directory")
	}
	return nil
}

// OpenReadOnly opens an existing SQLite database without migrating or
// otherwise modifying it.
func OpenReadOnly(path string) (*sql.DB, error) {
	return openExistingReadOnly(path, false)
}

// OpenBackupSource opens an existing ConfigHub SQLite database without
// creating files or applying migrations. Committed WAL state remains visible.
func OpenBackupSource(path string) (*sql.DB, error) {
	db, err := openExistingReadOnly(path, true)
	if err != nil {
		return nil, err
	}
	var appliedMigrations int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version >= 1`).Scan(&appliedMigrations); err != nil || appliedMigrations < 1 {
		_ = db.Close()
		return nil, errors.New("backup source is not a ConfigHub database")
	}
	var coreTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('users', 'projects', 'environments', 'revisions')`).Scan(&coreTables); err != nil || coreTables < 1 {
		_ = db.Close()
		return nil, errors.New("backup source is not a ConfigHub database")
	}
	return db, nil
}

func openExistingReadOnly(path string, rejectEmpty bool) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("read-only database path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("resolve read-only database path")
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, errors.New("read-only database is unavailable")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("read-only database is not a regular file")
	}
	if rejectEmpty && info.Size() == 0 {
		return nil, errors.New("backup source is empty")
	}

	dsn := sqliteFileDSN(absPath, true)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, errors.New("open read-only database")
	}
	var schemaVersion int
	if err := db.QueryRow(`PRAGMA schema_version`).Scan(&schemaVersion); err != nil {
		_ = db.Close()
		return nil, errors.New("validate read-only database")
	}
	return db, nil
}

func prepareBackupDirectory(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return errors.New("create backup directory")
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("inspect backup directory")
	}
	if info.Mode().Perm()&0o077 != 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("backup directory permissions are unsafe")
	}
	directoryFD, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("verify backup directory")
	}
	defer unix.Close(directoryFD)
	var stat unix.Stat_t
	if err := unix.Fstat(directoryFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&(unix.S_IRWXG|unix.S_IRWXO|unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0 {
		return errors.New("verify backup directory")
	}
	return nil
}

func copyOnline(ctx context.Context, source *sql.DB, destination string) error {
	conn, err := source.Conn(ctx)
	if err != nil {
		return safeBackupError("acquire backup connection", err)
	}
	defer conn.Close()

	err = conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(sqliteBackuper)
		if !ok {
			return errors.New("SQLite online backup is unsupported")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		if err := stepOnlineBackup(ctx, backup, backupStepRetryWindow, backupStepRetryInterval); err != nil {
			return err
		}
		finished = true
		return backup.Finish()
	})
	if err != nil {
		return safeBackupError("create online backup", err)
	}
	return nil
}

func stepOnlineBackup(ctx context.Context, backup backupStepper, retryWindow, retryInterval time.Duration) error {
	var retryDeadline time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		more, err := backup.Step(backupStepPages)
		if err == nil {
			if !more {
				return nil
			}
			continue
		}
		if !isSQLiteBusyOrLocked(err) {
			return err
		}
		if retryDeadline.IsZero() {
			retryDeadline = time.Now().Add(retryWindow)
		}
		remaining := time.Until(retryDeadline)
		if remaining <= 0 {
			return err
		}
		delay := retryInterval
		if delay <= 0 || delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func isSQLiteBusyOrLocked(err error) bool {
	var sqliteErr sqliteCodedError
	if !errors.As(err, &sqliteErr) {
		return false
	}
	switch sqliteErr.Code() & 0xff {
	case 5, 6:
		return true
	default:
		return false
	}
}

func normalizeAndVerifyBackup(ctx context.Context, path string) error {
	db, err := sql.Open(driverName, sqliteFileDSN(path, false))
	if err != nil {
		return errors.New("open backup for verification")
	}
	defer db.Close()
	var journalMode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&journalMode); err != nil {
		return safeBackupError("normalize backup journal", err)
	}
	if journalMode != "delete" {
		return errors.New("normalize backup journal")
	}
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return safeBackupError("check backup integrity", err)
	}
	if result != "ok" {
		return errors.New("backup integrity check failed")
	}
	return nil
}

func syncFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return errors.New("open backup for synchronization")
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return errors.New("synchronize backup")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("open backup directory for synchronization")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("synchronize backup directory")
	}
	return nil
}

func renameNoReplace(oldPath, newPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
}

func safeBackupError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return errors.New(operation)
}

func sqliteFileDSN(path string, readOnly bool) string {
	u := &url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	if readOnly {
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(ON)")
	}
	u.RawQuery = query.Encode()
	return u.String()
}
