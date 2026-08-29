package administration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

func TestServiceReturnsDeterministicSafeAdministrationState(t *testing.T) {
	store := openAdministrationStore(t)
	lastSync := time.Date(2026, time.August, 29, 9, 30, 0, 0, time.UTC)
	status := testLifecycleStatus{live: true, ready: true, lastSync: lastSync}
	service := NewService(store, status, "v0.15.0")
	actor := auth.User{ID: "admin-id", Role: "member", Enabled: false}

	register, err := service.ListUsers(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if len(register.Users) != 3 || register.Users[0].Username != "admin" || register.Users[1].Username != "developer-a" || register.Users[2].Username != "operator-z" {
		t.Fatalf("users=%+v, want username order", register.Users)
	}
	if !register.LastSuccessfulUserSyncAt.Equal(lastSync) {
		t.Fatalf("last sync=%s, want %s", register.LastSuccessfulUserSyncAt, lastSync)
	}
	if register.Users[1].DisplayName != "开发者 A 🚀" || register.Users[1].Role != "member" || !register.Users[1].Enabled || !register.Users[1].UpdatedAt.Equal(time.Unix(30, 0).UTC()) {
		t.Fatalf("developer status=%+v", register.Users[1])
	}
	if register.Users[2].Enabled {
		t.Fatalf("disabled user reported enabled: %+v", register.Users[2])
	}

	system, err := service.System(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if system.BuildVersion != "v0.15.0" || !system.Live || !system.Ready || !system.SQLiteReady || !system.LastSuccessfulUserSyncAt.Equal(lastSync) {
		t.Fatalf("system=%+v", system)
	}
}

func TestServiceRechecksCurrentAdminRoleAndDatabaseAvailability(t *testing.T) {
	store := openAdministrationStore(t)
	service := NewService(store, testLifecycleStatus{live: true, ready: true}, "dev")
	elevatedClientActor := auth.User{ID: "member-id", Role: "admin", Enabled: true}

	if _, err := service.ListUsers(context.Background(), elevatedClientActor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListUsers error=%v, want ErrForbidden", err)
	}
	if _, err := service.System(context.Background(), elevatedClientActor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("System error=%v, want ErrForbidden", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.System(context.Background(), auth.User{ID: "admin-id"}); err == nil {
		t.Fatal("System succeeded after SQLite closed")
	}
}

type testLifecycleStatus struct {
	live, ready bool
	lastSync    time.Time
}

func (s testLifecycleStatus) Live() bool                          { return s.live }
func (s testLifecycleStatus) Ready() bool                         { return s.ready }
func (s testLifecycleStatus) LastSuccessfulUserSyncAt() time.Time { return s.lastSync }

func openAdministrationStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "database", "administration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, statement := range []string{
		`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('member-id', 'developer-a', '开发者 A 🚀', 'secret-hash-a', 'member', 1, 1, 30)`,
		`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('disabled-id', 'operator-z', 'Operator Z', 'secret-hash-z', 'member', 0, 1, 20)`,
		`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES ('admin-id', 'admin', 'Administrator', 'secret-admin-hash', 'admin', 1, 1, 10)`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return store
}
