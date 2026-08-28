package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	"confighub.local/internal/revisions"
)

func TestRevisionRoutesRequireCookieSessionsAndProtectWrites(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	for _, test := range []struct {
		method, path, body string
	}{
		{method: http.MethodGet, path: revisionConfigPath},
		{method: http.MethodPut, path: revisionConfigPath, body: `{"base_revision":0,"entries":[]}`},
		{method: http.MethodGet, path: revisionListPath},
		{method: http.MethodGet, path: revisionListPath + "/1"},
		{method: http.MethodGet, path: revisionListPath + "/1/diff"},
		{method: http.MethodPost, path: revisionListPath + "/1/rollback", body: `{"message":"restore"}`},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		if test.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		assertRevisionHTTPError(t, response, http.StatusUnauthorized, "invalid_session")
	}

	for _, test := range []struct {
		name, origin, csrf, wantCode string
	}{
		{name: "missing origin", csrf: fixture.csrf["editor"], wantCode: "invalid_origin"},
		{name: "wrong csrf", origin: testOrigin, csrf: "wrong", wantCode: "invalid_csrf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{"base_revision":0,"entries":[]}`)
			request.Header.Del("Origin")
			request.Header.Del(CSRFHeaderName)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.csrf != "" {
				request.Header.Set(CSRFHeaderName, test.csrf)
			}
			response := fixture.serve(t, request)
			assertRevisionHTTPError(t, response, http.StatusForbidden, test.wantCode)
		})
	}
}

