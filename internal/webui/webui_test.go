package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/httpx"
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

// Assets go out compressed ahead of time, identical to the file, through the
// compression middleware without being compressed twice.
func TestAssetsPrecompressed(t *testing.T) {
	ents, err := fs.ReadDir(dist, "dist/assets")
	if err != nil {
		t.Skip("no built assets")
	}
	name := ""
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".js") {
			name = e.Name()
			break
		}
	}
	if name == "" {
		t.Skip("no built js")
	}
	raw, _ := fs.ReadFile(dist, "dist/assets/"+name)
	srv := httptest.NewServer(httpx.Middleware(Handler()))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/assets/"+name, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultTransport.RoundTrip(req) // no transparent decompression
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("headers: %v", res.Header)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("body differs from the file (err %v, %d vs %d bytes)", err, len(got), len(raw))
	}
	// Without gzip the plain file is served.
	req.Header.Del("Accept-Encoding")
	res2, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(res2.Body)
	res2.Body.Close()
	if res2.Header.Get("Content-Encoding") != "" || !bytes.Equal(b2, raw) {
		t.Fatalf("plain: %q %d bytes", res2.Header.Get("Content-Encoding"), len(b2))
	}
}
