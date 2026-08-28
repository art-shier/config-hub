package httpapi

import (
	"bytes"
	"context"
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
)

const testOrigin = "https://config.example"

func TestLoginCreatesSecureBrowserSession(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value == "" || cookie.Path != "/" {
		t.Fatalf("cookie=%+v", cookie)
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe cookie=%+v", cookie)
	}
	if cookie.MaxAge <= 0 || cookie.Expires.IsZero() {
		t.Fatalf("cookie expiry missing: %+v", cookie)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q", got)
	}
	if response.Header().Get("X-Request-ID") == "" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers=%v", response.Header())
	}
	var payload struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		CSRFToken string    `json:"csrf_token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.User.Username != "admin" || payload.CSRFToken == "" || payload.ExpiresAt.IsZero() {
		t.Fatalf("payload=%+v", payload)
	}
}

func TestStrictJSONBodyLimitAndStableErrors(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	for _, test := range []struct {
		name, contentType, body string
		wantStatus              int
		wantCode                string
	}{
		{name: "unknown field", contentType: "application/json", body: `{"username":"admin","password":"secret","extra":true}`, wantStatus: 400, wantCode: "malformed_request"},
		{name: "multiple documents", contentType: "application/json", body: `{"username":"admin","password":"secret"} {}`, wantStatus: 400, wantCode: "malformed_request"},
		{name: "wrong content type", contentType: "text/plain", body: `{"username":"admin","password":"secret"}`, wantStatus: 400, wantCode: "malformed_request"},
		{name: "oversized", contentType: "application/json", body: strings.Repeat(" ", int(maxRequestBodyBytes)+1), wantStatus: 413, wantCode: "request_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(test.body))
			request.Header.Set("Origin", testOrigin)
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("X-Request-ID", "req_attacker_controlled")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || responseErrorCode(t, response) != test.wantCode {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("X-Request-ID") == "req_attacker_controlled" {
				t.Fatal("trusted attacker-supplied request ID")
			}
		})
	}
}

func TestDeclaredOversizedBodyIsRejectedBeforeRouteChecks(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{}"))
	request.ContentLength = maxRequestBodyBytes + 1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || responseErrorCode(t, response) != "request_too_large" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestPanicRecoverySecurityHeadersAndRedactedLogs(t *testing.T) {
	const secret = "DO_NOT_LOG_secret_password_token"
	handler, _, logs := testRouter(t, func(options *Options) {
		options.Register = func(mux *http.ServeMux) {
			mux.HandleFunc("GET /api/v1/test/panic", func(http.ResponseWriter, *http.Request) { panic(secret) })
		}
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/test/panic?password="+secret, nil)
	request.RemoteAddr = "198.51.100.7:4321"
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Cookie", "confighub_session="+secret)
	request.Header.Set(CSRFHeaderName, secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || responseErrorCode(t, response) != "internal_error" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for name, want := range map[string]string{
		"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
		"Content-Security-Policy": "frame-ancestors 'none'", "Referrer-Policy": "no-referrer",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s=%q want=%q", name, got, want)
		}
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "password=") || strings.Contains(logs.String(), "Authorization") || strings.Contains(logs.String(), "Cookie") || strings.Contains(logs.String(), CSRFHeaderName) {
		t.Fatalf("logs leaked secret material: %s", logs.String())
	}
	if !strings.Contains(logs.String(), `route="GET /api/v1/test/panic"`) || !strings.Contains(logs.String(), "status=500") || !strings.Contains(logs.String(), "source_ip=198.51.100.7") {
		t.Fatalf("safe access fields missing: %s", logs.String())
	}
}

func TestTrustedProxySourceIP(t *testing.T) {
	handler, _, logs := testRouter(t, func(options *Options) {
		options.TrustedProxyCIDRs = []string{"127.0.0.0/8", "10.0.0.0/8"}
	})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	request.RemoteAddr = "198.51.100.8:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.20")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), "source_ip=198.51.100.8") || strings.Contains(logs.String(), "source_ip=203.0.113.20") {
		t.Fatalf("untrusted proxy spoof accepted: %s", logs.String())
	}
	logs.Reset()
	request = httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.20, 10.1.2.3")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if !strings.Contains(logs.String(), "source_ip=203.0.113.20") || strings.Contains(logs.String(), "203.0.113.20, 10.1.2.3") {
		t.Fatalf("trusted proxy source incorrect/raw XFF logged: %s", logs.String())
	}
}

func TestLoginRateLimitUsesSourceAndLowercaseUsernameWithClockSeam(t *testing.T) {
	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	handler, _, _ := testRouter(t, func(options *Options) {
		options.Now = func() time.Time { return now }
		options.RateLimit = RateLimitOptions{Capacity: 2, RefillInterval: time.Minute, MaxEntries: 32}
	})
	for index, username := range []string{"missing", "MISSING"} {
		request := loginRequestFor(t, username, "wrong", "198.51.100.9:1234")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt=%d status=%d body=%s", index, response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequestFor(t, "Missing", "wrong", "198.51.100.9:1234"))
	if response.Code != http.StatusTooManyRequests || responseErrorCode(t, response) != "rate_limited" {
		t.Fatalf("rate status=%d body=%s", response.Code, response.Body.String())
	}
	// A different trusted source has an independent bucket.
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequestFor(t, "missing", "wrong", "198.51.100.10:1234"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("independent source status=%d", response.Code)
	}
	now = now.Add(time.Minute)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, loginRequestFor(t, "missing", "wrong", "198.51.100.9:1234"))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("refilled status=%d body=%s", response.Code, response.Body.String())
	}
}

func loginRequestFor(t *testing.T, username, password, remoteAddr string) *http.Request {
	t.Helper()
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.RemoteAddr = remoteAddr
	request.Header.Set("Origin", testOrigin)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestLoginRejectsWrongPasswordAndDisabledAccountUniformly(t *testing.T) {
	handler, store, _ := testRouter(t, nil)
	wantCode := loginErrorCode(t, handler, "admin", "wrong")
	if wantCode != "invalid_credentials" {
		t.Fatalf("code=%q", wantCode)
	}
	if _, err := store.DB().Exec(`UPDATE users SET enabled = 0 WHERE username = 'admin'`); err != nil {
		t.Fatal(err)
	}
	if got := loginErrorCode(t, handler, "admin", "secret"); got != wantCode {
		t.Fatalf("disabled code=%q want=%q", got, wantCode)
	}
	if got := loginErrorCode(t, handler, "missing", "secret"); got != wantCode {
		t.Fatalf("missing code=%q want=%q", got, wantCode)
	}
}

func TestSessionBootstrapAndLogoutSecurity(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	cookie, csrf := loginSession(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bootstrap status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var bootstrap sessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.User.Username != "admin" || bootstrap.CSRFToken != csrf {
		t.Fatalf("bootstrap=%+v", bootstrap)
	}

	for _, test := range []struct {
		name, origin, token string
		cookie              bool
		wantStatus          int
		wantCode            string
	}{
		{name: "unauthenticated first", origin: "", token: "", cookie: false, wantStatus: http.StatusUnauthorized, wantCode: "invalid_session"},
		{name: "missing origin", origin: "", token: csrf, cookie: true, wantStatus: http.StatusForbidden, wantCode: "invalid_origin"},
		{name: "wrong csrf", origin: testOrigin, token: "wrong", cookie: true, wantStatus: http.StatusForbidden, wantCode: "invalid_csrf"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
			if test.cookie {
				request.AddCookie(cookie)
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.token != "" {
				request.Header.Set(CSRFHeaderName, test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || responseErrorCode(t, response) != test.wantCode {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.AddCookie(cookie)
	request.Header.Set("Origin", testOrigin)
	request.Header.Set(CSRFHeaderName, csrf)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", response.Code, response.Body.String())
	}
	deleted := response.Result().Cookies()
	if len(deleted) != 1 || deleted[0].Name != SessionCookieName || deleted[0].MaxAge >= 0 || !deleted[0].HttpOnly || !deleted[0].Secure || deleted[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("deletion cookie=%+v", deleted)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || responseErrorCode(t, response) != "invalid_session" {
		t.Fatalf("revoked status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLoginRequiresExactTrustedOrigin(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	for _, origin := range []string{"", "null", "http://config.example", "https://config.example.evil", "https://user@config.example", "https://config.example/", "https://config.example?x=y", "https://config.example#fragment"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || responseErrorCode(t, response) != "invalid_origin" {
			t.Fatalf("origin=%q status=%d body=%s", origin, response.Code, response.Body.String())
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Add("Origin", testOrigin)
	request.Header.Add("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || responseErrorCode(t, response) != "invalid_origin" {
		t.Fatalf("multiple Origin headers accepted: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSessionDatabaseFailureMapsToSafeInternalError(t *testing.T) {
	handler, store, logs := testRouter(t, nil)
	cookie, _ := loginSession(t, handler)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || responseErrorCode(t, response) != "internal_error" {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sql") || strings.Contains(response.Body.String(), "closed") || strings.Contains(logs.String(), cookie.Value) {
		t.Fatalf("operational details leaked: body=%s logs=%s", response.Body.String(), logs.String())
	}
}

func TestAPINotFoundAndMethodNotAllowedUseStableEnvelope(t *testing.T) {
	handler, _, _ := testRouter(t, nil)
	for _, test := range []struct {
		method, path, code string
		status             int
	}{
		{method: http.MethodGet, path: "/api/v1/missing?secret=do-not-log", status: http.StatusNotFound, code: "not_found"},
		{method: http.MethodGet, path: "/api/v1/auth/login", status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
	} {
		request := httptest.NewRequest(test.method, test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || responseErrorCode(t, response) != test.code {
			t.Fatalf("%s %s status=%d body=%s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func loginSession(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	var payload sessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return response.Result().Cookies()[0], payload.CSRFToken
}

func loginErrorCode(t *testing.T, handler http.Handler, username, password string) string {
	t.Helper()
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", testOrigin)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return responseErrorCode(t, response)
}

func responseErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.RequestID == "" || envelope.Error.RequestID != response.Header().Get("X-Request-ID") || envelope.Error.Fields == nil {
		t.Fatalf("unstable error envelope=%+v headers=%v", envelope, response.Header())
	}
	return envelope.Error.Code
}

func testRouter(t *testing.T, configure func(*Options)) (http.Handler, *database.Store, *bytes.Buffer) {
	t.Helper()
	store, err := database.Open(filepath.Join(t.TempDir(), "config-hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	file := auth.UserFile{Users: []auth.UserSpec{{Username: "admin", DisplayName: "Admin", Password: "secret", Role: "admin", Enabled: true}}}
	if _, err := auth.NewUserSyncer(store).Sync(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	logs := new(bytes.Buffer)
	options := Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(logs, nil))}
	if configure != nil {
		configure(&options)
	}
	handler, err := NewRouter(Dependencies{
		Credentials: auth.NewCredentialService(store),
		Sessions:    auth.NewSessionManager(store, []byte("01234567890123456789012345678901"), time.Hour),
	}, options)
	if err != nil {
		t.Fatal(err)
	}
	return handler, store, logs
}
