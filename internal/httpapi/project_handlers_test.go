package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"confighub.local/internal/auth"
	"confighub.local/internal/database"
	"confighub.local/internal/projects"
)

func TestProjectRoutesRequireCookieSessionsAndProtectWrites(t *testing.T) {
	fixture := newProjectHTTPFixture(t)

	for _, test := range []struct {
		method, path string
		body         string
	}{
		{method: http.MethodGet, path: "/api/v1/projects"},
		{method: http.MethodPost, path: "/api/v1/projects", body: `{"slug":"app","name":"App"}`},
		{method: http.MethodGet, path: "/api/v1/projects/app"},
		{method: http.MethodPost, path: "/api/v1/projects/app/environments", body: `{"slug":"prod","name":"Prod"}`},
		{method: http.MethodGet, path: "/api/v1/projects/app/members"},
		{method: http.MethodPut, path: "/api/v1/projects/app/members/member", body: `{"permission":"viewer"}`},
		{method: http.MethodDelete, path: "/api/v1/projects/app/members/member"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		assertProjectHTTPError(t, response, http.StatusUnauthorized, "invalid_session")
	}

	for _, test := range []struct {
		name, origin, csrf string
		wantCode           string
	}{
		{name: "missing origin", csrf: fixture.csrf["admin"], wantCode: "invalid_origin"},
		{name: "wrong csrf", origin: testOrigin, csrf: "wrong", wantCode: "invalid_csrf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request(t, "admin", http.MethodPost, "/api/v1/projects", `{"slug":"app","name":"App"}`)
			request.Header.Del("Origin")
			request.Header.Del(CSRFHeaderName)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.csrf != "" {
				request.Header.Set(CSRFHeaderName, test.csrf)
			}
			response := httptest.NewRecorder()
			fixture.handler.ServeHTTP(response, request)
			assertProjectHTTPError(t, response, http.StatusForbidden, test.wantCode)
		})
	}
}

