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

	"modernc.org/sqlite"
)

const (
	backupTempPrefix        = ".confighub-backup-"
	backupTempSuffix        = ".tmp"
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

type backupHooks struct {
	beforePublish func(string) error
	afterPublish  func() error
}

// Backup creates a consistent SQLite backup and atomically publishes it at
// destination. An existing destination is never overwritten.
func Backup(ctx context.Context, source *sql.DB, destination string) error {
	return backupWithHooks(ctx, source, destination, backupHooks{})
}

func backupWithHooks(ctx context.Context, source *sql.DB, destination string, hooks backupHooks) error {
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
	finalName := filepath.Base(finalPath)
	if !isBackupBasename(finalName) {
		return errors.New("invalid backup destination")
	}
	directory, err := openBackupDirectory(filepath.Dir(finalPath), true)
	if err != nil {
		return err
	}
	defer directory.close()

	if !directory.matchesRequestedPath() {
		return errors.New("backup directory identity changed")
	}
	if err := directory.ensureNameAbsent(finalName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("backup destination already exists")
		}
		return errors.New("inspect backup destination")
	}
	temporaryName, temporaryPath, err := directory.createTemporary()
	if err != nil {
		return err
	}
	published, completed := false, false
	defer func() {
		if completed {
			return
		}
		directory.cleanupTemporary(temporaryName)
		if published {
			_ = directory.unlink(finalName)
		}
		_ = directory.sync()
	}()

	if err := copyOnline(ctx, source, temporaryPath); err != nil {
		return err
	}
	if err := normalizeAndVerifyBackup(ctx, temporaryPath); err != nil {
		return err
	}
	if err := directory.cleanupTemporarySidecars(temporaryName); err != nil {
		return errors.New("clean backup sidecars")
	}
	if err := directory.syncFile(temporaryName); err != nil {
		return err
	}
	if err := directory.sync(); err != nil {
		return err
	}
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(temporaryPath); err != nil {
			return errors.New("run backup publication hook")
		}
	}
	if !directory.matchesRequestedPath() {
		return errors.New("backup directory identity changed")
	}
	if err := directory.publish(temporaryName, finalName); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("backup destination already exists")
		}
		return errors.New("publish backup")
	}
	published = true
	if hooks.afterPublish != nil {
		if err := hooks.afterPublish(); err != nil {
			return errors.New("run post-publication hook")
		}
	}
	if !directory.matchesRequestedPath() {
		return errors.New("backup directory identity changed after publication")
	}
	if err := directory.sync(); err != nil {
		return errors.New("sync published backup directory")
	}
	completed = true
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
	var coreTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name IN (
			'schema_migrations',
			'users',
			'sessions',
			'projects',
			'project_members',
			'environments',
			'revisions',
			'revision_entries',
			'machine_identities',
			'machine_grants',
			'access_tokens'
		)`).Scan(&coreTables); err != nil || coreTables != 11 {
		_ = db.Close()
		return nil, errors.New("backup source is not a ConfigHub database")
	}
	var migrationOneApplied int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version = 1`).Scan(&migrationOneApplied); err != nil || migrationOneApplied != 1 {
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