func TestRevisionHTTPConfigLifecycleValidationAndServiceFilter(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)

	response := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("empty config status=%d body=%s", response.Code, response.Body.String())
	}
	var empty struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &empty)
	if empty.Revision.Version != 0 || empty.Revision.Entries == nil || len(empty.Revision.Entries) != 0 {
		t.Fatalf("empty revision=%+v", empty.Revision)
	}

	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{
		"base_revision":0,
		"message":" initial ",
		"entries":[{"key":" PORT ","value":" 8080\n"},{"key":"DATABASE_URL","value":"postgres://secret\nnext","service":" api "}]
	}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("replace status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &created)
	if created.Revision.Version != 1 || created.Revision.Message != "initial" || created.Revision.Entries[0].Key != "DATABASE_URL" || created.Revision.Entries[1].Value != " 8080\n" {
		t.Fatalf("created=%+v", created.Revision)
	}

	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath+"?service=api", ""))
	var filtered struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &filtered)
	if response.Code != http.StatusOK || len(filtered.Revision.Entries) != 1 || filtered.Revision.Entries[0].Key != "DATABASE_URL" {
		t.Fatalf("filtered status=%d revision=%+v", response.Code, filtered.Revision)
	}
	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath+"?service=%20api%20", ""))
	decodeResponse(t, response, &filtered)
	if response.Code != http.StatusOK || len(filtered.Revision.Entries) != 1 || filtered.Revision.Entries[0].Key != "DATABASE_URL" {
		t.Fatalf("trimmed filter status=%d revision=%+v", response.Code, filtered.Revision)
	}
	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath, ""))
	var unfiltered struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &unfiltered)
	if response.Code != http.StatusOK || len(unfiltered.Revision.Entries) != 2 {
		t.Fatalf("unfiltered status=%d revision=%+v", response.Code, unfiltered.Revision)
	}

	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodPut, revisionConfigPath, `{"base_revision":1,"entries":[]}`))
	assertRevisionHTTPError(t, response, http.StatusForbidden, "forbidden")
	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{"base_revision":0,"entries":[]}`))
	assertRevisionHTTPError(t, response, http.StatusConflict, "revision_conflict")
	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{"base_revision":1,"entries":[{"key":"BAD-KEY","value":"do-not-leak"}]}`))
	assertRevisionHTTPError(t, response, http.StatusUnprocessableEntity, "validation_failed")
	var validation errorEnvelope
	decodeResponse(t, response, &validation)
	if validation.Error.Fields["entries[0].key"] == "" || strings.Contains(response.Body.String(), "do-not-leak") {
		t.Fatalf("validation response=%s", response.Body.String())
	}
	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{"base_revision":1,"entries":[],"unknown":true}`))
	assertRevisionHTTPError(t, response, http.StatusBadRequest, "malformed_request")
}

func TestRevisionHTTPRejectsInvalidUnicodeAndDuplicateKeysWithoutMutation(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{name: "raw invalid UTF-8", body: append([]byte(`{"base_revision":0,"entries":[{"key":"VALUE","value":"`), append([]byte{0xff}, []byte(`"}]}`)...)...)},
		{name: "lone high surrogate", body: []byte(`{"base_revision":0,"entries":[{"key":"VALUE","value":"\ud800"}]}`)},
		{name: "lone low surrogate", body: []byte(`{"base_revision":0,"entries":[{"key":"VALUE","value":"\udc00"}]}`)},
		{name: "invalid surrogate pair", body: []byte(`{"base_revision":0,"entries":[{"key":"VALUE","value":"\ud800\u0041"}]}`)},
		{name: "duplicate top-level key", body: []byte(`{"base_revision":0,"entries":[],"entries":[{"key":"VALUE","value":"value-secret"}]}`)},
		{name: "case-folded field alias", body: []byte(`{"base_revision":0,"entries":[],"Entries":[{"key":"VALUE","value":"value-secret"}]}`)},
		{name: "duplicate nested key", body: []byte(`{"base_revision":0,"entries":[{"key":"VALUE","value":"first","value":"value-secret"}]}`)},
		{name: "Unicode case-folded field alias", body: []byte(`{"base_revision":0,"entries":[{"key":"SAFE","Key":"OVERRIDE","value":"value-secret"}]}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRevisionHTTPFixture(t)
			request := fixture.request(t, "editor", http.MethodPut, revisionConfigPath, string(test.body))
			response := fixture.serve(t, request)
			assertRevisionHTTPError(t, response, http.StatusBadRequest, "malformed_request")
			if strings.Contains(response.Body.String(), "value-secret") || strings.Contains(response.Body.String(), "replacement") {
				t.Fatalf("invalid JSON content leaked: %s", response.Body.String())
			}
			var revisionCount int
			var currentID sql.NullString
			if err := fixture.store.DB().QueryRow(`SELECT COUNT(r.id), e.current_revision_id
				FROM environments e LEFT JOIN revisions r ON r.environment_id = e.id
				WHERE e.id = 'visible-environment' GROUP BY e.id`).Scan(&revisionCount, &currentID); err != nil {
				t.Fatal(err)
			}
			if revisionCount != 0 || currentID.Valid {
				t.Fatalf("invalid JSON mutated revisions=%d current=%v", revisionCount, currentID)
			}
		})
	}
}

