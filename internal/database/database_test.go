package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndEnablesSQLiteSafety(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "confighub.db"))
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
	if len(migrations) != 1 || migrations[0].name != "001_initial.sql" || migrations[0].version != 1 {
		t.Fatalf("migrations = %#v", migrations)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "confighub.db")
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
	if _, err := db.Exec("DELETE FROM revisions WHERE id = 'r2'"); err != nil {
		t.Fatalf("deleting non-current revision: %v", err)
	}
	assertRowCount(t, store, "SELECT count(*) FROM revisions WHERE id = 'r2'", 0)

	if _, err := db.Exec("UPDATE environments SET project_id = 'p2' WHERE id = 'e1'"); err == nil {
		t.Fatal("moving environment with a grant to another project succeeded")
	}
	assertRowCount(t, store, "SELECT count(*) FROM environments WHERE id = 'e1' AND project_id = 'p1'", 1)
	assertRowCount(t, store, "SELECT count(*) FROM machine_grants WHERE identity_id = 'm1' AND project_id = 'p1' AND environment_id = 'e1'", 1)
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

func TestOpenHandlesURISpecialCharactersInPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config hub ? #.db")
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
	store, err := Open(filepath.Join(t.TempDir(), "confighub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
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
