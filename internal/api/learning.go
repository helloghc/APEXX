package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/engage"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/recs"
)

// Recommendations and learning search. The storefront reports, without
// knowing who the shopper is, which products were seen together in a visit
// and which product was chosen after a search. Counts are kept in memory,
// saved in batches and, together with the paid orders, turned into an index
// per store every few seconds. Shoppers never wait for any of it.

const aiDocKind, aiDocID = "ai", "config"

// aiConfig is what the merchant decides about the store's learning features.
type aiConfig struct {
	RecsOff bool     `json:"recsOff,omitempty"` // no "also bought" on product pages and cart
	Exclude []string `json:"exclude,omitempty"` // never recommended
	MCP     bool     `json:"mcp,omitempty"`     // AI assistants may use the store (off until turned on)
	// SemanticOff stops vectors for this store: search stays lexical.
	SemanticOff bool `json:"semanticOff,omitempty"`
	// BudgetUSD caps what paid AI providers may cost this store per month
	// (0: the default of 10 USD). Local embeddings cost nothing.
	BudgetUSD float64 `json:"budgetUsd,omitempty"`
}

const defaultAIBudgetUSD = 10

func (c aiConfig) budgetMicros() int64 {
	b := c.BudgetUSD
	if b <= 0 {
		b = defaultAIBudgetUSD
	}
	return int64(b * 1e6)
}

type tenantLearn struct {
	recs   *recs.Index
	boosts map[string]map[string]int // term -> product -> boost
	clicks map[string]map[string][2]int
	cfg    aiConfig
}

type learnState struct {
	mu     sync.Mutex
	views  map[string]map[recs.Pair]int
	clicks map[string]map[[2]string][2]int // tenant -> {term, product} -> {clicks, carts}
	dirty  map[string]bool
	since  time.Time // orders changed after this are not in the index yet
	idx    sync.Map  // tenant -> *tenantLearn
}

func (s *Server) routesLearning(handle func(string, http.HandlerFunc)) {
	handle("POST /s/{tenant}/api/views", s.shopViews)
	handle("POST /s/{tenant}/api/search-click", s.shopSearchClick)
	handle("GET /s/{tenant}/api/recommendations", s.shopRecommendations)
	handle("GET /v1/ai/settings", s.authed("catalog", auth.Read, noStorefront(s.getAISettings)))
	handle("PUT /v1/ai/settings", s.authed("catalog", auth.Admin, s.putAISettings))
	handle("GET /v1/products/{id}/recommendations", s.authed("catalog", auth.Read, noStorefront(s.productRecs)))
	handle("GET /v1/search/learned", s.authed("catalog", auth.Read, noStorefront(s.searchLearned)))
}

func (s *Server) learned(tenant string) *tenantLearn {
	if v, ok := s.learn.idx.Load(tenant); ok {
		return v.(*tenantLearn)
	}
	s.markLearn(tenant)
	return nil
}

func (s *Server) markLearn(tenant string) {
	s.learn.mu.Lock()
	if s.learn.dirty == nil {
		s.learn.dirty = map[string]bool{}
	}
	s.learn.dirty[tenant] = true
	s.learn.mu.Unlock()
}

// learnedBoost is how much each product rises for a search term.
func (s *Server) learnedBoost(tenant, q string) map[string]int {
	l := s.learned(tenant)
	if l == nil {
		return nil
	}
	return l.boosts[engage.Fold(q)]
}

// boostOf turns clicks and add-to-carts into a rise in the ranking: it grows
// slowly and stops at 40, so it reorders matching products but never beats a
// product the merchant pinned (1000).
func boostOf(clicks, carts int) int {
	return min(int(math.Round(6*math.Log1p(float64(clicks+3*carts)))), 40)
}

// ---------- storefront ----------

