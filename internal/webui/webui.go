// Package webui serves the builder editor (web/) embedded in the binary.
//
// Mount it under a prefix with StripPrefix, for example:
//
//	mux.Handle("/admin/", http.StripPrefix("/admin", webui.Handler()))
//
// The editor calls the API with absolute paths (/v1/...), so it works under any prefix.
// Build the assets with `cd web && npm ci && npm run build`; until then a placeholder page is served.
package webui

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

//go:embed all:dist
var dist embed.FS

// csp allows the preview iframe (srcdoc inherits this policy) to show inline styles
// and remote images, but no scripts other than the editor's own bundle. Apps'
// panel pages load in https iframes the panel sandboxes (no same-origin access).
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; font-src 'self' data:; connect-src 'self'; frame-src 'self' https:; " +
	"frame-ancestors 'none'; base-uri 'none'; object-src 'none'; form-action 'self'"

// Handler serves the editor with SPA fallback to index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory exists
	}
	files := http.FileServer(http.FS(sub))
	zipped := &packed{fsys: sub}
	go zipped.warm()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			if path.Ext(p) != "" {
				http.NotFound(w, r) // a missing asset is a real 404, not the app shell
				return
			}
			p = "index.html" // client-side route
		}
		if strings.HasPrefix(p, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable") // hashed file names
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		if strings.HasPrefix(p, "assets/") && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			if b := zipped.get(p); b != nil {
				ct := mime.TypeByExtension(path.Ext(p))
				if ct == "" {
					ct = "application/octet-stream"
				}
				h.Set("Content-Type", ct)
				h.Set("Content-Encoding", "gzip")
				h.Set("Content-Length", strconv.Itoa(len(b)))
				h.Set("Vary", "Accept-Encoding")
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				_, _ = w.Write(b)
				return
			}
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		if p == "index.html" {
			r2.URL.Path = "/" // FileServer redirects explicit /index.html requests
		}
		files.ServeHTTP(w, r2)
	})
}

// packed keeps each text asset compressed once, at the highest level, instead
// of compressing it again on every request (the main bundle took 7 ms of CPU
// per request and went out 16 % larger).
type packed struct {
	fsys fs.FS
	m    sync.Map // path -> []byte (nil: not worth compressing)
}

// compressible lists the asset types that shrink; fonts and images are
// already compressed.
func compressible(p string) bool {
	switch path.Ext(p) {
	case ".js", ".css", ".svg", ".json", ".html", ".txt", ".map":
		return true
	}
	return false
}

func (z *packed) get(p string) []byte {
	if v, ok := z.m.Load(p); ok {
		return v.([]byte)
	}
	var out []byte
	if compressible(p) {
		if raw, err := fs.ReadFile(z.fsys, p); err == nil {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
			_, _ = zw.Write(raw)
			if zw.Close() == nil {
				out = buf.Bytes()
			}
		}
	}
	z.m.Store(p, out)
	return out
}

// warm compresses every asset in the background at start, so no person
// waits for it.
func (z *packed) warm() {
	_ = fs.WalkDir(z.fsys, "assets", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			z.get(p)
		}
		return nil
	})
}
