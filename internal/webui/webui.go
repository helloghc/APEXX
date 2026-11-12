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
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
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
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + p
		if p == "index.html" {
			r2.URL.Path = "/" // FileServer redirects explicit /index.html requests
		}
		files.ServeHTTP(w, r2)
	})
}