func (s *Server) shopViews(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, suggestLimiter) {
		return
	}
	var in struct {
		P    string   `json:"p"`
		Seen []string `json:"seen"`
	}
	if !decode(w, r, &in) {
		return
	}
	tenant := r.PathValue("tenant")
	t, _ := s.Proj.Tenant(tenant)
	id, ok := t.Catalog.IDByHandle(in.P)
	if !ok || len(in.Seen) > 8 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.learn.mu.Lock()
	if s.learn.views == nil {
		s.learn.views = map[string]map[recs.Pair]int{}
	}
	m := s.learn.views[tenant]
	if m == nil {
		m = map[recs.Pair]int{}
		s.learn.views[tenant] = m
	}
	for _, h := range in.Seen {
		if o, ok := t.Catalog.IDByHandle(h); ok && o != id && len(m) < 20000 {
			m[recs.Of(id, o)]++
		}
	}
	s.learn.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopSearchClick(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, suggestLimiter) {
		return
	}
	var in struct {
		Q    string `json:"q"`
		P    string `json:"p"`
		Kind string `json:"kind"` // click | cart
	}
	if !decode(w, r, &in) {
		return
	}
	tenant := r.PathValue("tenant")
	t, _ := s.Proj.Tenant(tenant)
	term := engage.Fold(in.Q)
	id, ok := t.Catalog.IDByHandle(in.P)
	if !ok || term == "" || utf8.RuneCountInString(term) > 80 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.learn.mu.Lock()
	if s.learn.clicks == nil {
		s.learn.clicks = map[string]map[[2]string][2]int{}
	}
	m := s.learn.clicks[tenant]
	if m == nil {
		m = map[[2]string][2]int{}
		s.learn.clicks[tenant] = m
	}
	k := [2]string{term, id}
	if _, seen := m[k]; seen || len(m) < 5000 {
		c := m[k]
		if in.Kind == "cart" {
			c[1]++
		} else {
			c[0]++
		}
		m[k] = c
	}
	s.learn.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// recsFor returns the products to show next to the given ones (a product
// page or a cart), best first, leaving out those listed in skip.
func (s *Server) recsFor(tenant string, ids []string, skip map[string]bool, limit int) []recs.Rec {
	l := s.learned(tenant)
	if l == nil || l.cfg.RecsOff {
		return nil
	}
	var out []recs.Rec
	for _, r := range l.recs.ForMany(ids, limit+len(skip)) {
		if !skip[r.ID] && len(out) < limit {
			out = append(out, r)
		}
	}
	if len(out) < limit {
		out = append(out, s.similarRecs(tenant, ids, skip, l.cfg.Exclude, out, limit-len(out))...)
	}
	return out
}

// similarRecs fills recommendations with the products closest in meaning,
// so a new product without sales or visits still shows related ones. They
// come after what shoppers chose together, never before.
func (s *Server) similarRecs(tenant string, ids []string, skip map[string]bool, exclude []string, have []recs.Rec, n int) []recs.Rec {
	if n <= 0 || len(ids) == 0 || !s.semActive(tenant) {
		return nil
	}
	x := s.loadedIndex(tenant)
	c, ok := x.Centroid(ids)
	if !ok {
		return nil
	}
	no := map[string]bool{}
	for _, id := range ids {
		no[id] = true
	}
	for _, id := range exclude {
		no[id] = true
	}
	for _, r := range have {
		no[r.ID] = true
	}
	t, _ := s.Proj.Tenant(tenant)
	hits, err := x.Search(c, n+len(no)+8, func(id string) bool {
		if no[id] || skip[id] {
			return false
		}
		if t == nil {
			return true
		}
		p, err := t.Catalog.Get(id)
		return err == nil && p.Status == "active"
	})
	if err != nil {
		return nil
	}
	var out []recs.Rec
	for _, h := range hits {
		if h.Score < semanticFloor || len(out) >= n {
			break
		}
		out = append(out, recs.Rec{ID: h.ID, Score: h.Score})
	}
	return out
}

