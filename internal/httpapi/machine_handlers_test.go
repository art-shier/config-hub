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
	"confighub.local/internal/machineaccess"
	"confighub.local/internal/projects"
	"confighub.local/internal/revisions"
)

const (
	machineConfigPath  = "/api/v1/projects/shop/environments/production/config"
	machineHistoryPath = "/api/v1/projects/shop/environments/production/revisions"
)

type machineHTTPFixture struct {
	handler  http.Handler
	store    *database.Store
	path     string
	service  *machineaccess.Service
	users    map[string]auth.User
	cookies  map[string]*http.Cookie
	csrf     map[string]string
	logs     *bytes.Buffer
	identity machineaccess.Identity
	token    machineaccess.IssuedToken
}

type blockingHTTPMachineRead struct {
	*machineaccess.Service
	store   *database.Store
	started chan struct{}
	release chan struct{}
}

func (s *blockingHTTPMachineRead) ReadCurrentForProject(ctx context.Context, plaintext, project, environment, service string) (machineaccess.CurrentConfig, error) {
	config, err := s.Service.ReadCurrentForProject(ctx, plaintext, project, environment, service)
	if err != nil {
		return machineaccess.CurrentConfig{}, err
	}
	err = s.store.InReadTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM revisions`).Scan(&count); err != nil {
			return err
		}
		close(s.started)
		select {
		case <-s.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	return config, err
}

func TestMachineAdminLifecycleRequiresAdminSessionAndProtectsWrites(t *testing.T) {
	fixture := newMachineHTTPFixture(t)

	response := fixture.serve(t, httptest.NewRequest(http.MethodGet, "/api/v1/machine-identities", nil))
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_session")
	response = fixture.serve(t, fixture.request(t, "member", http.MethodGet, "/api/v1/machine-identities", ""))
	assertMachineHTTPError(t, response, http.StatusForbidden, "forbidden")

	for _, test := range []struct {
		name, origin, csrf string
		wantCode           string
	}{
		{name: "missing origin", csrf: fixture.csrf["admin"], wantCode: "invalid_origin"},
		{name: "wrong csrf", origin: testOrigin, csrf: "wrong", wantCode: "invalid_csrf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := fixture.request(t, "admin", http.MethodPost, "/api/v1/machine-identities", `{"name":"new-ci","enabled":true}`)
			request.Header.Del("Origin")
			request.Header.Del(CSRFHeaderName)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.csrf != "" {
				request.Header.Set(CSRFHeaderName, test.csrf)
			}
			response := fixture.serve(t, request)
			assertMachineHTTPError(t, response, http.StatusForbidden, test.wantCode)
		})
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/machine-identities", `{"name":" deploy-ci ","description":" deploys ","enabled":true}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Identity machineaccess.Identity `json:"identity"`
	}
	decodeResponse(t, response, &created)
	if created.Identity.Name != "deploy-ci" || created.Identity.Description != "deploys" {
		t.Fatalf("identity=%+v", created.Identity)
	}
	identityPath := "/api/v1/machine-identities/" + created.Identity.ID

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath, `{"description":" paused ","enabled":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	var updated struct {
		Identity machineaccess.Identity `json:"identity"`
	}
	decodeResponse(t, response, &updated)
	if updated.Identity.Enabled || updated.Identity.Description != "paused" {
		t.Fatalf("updated=%+v", updated.Identity)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath, `{"description":" active ","enabled":true}`))
	if response.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", response.Code, response.Body.String())
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath+"/grants", `{"grants":[{"project_id":"shop-project","environment_id":"shop-production"}]}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("grants status=%d body=%s", response.Code, response.Body.String())
	}
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	body, err := json.Marshal(map[string]any{"name": "primary", "expires_at": expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, identityPath+"/tokens", string(body)))
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", response.Code, response.Body.String())
	}
	var issued struct {
		Token machineaccess.IssuedToken `json:"token"`
	}
	decodeResponse(t, response, &issued)
	if !strings.HasPrefix(issued.Token.Plaintext, "ch_") {
		t.Fatalf("issued token prefix missing")
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, identityPath, ""))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), issued.Token.Plaintext) || strings.Contains(response.Body.String(), "token_hash") {
		t.Fatalf("detail status=%d body=%s", response.Code, response.Body.String())
	}
	var detail struct {
		Identity machineaccess.IdentityDetail `json:"identity"`
	}
	decodeResponse(t, response, &detail)
	if len(detail.Identity.Grants) != 1 || len(detail.Identity.Tokens) != 1 || detail.Identity.Tokens[0].Prefix != issued.Token.Prefix {
		t.Fatalf("detail=%+v", detail.Identity)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, "/api/v1/machine-identities", ""))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), issued.Token.Plaintext) || strings.Contains(response.Body.String(), "token_hash") {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodDelete, identityPath+"/tokens/"+issued.Token.ID, ""))
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodDelete, identityPath+"/tokens/"+issued.Token.ID, ""))
	if response.Code != http.StatusNoContent {
		t.Fatalf("idempotent revoke status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMachineGrantPermissionsHTTPDefaultRoundTripAndValidation(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	identityPath := "/api/v1/machine-identities/" + fixture.identity.ID

	response := fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath+"/grants", `{"grants":[{"project_id":"shop-project","environment_id":"shop-production"}]}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("default grants status=%d", response.Code)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, identityPath, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("default detail status=%d", response.Code)
	}
	var detail struct {
		Identity machineaccess.IdentityDetail `json:"identity"`
	}
	decodeResponse(t, response, &detail)
	if len(detail.Identity.Grants) != 1 || detail.Identity.Grants[0].Permission != machineaccess.GrantRead {
		t.Fatalf("default grant count=%d permission=%q", len(detail.Identity.Grants), detail.Identity.Grants[0].Permission)
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath+"/grants", `{"grants":[{"project_id":"shop-project","environment_id":"shop-production","permission":"write"}]}`))
	if response.Code != http.StatusNoContent {
		t.Fatalf("write grants status=%d", response.Code)
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, identityPath, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("write detail status=%d", response.Code)
	}
	decodeResponse(t, response, &detail)
	if len(detail.Identity.Grants) != 1 || detail.Identity.Grants[0].Permission != machineaccess.GrantWrite {
		t.Fatalf("write grant count=%d permission=%q", len(detail.Identity.Grants), detail.Identity.Grants[0].Permission)
	}

	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPut, identityPath+"/grants", `{"grants":[{"project_id":"shop-project","environment_id":"shop-production","permission":"admin"}]}`))
	if response.Code != http.StatusUnprocessableEntity || responseErrorCode(t, response) != "validation_failed" {
		t.Fatalf("invalid permission status=%d code=%q", response.Code, responseErrorCode(t, response))
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodGet, identityPath, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("preserved detail status=%d", response.Code)
	}
	decodeResponse(t, response, &detail)
	if len(detail.Identity.Grants) != 1 || detail.Identity.Grants[0].Permission != machineaccess.GrantWrite {
		t.Fatalf("preserved grant count=%d permission=%q", len(detail.Identity.Grants), detail.Identity.Grants[0].Permission)
	}
}

func TestMachineAdminStrictInputsErrorsAndDatabaseBusy(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	for _, test := range []struct {
		method, path, body string
		wantStatus         int
		wantCode           string
	}{
		{method: http.MethodGet, path: "/api/v1/machine-identities?unknown=1", wantStatus: http.StatusBadRequest, wantCode: "malformed_query"},
		{method: http.MethodPost, path: "/api/v1/machine-identities", body: `{"name":"x","enabled":true,"unknown":1}`, wantStatus: http.StatusBadRequest, wantCode: "malformed_request"},
		{method: http.MethodPost, path: "/api/v1/machine-identities", body: `{"name":" ","enabled":true}`, wantStatus: http.StatusUnprocessableEntity, wantCode: "validation_failed"},
		{method: http.MethodGet, path: "/api/v1/machine-identities/missing", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{method: http.MethodPut, path: "/api/v1/machine-identities/missing/grants", body: `{"grants":[]}`, wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{method: http.MethodPut, path: "/api/v1/machine-identities/" + fixture.identity.ID + "/grants", body: `{"grants":[{"project_id":"other-project","environment_id":"shop-production"}]}`, wantStatus: http.StatusUnprocessableEntity, wantCode: "validation_failed"},
	} {
		response := fixture.serve(t, fixture.request(t, "admin", test.method, test.path, test.body))
		assertMachineHTTPError(t, response, test.wantStatus, test.wantCode)
	}

	response := fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/machine-identities", `{"name":"runner","enabled":true}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/machine-identities", `{"name":" runner ","enabled":true}`))
	assertMachineHTTPError(t, response, http.StatusConflict, "resource_conflict")

	fixture.store.DB().SetMaxOpenConns(1)
	if _, err := fixture.store.DB().Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	lockMachineHTTPDatabase(t, fixture.path)
	response = fixture.serve(t, fixture.request(t, "admin", http.MethodPost, "/api/v1/machine-identities", `{"name":"busy","enabled":true}`))
	assertMachineHTTPError(t, response, http.StatusServiceUnavailable, "service_unavailable")
	if strings.Contains(response.Body.String(), "busy") {
		t.Fatalf("busy input leaked: %s", response.Body.String())
	}
}

func TestCurrentConfigSupportsCookieOrStrictBearerWithoutFallback(t *testing.T) {
	fixture := newMachineHTTPFixture(t)

	request := httptest.NewRequest(http.MethodGet, machineConfigPath+"?service=api", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
	response := fixture.serve(t, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bearer read status=%d body=%s", response.Code, response.Body.String())
	}
	var machine machineaccess.CurrentConfig
	decodeResponse(t, response, &machine)
	if machine.Project != "shop" || machine.Environment != "production" || machine.Revision != 1 || len(machine.Values) != 1 || machine.Values["DATABASE_URL"] != "postgres://config-secret" {
		t.Fatalf("machine response=%+v", machine)
	}

	response = fixture.serve(t, fixture.request(t, "member", http.MethodGet, machineConfigPath, ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision":{`) {
		t.Fatalf("cookie response status=%d body=%s", response.Code, response.Body.String())
	}

	request = fixture.request(t, "member", http.MethodGet, machineConfigPath, "")
	request.Header.Set("Authorization", "Bearer invalid")
	response = fixture.serve(t, request)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")

	for _, scheme := range []string{"bearer", "BEARER", "BeArEr"} {
		request = httptest.NewRequest(http.MethodGet, machineConfigPath, nil)
		request.Header.Set("Authorization", scheme+" "+fixture.token.Plaintext)
		response = fixture.serve(t, request)
		if response.Code != http.StatusOK {
			t.Fatalf("scheme=%q status=%d body=%s", scheme, response.Code, response.Body.String())
		}
	}

	for _, authorization := range []string{"", "Basic abc", "Bearer", "Bearer\t" + fixture.token.Plaintext, "Bearer  " + fixture.token.Plaintext, "Bearer " + fixture.token.Plaintext + " extra"} {
		request = fixture.request(t, "member", http.MethodGet, machineConfigPath, "")
		request.Header["Authorization"] = []string{authorization}
		response = fixture.serve(t, request)
		assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
	}

	request = fixture.request(t, "member", http.MethodGet, machineConfigPath, "")
	request.Header.Add("Authorization", "Bearer "+fixture.token.Plaintext)
	request.Header.Add("Authorization", "Bearer "+fixture.token.Plaintext)
	response = fixture.serve(t, request)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
}

func TestAuthorizationGuardUsesActualServeMuxPattern(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	for _, path := range []string{
		"/api/v1/projects/shop%2Fenvironments/production/config",
		"/api/v1/projects/shop/environments/production%2Fconfig",
	} {
		request := fixture.request(t, "admin", http.MethodGet, path, "")
		request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
		response := fixture.serve(t, request)
		assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
	}

	request := fixture.request(t, "member", http.MethodGet, machineConfigPath, "")
	request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
	response := fixture.serve(t, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"revision":{`) {
		t.Fatalf("cookie plus bearer did not use machine auth: status=%d body=%s", response.Code, response.Body.String())
	}

	request = fixture.request(t, "admin", http.MethodPost, machineConfigPath, "")
	request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
	response = fixture.serve(t, request)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")

	sessions := auth.NewSessionManager(fixture.store, []byte("01234567890123456789012345678901"), time.Hour)
	custom, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(fixture.store), Sessions: sessions,
		Projects: projects.NewService(fixture.store), Revisions: revisions.NewService(fixture.store), Machines: fixture.service,
	}, Options{
		PublicOrigin: testOrigin,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Register: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /api/v1/{rest...}", func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, 299, map[string]string{"status": "custom"})
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v1/custom",
		"/api/v1/projects/shop%2Fenvironments/production/config",
	} {
		request = fixture.request(t, "admin", http.MethodGet, path, "")
		request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
		response = httptest.NewRecorder()
		custom.ServeHTTP(response, request)
		assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
	}
}

func TestMachineHTTPReadOnlyRequestDoesNotBlockConfigWrite(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	blocking := &blockingHTTPMachineRead{
		Service: fixture.service,
		store:   fixture.store,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	sessions := auth.NewSessionManager(fixture.store, []byte("01234567890123456789012345678901"), time.Hour)
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(fixture.store), Sessions: sessions,
		Projects: projects.NewService(fixture.store), Revisions: revisions.NewService(fixture.store), Machines: blocking,
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}

	readResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, machineConfigPath, nil)
		request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		readResponse <- response
	}()
	<-blocking.started

	writeResponse := make(chan *httptest.ResponseRecorder, 1)
	writeRequest := fixture.request(t, "admin", http.MethodPut, machineConfigPath, `{"base_revision":1,"entries":[{"key":"VALUE","value":"after"}]}`)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, writeRequest)
		writeResponse <- response
	}()
	select {
	case response := <-writeResponse:
		if response.Code != http.StatusCreated {
			close(blocking.release)
			<-readResponse
			t.Fatalf("write status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(2 * time.Second):
		close(blocking.release)
		<-readResponse
		t.Fatal("HTTP machine read blocked the config write")
	}
	close(blocking.release)
	response := <-readResponse
	if response.Code != http.StatusOK {
		t.Fatalf("read status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBearerTokenStateScopeAndRouteBinding(t *testing.T) {
	fixture := newMachineHTTPFixture(t)

	for _, path := range []string{
		"/api/v1/projects/shop/environments/staging/config",
		"/api/v1/projects/other/environments/production/config",
		"/api/v1/projects/shop/environments/missing/config",
	} {
		response := fixture.bearerRead(t, fixture.token.Plaintext, path)
		assertMachineHTTPError(t, response, http.StatusForbidden, "scope_denied")
	}

	expired := fixture.issueToken(t, "expired")
	if _, err := fixture.store.DB().Exec(`UPDATE access_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).Unix(), expired.ID); err != nil {
		t.Fatal(err)
	}
	revoked := fixture.issueToken(t, "revoked")
	if err := fixture.service.RevokeToken(context.Background(), fixture.users["admin"], fixture.identity.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"expired": expired.Plaintext, "revoked": revoked.Plaintext} {
		t.Run(name, func(t *testing.T) {
			response := fixture.bearerRead(t, token, machineConfigPath)
			assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
		})
	}
	disabled := fixture.issueToken(t, "disabled")
	if _, err := fixture.service.UpdateIdentity(context.Background(), fixture.users["admin"], fixture.identity.ID, machineaccess.UpdateIdentityInput{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	response := fixture.bearerRead(t, disabled.Plaintext, machineConfigPath)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
}

func TestAuthorizationIsRejectedOnEveryNonMachineReadSurface(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	for _, test := range []struct{ method, path, body string }{
		{method: http.MethodPut, path: machineConfigPath, body: `{"base_revision":1,"entries":[]}`},
		{method: http.MethodGet, path: machineHistoryPath},
		{method: http.MethodGet, path: machineHistoryPath + "/1"},
		{method: http.MethodPost, path: machineHistoryPath + "/1/rollback", body: `{"message":"no"}`},
		{method: http.MethodGet, path: "/api/v1/projects"},
		{method: http.MethodPost, path: "/api/v1/projects", body: `{"slug":"no","name":"No"}`},
		{method: http.MethodGet, path: "/api/v1/machine-identities"},
		{method: http.MethodPost, path: "/api/v1/machine-identities", body: `{"name":"no","enabled":true}`},
		{method: http.MethodPost, path: "/api/v1/auth/logout"},
		{method: http.MethodPatch, path: "/api/v1/unknown"},
	} {
		request := fixture.request(t, "admin", test.method, test.path, test.body)
		request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
		response := fixture.serve(t, request)
		assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
	}
}

func TestMachineRoutesOptionalFallbackAndAllowContracts(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	withoutMachine, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(fixture.store), Sessions: auth.NewSessionManager(fixture.store, []byte("abcdefghijklmnopqrstuvwxyz123456"), time.Hour),
		Projects: projects.NewService(fixture.store), Revisions: revisions.NewService(fixture.store),
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ method, path string }{
		{method: http.MethodGet, path: "/api/v1/machine-identities"},
		{method: http.MethodPost, path: "/api/v1/machine-identities"},
		{method: http.MethodPut, path: "/api/v1/machine-identities/id/grants"},
		{method: http.MethodPost, path: "/api/v1/machine-identities/id/tokens"},
	} {
		response := httptest.NewRecorder()
		withoutMachine.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		assertMachineHTTPError(t, response, http.StatusNotFound, "not_found")
		if response.Header().Get("Allow") != "" {
			t.Fatalf("disabled route advertised Allow=%q", response.Header().Get("Allow"))
		}
	}
	request := httptest.NewRequest(http.MethodGet, machineConfigPath, nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token.Plaintext)
	response := httptest.NewRecorder()
	withoutMachine.ServeHTTP(response, request)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")

	for _, test := range []struct{ method, path, allow string }{
		{method: http.MethodPatch, path: "/api/v1/machine-identities", allow: "GET, POST"},
		{method: http.MethodPost, path: "/api/v1/machine-identities/id", allow: "GET, PUT"},
		{method: http.MethodGet, path: "/api/v1/machine-identities/id/grants", allow: http.MethodPut},
		{method: http.MethodGet, path: "/api/v1/machine-identities/id/tokens", allow: http.MethodPost},
		{method: http.MethodPost, path: "/api/v1/machine-identities/id/tokens/token", allow: http.MethodDelete},
	} {
		response := fixture.serve(t, httptest.NewRequest(test.method, test.path, nil))
		assertMachineHTTPError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		if response.Header().Get("Allow") != test.allow {
			t.Fatalf("%s %s Allow=%q want=%q", test.method, test.path, response.Header().Get("Allow"), test.allow)
		}
	}
}

func TestMachineAccessLogsRedactBearerAndConfiguration(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	fixture.logs.Reset()
	response := fixture.bearerRead(t, fixture.token.Plaintext, machineConfigPath)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	logs := fixture.logs.String()
	for _, secret := range []string{fixture.token.Plaintext, "postgres://config-secret", "Authorization", "Bearer"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs)
		}
	}
	if !strings.Contains(logs, `route="GET /api/v1/projects/{project}/environments/{environment}/config"`) {
		t.Fatalf("safe route missing: %s", logs)
	}

	fixture.logs.Reset()
	response = fixture.bearerRead(t, fixture.token.Plaintext+"leak", machineConfigPath)
	assertMachineHTTPError(t, response, http.StatusUnauthorized, "invalid_token")
	if strings.Contains(response.Body.String(), fixture.token.Plaintext) || strings.Contains(fixture.logs.String(), fixture.token.Plaintext) {
		t.Fatalf("invalid token leaked: body=%s logs=%s", response.Body.String(), fixture.logs.String())
	}
}

func TestMachineHTTPCorruptSnapshotReturnsSafeInternalError(t *testing.T) {
	fixture := newMachineHTTPFixture(t)
	if _, err := fixture.store.DB().Exec(`INSERT INTO revisions (id, environment_id, version, created_by, created_at)
		VALUES ('http-corrupt-revision', 'shop-production', 2, 'admin-id', 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`INSERT INTO revision_entries (revision_id, key, value)
		VALUES ('http-corrupt-revision', 'SECRET', CAST('http-corrupt-secret' || X'FF' AS TEXT))`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.DB().Exec(`UPDATE environments SET current_revision_id = 'http-corrupt-revision' WHERE id = 'shop-production'`); err != nil {
		t.Fatal(err)
	}
	fixture.logs.Reset()

	response := fixture.bearerRead(t, fixture.token.Plaintext, machineConfigPath)
	assertMachineHTTPError(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), "http-corrupt-secret") || strings.Contains(fixture.logs.String(), "http-corrupt-secret") {
		t.Fatalf("corrupt snapshot leaked secret: body=%s logs=%s", response.Body.String(), fixture.logs.String())
	}
}

func newMachineHTTPFixture(t *testing.T) *machineHTTPFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "database", "machine-http.db")
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	users := map[string]auth.User{
		"admin":  {ID: "admin-id", Username: "admin", DisplayName: "Admin", Role: "admin", Enabled: true},
		"member": {ID: "member-id", Username: "member", DisplayName: "Member", Role: "member", Enabled: true},
	}
	for _, user := range users {
		if _, err := store.DB().Exec(`INSERT INTO users (id, username, display_name, password_hash, role, enabled, created_at, updated_at)
			VALUES (?, ?, ?, 'hash', ?, 1, 1, 1)`, user.ID, user.Username, user.DisplayName, user.Role); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO projects (id, slug, name, created_by, created_at, updated_at) VALUES ('shop-project', 'shop', 'Shop', 'admin-id', 1, 1), ('other-project', 'other', 'Other', 'admin-id', 1, 1)`,
		`INSERT INTO environments (id, project_id, slug, name, created_at, updated_at) VALUES ('shop-production', 'shop-project', 'production', 'Production', 1, 1), ('shop-staging', 'shop-project', 'staging', 'Staging', 1, 1), ('other-production', 'other-project', 'production', 'Production', 1, 1)`,
		`INSERT INTO project_members (project_id, user_id, permission) VALUES ('shop-project', 'member-id', 'viewer')`,
	} {
		if _, err := store.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	revisionService := revisions.NewService(store)
	if _, err := revisionService.ReplaceForProject(context.Background(), users["admin"], "shop", "production", revisions.ReplaceInput{Entries: []revisions.Entry{
		{Key: "DATABASE_URL", Value: "postgres://config-secret", Service: "api"},
		{Key: "PORT", Value: "8080"},
	}}); err != nil {
		t.Fatal(err)
	}
	machineService := machineaccess.NewService(store)
	identity, err := machineService.CreateIdentity(context.Background(), users["admin"], machineaccess.CreateIdentity{Name: "shop-ci", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := machineService.ReplaceGrants(context.Background(), users["admin"], identity.ID, []machineaccess.EnvironmentGrant{{ProjectID: "shop-project", EnvironmentID: "shop-production"}}); err != nil {
		t.Fatal(err)
	}
	token, err := machineService.IssueToken(context.Background(), users["admin"], identity.ID, machineaccess.IssueToken{Name: "fixture", ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour)
	fixture := &machineHTTPFixture{
		store: store, path: path, service: machineService, users: users, cookies: make(map[string]*http.Cookie), csrf: make(map[string]string),
		logs: new(bytes.Buffer), identity: identity, token: token,
	}
	for username, user := range users {
		issued, err := sessions.Create(context.Background(), user)
		if err != nil {
			t.Fatal(err)
		}
		fixture.cookies[username] = &http.Cookie{Name: SessionCookieName, Value: issued.CookieValue}
		fixture.csrf[username] = issued.CSRFToken
	}
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(store), Sessions: sessions, Projects: projects.NewService(store), Revisions: revisionService, Machines: machineService,
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(fixture.logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	fixture.handler = handler
	return fixture
}

func (f *machineHTTPFixture) request(t *testing.T, username, method, path, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie := f.cookies[username]; cookie != nil {
		request.AddCookie(cookie)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete {
		request.Header.Set("Origin", testOrigin)
		request.Header.Set(CSRFHeaderName, f.csrf[username])
	}
	return request
}

func (f *machineHTTPFixture) serve(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control=%q status=%d body=%s", response.Header().Get("Cache-Control"), response.Code, response.Body.String())
	}
	return response
}

func (f *machineHTTPFixture) bearerRead(t *testing.T, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return f.serve(t, request)
}

func (f *machineHTTPFixture) issueToken(t *testing.T, name string) machineaccess.IssuedToken {
	t.Helper()
	token, err := f.service.IssueToken(context.Background(), f.users["admin"], f.identity.ID, machineaccess.IssueToken{Name: name, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func assertMachineHTTPError(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus || responseErrorCode(t, response) != wantCode || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func lockMachineHTTPDatabase(t *testing.T, path string) {
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
	if _, err := transaction.Exec(`INSERT INTO machine_identities (id, name, enabled, created_at, updated_at) VALUES ('machine-http-lock', 'machine-http-lock', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
}
