package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/pricing"
)

// CatalogData adapts catalog and pricing to the builder's Data interface.
type CatalogData struct {
	Catalog *catalog.Store
	Pricing *pricing.Service
	Channel string
}

// Products returns the first limit products as cards.
func (d CatalogData) Products(limit int) []builder.Card {
	ps, _ := d.Catalog.List("", limit)
	out := make([]builder.Card, 0, len(ps))
	for _, p := range ps {
		c := builder.Card{Title: p.Title, URL: "/p/" + p.Handle}
		if len(p.Variants) > 0 {
			if q, ok := d.Pricing.Resolve(d.Channel, p.Variants[0].SKU, 1); ok {
				c.Price = fmt.Sprintf("$%d.%02d %s", q.Unit.Amount/100, q.Unit.Amount%100, q.Unit.Currency)
			}
		}
		out = append(out, c)
	}
	return out
}

func (s *Server) builderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/builder/sections", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"items": s.Library.Definitions()})
	})
	mux.HandleFunc("GET /v1/sites/{site}/templates/{handle}/draft", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		t, ver, err := site.Draft(r.PathValue("handle"))
		if err != nil {
			apierr.NotFound(w, "Plantilla no encontrada")
			return
		}
		httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "version": ver})
	}))
	mux.HandleFunc("PATCH /v1/sites/{site}/templates/{handle}/draft", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		var body struct {
			Ops       []builder.Op `json:"ops"`
			IfVersion int64        `json:"ifVersion"`
		}
		if err := httpx.Decode(r, &body); err != nil || len(body.Ops) == 0 {
			apierr.BadRequest(w, "Envía {\"ops\": [...]} con operaciones JSON Patch")
			return
		}
		t, rep, ver, err := site.Patch(r.PathValue("handle"), body.Ops, body.IfVersion)
		switch {
		case errors.Is(err, builder.ErrNotFound):
			apierr.NotFound(w, "Plantilla no encontrada")
		case errors.Is(err, builder.ErrStaleDraft):
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Otra persona cambió el borrador; recarga y vuelve a intentar"})
		case err != nil:
			apierr.BadRequest(w, err.Error())
		default:
			httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "report": rep, "version": ver})
		}
	}))
	mux.HandleFunc("GET /v1/sites/{site}/templates/{handle}/preview", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		html, _, err := site.Preview(r.PathValue("handle"))
		if err != nil {
			apierr.NotFound(w, "Plantilla no encontrada")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(html)
	}))
	mux.HandleFunc("POST /v1/sites/{site}/publish", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		rel, reports, err := site.Publish(actor(r), r.URL.Query().Get("message"))
		if errors.Is(err, builder.ErrBlocked) {
			w.Header().Set("Content-Type", "application/problem+json")
			httpx.JSON(w, r, http.StatusUnprocessableEntity, map[string]any{
				"type": "https://docs.apexx.dev/errors/publish_blocked", "title": "Publicación bloqueada",
				"status": http.StatusUnprocessableEntity, "code": "publish_blocked",
				"detail": "Hay problemas que impiden publicar; revisa reports", "reports": reports,
			})
			return
		}
		httpx.JSON(w, r, http.StatusOK, map[string]any{"release": rel, "reports": reports})
	}))
	mux.HandleFunc("POST /v1/sites/{site}/rollback", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		rel, err := site.Rollback(actor(r))
		if err != nil {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "No hay una publicación anterior"})
			return
		}
		httpx.JSON(w, r, http.StatusOK, map[string]any{"release": rel})
	}))
	mux.HandleFunc("GET /v1/sites/{site}/releases", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"items": site.Releases()})
	}))
	// Storefront: published static HTML, cacheable at the edge and purged by revision.
	mux.HandleFunc("GET /s/{site}/{handle}", s.withSite(func(w http.ResponseWriter, r *http.Request, site *builder.Site) {
		html, rev, err := site.Live(r.PathValue("handle"))
		if err != nil {
			apierr.NotFound(w, "Página no publicada")
			return
		}
		etag := `"` + rev + `"`
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=31536000, stale-while-revalidate=60")
		w.Header().Set("Surrogate-Key", "site-"+site.ID+" "+rev)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(html)
	}))
}

func (s *Server) withSite(h func(http.ResponseWriter, *http.Request, *builder.Site)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		site, ok := s.Sites[r.PathValue("site")]
		if !ok {
			apierr.NotFound(w, "Sitio no encontrado")
			return
		}
		h(w, r, site)
	}
}

// actor identifies who made a change. Until auth lands it reads a header.
func actor(r *http.Request) string {
	if a := r.Header.Get("X-Apexx-Actor"); a != "" {
		return a
	}
	return "anonymous"
}