func (s *Server) shopRecommendations(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, suggestLimiter) {
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	pd := pageData{Tenant: t.ID, Base: "/s/" + t.ID + "/" + siteParam(r), Channel: "mx"}
	var ids []string
	for _, h := range strings.Split(r.URL.Query().Get("p"), ",") {
		if id, ok := t.Catalog.IDByHandle(strings.TrimSpace(h)); ok && len(ids) < 20 {
			ids = append(ids, id)
		}
	}
	for _, sku := range strings.Split(r.URL.Query().Get("sku"), ",") {
		if _, p, err := t.Catalog.VariantBySKU(strings.TrimSpace(sku)); err == nil && len(ids) < 20 {
			ids = append(ids, p.ID)
		}
	}
	of := offerOf(t, pd.Channel)
	cards := []cardJSON{}
	for _, rec := range s.recsFor(t.ID, ids, nil, 4) {
		if p, err := t.Catalog.Get(rec.ID); err == nil && p.Status == "active" {
			cards = append(cards, cardJSONOf(t, pd, &p, of(&p)))
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": cards})
}

// ---------- index ----------

// RunLearning saves what the storefront reported and rebuilds the index of
// every store whose orders, views or settings changed. It returns how many
// stores were rebuilt.
func (s *Server) RunLearning(ctx context.Context) (int, error) {
	s.learn.mu.Lock()
	views, clicks := s.learn.views, s.learn.clicks
	s.learn.views, s.learn.clicks = nil, nil
	dirty := s.learn.dirty
	s.learn.dirty = nil
	since := s.learn.since
	s.learn.mu.Unlock()
	if dirty == nil {
		dirty = map[string]bool{}
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for tenant, m := range views {
		dirty[tenant] = true
		keep(s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			for p, n := range m {
				if _, err := tx.Exec(ctx, `INSERT INTO product_coviews (tenant_id, a, b, views) VALUES ($1,$2,$3,$4)
					ON CONFLICT (tenant_id, a, b) DO UPDATE SET views=product_coviews.views+EXCLUDED.views, updated_at=now()`, tenant, p.A, p.B, n); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	for tenant, m := range clicks {
		dirty[tenant] = true
		keep(s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			for k, c := range m {
				if _, err := tx.Exec(ctx, `INSERT INTO search_clicks (tenant_id, term, product_id, clicks, carts) VALUES ($1,$2,$3,$4,$5)
					ON CONFLICT (tenant_id, term, product_id) DO UPDATE SET clicks=search_clicks.clicks+EXCLUDED.clicks, carts=search_clicks.carts+EXCLUDED.carts, updated_at=now()`,
					tenant, k[0], k[1], c[0], c[1]); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	// Stores with orders paid, cancelled or refunded since the last round.
	now := time.Now()
	if !since.IsZero() {
		rows, err := s.DB.System.Query(ctx, `SELECT DISTINCT tenant_id FROM orders WHERE updated_at > $1`, since.Add(-2*time.Second))
		if err == nil {
			for rows.Next() {
				var t string
				if rows.Scan(&t) == nil {
					dirty[t] = true
				}
			}
			rows.Close()
		}
		keep(err)
	}
	s.learn.mu.Lock()
	s.learn.since = now
	s.learn.mu.Unlock()
	n := 0
	for tenant := range dirty {
		if err := s.buildLearn(ctx, tenant); err != nil {
			keep(err)
			s.markLearn(tenant)
			continue
		}
		n++
	}
	return n, first
}

func (s *Server) buildLearn(ctx context.Context, tenant string) error {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return nil
	}
	var cfg aiConfig
	if _, err := s.DB.GetDoc(ctx, tenant, aiDocKind, aiDocID, &cfg); err != nil {
		return err
	}
	excluded := map[string]bool{}
	for _, id := range cfg.Exclude {
		excluded[id] = true
	}
	buys, views := map[recs.Pair]int{}, map[recs.Pair]int{}
	clicks := map[string]map[string][2]int{}
	orders := 0
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// Orders that were paid and not cancelled or refunded, last year.
		rows, err := tx.Query(ctx, `SELECT l.order_id, l.sku FROM order_lines l JOIN orders o ON o.tenant_id=l.tenant_id AND o.id=l.order_id
			WHERE o.tenant_id=$1 AND o.status IN ('paid','fulfilled') AND o.created_at > now() - interval '365 days'
			ORDER BY l.order_id LIMIT 500000`, tenant)
		if err != nil {
			return err
		}
		cur, basket := "", []string{}
		flush := func() {
			if len(basket) > 1 {
				recs.Basket(basket, buys)
			}
			if cur != "" {
				orders++
			}
		}
		for rows.Next() {
			var oid, sku string
			if err := rows.Scan(&oid, &sku); err != nil {
				rows.Close()
				return err
			}
			if oid != cur {
				flush()
				cur, basket = oid, basket[:0]
			}
			if _, p, err := t.Catalog.VariantBySKU(sku); err == nil {
				basket = append(basket, p.ID)
			}
		}
		flush()
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT a, b, views FROM product_coviews WHERE tenant_id=$1 AND updated_at > now() - interval '180 days'`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a, b string
			var n int
			if err := rows.Scan(&a, &b, &n); err != nil {
				rows.Close()
				return err
			}
			views[recs.Of(a, b)] += n
		}
		rows.Close()
		rows, err = tx.Query(ctx, `SELECT term, product_id, clicks, carts FROM search_clicks WHERE tenant_id=$1 AND updated_at > now() - interval '90 days'`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var term, id string
			var c [2]int
			if err := rows.Scan(&term, &id, &c[0], &c[1]); err != nil {
				return err
			}
			if clicks[term] == nil {
				clicks[term] = map[string][2]int{}
			}
			clicks[term][id] = c
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	active := func(id string) bool {
		if excluded[id] {
			return false
		}
		p, err := t.Catalog.Get(id)
		return err == nil && p.Status == "active"
	}
	idx := recs.Build(buys, views, active, 12)
	idx.Orders = orders
	boosts := map[string]map[string]int{}
	for term, m := range clicks {
		for id, c := range m {
			if b := boostOf(c[0], c[1]); b > 0 {
				if boosts[term] == nil {
					boosts[term] = map[string]int{}
				}
				boosts[term][id] = b
			}
		}
	}
	s.learn.idx.Store(tenant, &tenantLearn{recs: idx, boosts: boosts, clicks: clicks, cfg: cfg})
	return nil
}

// ---------- panel ----------

func (s *Server) getAISettings(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var cfg aiConfig
	if _, err := s.DB.GetDoc(r.Context(), p.Tenant, aiDocKind, aiDocID, &cfg); err != nil {
		internalError(w, s.Log, err)
		return
	}
	t, _ := s.Proj.Tenant(p.Tenant)
	out := map[string]any{"settings": cfg, "excluded": refsOf(t, cfg.Exclude), "mcpUrl": s.publicOrigin(r) + "/mcp/" + p.Tenant}
	out["semantic"] = s.semanticStatus(r.Context(), p.Tenant, t, cfg)
	if l := s.learned(p.Tenant); l != nil {
		out["index"] = map[string]any{"builtAt": l.recs.Built, "products": l.recs.Products(), "orders": l.recs.Orders, "terms": len(l.boosts)}
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) putAISettings(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var in aiConfig
	if !decode(w, r, &in) {
		return
	}
	t, _ := s.Proj.Tenant(p.Tenant)
	seen := map[string]bool{}
	var ex []string
	for _, id := range in.Exclude {
		if _, err := t.Catalog.Get(id); err != nil {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "exclude", Code: "not_found", Message: "El producto " + id + " no existe"})
			return
		}
		if !seen[id] {
			seen[id] = true
			ex = append(ex, id)
		}
	}
	if len(ex) > 500 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "exclude", Code: "too_many", Message: "Hasta 500 productos excluidos"})
		return
	}
	in.Exclude = ex
	if in.BudgetUSD < 0 || in.BudgetUSD > 100000 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "budgetUsd", Code: "out_of_range", Message: "El presupuesto mensual va de 0 a 100 000 USD"})
		return
	}
	if err := s.DB.SetDoc(r.Context(), p.Tenant, p.Actor(), aiDocKind, aiDocID, "", in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	// The index picks the change up now, not on the next round.
	if err := s.buildLearn(r.Context(), p.Tenant); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"settings": in, "excluded": refsOf(t, in.Exclude)})
}

// productRecs shows the merchant what a product page recommends and why.
func (s *Server) productRecs(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	id := r.PathValue("id")
	if _, err := t.Catalog.Get(id); err != nil {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	type row struct {
		productRef
		Buys  int     `json:"buys"`
		Views int     `json:"views"`
		Score float64 `json:"score"`
	}
	items := []row{}
	l := s.learned(p.Tenant)
	if l != nil {
		for _, rec := range l.recs.For(id) {
			ref := refsOf(t, []string{rec.ID})[0]
			items = append(items, row{productRef: ref, Buys: rec.Buys, Views: rec.Views, Score: rec.Score})
		}
	}
	off := l != nil && l.cfg.RecsOff
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "off": off, "ready": l != nil})
}

// searchLearned shows how shoppers' choices reorder a search term.
func (s *Server) searchLearned(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	term := engage.Fold(r.URL.Query().Get("q"))
	type row struct {
		productRef
		Clicks int `json:"clicks"`
		Carts  int `json:"carts"`
		Boost  int `json:"boost"`
	}
	items := []row{}
	if l := s.learned(p.Tenant); l != nil {
		for id, c := range l.clicks[term] {
			items = append(items, row{productRef: refsOf(t, []string{id})[0], Clicks: c[0], Carts: c[1], Boost: l.boosts[term][id]})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Boost != items[j].Boost {
			return items[i].Boost > items[j].Boost
		}
		return items[i].ID < items[j].ID
	})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"term": term, "items": items, "learning": !t.Search().NoLearning})
}
