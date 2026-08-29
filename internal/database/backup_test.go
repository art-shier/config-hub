package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupCreatesIndependentlyReadableDatabaseFromLiveWAL(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.DB().Exec(`INSERT INTO machine_identities
		(id, name, enabled, created_at, updated_at)
		VALUES ('in-wal', 'written in WAL', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var schema, sourcePath string
	if err := store.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &schema, &sourcePath); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(sourcePath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("source has no live WAL writes: info=%v err=%v", walInfo, err)
	}

	destination := filepath.Join(t.TempDir(), "backups", "backup.db")
	if err := Backup(context.Background(), store.DB(), destination); err != nil {
		t.Fatal(err)
	}
	backup, err := OpenReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var result string
	if err := backup.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity result=%q err=%v", result, err)
	}
	var count int
	if err := backup.QueryRow(`SELECT count(*) FROM machine_identities WHERE id = 'in-wal'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backed-up WAL row count=%d, want 1", count)
	}
}

func TestBackupRejectsExistingDestinationWithoutAlteringIt(t *testing.T) {
	store := openTestStore(t)
	dir := t.TempDir()
	destination := filepath.Join(dir, "backup.db")
	want := []byte("existing destination")
	if err := os.WriteFile(destination, want, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := Backup(context.Background(), store.DB(), destination); err == nil {
		t.Fatal("Backup succeeded with an existing destination")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("destination changed: got %q want %q", got, want)
	}
	assertNoBackupTemps(t, dir)
}

func TestBackupPublishesCompleteFileAtomicallyAndLosesDestinationRace(t *testing.T) {
	store := openTestStore(t)
	dir := createSafeBackupDirectory(t)
	destination := filepath.Join(dir, "backup.db")
	raceContents := []byte("created by another operation")
	originalPublish := publishBackup
	publishBackup = func(temp, final string) error {
		if _, err := os.Stat(final); !os.IsNotExist(err) {
			t.Fatalf("final destination visible before publication: %v", err)
		}
		candidate, err := OpenReadOnly(temp)
		if err != nil {
			t.Fatalf("temporary backup is not independently readable: %v", err)
		}
		var result string
		err = candidate.QueryRow(`PRAGMA integrity_check`).Scan(&result)
		_ = candidate.Close()
		if err != nil || result != "ok" {
			t.Fatalf("temporary backup incomplete: result=%q err=%v", result, err)
		}
		if err := os.WriteFile(final, raceContents, 0o600); err != nil {
			t.Fatal(err)
		}
		return originalPublish(temp, final)
	}
	t.Cleanup(func() { publishBackup = originalPublish })

	if err := Backup(context.Background(), store.DB(), destination); err == nil {
		t.Fatal("Backup overwrote a destination created during publication")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raceContents) {
		t.Fatalf("racing destination changed: got %q want %q", got, raceContents)
	}
	assertNoBackupTemps(t, dir)
}

func TestBackupFailureCleansOnlyItsTemporaryFile(t *testing.T) {
	dir := createSafeBackupDirectory(t)
	sourcePath := filepath.Join(filepath.Dir(dir), "source.db")
	store, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	destination := filepath.Join(dir, "backup.db")
	neighbor := filepath.Join(dir, ".confighub-backup-neighbor.tmp")
	if err := os.WriteFile(neighbor, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Backup(ctx, store.DB(), destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Backup error=%v, want context cancellation", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	got, err := os.ReadFile(neighbor)
	if err != nil || string(got) != "keep me" {
		t.Fatalf("neighbor changed: contents=%q err=%v", got, err)
	}
	sourceAfter, err := os.Stat(sourcePath)
	if err != nil || sourceAfter.Size() != sourceBefore.Size() {
		t.Fatalf("source changed: before=%v after=%v err=%v", sourceBefore, sourceAfter, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".confighub-backup-") && entry.Name() != filepath.Base(neighbor) {
			t.Errorf("operation temporary file left behind: %s", entry.Name())
		}
	}
}

func TestBackupPublicationFailureRemovesOwnedRollbackJournalOnly(t *testing.T) {
	dir := createSafeBackupDirectory(t)
	sourcePath := filepath.Join(filepath.Dir(dir), "source.db")
	store, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	destination := filepath.Join(dir, "backup.db")
	neighbor := filepath.Join(dir, "neighbor-journal")
	if err := os.WriteFile(neighbor, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("injected publication failure")
	var ownedJournal string
	originalPublish := publishBackup
	publishBackup = func(temp, _ string) error {
		ownedJournal = temp + "-journal"
		if err := os.WriteFile(ownedJournal, []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		return sentinel
	}
	t.Cleanup(func() { publishBackup = originalPublish })

	if err := Backup(context.Background(), store.DB(), destination); err == nil {
		t.Fatal("Backup succeeded after injected publication failure")
	}
	if ownedJournal == "" {
		t.Fatal("publication seam was not reached")
	}
	if _, err := os.Stat(ownedJournal); !os.IsNotExist(err) {
		t.Fatalf("owned rollback journal remains after failure: %v", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after failure: %v", err)
	}
	if got, err := os.ReadFile(neighbor); err != nil || string(got) != "unrelated" {
		t.Fatalf("unrelated neighbor changed: contents=%q err=%v", got, err)
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("source changed or became unavailable: %v", err)
	}
}

func TestBackupRejectsUnsafeExistingDestinationDirectoryWithoutChangingIt(t *testing.T) {
	store := openTestStore(t)
	for name, mode := range map[string]os.FileMode{
		"world readable": 0o755,
		"sticky":         0o700 | os.ModeSticky,
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "backups")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(dir, "backup.db")
			if err := Backup(context.Background(), store.DB(), destination); err == nil {
				t.Fatal("Backup succeeded with an unsafe destination directory")
			}
			after, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode() != before.Mode() {
				t.Fatalf("directory mode changed: before=%v after=%v", before.Mode(), after.Mode())
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("destination exists after rejection: %v", err)
			}
		})
	}
}

func TestBackupRejectsSymlinkDestinationDirectoryWithoutChangingTarget(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(target, "keep")
	if err := os.WriteFile(neighbor, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := Backup(context.Background(), store.DB(), filepath.Join(linked, "backup.db")); err == nil {
		t.Fatal("Backup followed a symlinked destination directory")
	}
	after, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode() != before.Mode() {
		t.Fatalf("symlink target mode changed: before=%v after=%v", before.Mode(), after.Mode())
	}
	if got, err := os.ReadFile(neighbor); err != nil || string(got) != "unchanged" {
		t.Fatalf("symlink target contents changed: contents=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "backup.db")); !os.IsNotExist(err) {
		t.Fatalf("backup created through symlink: %v", err)
	}
}

func TestBackupUsesSafeExistingAndNewDestinationDirectories(t *testing.T) {
	store := openTestStore(t)
	for name, create := range map[string]bool{"existing": true, "new": false} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "backups")
			if create {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			destination := filepath.Join(dir, "backup.db")
			if err := Backup(context.Background(), store.DB(), destination); err != nil {
				t.Fatal(err)
			}
			fileInfo, err := os.Stat(destination)
			if err != nil {
				t.Fatal(err)
			}
			dirInfo, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := fileInfo.Mode().Perm(); got&0o077 != 0 || got&0o600 != 0o600 {
				t.Fatalf("backup mode=%#o, want no wider than 0600", got)
			}
			if got := dirInfo.Mode().Perm(); got&0o077 != 0 || got&0o700 != 0o700 {
				t.Fatalf("directory mode=%#o, want no wider than 0700", got)
			}
		})
	}
}

func TestBackupRejectsInvalidInputsWithoutCreatingDestination(t *testing.T) {
	store := openTestStore(t)
	dir := t.TempDir()
	closed, err := sql.Open(driverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		source      *sql.DB
		destination string
	}{
		"nil source":        {destination: filepath.Join(dir, "nil.db")},
		"empty destination": {source: store.DB()},
		"directory target":  {source: store.DB(), destination: dir},
		"closed source":     {source: closed, destination: filepath.Join(dir, "closed.db")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Backup(context.Background(), test.source, test.destination); err == nil {
				t.Fatal("Backup succeeded")
			}
			if test.destination != "" && test.destination != dir {
				if _, err := os.Stat(test.destination); !os.IsNotExist(err) {
					t.Fatalf("destination created: %v", err)
				}
			}
		})
	}
}

func TestOpenReadOnlyRejectsMissingAndInvalidFilesWithoutCreatingThem(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if db, err := OpenReadOnly(missing); err == nil {
		_ = db.Close()
		t.Fatal("OpenReadOnly opened a missing file")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing file was created: %v", err)
	}
	invalid := filepath.Join(dir, "invalid.db")
	if err := os.WriteFile(invalid, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if db, err := OpenReadOnly(invalid); err == nil {
		_ = db.Close()
		t.Fatal("OpenReadOnly opened an invalid database")
	}
}

func TestOpenBackupSourceRejectsMissingEmptyAndForeignDatabases(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	zeroByte := filepath.Join(dir, "zero.db")
	if err := os.WriteFile(zeroByte, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	emptySQLite := filepath.Join(dir, "empty-sqlite.db")
	createSQLiteFixture(t, emptySQLite, `VACUUM`)
	foreign := filepath.Join(dir, "foreign.db")
	createSQLiteFixture(t, foreign, `CREATE TABLE unrelated (id INTEGER PRIMARY KEY)`)

	for name, path := range map[string]string{
		"missing":      missing,
		"zero byte":    zeroByte,
		"empty SQLite": emptySQLite,
		"foreign":      foreign,
	} {
		t.Run(name, func(t *testing.T) {
			db, err := OpenBackupSource(path)
			if err == nil {
				_ = db.Close()
				t.Fatal("OpenBackupSource succeeded")
			}
		})
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing source was created: %v", err)
	}
}

func TestOpenBackupSourceBacksUpOlderSchemaWithoutMigratingOrMutatingIt(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "older.db")
	createSQLiteFixture(t, sourcePath, `
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations (version, applied_at) VALUES (1, 1);
		CREATE TABLE users (id TEXT PRIMARY KEY NOT NULL, legacy_value TEXT NOT NULL);
		INSERT INTO users (id, legacy_value) VALUES ('legacy-user', 'unchanged');
	`)
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	source, err := OpenBackupSource(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "backup", "older.db")
	if err := Backup(context.Background(), source, destination); err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) || afterInfo.Size() != beforeInfo.Size() || !afterInfo.ModTime().Equal(beforeInfo.ModTime()) {
		t.Fatal("backup source file was modified")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(sourcePath + suffix); !os.IsNotExist(err) {
			t.Fatalf("source sidecar %q was created: %v", suffix, err)
		}
	}

	backup, err := OpenReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var value string
	if err := backup.QueryRow(`SELECT legacy_value FROM users WHERE id = 'legacy-user'`).Scan(&value); err != nil || value != "unchanged" {
		t.Fatalf("legacy data=%q err=%v", value, err)
	}
	var migratedTableCount int
	if err := backup.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='projects'`).Scan(&migratedTableCount); err != nil {
		t.Fatal(err)
	}
	if migratedTableCount != 0 {
		t.Fatal("older schema was migrated before backup")
	}
}

func TestOpenBackupSourceIncludesCommittedLiveWALData(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	store, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`INSERT INTO machine_identities
		(id, name, enabled, created_at, updated_at)
		VALUES ('source-opener-wal', 'source opener WAL', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	walInfo, err := os.Stat(sourcePath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("source has no live WAL: info=%v err=%v", walInfo, err)
	}

	source, err := OpenBackupSource(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	destination := filepath.Join(dir, "backup", "wal.db")
	if err := Backup(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	backup, err := OpenReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var count int
	if err := backup.QueryRow(`SELECT count(*) FROM machine_identities WHERE id='source-opener-wal'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("live WAL row count=%d err=%v", count, err)
	}
}

func TestStepOnlineBackupRetriesBusyAndLockedUntilComplete(t *testing.T) {
	stepper := &fakeBackupStepper{results: []backupStepResult{
		{err: fakeSQLiteError{code: 5}},
		{err: fakeSQLiteError{code: 6}},
		{more: true},
		{more: false},
	}}

	if err := stepOnlineBackup(context.Background(), stepper, 100*time.Millisecond, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if stepper.calls != 4 {
		t.Fatalf("Step calls=%d, want 4", stepper.calls)
	}
}

func TestStepOnlineBackupReturnsNonRetryableErrorImmediately(t *testing.T) {
	sentinel := errors.New("non-retryable")
	stepper := &fakeBackupStepper{results: []backupStepResult{{err: sentinel}}}

	err := stepOnlineBackup(context.Background(), stepper, time.Second, 200*time.Millisecond)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error=%v, want %v", err, sentinel)
	}
	if stepper.calls != 1 {
		t.Fatalf("Step calls=%d, want 1", stepper.calls)
	}
}

func TestStepOnlineBackupPersistentBusyHonorsContextWithoutSpinning(t *testing.T) {
	stepper := &fakeBackupStepper{persistentErr: fakeSQLiteError{code: 5}}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	started := time.Now()

	err := stepOnlineBackup(ctx, stepper, time.Second, 10*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("cancellation took %v, want at most 500ms", elapsed)
	}
	if stepper.calls < 2 || stepper.calls > 10 {
		t.Fatalf("Step calls=%d, want bounded retries without busy-spinning", stepper.calls)
	}
}

func TestStepOnlineBackupPersistentLockedStopsAtRetryWindow(t *testing.T) {
	want := fakeSQLiteError{code: 6}
	stepper := &fakeBackupStepper{persistentErr: want}
	started := time.Now()

	err := stepOnlineBackup(context.Background(), stepper, 25*time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, want) {
		t.Fatalf("error=%v, want SQLite locked error", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("retry window took %v, want at most 500ms", elapsed)
	}
	if stepper.calls < 2 || stepper.calls > 10 {
		t.Fatalf("Step calls=%d, want bounded retries", stepper.calls)
	}
}

func TestBackupPersistentExclusiveContentionHonorsContextAndCleansArtifacts(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	createSQLiteFixture(t, sourcePath, `
		PRAGMA journal_mode=DELETE;
		CREATE TABLE values_to_backup (id INTEGER PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO values_to_backup (value) VALUES ('committed');
	`)
	source, err := OpenReadOnly(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	locker, err := sql.Open(driverName, sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	lockConn, err := locker.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockConn.Close()
	if _, err := lockConn.ExecContext(context.Background(), `PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := lockConn.ExecContext(context.Background(), `BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer lockConn.ExecContext(context.Background(), `ROLLBACK`)
	if _, err := lockConn.ExecContext(context.Background(), `INSERT INTO values_to_backup (value) VALUES ('uncommitted')`); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(dir, "backups")
	destination := filepath.Join(backupDir, "backup.db")
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err = Backup(ctx, source, destination)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Backup error=%v, want context deadline exceeded", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after contention: %v", err)
	}
	assertNoBackupTemps(t, backupDir)
}

type backupStepResult struct {
	more bool
	err  error
}

type fakeBackupStepper struct {
	results       []backupStepResult
	persistentErr error
	calls         int
}

func (f *fakeBackupStepper) Step(int32) (bool, error) {
	f.calls++
	if len(f.results) == 0 {
		return false, f.persistentErr
	}
	result := f.results[0]
	f.results = f.results[1:]
	return result.more, result.err
}

type fakeSQLiteError struct {
	code int
}

func (e fakeSQLiteError) Error() string {
	return "fake SQLite contention"
}

func (e fakeSQLiteError) Code() int {
	return e.code
}

func createSQLiteFixture(t *testing.T, path, statements string) {
	t.Helper()
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statements); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func createSafeBackupDirectory(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func assertNoBackupTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".confighub-backup-") {
			t.Errorf("temporary backup left behind: %s", entry.Name())
		}
	}
}