func TestRevisionHTTPPreservesValidSurrogatePair(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	response := fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath,
		`{"base_revision":0,"entries":[{"key":"EMOJI","value":"\ud83d\ude00"}]}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &result)
	if len(result.Revision.Entries) != 1 || result.Revision.Entries[0].Value != "😀" {
		t.Fatalf("revision=%+v", result.Revision)
	}
}

func TestRevisionHTTPListDetailDiffAndRollback(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	first := replaceRevisionHTTP(t, fixture, 0, "first", []revisions.Entry{{Key: "A", Value: "before", Service: "api"}, {Key: "DELETE", Value: "gone"}})
	second := replaceRevisionHTTP(t, fixture, first.Version, "second", []revisions.Entry{{Key: "A", Value: "after", Service: "worker"}, {Key: "NEW", Value: "new"}})

	response := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionListPath, ""))
	var list struct {
		Revisions []map[string]any `json:"revisions"`
	}
	decodeResponse(t, response, &list)
	if response.Code != http.StatusOK || len(list.Revisions) != 2 || list.Revisions[0]["version"] != float64(2) || list.Revisions[0]["message"] != "second" || list.Revisions[0]["created_by"] != fixture.users["editor"].ID || list.Revisions[0]["created_at"] == nil {
		t.Fatalf("list status=%d revisions=%+v", response.Code, list.Revisions)
	}
	if _, includesEntries := list.Revisions[0]["entries"]; includesEntries {
		t.Fatalf("revision list unexpectedly contains entries: %+v", list.Revisions[0])
	}

	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionListPath+"/1", ""))
	var detail struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &detail)
	if response.Code != http.StatusOK || detail.Revision.ID != first.ID || len(detail.Revision.Entries) != 2 || detail.Revision.Entries[0].Value != "before" {
		t.Fatalf("detail status=%d revision=%+v", response.Code, detail.Revision)
	}

	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionListPath+"/1/diff", ""))
	var diff struct {
		BeforeRevision int64              `json:"before_revision"`
		AfterRevision  int64              `json:"after_revision"`
		Changes        []revisions.Change `json:"changes"`
	}
	decodeResponse(t, response, &diff)
	if response.Code != http.StatusOK || diff.BeforeRevision != first.Version || diff.AfterRevision != second.Version || len(diff.Changes) != 3 || diff.Changes[0].Before != "before" || diff.Changes[0].After != "after" {
		t.Fatalf("diff status=%d result=%+v", response.Code, diff)
	}

	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPost, revisionListPath+"/1/rollback", `{"message":" restore first "}`))
	var rolledBack struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &rolledBack)
	if response.Code != http.StatusCreated || rolledBack.Revision.Version != 3 || rolledBack.Revision.Message != "restore first" || len(rolledBack.Revision.Entries) != 2 || rolledBack.Revision.Entries[0].Value != "before" {
		t.Fatalf("rollback status=%d revision=%+v", response.Code, rolledBack.Revision)
	}

	response = fixture.serve(t, fixture.request(t, "viewer", http.MethodPost, revisionListPath+"/1/rollback", `{"message":"denied"}`))
	assertRevisionHTTPError(t, response, http.StatusForbidden, "forbidden")
	response = fixture.serve(t, fixture.request(t, "editor", http.MethodPost, revisionListPath+"/99/rollback", `{"message":"missing"}`))
	assertRevisionHTTPError(t, response, http.StatusNotFound, "not_found")
}

func TestRevisionHTTPRejectsAmbiguousQueriesAndInvalidVersions(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	replaceRevisionHTTP(t, fixture, 0, "first", []revisions.Entry{{Key: "A", Value: "one"}})

	tests := []struct {
		name, path string
		status     int
		code       string
	}{
		{name: "multiple service values", path: revisionConfigPath + "?service=api&service=worker", status: http.StatusBadRequest, code: "malformed_query"},
		{name: "explicit empty service", path: revisionConfigPath + "?service=", status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "whitespace service", path: revisionConfigPath + "?service=%20%20", status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "invalid UTF-8 service", path: revisionConfigPath + "?service=%FF", status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "overlong service", path: revisionConfigPath + "?service=service-secret-" + strings.Repeat("x", revisions.MaxServiceBytes+1), status: http.StatusUnprocessableEntity, code: "validation_failed"},
		{name: "unknown query parameter", path: revisionConfigPath + "?scope=all", status: http.StatusBadRequest, code: "malformed_query"},
		{name: "zero version", path: revisionListPath + "/0", status: http.StatusBadRequest, code: "malformed_request"},
		{name: "negative version", path: revisionListPath + "/-1", status: http.StatusBadRequest, code: "malformed_request"},
		{name: "plus version", path: revisionListPath + "/+1", status: http.StatusBadRequest, code: "malformed_request"},
		{name: "leading zero version", path: revisionListPath + "/01", status: http.StatusBadRequest, code: "malformed_request"},
		{name: "overflow version", path: revisionListPath + "/9223372036854775808", status: http.StatusBadRequest, code: "malformed_request"},
		{name: "missing version", path: revisionListPath + "/99", status: http.StatusNotFound, code: "not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, test.path, ""))
			assertRevisionHTTPError(t, response, test.status, test.code)
			if strings.Contains(response.Body.String(), "service-secret") || strings.Contains(response.Body.String(), "%FF") {
				t.Fatalf("service query leaked in error envelope: %s", response.Body.String())
			}
		})
	}
}

func TestRevisionHTTPRejectsQueriesOnRoutesWithoutQueryContractsBeforeWrites(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	first := replaceRevisionHTTP(t, fixture, 0, "first", []revisions.Entry{{Key: "VALUE", Value: "original"}})

	tests := []struct {
		name, username, method, path, body string
	}{
		{name: "replace empty query value", username: "editor", method: http.MethodPut, path: revisionConfigPath + "?service=", body: `{"base_revision":1,"entries":[{"key":"VALUE","value":"value-secret"}]}`},
		{name: "list service query", username: "viewer", method: http.MethodGet, path: revisionListPath + "?service=service-secret"},
		{name: "detail duplicate query", username: "viewer", method: http.MethodGet, path: revisionListPath + "/1?view=one&view=two"},
		{name: "diff empty unknown query", username: "viewer", method: http.MethodGet, path: revisionListPath + "/1/diff?unknown="},
		{name: "rollback unknown query", username: "editor", method: http.MethodPost, path: revisionListPath + "/1/rollback?service=service-secret", body: `{"message":"value-secret"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := fixture.serve(t, fixture.request(t, test.username, test.method, test.path, test.body))
			assertRevisionHTTPError(t, response, http.StatusBadRequest, "malformed_query")
			if strings.Contains(response.Body.String(), "service-secret") || strings.Contains(response.Body.String(), "value-secret") {
				t.Fatalf("query or value leaked in error envelope: %s", response.Body.String())
			}
		})
	}

	currentResponse := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath, ""))
	var current struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, currentResponse, &current)
	if currentResponse.Code != http.StatusOK || current.Revision.ID != first.ID || current.Revision.Version != first.Version || len(current.Revision.Entries) != 1 || current.Revision.Entries[0].Value != "original" {
		t.Fatalf("query-bearing writes changed current: status=%d revision=%+v", currentResponse.Code, current.Revision)
	}
	var revisionCount int
	if err := fixture.store.DB().QueryRow(`SELECT COUNT(*) FROM revisions WHERE environment_id = 'visible-environment'`).Scan(&revisionCount); err != nil {
		t.Fatal(err)
	}
	if revisionCount != 1 {
		t.Fatalf("query-bearing writes created revisions=%d", revisionCount)
	}
}