func TestOptionalProjectRoutesAreNotAdvertisedWithoutProjectDependency(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	for _, test := range []struct {
		method, path string
	}{
		{method: http.MethodGet, path: "/api/v1/projects"},
		{method: http.MethodPost, path: "/api/v1/projects"},
		{method: http.MethodGet, path: "/api/v1/projects/app"},
		{method: http.MethodPost, path: "/api/v1/projects/app"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		assertProjectHTTPError(t, response, http.StatusNotFound, "not_found")
		if allowed := response.Header().Get("Allow"); allowed != "" {
			t.Fatalf("%s %s advertised Allow=%q", test.method, test.path, allowed)
		}
	}
}

func TestEnabledProjectRoutesReturnPreciseMethodNotAllowed(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	for _, test := range []struct {
		method, path, allowed string
	}{
		{method: http.MethodPut, path: "/api/v1/projects", allowed: "GET, POST"},
		{method: http.MethodPost, path: "/api/v1/projects/app", allowed: http.MethodGet},
		{method: http.MethodGet, path: "/api/v1/projects/app/environments", allowed: http.MethodPost},
		{method: http.MethodPatch, path: "/api/v1/projects/app/members/member", allowed: "DELETE, PUT"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := fixture.serve(t, request)
		assertProjectHTTPError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if allowed := response.Header().Get("Allow"); allowed != test.allowed {
			t.Fatalf("%s %s Allow=%q want=%q", test.method, test.path, allowed, test.allowed)
		}
	}
}

func TestProjectHTTPRoleIsolationAndResourceDisclosure(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	createProjectHTTP(t, fixture, "admin", "visible", "Visible")
	createProjectHTTP(t, fixture, "admin", "hidden", "Hidden")
	setMemberHTTP(t, fixture, "visible", "member", "viewer", http.StatusNoContent)

	response := fixture.serve(t, fixture.request(t, "member", http.MethodGet, "/api/v1/projects", ""))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var list struct {
		Projects []projects.Project `json:"projects"`
	}
	decodeResponse(t, response, &list)
	if len(list.Projects) != 1 || list.Projects[0].Slug != "visible" {
		t.Fatalf("projects=%+v", list.Projects)
	}

	for _, path := range []string{"/api/v1/projects/hidden", "/api/v1/projects/missing"} {
		response = fixture.serve(t, fixture.request(t, "member", http.MethodGet, path, ""))
		assertProjectHTTPError(t, response, http.StatusForbidden, "forbidden")
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, "/api/v1/projects/missing", ""))
	assertProjectHTTPError(t, response, http.StatusNotFound, "not_found")

	response = fixture.serve(t, fixture.request(t, "member", http.MethodPost, "/api/v1/projects", `{"slug":"denied","name":"Denied"}`))
	assertProjectHTTPError(t, response, http.StatusForbidden, "forbidden")
	response = fixture.serve(t, fixture.request(t, "member", http.MethodPost, "/api/v1/projects/visible/environments", `{"slug":"prod","name":"Prod"}`))
	assertProjectHTTPError(t, response, http.StatusForbidden, "forbidden")
	response = fixture.serve(t, fixture.request(t, "member", http.MethodPut, "/api/v1/projects/visible/members/member", `{"permission":"editor"}`))
	assertProjectHTTPError(t, response, http.StatusForbidden, "forbidden")
}

func TestProjectHTTPCreateDetailEnvironmentAndValidation(t *testing.T) {
	fixture := newProjectHTTPFixture(t)

	response := fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects", `{"slug":"Bad","name":" "}`))
	assertProjectHTTPError(t, response, http.StatusUnprocessableEntity, "validation_failed")
	var envelope errorEnvelope
	decodeResponse(t, response, &envelope)
	if envelope.Error.Fields["slug"] == "" || envelope.Error.Fields["name"] == "" {
		t.Fatalf("fields=%v", envelope.Error.Fields)
	}

	response = createProjectHTTP(t, fixture, "admin", "app", " App ")
	var created struct {
		Project projects.Project `json:"project"`
	}
	decodeResponse(t, response, &created)
	if response.Code != http.StatusCreated || created.Project.Name != "App" || created.Project.ID == "" {
		t.Fatalf("created=%+v status=%d", created, response.Code)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects", `{"slug":"app","name":"Duplicate"}`))
	assertProjectHTTPError(t, response, http.StatusConflict, "resource_conflict")

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects/app/environments", `{"slug":"prod","name":" Production "}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("environment status=%d body=%s", response.Code, response.Body.String())
	}
	var environment struct {
		Environment projects.Environment `json:"environment"`
	}
	decodeResponse(t, response, &environment)
	if environment.Environment.Slug != "prod" || environment.Environment.Name != "Production" {
		t.Fatalf("environment=%+v", environment.Environment)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects/app/environments", `{"slug":"prod","name":"Duplicate"}`))
	assertProjectHTTPError(t, response, http.StatusConflict, "resource_conflict")

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, "/api/v1/projects/app", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var detail struct {
		Project projects.ProjectDetail `json:"project"`
	}
	decodeResponse(t, response, &detail)
	if detail.Project.Slug != "app" || len(detail.Project.Environments) != 1 || detail.Project.Environments[0].Slug != "prod" {
		t.Fatalf("detail=%+v", detail.Project)
	}
}

func TestProjectHTTPMemberGrantLifecycleAndStrictBodies(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	createProjectHTTP(t, fixture, "admin", "app", "App")

	for _, permission := range []string{"viewer", "viewer", "editor"} {
		setMemberHTTP(t, fixture, "app", "member", permission, http.StatusNoContent)
	}
	response := fixture.serve(t, fixture.request(t, "admin", http.MethodGet, "/api/v1/projects/app/members", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("members status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Members []projects.MemberGrant `json:"members"`
	}
	decodeResponse(t, response, &result)
	if len(result.Members) != 1 || result.Members[0].Username != "member" || result.Members[0].Permission != "editor" {
		t.Fatalf("members=%+v", result.Members)
	}

	for _, test := range []struct {
		name, body string
		status     int
		code       string
	}{
		{name: "unknown field", body: `{"permission":"viewer","extra":true}`, status: http.StatusBadRequest, code: "malformed_request"},
		{name: "multiple documents", body: `{"permission":"viewer"}{}`, status: http.StatusBadRequest, code: "malformed_request"},
		{name: "invalid permission", body: `{"permission":"owner"}`, status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "disabled target", body: `{"permission":"viewer"}`, status: http.StatusUnprocessableEntity, code: "validation_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			username := "member"
			if test.name == "disabled target" {
				username = "disabled"
			}
			response := fixture.serve(t, fixture.request(t, "admin", http.MethodPut, "/api/v1/projects/app/members/"+username, test.body))
			assertProjectHTTPError(t, response, test.status, test.code)
		})
	}

	for range 2 {
		response = fixture.serve(t, fixture.request(t, "admin", http.MethodDelete, "/api/v1/projects/app/members/member", ""))
		if response.Code != http.StatusNoContent || response.Body.Len() != 0 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("delete status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestProjectHTTPRejectsDuplicateJSONKeysWithoutMutation(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	response := fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects", `{
		"slug":"duplicate-json","name":"First","name":"Second"
	}`))
	assertProjectHTTPError(t, response, http.StatusBadRequest, "malformed_request")
	var projectCount int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projectCount); err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 {
		t.Fatalf("duplicate JSON created projects=%d", projectCount)
	}
}

func TestProjectHTTPDisabledSessionAndSQLiteBusy(t *testing.T) {
	fixture := newProjectHTTPFixture(t)
	issued := fixture.cookies["member"]
	if _, err := fixture.store.DB().Exec(`UPDATE users SET enabled = 0 WHERE username = 'member'`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	request.AddCookie(issued)
	response := fixture.serve(t, request)
	assertProjectHTTPError(t, response, http.StatusUnauthorized, "invalid_session")

	fixture.store.DB().SetMaxOpenConns(1)
	if _, err := fixture.store.DB().Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	locker, err := sql.Open("sqlite", fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	locker.SetMaxOpenConns(1)
	if _, err := locker.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	lockTx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lockTx.Rollback() })
	if _, err := lockTx.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('lock', 'lock', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/projects", `{"slug":"busy","name":"Busy"}`))
	assertProjectHTTPError(t, response, http.StatusServiceUnavailable, "service_unavailable")
	if strings.Contains(response.Body.String(), "locked") || strings.Contains(response.Body.String(), "SQLITE") {
		t.Fatalf("database details leaked: %s", response.Body.String())
	}
}

type projectHTTPFixture struct {
	handler http.Handler
	store   *database.Store
	path    string
	cookies map[string]*http.Cookie
	csrf    map[string]string
}

func newProjectHTTPFixture(t *testing.T) *projectHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "project-http.db")
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	users := []auth.User{
		{ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		{ID: "member-id", Username: "member", DisplayName: "Member", Role: "member", Enabled: true},
		{ID: "disabled-id", Username: "disabled", DisplayName: "Disabled", Role: "member", Enabled: false},
	}
	for _, user := range users {
		enabled := 0
		if user.Enabled {
			enabled = 1
		}
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at) VALUES (?, ?, ?, 'hash', ?, ?, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role, enabled); err != nil {
			t.Fatal(err)
		}
	}
	sessions := auth.NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	fixture := &projectHTTPFixture{store: store, path: path, cookies: make(map[string]*http.Cookie), csrf: make(map[string]string)}
	for _, user := range users[:2] {
		issued, err := sessions.Create(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		fixture.cookies[user.Username] = &http.Cookie{Name: SessionCookieName, Value: issued.CookieValue}
		fixture.csrf[user.Username] = issued.CSRFToken
	}
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(store),
		Sessions:    sessions,
		Projects:    projects.NewService(store),
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	return fixture
}

func (f *projectHTTPFixture) request(t *testing.T, username, method, path, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.AddCookie(f.cookies[username])
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete {
		request.Header.Set("Origin", testOrigin)
		request.Header.Set(CSRFHeaderName, f.csrf[username])
	}
	return request
}

func (f *projectHTTPFixture) serve(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q status=%d body=%s", response.Header().Get("Cache-Control"), response.Code, response.Body.String())
	}
	return response
}

func createProjectHTTP(t *testing.T, fixture *projectHTTPFixture, username, slug, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"slug": slug, "name": name})
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.serve(t, fixture.request(t, username, http.MethodPost, "/api/v1/projects", string(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("create project status=%d body=%s", response.Code, response.Body.String())
	}
	return response
}

func setMemberHTTP(t *testing.T, fixture *projectHTTPFixture, project, username, permission string, wantStatus int) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"permission": permission})
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.serve(t, fixture.request(t, "admin", http.MethodPut, "/api/v1/projects/"+project+"/members/"+username, string(body)))
	if response.Code != wantStatus {
		t.Fatalf("set member status=%d body=%s", response.Code, response.Body.String())
	}
}

func assertProjectHTTPError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus || responseErrorCode(t, response) != wantCode || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
}
