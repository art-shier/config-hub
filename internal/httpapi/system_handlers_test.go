package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"confighub.local/internal/auth"
)

func TestSystemHealthReflectsLiveAndReadyState(t *testing.T) {
	status := new(testSystemStatus)
	handler := systemTestRouter(t, status, nil)

	assertSystemStatus(t, handler, "/api/v1/health/live", http.StatusServiceUnavailable)
	assertSystemStatus(t, handler, "/api/v1/health/ready", http.StatusServiceUnavailable)
	status.live.Store(true)
	assertSystemStatus(t, handler, "/api/v1/health/live", http.StatusOK)
	assertSystemStatus(t, handler, "/api/v1/health/ready", http.StatusServiceUnavailable)
	status.ready.Store(true)
	assertSystemStatus(t, handler, "/api/v1/health/ready", http.StatusOK)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/health/ready", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method status=%d Allow=%q body=%s", response.Code, response.Header().Get("Allow"), response.Body.String())
	}
}

func TestRouterSendsOnlyNonAPIPathsToWebUI(t *testing.T) {
	web := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(299) })
	handler := systemTestRouter(t, healthyTestSystemStatus(), web)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/projects/shop", nil))
	if response.Code != 299 {
		t.Fatalf("SPA status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if response.Code != http.StatusNotFound || response.Code == 299 {
		t.Fatalf("API status=%d body=%s", response.Code, response.Body.String())
	}
}

type testSystemStatus struct {
	live  atomic.Bool
	ready atomic.Bool
}

func healthyTestSystemStatus() *testSystemStatus {
	status := new(testSystemStatus)
	status.live.Store(true)
	status.ready.Store(true)
	return status
}

func (s *testSystemStatus) Live() bool  { return s.live.Load() }
func (s *testSystemStatus) Ready() bool { return s.ready.Load() }

type rejectingCredentials struct{}

func (rejectingCredentials) Verify(context.Context, string, string) (auth.VerifiedCredential, error) {
	return auth.VerifiedCredential{}, auth.ErrInvalidCredentials
}

func systemTestRouter(t *testing.T, status SystemStatus, web http.Handler) http.Handler {
	t.Helper()
	handler, err := NewRouter(Dependencies{
		Credentials: rejectingCredentials{},
		Sessions:    auth.NewSessionManager(nil, []byte("01234567890123456789012345678901"), time.Hour),
		System:      status,
	}, Options{PublicOrigin: testOrigin, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), WebUI: web})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertSystemStatus(t *testing.T, handler http.Handler, path string, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != want || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
	}
}
