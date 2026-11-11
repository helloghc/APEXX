package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// Master catalog: a store can take its products from another store of the
// same business and keep them up to date. The source store hands over an API
// key with read access to its catalog, so it decides who copies it and can
// cut it off by revoking the key. The copy brings the product content
// (names, descriptions, variants, SEO); prices, stock, categories, images and
// related products stay local, so each store adjusts them for its market.
// A product removed or archived at the source is archived in the copy, never
// deleted, so its orders and history stay intact.

const catalogSourceKind = "catalog_source"

type catalogSource struct {
	Source    string           `json:"source"`
	Key       []byte           `json:"key"`       // API key of the source, encrypted with this store's key
	NewPrices bool             `json:"newPrices"` // new products start with the source price
	Synced    map[string]int64 `json:"synced"`    // product id -> source version already copied
	LastRun   *time.Time       `json:"lastRun,omitempty"`
	LastError string           `json:"lastError,omitempty"`
	Copied    int              `json:"copied"` // products copied in the last run
	Failed    []string         `json:"failed,omitempty"`
}

var catalogSyncLocks sync.Map // tenant -> *sync.Mutex

func (s *Server) getCatalogSource(w http.ResponseWriter, r *http.Request) {
	var cs catalogSource
	found, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, catalogSourceKind, "config", &cs)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !found || cs.Source == "" {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"source": ""})
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"source": cs.Source, "newPrices": cs.NewPrices, "products": len(cs.Synced),
		"lastRun": cs.LastRun, "lastError": cs.LastError, "copied": cs.Copied, "failed": cs.Failed})
}

func (s *Server) putCatalogSource(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source    string `json:"source"`
		APIKey    string `json:"apiKey"`
		NewPrices bool   `json:"newPrices"`
	}
	if !decode(w, r, &body) {
		return
	}
	pr := principal(r)
	body.Source, body.APIKey = strings.TrimSpace(body.Source), strings.TrimSpace(body.APIKey)
	src, err := s.Auth.Authenticate(r.Context(), body.APIKey)
	switch {
	case body.Source == pr.Tenant:
		apierr.BadRequest(w, "", fe("source", "invalid", "Elige otra tienda: esta no puede copiarse a sí misma"))
		return
	case err != nil || src.Kind != "api_key" || src.Tenant != body.Source:
		apierr.BadRequest(w, "", fe("apiKey", "invalid", "La llave no es una llave de API vigente de esa tienda"))
		return
	case !src.Permissions.Allows("catalog", auth.Read) || src.Storefront:
		apierr.BadRequest(w, "", fe("apiKey", "forbidden", "La llave necesita permiso de lectura del catálogo"))
		return
	}
	enc, err := s.Keys.Encrypt(pr.Tenant, []byte(body.APIKey), catalogSourceKind)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var cs catalogSource
	if _, err := s.DB.GetDoc(r.Context(), pr.Tenant, catalogSourceKind, "config", &cs); err != nil {
		internalError(w, s.Log, err)
		return
	}
	if cs.Source != body.Source {
		cs = catalogSource{Synced: map[string]int64{}}
	}
	cs.Source, cs.Key, cs.NewPrices, cs.LastError = body.Source, enc, body.NewPrices, ""
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), catalogSourceKind, "config", "", cs); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.syncCatalog(r.Context(), pr.Tenant)
	s.getCatalogSource(w, r)
}

func (s *Server) deleteCatalogSource(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), catalogSourceKind, "config", "", catalogSource{}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) runCatalogSource(w http.ResponseWriter, r *http.Request) {
	s.syncCatalog(r.Context(), principal(r).Tenant)
	s.getCatalogSource(w, r)
}

