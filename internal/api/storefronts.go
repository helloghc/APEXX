package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Storefronts: a store can run several sites (a brand, an outlet, a
// wholesale front), each with its own design and, optionally, its own
// market. They share catalog, stock, customers and orders. A new one can
// start as a copy of another's design.

type storefront struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Market    string            `json:"market"`
	Live      bool              `json:"live"`
	CreatedAt time.Time         `json:"createdAt"`
	Domains   []storefrontHosts `json:"domains"`
	URL       string            `json:"url"`
}

type storefrontHosts struct {
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
	Market   string `json:"market"`
}

var storefrontID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}$`)

const maxStorefronts = 20

func (s *Server) routesStorefronts(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/storefronts", s.authed("builder", auth.Read, noStorefront(s.listStorefronts)))
	handle("POST /v1/storefronts", s.authed("builder", auth.Admin, s.createStorefront))
	handle("PUT /v1/storefronts/{id}", s.authed("builder", auth.Admin, s.updateStorefront))
	handle("DELETE /v1/storefronts/{id}", s.authed("builder", auth.Admin, s.deleteStorefront))
	handle("PUT /v1/domains/{id}/market", s.authed("settings", auth.Admin, s.setDomainMarket))
}

// validMarket accepts "" (main channel) or a market the store sells in.
func (s *Server) validMarket(tenant, mk string) bool {
	if mk == "" || mk == mainChannel {
		return true
	}
	t, ok := s.Proj.Tenant(tenant)
	return ok && marketParam(t, mk) == mk
}

func (s *Server) listStorefronts(w http.ResponseWriter, r *http.Request) {
	s.writeStorefronts(w, r, http.StatusOK)
}

func (s *Server) writeStorefronts(w http.ResponseWriter, r *http.Request, status int) {
	t := principal(r).Tenant
	var out []storefront
	err := s.DB.Tx(r.Context(), t, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, market, live_revision IS NOT NULL, created_at FROM sites WHERE tenant_id=$1 AND archived_at IS NULL ORDER BY id <> 'main', created_at`, t)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (storefront, error) {
			var f storefront
			return f, row.Scan(&f.ID, &f.Name, &f.Market, &f.Live, &f.CreatedAt)
		})
		if err != nil {
			return err
		}
		rows, err = tx.Query(r.Context(), `SELECT site, hostname, status, market FROM domains WHERE tenant_id=$1 ORDER BY hostname`, t)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var site string
			var h storefrontHosts
			if err := rows.Scan(&site, &h.Hostname, &h.Status, &h.Market); err != nil {
				return err
			}
			for i := range out {
				if out[i].ID == site {
					out[i].Domains = append(out[i].Domains, h)
				}
			}
		}
		return rows.Err()
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for i := range out {
		out[i].URL = "/s/" + t + "/" + out[i].ID + "/index"
		if out[i].Domains == nil {
			out[i].Domains = []storefrontHosts{}
		}
	}
	if out == nil {
		out = []storefront{}
	}
	httpx.JSON(w, r, status, map[string]any{"items": out, "max": maxStorefronts})
}

