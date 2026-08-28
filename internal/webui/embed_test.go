package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetsContainBootstrapFile(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(assets, "bootstrap.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ConfigHub web assets are replaced by the Vite build.\n" {
		t.Fatalf("unexpected bootstrap asset: %q", data)
	}
}

func TestAssetsAlwaysContainBootstrapIndex(t *testing.T) {
	assets, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ConfigHub") {
		t.Fatalf("unexpected bootstrap index: %q", data)
	}
}

func TestHandlerFallsBackOnlyForSPARoutesAndSetsCachePolicy(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":                     {Data: []byte("<!doctype html><title>ConfigHub Test</title>")},
		"assets/app-abcdef1234567890.js": {Data: []byte("console.log('ok')")},
		"assets/plain.js":                {Data: []byte("console.log('plain')")},
	}
	handler := NewHandler(assets)

	for _, path := range []string{"/", "/index.html", "/projects/shop"} {
		response := serveWeb(t, handler, path)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ConfigHub Test") {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s Cache-Control=%q", path, got)
		}
	}

	response := serveWeb(t, handler, "/assets/app-abcdef1234567890.js")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("hashed status=%d headers=%v", response.Code, response.Header())
	}
	response = serveWeb(t, handler, "/assets/plain.js")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") == "public, max-age=31536000, immutable" {
		t.Fatalf("plain status=%d headers=%v", response.Code, response.Header())
	}

	for _, path := range []string{"/assets/missing.js", "/api/v1/missing", "/api"} {
		response := serveWeb(t, handler, path)
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "ConfigHub Test") {
			t.Fatalf("%s status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func serveWeb(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}
