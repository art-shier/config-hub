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

	"confighub.local/migrations"

	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

func TestInReadTxDoesNotReserveWriterWhileCallbackIsBlocked(t *testing.T) {
	store := openTestStore(t)
	store.DB().SetMaxOpenConns(4)
	readerStarted := make(chan struct{})
	releaseReader := make(chan struct{})
	readerResult := make(chan error, 1)
	go func() {
		readerResult <- store.InReadTx(context.Background(), func(tx *sql.Tx) error {
			var one int
			if err := tx.QueryRow(`SELECT 1`).Scan(&one); err != nil {
				return err
			}
			close(readerStarted)
			<-releaseReader
			return nil
		})
	}()
	<-readerStarted

	writerResult := make(chan error, 1)
	go func() {
		writerResult <- store.InTx(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at)
				VALUES ('read-tx-writer', 'read-tx-writer', 1, 1, 1)`)
			return err
		})
	}()
	select {
	case err := <-writerResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer was blocked by read-only transaction")
	}
	close(releaseReader)
	if err := <-readerResult; err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, store, `SELECT count(*) FROM machine_identities WHERE id = 'read-tx-writer'`, 1)
}

func TestInReadTxPropagatesCallbackErrorsAndRollsBackPanics(t *testing.T) {
	store := openTestStore(t)
	sentinel := errors.New("stop read")
	if err := store.InReadTx(context.Background(), func(*sql.Tx) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("callback error=%v", err)
	}
	if err := store.InReadTx(context.Background(), nil); err == nil {
		t.Fatal("nil callback succeeded")
	}
	const panicValue = "read transaction panic"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("panic=%v", recovered)
			}
		}()
		_ = store.InReadTx(context.Background(), func(tx *sql.Tx) error {
			var one int
			if err := tx.QueryRow(`SELECT 1`).Scan(&one); err != nil {
				return err
			}
			panic(panicValue)
		})
	}()
	if err := store.InTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at)
			VALUES ('after-read-panic', 'after-read-panic', 1, 1, 1)`)
		return err
	}); err != nil {
		t.Fatalf("writer after read panic: %v", err)
	}
}

func TestInTxClassifiesSQLiteBusyAndPreservesDriverError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "busy.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.DB().SetMaxOpenConns(1)
	if _, err := store.DB().Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	locker.SetMaxOpenConns(1)
	if _, err := locker.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	lockTx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer lockTx.Rollback()
	if _, err := lockTx.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('lock', 'lock', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}

	err = store.InTx(context.Background(), func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("error=%v", err)
	}
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != 5 {
		t.Fatalf("driver error not preserved: error=%v sqlite=%v", err, sqliteErr)
	}
}

func TestOpenMigratesAndEnablesSQLiteSafety(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "database", "confighub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var foreignKeys int
	if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d", foreignKeys)
	}
	var journalMode string
	if err := store.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q", journalMode)
	}
	var busyTimeout int
	if err := store.DB().QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d", busyTimeout)
	}
	for _, table := range []string{"schema_migrations", "users", "sessions", "projects", "project_members", "environments", "revisions", "revision_entries", "machine_identities", "machine_grants", "access_tokens"} {
		var count int
		err := store.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count != 1 {
			t.Fatalf("table %s count=%d err=%v", table, count, err)
		}
	}
}

func TestEmbeddedMigrationsAcceptsInitialMigrationName(t *testing.T) {
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 2 || migrations[0].name != "001_initial.sql" || migrations[0].version != 1 || migrations[1].name != "002_machine_writes.sql" || migrations[1].version != 2 {
		t.Fatalf("migrations = %#v", migrations)
	}
}