// SyncCatalogs is the background round for every store with a source.
func (s *Server) SyncCatalogs(ctx context.Context) error {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id FROM tenant_docs WHERE kind=$1 AND id='config' AND data->>'source' <> ''`, catalogSourceKind)
	if err != nil {
		return err
	}
	var tenants []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			tenants = append(tenants, t)
		}
	}
	rows.Close()
	for _, t := range tenants {
		s.syncCatalog(ctx, t)
	}
	return rows.Err()
}

// syncCatalog copies what changed at the source since the last run.
func (s *Server) syncCatalog(ctx context.Context, tenant string) {
	mu, _ := catalogSyncLocks.LoadOrStore(tenant, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()

	var cs catalogSource
	if found, err := s.DB.GetDoc(ctx, tenant, catalogSourceKind, "config", &cs); err != nil || !found || cs.Source == "" {
		return
	}
	if cs.Synced == nil {
		cs.Synced = map[string]int64{}
	}
	now := time.Now().UTC()
	cs.LastRun, cs.LastError, cs.Copied, cs.Failed = &now, "", 0, nil
	save := func() { _ = s.DB.SetDoc(ctx, tenant, "system:catalog-sync", catalogSourceKind, "config", "", cs) }

	key, err := s.Keys.Decrypt(tenant, cs.Key, catalogSourceKind)
	if err != nil {
		cs.LastError = "No se pudo leer la llave guardada; vuelve a escribirla"
		save()
		return
	}
	// The source can stop the copy at any time by revoking the key.
	src, err := s.Auth.Authenticate(ctx, string(key))
	if err != nil || src.Tenant != cs.Source || !src.Permissions.Allows("catalog", auth.Read) {
		cs.LastError = "La tienda de origen revocó la llave o le quitó el permiso de catálogo"
		save()
		return
	}
	from, ok := s.Proj.Tenant(cs.Source)
	to, ok2 := s.Proj.Tenant(tenant)
	if !ok || !ok2 {
		cs.LastError = "La tienda de origen no está disponible"
		save()
		return
	}
	seen := map[string]bool{}
	after := ""
	for {
		ps, next := from.Catalog.Search("", after, 200)
		for _, p := range ps {
			seen[p.ID] = true
			if cs.Synced[p.ID] == p.Version {
				continue
			}
			if err := s.copyProduct(ctx, tenant, from, to, p, cs.NewPrices); err != nil {
				cs.Failed = append(cs.Failed, p.Title+": "+err.Error())
				continue
			}
			cs.Synced[p.ID] = p.Version
			cs.Copied++
		}
		if next == "" {
			break
		}
		after = next
	}
	for id := range cs.Synced {
		if seen[id] {
			continue
		}
		// Gone at the source: archive here, keep the history.
		if local, err := to.Catalog.Get(id); err == nil && local.Status != "archived" {
			local.Status = "archived"
			if _, err := s.DB.SaveProduct(ctx, tenant, "system:catalog-sync", store.ProductWrite{Product: local, Channel: mainChannel}, 0); err != nil {
				cs.Failed = append(cs.Failed, local.Title+": "+err.Error())
				continue
			}
		}
		delete(cs.Synced, id)
	}
	if len(cs.Failed) > 20 {
		cs.Failed = cs.Failed[:20]
	}
	save()
}

// copyProduct writes the source content over the local product, keeping
// what each store decides by itself.
func (s *Server) copyProduct(ctx context.Context, tenant string, from, to *projection.Tenant, p catalog.Product, newPrices bool) error {
	local, err := to.Catalog.Get(p.ID)
	exists := err == nil
	out := p
	out.Version, out.UpdatedAt, out.CreatedAt = 0, time.Time{}, time.Time{}
	// Local decisions: taxonomy, images and related products belong to each store.
	out.Categories, out.BrandID, out.Media, out.Related, out.Attributes = nil, "", nil, catalog.Related{}, nil
	for i := range out.Variants {
		out.Variants[i].MediaID = ""
	}
	if exists {
		out.Categories, out.BrandID, out.Media, out.Related, out.Attributes = local.Categories, local.BrandID, local.Media, local.Related, local.Attributes
		media := map[string]string{}
		for _, v := range local.Variants {
			media[v.SKU] = v.MediaID
		}
		for i := range out.Variants {
			out.Variants[i].MediaID = media[out.Variants[i].SKU]
		}
	}
	w := store.ProductWrite{Product: out, Channel: mainChannel, Create: !exists}
	if !exists && newPrices {
		w.Prices = map[string]store.PriceInput{}
		for _, v := range p.Variants {
			if q, ok := from.Pricing.Resolve(mainChannel, v.SKU, 1); ok {
				pi := store.PriceInput{Amount: q.Unit.Amount, Currency: string(q.Unit.Currency)}
				if q.CompareAt != nil {
					ca := q.CompareAt.Amount
					pi.CompareAt = &ca
				}
				w.Prices[v.SKU] = pi
			}
		}
	}
	_, err = s.DB.SaveProduct(ctx, tenant, "system:catalog-sync", w, 0)
	if errors.Is(err, store.ErrConflict) {
		return errSyncConflict
	}
	return err
}

var errSyncConflict = errors.New("la dirección o un SKU ya existe en otro producto de esta tienda")
