package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// tenantData adapts a tenant's read models to builder sections.
type tenantData struct {
	proj   *projection.Projector
	tenant string
}

func (d tenantData) Products(limit int) []builder.Card {
	return d.Query(builder.ProductQuery{Limit: limit})
}

// Query implements builder.Querier: active products, optionally filtered by
// brand or category, as cards with price, sale price and stock state.
func (d tenantData) Query(q builder.ProductQuery) []builder.Card {
	t, ok := d.proj.Tenant(d.tenant)
	if !ok || q.Limit <= 0 {
		return nil
	}
	out := make([]builder.Card, 0, q.Limit)
	after := ""
	for len(out) < q.Limit {
		ps, next := t.Catalog.Search("", after, 100)
		for _, p := range ps {
			if q.Brand != "" && !strings.EqualFold(p.Brand, q.Brand) {
				continue
			}
			if q.Category != "" && !containsFold(p.Categories, q.Category) && !inCategory(t, &p, q.Category) {
				continue
			}
			c := cardOf(t, "mx", p)
			out = append(out, builder.Card{Title: p.Title, URL: "p/" + p.Handle, Price: c.Price, CompareAt: c.CompareAt,
				Brand: p.Brand, Initial: c.Initial, Tone: c.Tone, SKU: c.SKU, SoldOut: c.SoldOut, Image: coverHTML(t, &p, true)})
			if len(out) == q.Limit {
				break
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	return out
}

// inCategory reports whether p is in the category named by ref (id, handle or
// name) or in one of its subcategories.
func inCategory(t *projection.Tenant, p *catalog.Product, ref string) bool {
	id, ok := t.Taxo.ResolveCategory(ref)
	return ok && t.Taxo.ProductCategories(p)[id]
}

func containsFold(xs []string, x string) bool {
	for _, v := range xs {
		if strings.EqualFold(v, x) {
			return true
		}
	}
	return false
}

// DefaultHome is the template a new site starts from.
var DefaultHome func() builder.Template

// errNoSite is returned by openSite for sites that were never created.
var errNoSite = errors.New("api: site does not exist")

// site returns (loading or creating) a builder site of tenant.
func (s *Server) site(tenant, id string) (*builder.Site, error) {
	return s.loadSite(tenant, id, true)
}

// openSite loads an existing site; public routes never create sites.
func (s *Server) openSite(tenant, id string) (*builder.Site, error) {
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/ .") {
		return nil, errNoSite
	}
	return s.loadSite(tenant, id, false)
}

func (s *Server) loadSite(tenant, id string, create bool) (*builder.Site, error) {
	key := tenant + "/" + id
	s.sitesMu.Lock()
	defer s.sitesMu.Unlock()
	if st, ok := s.sites[key]; ok {
		return st, nil
	}
	per := store.SitePersister{DB: s.DB, Tenant: tenant, Site: id}
	drafts, ver, revs, rels, found, err := per.LoadSite()
	if err != nil {
		return nil, err
	}
	if !found && !create {
		return nil, errNoSite
	}
	st := builder.NewSite(id, s.Library, tenantData{proj: s.Proj, tenant: tenant})
	if !found {
		if err := per.Ensure(id); err != nil {
			return nil, err
		}
		if DefaultHome != nil {
			t := DefaultHome()
			if err := per.SaveDraft(t.Handle, t, 1); err != nil {
				return nil, err
			}
			drafts, ver = map[string]builder.Template{t.Handle: t}, 1
		}
	}
	st.Restore(drafts, ver, revs, rels)
	st.SetPersister(per)
	var sched []builder.SiteSchedule
	if err := s.DB.Tx(context.Background(), tenant, func(tx pgx.Tx) error {
		var e error
		sched, e = loadSchedulesTx(context.Background(), tx, tenant, id)
		return e
	}); err != nil {
		return nil, err
	}
	st.SetSchedules(sched)
	s.sites[key] = st
	return st, nil
}

func (s *Server) withSite(w http.ResponseWriter, r *http.Request) (*builder.Site, bool) {
	id := r.PathValue("site")
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/ ") {
		apierr.NotFound(w, "Sitio no encontrado")
		return nil, false
	}
	st, err := s.site(principal(r).Tenant, id)
	if err != nil {
		internalError(w, s.Log, err)
		return nil, false
	}
	return st, true
}

// themes lists the storefront themes and the bundled fonts.
func (s *Server) themes(w http.ResponseWriter, r *http.Request) {
	starters := map[string]builder.Template{}
	for _, t := range builder.ThemeList() {
		starters[t.ID] = builder.Starter(t.ID)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": builder.ThemeList(), "fonts": builder.FontList(),
		"tokens": builder.ColorTokens, "contrastPairs": builder.ContrastPairs, "starters": starters})
}

