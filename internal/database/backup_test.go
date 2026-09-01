package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"confighub.local/migrations"

	"golang.org/x/sys/unix"
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

	err := backupWithHooks(context.Background(), store.DB(), destination, backupHooks{beforePublish: func(temp string) error {
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
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
		return os.WriteFile(destination, raceContents, 0o600)
	}})
	if err == nil {
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
	sourcePath := filepath.Join(filepath.Dir(dir), "database", "source.db")
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
	sourcePath := filepath.Join(filepath.Dir(dir), "database", "source.db")
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
	err = backupWithHooks(context.Background(), store.DB(), destination, backupHooks{beforePublish: func(temp string) error {
		ownedJournal = filepath.Join(dir, filepath.Base(temp)+"-journal")
		if err := os.WriteFile(temp+"-journal", []byte("owned"), 0o600); err != nil {
			t.Fatal(err)
		}
		return sentinel
	}})
	if err == nil {
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

func TestBackupRejectsAncestorSymlinkWithoutChangingTarget(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	target := filepath.Join(root, "target")
	destinationDirectory := filepath.Join(target, "nested")
	if err := os.MkdirAll(destinationDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(destinationDirectory, "keep")
	if err := os.WriteFile(neighbor, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	linkedAncestor := filepath.Join(root, "linked-ancestor")
	if err := os.Symlink(target, linkedAncestor); err != nil {
		t.Fatal(err)
	}

	err := Backup(context.Background(), store.DB(), filepath.Join(linkedAncestor, "nested", "backup.db"))
	if err == nil {
		t.Fatal("Backup followed an ancestor symlink")
	}
	if got, err := os.ReadFile(neighbor); err != nil || string(got) != "unchanged" {
		t.Fatalf("symlink target changed: contents=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destinationDirectory, "backup.db")); !os.IsNotExist(err) {
		t.Fatalf("backup created through ancestor symlink: %v", err)
	}
	assertNoBackupTemps(t, destinationDirectory)
}

func TestBackupDirectorySwapBeforePublicationDoesNotWriteReplacement(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	directory := filepath.Join(root, "backups")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	movedDirectory := filepath.Join(root, "validated-backups")
	replacementNeighbor := filepath.Join(directory, "replacement-neighbor")
	hookRan := false

	err := backupWithHooks(context.Background(), store.DB(), filepath.Join(directory, "backup.db"), backupHooks{
		beforePublish: func(string) error {
			if err := os.Rename(directory, movedDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(directory, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(replacementNeighbor, []byte("replacement"), 0o600); err != nil {
				return err
			}
			hookRan = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("Backup succeeded after the validated directory was replaced")
	}
	if !hookRan {
		t.Fatalf("directory-swap hook did not complete: %v", err)
	}
	if got, err := os.ReadFile(replacementNeighbor); err != nil || string(got) != "replacement" {
		t.Fatalf("replacement directory changed: contents=%q err=%v", got, err)
	}
	for _, dir := range []string{directory, movedDirectory} {
		if _, err := os.Stat(filepath.Join(dir, "backup.db")); !os.IsNotExist(err) {
			t.Fatalf("backup remains in %s: %v", dir, err)
		}
		assertNoBackupTemps(t, dir)
	}
}

func TestBackupDirectorySwapAfterPublicationRemovesPublishedFile(t *testing.T) {
	store := openTestStore(t)
	root := t.TempDir()
	directory := filepath.Join(root, "backups")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	movedDirectory := filepath.Join(root, "published-backups")
	hookRan := false

	err := backupWithHooks(context.Background(), store.DB(), filepath.Join(directory, "backup.db"), backupHooks{
		afterPublish: func() error {
			if err := os.Rename(directory, movedDirectory); err != nil {
				return err
			}
			if err := os.Mkdir(directory, 0o700); err != nil {
				return err
			}
			hookRan = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("Backup succeeded after the published directory was replaced")
	}
	if !hookRan {
		t.Fatalf("post-publication swap hook did not complete: %v", err)
	}
	for _, dir := range []string{directory, movedDirectory} {
		if _, err := os.Stat(filepath.Join(dir, "backup.db")); !os.IsNotExist(err) {
			t.Fatalf("published backup remains in %s: %v", dir, err)
		}
		assertNoBackupTemps(t, dir)
	}
}

func TestBackupDirectoryStatRequiresCurrentOwnerPrivateModeAndDirectory(t *testing.T) {
	safe := unix.Stat_t{Uid: uint32(os.Geteuid()), Mode: unix.S_IFDIR | 0o700}
	for name, stat := range map[string]unix.Stat_t{
		"safe":         safe,
		"wrong owner":  {Uid: safe.Uid + 1, Mode: safe.Mode},
		"group read":   {Uid: safe.Uid, Mode: unix.S_IFDIR | 0o740},
		"sticky":       {Uid: safe.Uid, Mode: unix.S_IFDIR | 0o700 | unix.S_ISVTX},
		"regular file": {Uid: safe.Uid, Mode: unix.S_IFREG | 0o700},
	} {
		t.Run(name, func(t *testing.T) {
			want := name == "safe"
			if got := isSafeBackupDirectoryStat(&stat); got != want {
				t.Fatalf("isSafeBackupDirectoryStat()=%v, want %v", got, want)
			}
		})
	}
}

func TestWalkDirectoryPathSyncsEachParentImmediatelyAfterCreatingChild(t *testing.T) {
	base := createSafeBackupDirectory(t)
	destinationDirectory := filepath.Join(base, "first", "second", "third")
	var events []string
	ops := directoryWalkOps{
		mkdirat: func(parentFD int, name string, mode uint32) error {
			parent := directoryFDPath(t, parentFD)
			events = append(events, "mkdir "+filepath.Join(parent, name))
			return unix.Mkdirat(parentFD, name, mode)
		},
		fsync: func(fd int) error {
			events = append(events, "fsync "+directoryFDPath(t, fd))
			return unix.Fsync(fd)
		},
	}

	fd, _, err := walkDirectoryPathWithOps(destinationDirectory, true, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	want := []string{
		"mkdir " + filepath.Join(base, "first"),
		"fsync " + base,
		"mkdir " + filepath.Join(base, "first", "second"),
		"fsync " + filepath.Join(base, "first"),
		"mkdir " + destinationDirectory,
		"fsync " + filepath.Join(base, "first", "second"),
	}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("directory durability events:\n%s\nwant:\n%s", strings.Join(events, "\n"), strings.Join(want, "\n"))
	}
}

func TestWalkDirectoryPathSyncsParentAfterMkdirCreationRace(t *testing.T) {
	base := createSafeBackupDirectory(t)
	destinationDirectory := filepath.Join(base, "raced")
	var events []string
	ops := directoryWalkOps{
		mkdirat: func(parentFD int, name string, mode uint32) error {
			if err := unix.Mkdirat(parentFD, name, mode); err != nil {
				return err
			}
			events = append(events, "mkdir raced")
			return unix.EEXIST
		},
		fsync: func(fd int) error {
			events = append(events, "fsync "+directoryFDPath(t, fd))
			return unix.Fsync(fd)
		},
	}

	fd, _, err := walkDirectoryPathWithOps(destinationDirectory, true, ops)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	want := []string{"mkdir raced", "fsync " + base}
	if strings.Join(events, "\n") != strings.Join(want, "\n") {
		t.Fatalf("creation-race durability events=%q, want %q", events, want)
	}
}

func TestBackupParentSyncFailureAbortsWithoutPublicationOrRecursiveCleanup(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "database", "source.db")
	store, err := Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().Exec(`INSERT INTO machine_identities
		(id, name, enabled, created_at, updated_at)
		VALUES ('parent-sync', 'unchanged source', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(root, "existing-backup.db")
	neighborContents := []byte("existing replacement")
	if err := os.WriteFile(neighbor, neighborContents, 0o600); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "new-first")
	second := filepath.Join(first, "new-second")
	destination := filepath.Join(second, "backup.db")
	sentinel := errors.New("injected parent fsync failure")
	fsyncCalls := 0

	err = backupWithHooks(context.Background(), store.DB(), destination, backupHooks{
		directoryOps: directoryWalkOps{
			fsync: func(fd int) error {
				fsyncCalls++
				if fsyncCalls == 2 {
					return sentinel
				}
				return unix.Fsync(fd)
			},
		},
	})
	if err == nil {
		t.Fatal("Backup succeeded after parent fsync failure")
	}
	if fsyncCalls != 2 {
		t.Fatalf("parent fsync calls=%d, want 2", fsyncCalls)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination exists after parent fsync failure: %v", err)
	}
	if got, err := os.ReadFile(neighbor); err != nil || string(got) != string(neighborContents) {
		t.Fatalf("existing replacement changed: contents=%q err=%v", got, err)
	}
	sourceAfter, err := os.ReadFile(sourcePath)
	if err != nil || string(sourceAfter) != string(sourceBefore) {
		t.Fatalf("source changed: err=%v", err)
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("source became unavailable: %v", err)
	}
	// Creation was successful even though its parent sync failed. The private
	// empty hierarchy is intentionally retained instead of recursively removed.
	for _, directory := range []string{first, second} {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("created directory was removed: path=%q info=%v err=%v", directory, info, err)
		}
		assertNoBackupTemps(t, directory)
	}
}

func TestBackupUsesSafeExistingAndNewDestinationDirectories(t *testing.T) {
	store := openTestStore(t)
	for name, create := range map[string]bool{"existing": true, "new": false} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "backups")
			var before unix.Stat_t
			if create {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := unix.Stat(dir, &before); err != nil {
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
			var after unix.Stat_t
			if err := unix.Stat(dir, &after); err != nil {
				t.Fatal(err)
			}
			if got := fileInfo.Mode().Perm(); got&0o077 != 0 || got&0o600 != 0o600 {
				t.Fatalf("backup mode=%#o, want no wider than 0600", got)
			}
			if got := dirInfo.Mode().Perm(); got&0o077 != 0 || got&0o700 != 0o700 {
				t.Fatalf("directory mode=%#o, want no wider than 0700", got)
			}
			if after.Uid != uint32(os.Geteuid()) {
				t.Fatalf("directory uid=%d, want current euid=%d", after.Uid, os.Geteuid())
			}
			if create && (after.Dev != before.Dev || after.Ino != before.Ino || after.Uid != before.Uid || after.Gid != before.Gid || after.Mode != before.Mode) {
				t.Fatalf("existing directory metadata changed: before=%+v after=%+v", before, after)
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
	directory := filepath.Join(dir, "source-directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "source-symlink.db")
	if err := os.Symlink(foreign, symlink); err != nil {
		t.Fatal(err)
	}

	for name, path := range map[string]string{
		"missing":      missing,
		"zero byte":    zeroByte,
		"empty SQLite": emptySQLite,
		"foreign":      foreign,
		"directory":    directory,
		"symlink":      symlink,
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

func TestOpenBackupSourceRejectsVersionOneSchemaMissingAnyCoreTableWithoutMutation(t *testing.T) {
	for _, missingTable := range migrationOneCoreTables {
		t.Run(missingTable, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "database", "partial.db")
			createVersionOneConfigHubFixture(t, sourcePath)
			db, err := sql.Open(driverName, sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF; DROP TABLE "` + missingTable + `"`); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}

			source, err := OpenBackupSource(sourcePath)
			if err == nil {
				_ = source.Close()
				t.Fatal("OpenBackupSource accepted a partial migration-001 schema")
			}
			after, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("rejected partial source was modified")
			}
		})
	}
}

func TestOpenBackupSourceRejectsForeignMigrationLedgerWithUsersWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "foreign-ledger.db")
	createSQLiteFixture(t, sourcePath, `
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, dirty INTEGER NOT NULL);
		INSERT INTO schema_migrations (version, dirty) VALUES (1, 0);
		CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL);
		INSERT INTO users (email) VALUES ('foreign@example.com');
	`)
	before, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	source, err := OpenBackupSource(sourcePath)
	if err == nil {
		_ = source.Close()
		t.Fatal("OpenBackupSource accepted a foreign migration ledger with users")
	}
	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("rejected foreign source was modified")
	}
	assertNoSQLiteSidecars(t, sourcePath)
}

func TestOpenBackupSourceBacksUpVersionOneSchemaWithoutMigratingOrMutatingIt(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "database", "version-one.db")
	createVersionOneConfigHubFixture(t, sourcePath)
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
	destination := filepath.Join(dir, "backup", "version-one.db")
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
	backup, err := OpenReadOnly(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var name string
	if err := backup.QueryRow(`SELECT name FROM machine_identities WHERE id = 'version-one-machine'`).Scan(&name); err != nil || name != "Version One Machine" {
		t.Fatalf("version-one data=%q err=%v", name, err)
	}
	var migrationCount int
	if err := backup.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 {
		t.Fatalf("migration count=%d, want exactly version 1", migrationCount)
	}
}

func TestOpenBackupSourceIncludesCommittedLiveWALData(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "database", "source.db")
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
	if stepper.calls > 10 {
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

var migrationOneCoreTables = []string{
	"users",
	"sessions",
	"projects",
	"project_members",
	"environments",
	"revisions",
	"revision_entries",
	"machine_identities",
	"machine_grants",
	"access_tokens",
}

func createVersionOneConfigHubFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := migrations.FS.ReadFile("001_initial.sql")
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(string(initial)); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (1, 1)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO machine_identities
		(id, name, description, enabled, created_at, updated_at)
		VALUES ('version-one-machine', 'Version One Machine', 'preserved', 1, 1, 1)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
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

func assertNoSQLiteSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("source sidecar %q exists: %v", suffix, err)
		}
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

func directoryFDPath(t *testing.T, fd int) string {
	t.Helper()
	path, err := os.Readlink(filepath.Join("/proc/self/fd", strconv.Itoa(fd)))
	if err != nil {
		t.Fatal(err)
	}
	return path
}
