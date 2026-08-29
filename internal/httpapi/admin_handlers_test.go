package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"confighub.local/internal/administration"
	"confighub.local/internal/auth"
	"confighub.local/internal/database"
)

func TestAdministrationEndpointsRequireCurrentAdminAndReturnOnlySafeFields(t *testing.T) {
	fixture := newAdministrationHTTPFixture(t)

	response := fixture.serve(httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	assertAdministrationError(t, response, http.StatusUnauthorized, "invalid_session")
	response = fixture.get("member", "/api/v1/users")
	assertAdministrationError(t, response, http.StatusForbidden, "forbidden")
	response = fixture.get("member", "/api/v1/system")
	assertAdministrationError(t, response, http.StatusForbidden, "forbidden")

	response = fixture.get("admin", "/api/v1/users")
	if response.Code != http.StatusOK {
		t.Fatalf("users status=%d body=%s", response.Code, response.Body.String())
	}
	var register administration.UserRegister
	if err := json.Unmarshal(response.Body.Bytes(), &register); err != nil {
		t.Fatal(err)
	}
	if len(register.Users) != 2 || register.Users[0].Username != "admin" || register.Users[1].Username != "member" {
		t.Fatalf("users=%+v", register.Users)
	}
	usersBody := response.Body.String()
	for _, forbidden := range []string{"password", "secret-hash", "session", fixture.databasePath} {
		if strings.Contains(usersBody, forbidden) {
			t.Fatalf("users response leaked %q: %s", forbidden, usersBody)
		}
	}

	response = fixture.get("admin", "/api/v1/system")
	if response.Code != http.StatusOK {
		t.Fatalf("system status=%d body=%s", response.Code, response.Body.String())
	}
	var system administration.SystemStatus
	if err := json.Unmarshal(response.Body.Bytes(), &system); err != nil {
		t.Fatal(err)
	}
	if system.BuildVersion != "test-build" || !system.Live || !system.Ready || !system.SQLiteReady || !system.LastSuccessfulUserSyncAt.Equal(fixture.lastSync) {
		t.Fatalf("system=%+v", system)
	}
	systemBody := response.Body.String()
	for _, forbidden := range []string{fixture.databasePath, "users.yaml", "secret-hash", "password"} {
		if strings.Contains(systemBody, forbidden) {
			t.Fatalf("system response leaked %q: %s", forbidden, systemBody)
		}
	}
}

func TestAdministrationRoutesRejectQueriesMethodsAndBusySafely(t *testing.T) {
	fixture := newAdministrationHTTPFixture(t)

	response := fixture.get("admin", "/api/v1/users?include=passwords")
	assertAdministrationError(t, response, http.StatusBadRequest, "malformed_query")
	response = fixture.get("admin", "/api/v1/system?details=1")
	assertAdministrationError(t, response, http.StatusBadRequest, "malformed_query")
	for _, path := range []string{"/api/v1/users", "/api/v1/system"} {
		request := httptest.NewRequest(http.MethodHead, path, nil)
		request.AddCookie(fixture.cookies["admin"])
		response = fixture.serve(request)
		if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("HEAD %s status=%d Allow=%q body=%s", path, response.Code, response.Header().Get("Allow"), response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(`{}`))
	request.AddCookie(fixture.cookies["admin"])
	response = fixture.serve(request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method status=%d Allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
	}

	busyHandler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(fixture.store),
		Sessions:    fixture.sessions,
		Admin:       busyAdministrationService{},
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
	request.AddCookie(fixture.cookies["admin"])
	response = httptest.NewRecorder()
	busyHandler.ServeHTTP(response, request)
	assertAdministrationError(t, response, http.StatusServiceUnavailable, "service_unavailable")
	if strings.Contains(response.Body.String(), "locked") || strings.Contains(response.Body.String(), "SQLITE") {
		t.Fatalf("busy response leaked database details: %s", response.Body.String())
	}
}

type administrationHTTPFixture struct {
	handler      http.Handler
	store        *database.Store
	sessions     *auth.SessionManager
	cookies      map[string]*http.Cookie
	databasePath string
	lastSync     time.Time
}

func newAdministrationHTTPFixture(t *testing.T) *administrationHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "admin-http-secret-name.db")
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	users := []auth.User{
		{ID: "admin-id", Username: "admin", DisplayName: "Administrator", Role: "admin", Enabled: true},
		{ID: "member-id", Username: "member", DisplayName: "Member", Role: "member", Enabled: true},
	}
	for _, user := range users {
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (?, ?, ?, 'secret-hash', ?, 1, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role); err != nil {
			t.Fatal(err)
		}
	}
	sessions := auth.NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	cookies := make(map[string]*http.Cookie)
	for _, user := range users {
		issued, err := sessions.Create(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		cookies[user.Username] = &http.Cookie{Name: SessionCookieName, Value: issued.CookieValue}
	}
	lastSync := time.Date(2026, time.August, 29, 9, 30, 0, 0, time.UTC)
	service := administration.NewService(store, administrationTestStatus{lastSync: lastSync}, "test-build")
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(store), Sessions: sessions, Admin: service,
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return &administrationHTTPFixture{handler: handler, store: store, sessions: sessions, cookies: cookies, databasePath: path, lastSync: lastSync}
}

func (f *administrationHTTPFixture) get(username, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(f.cookies[username])
	return f.serve(request)
}

func (f *administrationHTTPFixture) serve(request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	return response
}

type administrationTestStatus struct{ lastSync time.Time }

func (administrationTestStatus) Live() bool                            { return true }
func (administrationTestStatus) Ready() bool                           { return true }
func (s administrationTestStatus) LastSuccessfulUserSyncAt() time.Time { return s.lastSync }

type busyAdministrationService struct{}

func (busyAdministrationService) ListUsers(context.Context, auth.User) (administration.UserRegister, error) {
	return administration.UserRegister{}, database.ErrBusy
}
func (busyAdministrationService) System(context.Context, auth.User) (administration.SystemStatus, error) {
	return administration.SystemStatus{}, database.ErrBusy
}

func assertAdministrationError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus || responseErrorCode(t, response) != wantCode {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}