// applyTheme replaces the home draft with the theme's starter template. The
// previous draft is kept as a revision so the change can be undone.
func (s *Server) applyTheme(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	var body struct {
		ThemeID string `json:"themeId"`
		Keep    bool   `json:"keepContent"` // only switch the look, keep sections
	}
	if !decode(w, r, &body) {
		return
	}
	if _, ok := builder.Themes[body.ThemeID]; !ok {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "themeId", Code: "unknown", Message: "Tema desconocido"})
		return
	}
	tpl := builder.Starter(body.ThemeID)
	if body.Keep {
		if cur, _, err := st.Draft("index"); err == nil {
			cur.Theme = &builder.ThemeSettings{ID: body.ThemeID}
			tpl = cur
		}
	}
	prev, ver, err := st.ApplyStarter("index", tpl, principal(r).Actor())
	if errors.Is(err, builder.ErrStaleDraft) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Otra persona cambió el borrador; recarga y vuelve a intentar"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	t, _, _ := st.Draft("index")
	_, rep, _ := st.Preview("index")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "version": ver, "report": rep, "previousRevisionId": prev})
}

// restoreRevision copies a revision back into the draft (undo a theme change).
func (s *Server) restoreRevision(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	var body struct {
		RevisionID string `json:"revisionId"`
	}
	if !decode(w, r, &body) {
		return
	}
	ver, err := st.RestoreRevision(body.RevisionID)
	switch {
	case errors.Is(err, builder.ErrNotFound):
		apierr.NotFound(w, "Revisión no encontrada")
	case errors.Is(err, builder.ErrStaleDraft):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Otra persona cambió el borrador; recarga y vuelve a intentar"})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		t, _, _ := st.Draft("index")
		httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "version": ver})
	}
}

func (s *Server) sections(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": s.Library.Definitions()})
}

func (s *Server) getDraft(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	t, ver, err := st.Draft(r.PathValue("handle"))
	if err != nil {
		apierr.NotFound(w, "Plantilla no encontrada")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "version": ver})
}

