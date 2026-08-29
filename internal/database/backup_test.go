package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	destination := filepath.Join(t.TempDir(), "backup.db")
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
	dir := t.TempDir()
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
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
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
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
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

func TestBackupRestrictsDestinationAndDirectoryPermissions(t *testing.T) {
	store := openTestStore(t)
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "backup.db")
	if err := Backup(context.Background(), store.DB(), destination); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got&0o077 != 0 || got&0o600 != 0o600 {
		t.Fatalf("backup mode=%#o, want no wider than 0600", got)
	}
	if got := dirInfo.Mode().Perm(); got&0o077 != 0 || got&0o700 != 0o700 {
		t.Fatalf("directory mode=%#o, want no wider than 0700", got)
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