func TestRevisionHTTPSlugAuthorizationIsolationAndDisabledSessions(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	for _, path := range []string{
		revisionConfigPath,
		"/api/v1/projects/visible/environments/missing/config",
		"/api/v1/projects/visible/environments/hidden-only/config",
		"/api/v1/projects/missing/environments/production/config",
	} {
		response := fixture.serve(t, fixture.request(t, "outsider", http.MethodGet, path, ""))
		assertRevisionHTTPError(t, response, http.StatusForbidden, "forbidden")
	}
	for _, path := range []string{
		"/api/v1/projects/visible/environments/missing/config",
		"/api/v1/projects/visible/environments/hidden-only/config",
		"/api/v1/projects/missing/environments/production/config",
		"/api/v1/projects/visible%2Fhidden/environments/production/config",
	} {
		response := fixture.serve(t, fixture.request(t, "admin", http.MethodGet, path, ""))
		assertRevisionHTTPError(t, response, http.StatusNotFound, "not_found")
	}

	if _, err := fixture.store.DB().Exec(`UPDATE users SET enabled = 0 WHERE id = ?`, fixture.users["viewer"].ID); err != nil {
		t.Fatal(err)
	}
	response := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath, ""))
	assertRevisionHTTPError(t, response, http.StatusUnauthorized, "invalid_session")
}