func (s *Server) patchDraft(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	var body struct {
		Ops       []builder.Op `json:"ops"`
		IfVersion int64        `json:"ifVersion"`
	}
	if err := httpx.Decode(r, &body); err != nil || len(body.Ops) == 0 {
		apierr.BadRequest(w, "Envía {\"ops\": [...]} con operaciones JSON Patch")
		return
	}
	t, rep, ver, err := st.Patch(r.PathValue("handle"), body.Ops, body.IfVersion)
	switch {
	case errors.Is(err, builder.ErrNotFound):
		apierr.NotFound(w, "Plantilla no encontrada")
	case errors.Is(err, builder.ErrStaleDraft):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Otra persona cambió el borrador; recarga y vuelve a intentar"})
	case err != nil && strings.Contains(err.Error(), "save draft"):
		internalError(w, s.Log, err)
	case err != nil:
		apierr.BadRequest(w, err.Error())
	default:
		httpx.JSON(w, r, http.StatusOK, map[string]any{"template": t, "report": rep, "version": ver})
	}
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	html, _, err := st.Preview(r.PathValue("handle"))
	if err != nil {
		apierr.NotFound(w, "Plantilla no encontrada")
		return
	}
	// The preview runs in a sandboxed iframe without scripts: drop the storefront
	// script and allow the theme's stylesheets and fonts.
	html = []byte(strings.Replace(string(html), `<script src="/s/assets/shop.js?v=`+builder.AssetVersion+`" defer></script>`, "", 1))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src * data:; style-src 'self' 'unsafe-inline'; font-src 'self'")
	_, _ = w.Write(html)
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	rel, reports, err := st.Publish(principal(r).Actor(), r.URL.Query().Get("message"))
	if errors.Is(err, builder.ErrBlocked) {
		w.Header().Set("Content-Type", "application/problem+json")
		httpx.JSON(w, r, http.StatusUnprocessableEntity, map[string]any{
			"type": "https://docs.apexx.dev/errors/publish_blocked", "title": "Publicación bloqueada",
			"status": http.StatusUnprocessableEntity, "code": "publish_blocked",
			"detail": "Hay problemas que impiden publicar; revisa reports", "reports": reports,
		})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"release": rel, "reports": reports})
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	rel, err := st.Rollback(principal(r).Actor())
	if errors.Is(err, builder.ErrNoPrevious) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "No hay una publicación anterior"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"release": rel})
}

func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": st.Releases()})
}

// storefront serves the published HTML of a page. Public, cacheable at the
// edge and purged by revision id.
func (s *Server) storefront(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if _, ok := s.Proj.Tenant(tenant); !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	release, ok := s.Bulkhead.Acquire(tenant)
	if !ok {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	defer release()
	st, err := s.openSite(tenant, r.PathValue("site"))
	if errors.Is(err, errNoSite) {
		if _, pd, ok := s.shopContext(w, r); ok {
			s.shopNotFound(w, pd, "Página")
		}
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	html, rev, err := st.Live(r.PathValue("handle"))
	previewAt, preview := s.designPreviewAt(r, tenant)
	if preview {
		html, rev, err = st.LiveAt(r.PathValue("handle"), previewAt)
	}
	if err != nil {
		// A shopper who mistypes an address gets the store's own page with a
		// way forward, not an API error.
		if _, pd, ok := s.shopContext(w, r); ok {
			s.shopNotFound(w, pd, "Página")
		}
		return
	}
	etag := `"` + rev + `"`
	h := w.Header()
	cache := "public, max-age=0, s-maxage=31536000, stale-while-revalidate=60"
	// A scheduled version changes the page without a publish: caches keep it a minute at most.
	if pendingSchedule(st.Schedules()) {
		cache = "public, max-age=0, s-maxage=60, stale-while-revalidate=30"
	}
	if preview {
		cache, etag = "no-store", `"preview-`+rev+`"`
		h.Set("X-Robots-Tag", "noindex")
	}
	// Navigation menus are store configuration, not part of the revision:
	// they are placed at serve time so a menu change shows without publishing.
	if t, ok := s.Proj.Tenant(tenant); ok && bytes.Contains(html, []byte(`data-menu="`)) {
		site := t.Site()
		if len(site.Menus) > 0 {
			html = applyMenus(html, site, siteBase(r))
			etag = `"` + rev + "-" + site.Version + `"`
			cache = "public, max-age=0, s-maxage=60, stale-while-revalidate=60"
		}
	}
	h.Set("ETag", etag)
	h.Set("Cache-Control", cache)
	h.Set("Surrogate-Key", "site-"+tenant+" "+rev)
	h.Set("Content-Security-Policy", s.shopCSPFor(tenant, true))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(html)
}

// ---------- payments ----------

func (s *Server) getPaymentAccount(w http.ResponseWriter, r *http.Request) {
	a, err := s.DB.GetPaymentAccount(r.Context(), principal(r).Tenant)
	if errors.Is(err, store.ErrNotFound) {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"connected": false})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"connected": true, "mode": a.Mode, "accountId": a.AccountID, "publishableKey": a.PublishableKey,
		"hasSecretKey": len(a.SecretEnc) > 0, "hasWebhookSecret": len(a.WebhookEnc) > 0, "feeBps": a.FeeBps,
	})
}

