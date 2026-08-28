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
		"index.html":                 {Data: []byte("<!doctype html><title>ConfigHub Test</title>")},
		"assets/index-DrZZUHo-.js":   {Data: []byte("console.log('ok')")},
		"assets/styles-Cm_X9ONV.css": {Data: []byte("body{}")},
		"assets/app-production.js":   {Data: []byte("console.log('production')")},
		"assets/app-B38A_N6.js":      {Data: []byte("console.log('short')")},
		"assets/app-B38A_N6xx.js":    {Data: []byte("console.log('long')")},
		"assets/app.B38A_N6x.js":     {Data: []byte("console.log('dot')")},
		"assets/plain.js":            {Data: []byte("console.log('plain')")},
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

	for _, path := range []string{"/assets/index-DrZZUHo-.js", "/assets/styles-Cm_X9ONV.css"} {
		response := serveWeb(t, handler, path)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("%s status=%d headers=%v", path, response.Code, response.Header())
		}
	}
	for _, path := range []string{
		"/assets/plain.js",
		"/assets/app-production.js",
		"/assets/app-B38A_N6.js",
		"/assets/app-B38A_N6xx.js",
		"/assets/app.B38A_N6x.js",
	} {
		response := serveWeb(t, handler, path)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") == "public, max-age=31536000, immutable" {
			t.Fatalf("%s status=%d headers=%v", path, response.Code, response.Header())
		}
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