func TestRevisionHTTPCorruptCurrentPointerReturnsSafeInternalError(t *testing.T) {
	tests := []struct {
		name    string
		pointer func(*testing.T, *revisionHTTPFixture) string
		secret  string
	}{
		{
			name: "cross-environment pointer",
			pointer: func(t *testing.T, fixture *revisionHTTPFixture) string {
				t.Helper()
				revision, err := revisions.NewService(fixture.store).Replace(context.Background(), fixture.users["admin"], "hidden-environment", revisions.ReplaceInput{
					Entries: []revisions.Entry{{Key: "HIDDEN_SECRET", Value: "cross-project-secret"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				return revision.ID
			},
			secret: "cross-project-secret",
		},
		{name: "missing pointer", pointer: func(*testing.T, *revisionHTTPFixture) string { return "missing-revision-id" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRevisionHTTPFixture(t)
			pointer := test.pointer(t, fixture)
			if _, err := fixture.store.DB().Exec(`DROP TRIGGER environments_current_revision_update`); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = ? WHERE id = 'visible-environment'`, pointer); err != nil {
				t.Fatal(err)
			}
			response := fixture.serve(t, fixture.request(t, "viewer", http.MethodGet, revisionConfigPath, ""))
			assertRevisionHTTPError(t, response, http.StatusInternalServerError, "internal_error")
			if test.secret != "" && (strings.Contains(response.Body.String(), test.secret) || strings.Contains(fixture.logs.String(), test.secret)) {
				t.Fatalf("corrupt pointer leaked secret: body=%s logs=%s", response.Body.String(), fixture.logs.String())
			}
		})
	}
}

func TestRevisionHTTPBusyIsServiceUnavailableAndDoesNotLeakValues(t *testing.T) {
	fixture := newRevisionHTTPFixture(t)
	fixture.store.DB().SetMaxOpenConns(1)
	if _, err := fixture.store.DB().Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	lockRevisionHTTPDatabase(t, fixture.path)

	response := fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, `{"base_revision":0,"entries":[{"key":"SECRET","value":"do-not-leak"}]}`))
	assertRevisionHTTPError(t, response, http.StatusServiceUnavailable, "service_unavailable")
	if strings.Contains(response.Body.String(), "do-not-leak") || strings.Contains(response.Body.String(), "locked") || strings.Contains(response.Body.String(), "SQLITE") {
		t.Fatalf("details leaked: %s", response.Body.String())
	}
}

func TestOptionalRevisionRoutesAndPreciseFallback(t *testing.T) {
	withoutRevisions := newProjectHTTPFixture(t)
	for _, test := range []struct {
		method, path string
	}{
		{method: http.MethodGet, path: revisionConfigPath},
		{method: http.MethodPut, path: revisionConfigPath},
		{method: http.MethodGet, path: revisionListPath},
		{method: http.MethodPost, path: revisionListPath + "/1/rollback"},
	} {
		response := withoutRevisions.serve(t, httptest.NewRequest(test.method, test.path, nil))
		assertRevisionHTTPError(t, response, http.StatusNotFound, "not_found")
		if response.Header().Get("Allow") != "" {
			t.Fatalf("disabled route advertised Allow=%q", response.Header().Get("Allow"))
		}
	}

	fixture := newRevisionHTTPFixture(t)
	for _, test := range []struct {
		method, path, allow string
	}{
		{method: http.MethodPost, path: revisionConfigPath, allow: "GET, PUT"},
		{method: http.MethodPut, path: revisionListPath, allow: http.MethodGet},
		{method: http.MethodPost, path: revisionListPath + "/1", allow: http.MethodGet},
		{method: http.MethodPost, path: revisionListPath + "/1/diff", allow: http.MethodGet},
		{method: http.MethodGet, path: revisionListPath + "/1/rollback", allow: http.MethodPost},
	} {
		response := fixture.serve(t, httptest.NewRequest(test.method, test.path, nil))
		assertRevisionHTTPError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if response.Header().Get("Allow") != test.allow {
			t.Fatalf("%s %s Allow=%q want=%q", test.method, test.path, response.Header().Get("Allow"), test.allow)
		}
	}
	for _, path := range []string{revisionConfigPath + "/", revisionListPath + "/1/", revisionListPath + "/1/unknown"} {
		response := fixture.serve(t, httptest.NewRequest(http.MethodPatch, path, nil))
		assertRevisionHTTPError(t, response, http.StatusNotFound, "not_found")
	}
}

const (
	revisionConfigPath = "/api/v1/projects/visible/environments/production/config"
	revisionListPath   = "/api/v1/projects/visible/environments/production/revisions"
)

type revisionHTTPFixture struct {
	handler http.Handler
	store   *database.Store
	path    string
	users   map[string]auth.User
	cookies map[string]*http.Cookie
	csrf    map[string]string
	logs    *bytes.Buffer
}

func newRevisionHTTPFixture(t *testing.T) *revisionHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "revision-http.db")
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	users := map[string]auth.User{
		"admin":    {ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		"editor":   {ID: "editor-id", Username: "editor", DisplayName: "Editor", Role: "member", Enabled: true},
		"viewer":   {ID: "viewer-id", Username: "viewer", DisplayName: "Viewer", Role: "member", Enabled: true},
		"outsider": {ID: "outsider-id", Username: "outsider", DisplayName: "Outsider", Role: "member", Enabled: true},
	}
	for _, user := range users {
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at)
			VALUES (?, ?, ?, 'hash', ?, 1, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`INSERT INTO projects (id, slug, name, description, created_by, created_at, updated_at)
		VALUES ('visible-project', 'visible', 'Visible', '', 'admin-id', 1, 1), ('hidden-project', 'hidden', 'Hidden', '', 'admin-id', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO environments (id, project_id, slug, name, created_at, updated_at)
		VALUES ('visible-environment', 'visible-project', 'production', 'Production', 1, 1), ('hidden-environment', 'hidden-project', 'hidden-only', 'Hidden', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO project_members (project_id, user_id, permission)
		VALUES ('visible-project', 'editor-id', 'editor'), ('visible-project', 'viewer-id', 'viewer')`); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	fixture := &revisionHTTPFixture{store: store, path: path, users: users, cookies: make(map[string]*http.Cookie), csrf: make(map[string]string), logs: new(bytes.Buffer)}
	for username, user := range users {
		issued, err := sessions.Create(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		fixture.cookies[username] = &http.Cookie{Name: SessionCookieName, Value: issued.CookieValue}
		fixture.csrf[username] = issued.CSRFToken
	}
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(store),
		Sessions:    sessions,
		Projects:    projects.NewService(store),
		Revisions:   revisions.NewService(store),
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(fixture.logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	return fixture
}

func (f *revisionHTTPFixture) request(t *testing.T, username, method, path, body string) *http.Request {
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

func (f *revisionHTTPFixture) serve(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q status=%d body=%s", response.Header().Get("Cache-Control"), response.Code, response.Body.String())
	}
	return response
}

func replaceRevisionHTTP(t *testing.T, fixture *revisionHTTPFixture, base int64, message string, entries []revisions.Entry) revisions.Revision {
	t.Helper()
	body, err := json.Marshal(struct {
		BaseRevision int64             `json:"base_revision"`
		Message      string            `json:"message"`
		Entries      []revisions.Entry `json:"entries"`
	}{BaseRevision: base, Message: message, Entries: entries})
	if err != nil {
		t.Fatal(err)
	}
	response := fixture.serve(t, fixture.request(t, "editor", http.MethodPut, revisionConfigPath, string(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("replace status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Revision revisions.Revision `json:"revision"`
	}
	decodeResponse(t, response, &result)
	return result.Revision
}

func assertRevisionHTTPError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus || responseErrorCode(t, response) != wantCode || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func lockRevisionHTTPDatabase(t *testing.T, path string) {
	t.Helper()
	locker, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	if _, err := locker.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	transaction, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	if _, err := transaction.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at)
		VALUES ('revision-http-lock', 'revision-http-lock', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
}
