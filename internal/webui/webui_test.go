package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexAndSPAFallback(t *testing.T) {
	for _, p := range []string{"/", "/index.html", "/editor/deep/link"} {
		rec := get(t, p)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: %d %q", p, rec.Code, rec.Body.String()[:min(80, rec.Body.Len())])
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: index must not be cached long", p)
		}
		if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s: missing CSP", p)
		}
	}
}

func TestMissingAssetIs404(t *testing.T) {
	if rec := get(t, "/assets/nope.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestNoPathTraversal(t *testing.T) {
	rec := get(t, "/../../go.mod")
	if strings.Contains(rec.Body.String(), "module ") {
		t.Fatal("served a file outside dist")
	}
}

func TestMountedUnderPrefix(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/admin/", http.StripPrefix("/admin", Handler()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
}