func (s *Server) createStorefront(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Market   string `json:"market"`
		CopyFrom string `json:"copyFrom"`
	}
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	in.ID, in.Name = strings.TrimSpace(in.ID), strings.TrimSpace(in.Name)
	if in.Market == mainChannel {
		in.Market = ""
	}
	var errs []apierr.FieldError
	if !storefrontID.MatchString(in.ID) || in.ID == "api" || in.ID == "assets" || in.ID == "fonts" {
		errs = append(errs, apierr.FieldError{Field: "id", Code: "invalid", Message: "Usa de 2 a 31 letras minúsculas, números o guiones"})
	}
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "name", Code: "invalid", Message: "Ponle un nombre de hasta 80 caracteres"})
	}
	if !s.validMarket(pr.Tenant, in.Market) {
		errs = append(errs, apierr.FieldError{Field: "market", Code: "unknown", Message: "Ese mercado no está activo en tu tienda"})
	}
	if in.CopyFrom != "" {
		if _, err := s.openSite(pr.Tenant, in.CopyFrom); err != nil {
			errs = append(errs, apierr.FieldError{Field: "copyFrom", Code: "unknown", Message: "La tienda a copiar no existe"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	var exists bool
	var count int
	if err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM sites WHERE tenant_id=$1 AND id=$2), (SELECT count(*) FROM sites WHERE tenant_id=$1 AND archived_at IS NULL)`, pr.Tenant, in.ID).Scan(&exists, &count)
	}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if exists {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "exists", Detail: "Ese identificador ya se usó en otra tienda (activa o archivada); elige otro"})
		return
	}
	if count >= maxStorefronts {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "limit", Detail: "Puedes tener hasta 20 tiendas"})
		return
	}
	if in.CopyFrom == "" {
		if _, err := s.loadSite(pr.Tenant, in.ID, true); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if in.CopyFrom != "" {
			// The copy starts as drafts of the source's current design.
			if _, err := tx.Exec(r.Context(), `INSERT INTO sites (tenant_id, id, name, draft_version) VALUES ($1,$2,$3,1)`, pr.Tenant, in.ID, in.Name); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO site_drafts (tenant_id, site_id, handle, template) SELECT tenant_id, $2, handle, template FROM site_drafts WHERE tenant_id=$1 AND site_id=$3`,
				pr.Tenant, in.ID, in.CopyFrom); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `UPDATE sites SET name=$3, market=$4 WHERE tenant_id=$1 AND id=$2`, pr.Tenant, in.ID, in.Name, in.Market); err != nil {
			return err
		}
		if err := store.Emit(r.Context(), tx, pr.Tenant, store.TopicSettings, store.KeyDomains, nil); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "storefront.create", in.ID, map[string]any{"name": in.Name, "market": in.Market, "copyFrom": in.CopyFrom})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.evictSite(pr.Tenant, in.ID)
	s.writeStorefronts(w, r, http.StatusCreated)
}

// evictSite drops the cached design of a site so the next read loads it again.
func (s *Server) evictSite(tenant, id string) {
	s.sitesMu.Lock()
	delete(s.sites, tenant+"/"+id)
	s.sitesMu.Unlock()
}

func (s *Server) updateStorefront(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Market string `json:"market"`
	}
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "name", Code: "invalid", Message: "Ponle un nombre de hasta 80 caracteres"})
		return
	}
	if !s.validMarket(pr.Tenant, in.Market) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "market", Code: "unknown", Message: "Ese mercado no está activo en tu tienda"})
		return
	}
	if in.Market == mainChannel {
		in.Market = ""
	}
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `UPDATE sites SET name=$3, market=$4 WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL`, pr.Tenant, r.PathValue("id"), in.Name, in.Market)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := store.Emit(r.Context(), tx, pr.Tenant, store.TopicSettings, store.KeyDomains, nil); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "storefront.update", r.PathValue("id"), map[string]any{"name": in.Name, "market": in.Market})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.listStorefronts(w, r)
}

func (s *Server) deleteStorefront(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	if id == "main" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "main", Detail: "La tienda principal no se puede borrar"})
		return
	}
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		var domains int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM domains WHERE tenant_id=$1 AND site=$2`, pr.Tenant, id).Scan(&domains); err != nil {
			return err
		}
		if domains > 0 {
			return errStorefrontInUse
		}
		ct, err := tx.Exec(r.Context(), `UPDATE sites SET archived_at=now(), market='' WHERE tenant_id=$1 AND id=$2 AND archived_at IS NULL`, pr.Tenant, id)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := store.Emit(r.Context(), tx, pr.Tenant, store.TopicSettings, store.KeyDomains, nil); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "storefront.delete", id, map[string]any{})
	})
	switch {
	case errors.Is(err, errStorefrontInUse):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_use", Detail: "Primero quita o mueve los dominios de esta tienda"})
		return
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Tienda no encontrada")
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	s.evictSite(pr.Tenant, id)
	w.WriteHeader(http.StatusNoContent)
}

var errStorefrontInUse = errors.New("storefront has domains")

func (s *Server) setDomainMarket(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Market string `json:"market"`
	}
	if !decode(w, r, &in) {
		return
	}
	pr := principal(r)
	if !s.validMarket(pr.Tenant, in.Market) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "market", Code: "unknown", Message: "Ese mercado no está activo en tu tienda"})
		return
	}
	if in.Market == mainChannel {
		in.Market = ""
	}
	if s.storeError(w, s.DB.SetDomainMarket(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), in.Market), "Dominio no encontrado") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