func (s *Server) putPaymentAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode           string `json:"mode"`
		AccountID      string `json:"accountId"`
		PublishableKey string `json:"publishableKey"`
		SecretKey      string `json:"secretKey"`
		WebhookSecret  string `json:"webhookSecret"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	a := store.PaymentAccount{Mode: body.Mode, PublishableKey: body.PublishableKey, FeeBps: s.Payments.PlatformFeeBps}
	switch body.Mode {
	case "connect":
		if !strings.HasPrefix(body.AccountID, "acct_") {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "accountId", Code: "invalid", Message: "Usa el id acct_… que devuelve Stripe Connect"})
			return
		}
		a.AccountID = body.AccountID
	case "own_keys":
		prev, perr := s.DB.GetPaymentAccount(r.Context(), p.Tenant)
		if body.SecretKey == "" && perr == nil && prev.Mode == "own_keys" && len(prev.SecretEnc) > 0 {
			a.SecretEnc, a.FeeBps = prev.SecretEnc, 0 // keep the stored key; only other fields change
			if body.WebhookSecret == "" {
				a.WebhookEnc = prev.WebhookEnc
			}
			if body.PublishableKey == "" {
				a.PublishableKey = prev.PublishableKey
			}
			break
		}
		if !strings.HasPrefix(body.SecretKey, "sk_") && !strings.HasPrefix(body.SecretKey, "rk_") {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "secretKey", Code: "invalid", Message: "Usa una llave secreta sk_… o restringida rk_… de tu cuenta Stripe"})
			return
		}
		enc, err := s.Keys.Encrypt(p.Tenant, []byte(body.SecretKey), "stripe-secret")
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		a.SecretEnc = enc
		a.FeeBps = 0
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "mode", Code: "invalid", Message: "mode debe ser connect u own_keys"})
		return
	}
	if body.Mode == "connect" && body.WebhookSecret == "" {
		if prev, err := s.DB.GetPaymentAccount(r.Context(), p.Tenant); err == nil {
			a.WebhookEnc = prev.WebhookEnc
		}
	}
	if body.WebhookSecret != "" {
		enc, err := s.Keys.Encrypt(p.Tenant, []byte(body.WebhookSecret), "stripe-webhook")
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		a.WebhookEnc = enc
	}
	if err := s.DB.SetPaymentAccount(r.Context(), p.Tenant, p.Actor(), a); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.Payments.Forget(p.Tenant)
	s.getPaymentAccount(w, r)
}

func (s *Server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	secret, err := s.Payments.WebhookSecret(r.Context(), tenant)
	if err != nil || secret == "" {
		http.Error(w, "webhook not configured", http.StatusBadRequest)
		return
	}
	if err := payments.VerifyWebhook(payload, r.Header.Get("Stripe-Signature"), secret, timeNow()); err != nil {
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			Object struct {
				Metadata map[string]string `json:"metadata"`
			} `json:"object"`
		} `json:"data"`
	}
	_ = jsonUnmarshal(payload, &ev)
	if order := ev.Data.Object.Metadata["apexx_order"]; order != "" &&
		(ev.Type == "payment_intent.succeeded" || ev.Type == "payment_intent.payment_failed") {
		if err := s.Orders.ResolvePending(r.Context(), tenant, order); err != nil {
			s.Log.Warn("stripe webhook resolve", "tenant", tenant, "order", order, "err", err)
			http.Error(w, "retry later", http.StatusServiceUnavailable) // Stripe retries
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}