func TestOpenMigratesVersionOneGrantAndRevisionAttribution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "version-one.db")
	seedVersionOneDatabase(t, path)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var permission, createdBy string
	var machineCreator sql.NullString
	if err := store.DB().QueryRow(`SELECT permission FROM machine_grants
		WHERE identity_id = 'm1' AND environment_id = 'e1'`).Scan(&permission); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT created_by, created_by_machine_identity_id
		FROM revisions WHERE id = 'r1'`).Scan(&createdBy, &machineCreator); err != nil {
		t.Fatal(err)
	}
	if permission != "read" || createdBy != "u1" || machineCreator.Valid {
		t.Fatalf("permission=%q created_by=%q machine_creator_valid=%t", permission, createdBy, machineCreator.Valid)
	}
	assertRowCount(t, store, `SELECT count(*) FROM revision_entries
		WHERE revision_id = 'r1' AND key = 'VALUE' AND value = 'preserved'`, 1)
	assertRowCount(t, store, `SELECT count(*) FROM schema_migrations WHERE version = 2`, 1)
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "confighub.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	var count int
	if err := second.DB().QueryRow("SELECT count(*) FROM schema_migrations WHERE version = 1").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration version 1 count = %d", count)
	}
}

func TestReadyFailsAfterClose(t *testing.T) {
	store := openTestStore(t)
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready before Close: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Ready(context.Background()); err == nil {
		t.Fatal("Ready after Close succeeded")
	}
}

func TestInTxRollsBackCallbackErrorAndCommitsSuccess(t *testing.T) {
	store := openTestStore(t)
	errSentinel := errors.New("stop transaction")
	err := store.InTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('rolled-back', 'rolled-back', 1, 1, 1)"); err != nil {
			return err
		}
		return errSentinel
	})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("InTx error = %v, want wrapped %v", err, errSentinel)
	}
	assertRowCount(t, store, "SELECT count(*) FROM machine_identities WHERE id = 'rolled-back'", 0)

	err = store.InTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('committed', 'committed', 1, 1, 1)")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM machine_identities WHERE id = 'committed'", 1)
}

func TestInTxRollsBackAndRepnics(t *testing.T) {
	store := openTestStore(t)
	const panicValue = "transaction panic"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("panic = %v, want %q", recovered, panicValue)
			}
		}()
		_ = store.InTx(context.Background(), func(tx *sql.Tx) error {
			if _, err := tx.Exec("INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('panic', 'panic', 1, 1, 1)"); err != nil {
				return err
			}
			panic(panicValue)
		})
	}()
	assertRowCount(t, store, "SELECT count(*) FROM machine_identities WHERE id = 'panic'", 0)
}

func TestSchemaGuardsRevisionAndMachineGrantRelationships(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p2', 'p2', 'Project Two', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e2', 'p1', 'e2', 'Environment Two', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e3', 'p2', 'e3', 'Environment Three', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r2', 'e2', 1, 'u1', 1)",
		"INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('m1', 'machine-one', 1, 1, 1)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("UPDATE environments SET current_revision_id = 'r2' WHERE id = 'e1'"); err == nil {
		t.Fatal("cross-environment current revision succeeded")
	}
	if _, err := db.Exec("UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'"); err != nil {
		t.Fatalf("same-environment current revision failed: %v", err)
	}
	if _, err := db.Exec("DELETE FROM revisions WHERE id = 'r1'"); err == nil {
		t.Fatal("deleting current revision succeeded")
	}
	if _, err := db.Exec("INSERT INTO machine_grants (identity_id, project_id, environment_id) VALUES ('m1', 'p1', 'e3')"); err == nil {
		t.Fatal("cross-project machine grant succeeded")
	}
	if _, err := db.Exec("INSERT INTO machine_grants (identity_id, project_id, environment_id) VALUES ('m1', 'p1', 'e1')"); err != nil {
		t.Fatalf("same-project machine grant failed: %v", err)
	}
	if _, err := db.Exec("UPDATE machine_grants SET project_id = 'p2' WHERE identity_id = 'm1' AND project_id = 'p1' AND environment_id = 'e1'"); err == nil {
		t.Fatal("cross-project machine grant update succeeded")
	}
	if _, err := db.Exec("INSERT INTO machine_grants (identity_id, project_id, environment_id, permission) VALUES ('m1', 'p1', 'e2', 'admin')"); err == nil {
		t.Fatal("machine grant with invalid permission succeeded")
	}
	if _, err := db.Exec("INSERT INTO revisions (id, environment_id, version, created_at) VALUES ('no-creator', 'e2', 2, 1)"); err == nil {
		t.Fatal("revision without creator succeeded")
	}
	if _, err := db.Exec("INSERT INTO revisions (id, environment_id, version, created_by, created_by_machine_identity_id, created_at) VALUES ('two-creators', 'e2', 2, 'u1', 'm1', 1)"); err == nil {
		t.Fatal("revision with both creators succeeded")
	}
	if _, err := db.Exec("INSERT INTO revisions (id, environment_id, version, created_by_machine_identity_id, created_at) VALUES ('machine-creator', 'e2', 2, 'm1', 1)"); err != nil {
		t.Fatalf("machine-owned revision failed: %v", err)
	}
}

func TestSchemaPreventsReverseMutationsOfRevisionAndGrantInvariants(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p2', 'p2', 'Project Two', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e2', 'p1', 'e2', 'Environment Two', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r2', 'e2', 1, 'u1', 1)",
		"UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'",
		"INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('m1', 'machine-one', 1, 1, 1)",
		"INSERT INTO machine_grants (identity_id, project_id, environment_id) VALUES ('m1', 'p1', 'e1')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("INSERT INTO environments (id, project_id, slug, name, current_revision_id, created_at, updated_at) VALUES ('bad', 'p1', 'bad', 'Bad', 'missing', 1, 1)"); err == nil {
		t.Fatal("environment with missing current revision succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'bad'", 0)

	if _, err := db.Exec("UPDATE revisions SET environment_id = 'e2' WHERE id = 'r1'"); err == nil {
		t.Fatal("moving current revision to another environment succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND environment_id = 'e1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND current_revision_id = 'r1'", 1)

	if _, err := db.Exec("UPDATE revisions SET id = 'r1-renamed' WHERE id = 'r1'"); err == nil {
		t.Fatal("renaming current revision succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1-renamed'", 0)

	if _, err := db.Exec("INSERT OR REPLACE INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1-replacement', 'e1', 1, 'u1', 2)"); err == nil {
		t.Fatal("replacing current revision succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND environment_id = 'e1' AND version = 1", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1-replacement'", 0)

	if _, err := db.Exec("INSERT OR REPLACE INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r2', 'e2', 2, 'u1', 2)"); err == nil {
		t.Fatal("replacing a historical revision succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r2' AND environment_id = 'e2' AND version = 1", 1)
	if _, err := db.Exec("DELETE FROM revisions WHERE id = 'r2'"); err == nil {
		t.Fatal("direct non-current revision delete succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r2'", 1)

	if _, err := db.Exec("UPDATE environments SET project_id = 'p2' WHERE id = 'e1'"); err == nil {
		t.Fatal("moving environment with a grant to another project succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND project_id = 'p1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM machine_grants WHERE identity_id = 'm1' AND project_id = 'p1' AND environment_id = 'e1'", 1)
}

func TestSchemaMakesRevisionsImmutableAgainstUpdateOrReplace(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('historical', 'e1', 1, 'u1', 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('current', 'e1', 2, 'u1', 1)",
		"UPDATE environments SET current_revision_id = 'current' WHERE id = 'e1'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("UPDATE OR REPLACE revisions SET version = 2 WHERE id = 'historical'"); err == nil {
		t.Fatal("UPDATE OR REPLACE replaced the current revision")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'historical' AND environment_id = 'e1' AND version = 1", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'current' AND environment_id = 'e1' AND version = 2", 1)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND current_revision_id = 'current'", 1)

	if _, err := db.Exec("UPDATE revisions SET message = 'changed' WHERE id = 'historical'"); err == nil {
		t.Fatal("ordinary revision update succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'historical' AND message = ''", 1)
}

func TestSchemaRejectsEnvironmentReplaceConflicts(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('current', 'e1', 1, 'u1', 1)",
		"UPDATE environments SET current_revision_id = 'current' WHERE id = 'e1'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("INSERT OR REPLACE INTO environments (id, project_id, slug, name, current_revision_id, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Replacement', 'current', 2, 2)"); err == nil {
		t.Fatal("same-id environment replacement succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND current_revision_id = 'current'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'current' AND environment_id = 'e1'", 1)

	if _, err := db.Exec("INSERT OR REPLACE INTO environments (id, project_id, slug, name, current_revision_id, created_at, updated_at) VALUES ('e1-copy', 'p1', 'e1', 'Replacement', 'current', 2, 2)"); err == nil {
		t.Fatal("same-project-slug environment replacement succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1-copy'", 0)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND current_revision_id = 'current'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'current' AND environment_id = 'e1'", 1)
}

func TestSchemaRejectsEnvironmentUpdateOrReplaceIDConflict(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p2', 'p2', 'Project Two', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('source', 'p1', 'source', 'Source', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('target', 'p1', 'target', 'Target', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('target-revision', 'target', 1, 'u1', 1)",
		"UPDATE environments SET current_revision_id = 'target-revision' WHERE id = 'target'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("UPDATE OR REPLACE environments SET id = 'target', current_revision_id = 'target-revision' WHERE id = 'source'"); err == nil {
		t.Fatal("UPDATE OR REPLACE environment id conflict succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'source' AND current_revision_id IS NULL", 1)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'target' AND current_revision_id = 'target-revision'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'target-revision' AND environment_id = 'target'", 1)

	if _, err := db.Exec("UPDATE environments SET id = 'source-renamed', project_id = 'p2', slug = 'source-renamed', name = 'Source updated', updated_at = 2 WHERE id = 'source'"); err != nil {
		t.Fatalf("non-conflicting environment key update: %v", err)
	}
	if _, err := db.Exec("INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('source-revision', 'source-renamed', 1, 'u1', 1)"); err != nil {
		t.Fatalf("create source revision: %v", err)
	}
	if _, err := db.Exec("UPDATE environments SET current_revision_id = 'source-revision' WHERE id = 'source-renamed'"); err != nil {
		t.Fatalf("normal current revision switch: %v", err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'source-renamed' AND project_id = 'p2' AND slug = 'source-renamed' AND name = 'Source updated' AND current_revision_id = 'source-revision'", 1)
}

func TestSchemaRejectsEnvironmentUpdateOrReplaceSlugConflict(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('source', 'p1', 'source', 'Source', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('target', 'p1', 'target', 'Target', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('target-revision', 'target', 1, 'u1', 1)",
		"UPDATE environments SET current_revision_id = 'target-revision' WHERE id = 'target'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("UPDATE OR REPLACE environments SET slug = 'target' WHERE id = 'source'"); err == nil {
		t.Fatal("UPDATE OR REPLACE environment slug conflict succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'source' AND project_id = 'p1' AND slug = 'source'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'target' AND current_revision_id = 'target-revision'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'target-revision' AND environment_id = 'target'", 1)
}

func TestSchemaEnforcesForeignKeysAndChecks(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.DB().Exec("INSERT INTO sessions (id, user_id, token_hash, csrf_hash, expires_at, created_at) VALUES ('s1', 'missing', x'01', x'02', 1, 1)"); err == nil {
		t.Fatal("session with missing user succeeded")
	}
	if _, err := store.DB().Exec("INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('bad', 'bad-enabled', 2, 1, 1)"); err == nil {
		t.Fatal("invalid enabled value succeeded")
	}
}

func TestSchemaSealsCurrentRevisionsAndProtectsEntries(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'key', 'initial')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1' AND key = 'key' AND value = 'initial'", 1)
	if _, err := db.Exec("UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'"); err != nil {
		t.Fatalf("set initial current revision: %v", err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND sealed = 1", 1)
	if _, err := db.Exec("UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'"); err != nil {
		t.Fatalf("reassert sealed current revision: %v", err)
	}

	for _, statement := range []string{
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'new', 'new-value')",
		"UPDATE revision_entries SET value = 'changed' WHERE revision_id = 'r1' AND key = 'key'",
		"DELETE FROM revision_entries WHERE revision_id = 'r1' AND key = 'key'",
		"INSERT OR REPLACE INTO revision_entries (revision_id, key, value) VALUES ('r1', 'key', 'replaced')",
		"UPDATE OR REPLACE revision_entries SET value = 'replaced' WHERE revision_id = 'r1' AND key = 'key'",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("sealed revision entry mutation succeeded: %q", statement)
		}
	}
	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1' AND key = 'key' AND value = 'initial'", 1)

	for _, statement := range []string{
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r2', 'e1', 2, 'u1', 2)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r2', 'key', 'second')",
		"UPDATE environments SET current_revision_id = 'r2' WHERE id = 'e1'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create and publish second revision %q: %v", statement, err)
		}
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id IN ('r1', 'r2') AND sealed = 1", 2)
	if _, err := db.Exec("UPDATE revision_entries SET value = 'changed-again' WHERE revision_id = 'r1' AND key = 'key'"); err == nil {
		t.Fatal("historical sealed revision entry update succeeded")
	}
	if _, err := db.Exec("DELETE FROM revisions WHERE id = 'r1'"); err == nil {
		t.Fatal("direct historical revision delete succeeded")
	}
	if _, err := db.Exec("DELETE FROM revisions WHERE id = 'r2'"); err == nil {
		t.Fatal("direct current revision delete succeeded")
	}
	for _, statement := range []string{
		"UPDATE revisions SET sealed = 0 WHERE id = 'r1'",
		"UPDATE revisions SET message = 'changed' WHERE id = 'r1'",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("sealed revision mutation succeeded: %q", statement)
		}
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND sealed = 1 AND message = ''", 1)
}

func TestSchemaAllowsRevisionCascadesAfterEnvironmentAndProjectDeletion(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'key', 'value')",
		"UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}
	if _, err := db.Exec("DELETE FROM environments WHERE id = 'e1'"); err != nil {
		t.Fatalf("delete environment with revision cascade: %v", err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1'", 0)
	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1'", 0)

	for _, statement := range []string{
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e2', 'p1', 'e2', 'Environment Two', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r2', 'e2', 1, 'u1', 1)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r2', 'key', 'value')",
		"UPDATE environments SET current_revision_id = 'r2' WHERE id = 'e2'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("second setup %q: %v", statement, err)
		}
	}
	if _, err := db.Exec("DELETE FROM projects WHERE id = 'p1'"); err != nil {
		t.Fatalf("delete project with revision cascade: %v", err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e2'", 0)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r2'", 0)
	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r2'", 0)
}

func TestSchemaRejectsProjectReplaceAndPreservesSealedHistory(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'key', 'value')",
		"UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	if _, err := db.Exec("INSERT OR REPLACE INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Replacement', 'u1', 2, 2)"); err == nil {
		t.Fatal("project replacement succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM projects WHERE id = 'p1' AND slug = 'p1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND project_id = 'p1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND environment_id = 'e1' AND sealed = 1", 1)
	assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1' AND key = 'key' AND value = 'value'", 1)
}

func TestSchemaRejectsProjectUpdateOrReplaceConflicts(t *testing.T) {
	for _, conflict := range []struct {
		name      string
		statement string
	}{
		{"id", "UPDATE OR REPLACE projects SET id = 'target' WHERE id = 'source'"},
		{"slug", "UPDATE OR REPLACE projects SET slug = 'target' WHERE id = 'source'"},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			store := openTestStore(t)
			db := store.DB()
			for _, statement := range []string{
				"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
				"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('source', 'source', 'Source', 'u1', 1, 1)",
				"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('target', 'target', 'Target', 'u1', 1, 1)",
				"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'target', 'e1', 'Environment One', 1, 1)",
				"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
				"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'key', 'value')",
				"UPDATE environments SET current_revision_id = 'r1' WHERE id = 'e1'",
			} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatalf("setup %q: %v", statement, err)
				}
			}

			if _, err := db.Exec(conflict.statement); err == nil {
				t.Fatalf("project %s conflict replacement succeeded", conflict.name)
			}
			assertRowCount(t, store, "SELECT count(*) FROM projects WHERE id = 'source' AND slug = 'source'", 1)
			assertRowCount(t, store, "SELECT count(*) FROM projects WHERE id = 'target' AND slug = 'target'", 1)
			assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND project_id = 'target'", 1)
			assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r1' AND environment_id = 'e1' AND sealed = 1", 1)
			assertRowCount(t, store, "SELECT count(*) FROM revision_entries WHERE revision_id = 'r1'", 1)
		})
	}
}

func TestSchemaRejectsNullSingleColumnPrimaryKeys(t *testing.T) {
	store := openTestStore(t)
	db := store.DB()
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('m1', 'machine-one', 1, 1, 1)",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup %q: %v", statement, err)
		}
	}

	for _, test := range []struct {
		table     string
		statement string
	}{
		{"users", "INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (NULL, 'u2', 'User Two', 'hash', 'member', 1, 1, 1)"},
		{"sessions", "INSERT INTO sessions (id, user_id, token_hash, csrf_hash, expires_at, created_at) VALUES (NULL, 'u1', x'01', x'02', 1, 1)"},
		{"projects", "INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES (NULL, 'p2', 'Project Two', 'u1', 1, 1)"},
		{"environments", "INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES (NULL, 'p1', 'e2', 'Environment Two', 1, 1)"},
		{"revisions", "INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES (NULL, 'e1', 1, 'u1', 1)"},
		{"machine_identities", "INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES (NULL, 'machine-two', 1, 1, 1)"},
		{"access_tokens", "INSERT INTO access_tokens (id, identity_id, name, prefix, token_hash, expires_at, created_at) VALUES (NULL, 'm1', 'token', 'prefix', x'03', 1, 1)"},
	} {
		t.Run(test.table, func(t *testing.T) {
			if _, err := db.Exec(test.statement); err == nil {
				t.Fatalf("NULL primary key succeeded for %s", test.table)
			}
		})
	}
}

func TestOpenRestrictsDatabaseAndSidecarPermissions(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "private-parent")
	path := filepath.Join(parent, "confighub.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('m1', 'machine-one', 1, 1, 1)"); err != nil {
		t.Fatal(err)
	}
	assertDatabaseFilesPrivate(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	assertDatabaseFilesPrivate(t, path)
	assertDatabaseDirectoryPrivateOwned(t, parent)

	existingPath := filepath.Join(parent, "existing.db")
	if err := os.WriteFile(existingPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existingPath, 0o644); err != nil {
		t.Fatal(err)
	}
	existing, err := Open(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := existing.Close(); err != nil {
		t.Fatal(err)
	}
	assertDatabaseFilesPrivate(t, existingPath)
}

func TestDatabasePathStatsRequireCurrentOwnerAndExpectedFileTypes(t *testing.T) {
	uid := uint32(os.Geteuid())
	safeDirectory := unix.Stat_t{Uid: uid, Mode: unix.S_IFDIR | 0o700}
	for name, stat := range map[string]unix.Stat_t{
		"safe directory": safeDirectory,
		"wrong owner":    {Uid: uid + 1, Mode: safeDirectory.Mode},
		"group read":     {Uid: uid, Mode: unix.S_IFDIR | 0o740},
		"sticky":         {Uid: uid, Mode: unix.S_IFDIR | 0o700 | unix.S_ISVTX},
		"regular file":   {Uid: uid, Mode: unix.S_IFREG | 0o700},
	} {
		t.Run("directory "+name, func(t *testing.T) {
			want := name == "safe directory"
			if got := isSafeDatabaseDirectoryStat(&stat, uid); got != want {
				t.Fatalf("isSafeDatabaseDirectoryStat()=%v, want %v", got, want)
			}
		})
	}

	safeFile := unix.Stat_t{Uid: uid, Mode: unix.S_IFREG | 0o600}
	for name, stat := range map[string]unix.Stat_t{
		"safe file":     safeFile,
		"wrong owner":   {Uid: uid + 1, Mode: safeFile.Mode},
		"directory":     {Uid: uid, Mode: unix.S_IFDIR | 0o600},
		"symbolic link": {Uid: uid, Mode: unix.S_IFLNK | 0o777},
	} {
		t.Run("file "+name, func(t *testing.T) {
			want := name == "safe file"
			if got := isOwnedRegularDatabaseFileStat(&stat, uid); got != want {
				t.Fatalf("isOwnedRegularDatabaseFileStat()=%v, want %v", got, want)
			}
		})
	}
}

func TestOpenRejectsUnsafeExistingDatabaseDirectoryWithoutCreatingDatabase(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "unsafe-database-directory")
			if err := os.Mkdir(parent, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(parent, mode); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "confighub.db")

			store, err := Open(path)
			if store != nil {
				_ = store.Close()
			}
			if err == nil {
				t.Error("Open accepted an unsafe existing database directory")
			} else {
				assertDatabaseOpenErrorOmitsPaths(t, err, parent, path)
			}
			if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
				t.Errorf("database was created in unsafe directory: %v", statErr)
			}
			info, statErr := os.Lstat(parent)
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != mode {
				t.Errorf("unsafe directory mode changed: got=%#o want=%#o", info.Mode().Perm(), mode)
			}
		})
	}
}

func TestOpenRejectsDatabaseSymlinkWithoutChangingTarget(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "database")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "sensitive-target")
	targetContents := []byte("sensitive target contents")
	if err := os.WriteFile(target, targetContents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "confighub.db")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if store != nil {
		_ = store.Close()
	}
	if err == nil {
		t.Error("Open followed a symlinked database path")
	} else {
		assertDatabaseOpenErrorOmitsPaths(t, err, path, target)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != string(targetContents) {
		t.Fatalf("database symlink target contents changed: contents=%q err=%v", got, readErr)
	}
	targetInfo, statErr := os.Stat(target)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if targetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("database symlink target mode changed: got=%#o want=0644", targetInfo.Mode().Perm())
	}
	linkInfo, statErr := os.Lstat(path)
	if statErr != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("database symlink changed: info=%v err=%v", linkInfo, statErr)
	}
}

func TestOpenRejectsSymlinkedDatabaseDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "database-directory")
	if err := os.Symlink(target, parent); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "confighub.db")

	store, err := Open(path)
	if store != nil {
		_ = store.Close()
	}
	if err == nil {
		t.Error("Open followed a symlinked database directory")
	} else {
		assertDatabaseOpenErrorOmitsPaths(t, err, parent, target)
	}
	if _, statErr := os.Lstat(filepath.Join(target, "confighub.db")); !os.IsNotExist(statErr) {
		t.Fatalf("database was created through a symlinked directory: %v", statErr)
	}
	linkInfo, statErr := os.Lstat(parent)
	if statErr != nil || linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("database directory symlink changed: info=%v err=%v", linkInfo, statErr)
	}
}

func TestOpenHardensExistingSidecarsBeforeSQLiteFailure(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "database")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "invalid.db")
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(file, []byte("not a SQLite database"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open accepted an invalid database")
	}
	assertFilesExactlyPrivate(t, path)
	assertDatabaseFilesPrivate(t, path)
}

func TestHardenDatabaseFilesRestrictsExistingSidecars(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "database")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "confighub.db")
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := hardenDatabaseFiles(path); err != nil {
		t.Fatal(err)
	}
	assertFilesExactlyPrivate(t, path, path+"-wal", path+"-shm")
}

func TestConnectionsReceiveSafetyPragmasAndImmediateTransactions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	first, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := store.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for _, conn := range []*sql.Conn{first, second} {
		var foreignKeys, busyTimeout int
		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatal(err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || busyTimeout != 5000 || journalMode != "wal" {
			t.Fatalf("connection pragmas foreign_keys=%d busy_timeout=%d journal_mode=%q", foreignKeys, busyTimeout, journalMode)
		}
	}

	firstTx, err := first.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer firstTx.Rollback()
	if _, err := second.ExecContext(ctx, "PRAGMA busy_timeout = 25"); err != nil {
		t.Fatal(err)
	}
	if secondTx, err := second.BeginTx(ctx, nil); err == nil {
		_ = secondTx.Rollback()
		t.Fatal("second BeginTx succeeded while first immediate transaction was open")
	}
	if err := firstTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	secondTx, err := second.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("second BeginTx after rollback: %v", err)
	}
	if err := secondTx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenHandlesURISpecialCharactersInPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database", "config hub ? #.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "database", "confighub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedVersionOneDatabase(t *testing.T, path string) {
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
	for _, statement := range []string{
		"INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('u1', 'u1', 'User One', 'hash', 'admin', 1, 1, 1)",
		"INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('p1', 'p1', 'Project One', 'u1', 1, 1)",
		"INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('e1', 'p1', 'e1', 'Environment One', 1, 1)",
		"INSERT INTO revisions (id, environment_id, version, created_by, created_at) VALUES ('r1', 'e1', 1, 'u1', 1)",
		"INSERT INTO revision_entries (revision_id, key, value) VALUES ('r1', 'VALUE', 'preserved')",
		"INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('m1', 'machine-one', 1, 1, 1)",
		"INSERT INTO machine_grants (identity_id, project_id, environment_id) VALUES ('m1', 'p1', 'e1')",
		"INSERT INTO access_tokens (id, identity_id, name, prefix, token_hash, expires_at, created_at) VALUES ('t1', 'm1', 'token-one', 'prefix', x'01', 2, 1)",
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("seed %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertRowCount(t *testing.T, store *Store, query string, want int) {
	t.Helper()
	var got int
	if err := store.DB().QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

func assertDatabaseFilesPrivate(t *testing.T, path string) {
	t.Helper()
	for _, file := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s mode = %o, want no group or other bits", file, info.Mode().Perm())
		}
	}
}

func assertFilesExactlyPrivate(t *testing.T, files ...string) {
	t.Helper()
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat %s: %v", file, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", file, info.Mode().Perm())
		}
	}
}

func assertDatabaseDirectoryPrivateOwned(t *testing.T, path string) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	if !isSafeDatabaseDirectoryStat(&stat, uint32(os.Geteuid())) {
		t.Fatalf("database directory mode=%#o uid=%d, want private directory owned by uid %d", stat.Mode, stat.Uid, os.Geteuid())
	}
}

func assertDatabaseOpenErrorOmitsPaths(t *testing.T, err error, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if strings.Contains(err.Error(), path) {
			t.Fatalf("database open error disclosed a filesystem path")
		}
	}
}
