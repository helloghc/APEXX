package api

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/engage"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// routesEngage registers search merchandising, reviews and wish lists.
func (s *Server) routesEngage(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/search/config", s.staff("catalog", auth.Read, s.getSearchConfig))
	handle("PUT /v1/search/config", s.authed("catalog", auth.Edit, s.putSearchConfig))
	handle("GET /v1/search/terms", s.staff("catalog", auth.Read, s.searchTerms))
	handle("GET /v1/search/preview", s.staff("catalog", auth.Read, s.searchPreview))
	handle("GET /v1/reviews", s.staff("catalog", auth.Read, s.listReviews))
	handle("POST /v1/reviews/{id}/moderate", s.authed("catalog", auth.Edit, s.moderateReview))
	handle("DELETE /v1/reviews/{id}", s.authed("catalog", auth.Admin, s.deleteReview))
	handle("GET /v1/wishlists/top", s.staff("catalog", auth.Read, s.topWishes))

	handle("GET /s/{tenant}/api/suggest", s.shopSuggest)
	handle("POST /s/{tenant}/api/ask", s.shopAsk)
	handle("POST /s/{tenant}/api/assistant", s.shopAssistant)
	handle("POST /v1/ai/copilot", s.staff("", "", s.postCopilot))
	s.routesGraph(handle)
	s.routesCopilots(handle)
	s.routesPanelCopilots(handle)
	s.routesWrite(handle)
	handle("GET /v1/ai/eval", s.staff("catalog", auth.Read, s.getStoreEval))
	handle("POST /v1/ai/rfq", s.staff("orders", auth.Read, s.postRFQ))
	handle("GET /s/{tenant}/api/reviews/{handle}", s.shopReviews)
	handle("POST /s/{tenant}/api/reviews/{handle}", s.shopReviewCreate)
	handle("POST /s/{tenant}/api/cards", s.shopCards)
	handle("GET /s/{tenant}/api/wishlist", s.shopWishlist)
	handle("PUT /s/{tenant}/api/wishlist/{handle}", s.shopWishSet)
	handle("DELETE /s/{tenant}/api/wishlist/{handle}", s.shopWishSet)
	handle("GET /s/{tenant}/{site}/favoritos", s.shopFavorites)
	handle("GET /s/{tenant}/{site}/comparar", s.shopCompare)
}

var (
	suggestLimiter = &shopLimiter{n: 240} // keystrokes
	reviewLimiter  = &shopLimiter{n: 6}
)

// engageState is the in-memory side of search and reviews.
type engageState struct {
	mu      sync.Mutex
	terms   map[string]map[string]store.TermCount // tenant -> term -> counts not yet saved
	reviews sync.Map                              // tenant|product -> cachedReviews
}

type cachedReviews struct {
	at   time.Time
	data store.ProductReviews
}

// noteSearch remembers a search until the next flush.
func (s *Server) noteSearch(tenant, q string, results int) {
	term := engage.Fold(q)
	if term == "" || utf8.RuneCountInString(term) > 80 {
		return
	}
	s.engage.mu.Lock()
	defer s.engage.mu.Unlock()
	if s.engage.terms == nil {
		s.engage.terms = map[string]map[string]store.TermCount{}
	}
	m := s.engage.terms[tenant]
	if m == nil {
		m = map[string]store.TermCount{}
		s.engage.terms[tenant] = m
	}
	if len(m) >= 2000 { // a flood of distinct terms does not grow memory
		if _, ok := m[term]; !ok {
			return
		}
	}
	c := m[term]
	c.Term, c.Searches, c.Results = term, c.Searches+1, results
	m[term] = c
}

// FlushSearchTerms saves the searches noted since the last call.
func (s *Server) FlushSearchTerms(ctx context.Context) error {
	s.engage.mu.Lock()
	pending := s.engage.terms
	s.engage.terms = nil
	s.engage.mu.Unlock()
	var first error
	for tenant, m := range pending {
		list := make([]store.TermCount, 0, len(m))
		for _, c := range m {
			list = append(list, c)
		}
		if err := s.DB.AddSearchTerms(ctx, tenant, list); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// searchQuery applies the store's search rules to a browse query.
// narrow limits a text query to the products the word index says can match
// it, its synonyms or the pins, so Browse does not scan the whole catalog.
func narrow(t *projection.Tenant, q *catalog.BrowseQuery) {
	if q.Q == "" {
		return
	}
	only := t.Catalog.Matching(q.Q)
	if only == nil {
		return
	}
	for _, a := range q.Alt {
		for id := range t.Catalog.Matching(a) {
			only[id] = true
		}
	}
	for _, id := range q.Pinned {
		only[id] = true
	}
	q.Only = only
}

func (s *Server) searchQuery(t *projection.Tenant, q *catalog.BrowseQuery) {
	cfg := t.Search()
	q.Exclude = cfg.HiddenSet()
	if q.Q != "" {
		q.Alt = cfg.Expand(q.Q)
		if q.Sort == "" {
			q.Pinned = cfg.PinsFor(q.Q)
			// A synonym of a pinned term gets the same pinned products.
			for i := 0; len(q.Pinned) == 0 && i < len(q.Alt); i++ {
				q.Pinned = cfg.PinsFor(q.Alt[i])
			}
			if !cfg.NoLearning {
				q.Boost = s.learnedBoost(t.ID, q.Q)
			}
		}
	}
}

// ---------- admin: search ----------

type productRef struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Handle string `json:"handle"`
}

func refsOf(t *projection.Tenant, ids []string) []productRef {
	out := make([]productRef, 0, len(ids))
	for _, id := range ids {
		r := productRef{ID: id, Title: "(producto eliminado)"}
		if p, err := t.Catalog.Get(id); err == nil {
			r.Title, r.Handle = p.Title, p.Handle
		}
		out = append(out, r)
	}
	return out
}

func (s *Server) getSearchConfig(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	cfg := *t.Search()
	cfg.Normalize()
	ids := append([]string{}, cfg.Hidden...)
	for _, p := range cfg.Pins {
		ids = append(ids, p.ProductIDs...)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": cfg, "products": refsOf(t, ids)})
}

func (s *Server) putSearchConfig(w http.ResponseWriter, r *http.Request) {
	var cfg engage.SearchConfig
	if !decode(w, r, &cfg) {
		return
	}
	if errs := cfg.Normalize(); len(errs) > 0 {
		apierr.BadRequest(w, strings.Join(errs, ". "))
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "search", "config", store.KeySearch, cfg); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": cfg})
}

func (s *Server) searchTerms(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 365 {
		days = 30
	}
	_ = s.FlushSearchTerms(r.Context()) // what was just searched shows up at once
	tenant := principal(r).Tenant
	top, err := s.DB.SearchTerms(r.Context(), tenant, days, 50, false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	zero, err := s.DB.SearchTerms(r.Context(), tenant, days, 50, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type zeroRow struct {
		store.TermCount
		Suggest *termSuggestion `json:"suggest,omitempty"`
	}
	rows := make([]zeroRow, 0, len(zero))
	t, _ := s.Proj.Tenant(tenant)
	for _, z := range zero {
		rows = append(rows, zeroRow{TermCount: z, Suggest: s.suggestFor(t, z.Term)})
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"days": days, "top": nonNil(top), "noResults": rows})
}

// termSuggestion is what the store could do about a search without results:
// the products closest in meaning and a word of the catalog to make it a
// synonym of. The merchant decides; nothing changes on its own.
type termSuggestion struct {
	Synonym  string       `json:"synonym,omitempty"`
	Products []productRef `json:"products"`
}

func (s *Server) suggestFor(t *projection.Tenant, term string) *termSuggestion {
	if t == nil || !s.semActive(t.ID) {
		return nil
	}
	x := s.loadedIndex(t.ID)
	v := s.queryVector(term)
	if x == nil || v == nil {
		return nil
	}
	hits, err := x.Search(v, 5, func(id string) bool {
		p, err := t.Catalog.Get(id)
		return err == nil && p.Status == "active"
	})
	// The merchant reviews these, so the floor is lower than in search.
	floor := semanticFloor / 2
	if err != nil || len(hits) == 0 || hits[0].Score < floor {
		return nil
	}
	out := &termSuggestion{}
	votes := map[string]int{}
	best := ""
	for _, h := range hits {
		if h.Score < max(floor, hits[0].Score*semanticRel) {
			break
		}
		p, err := t.Catalog.Get(h.ID)
		if err != nil {
			continue
		}
		if len(out.Products) < 3 {
			out.Products = append(out.Products, productRef{ID: p.ID, Title: p.Title, Handle: p.Handle})
		}
		// The word the catalog uses for this kind of product.
		w := strings.ToLower(strings.TrimSpace(p.Type))
		if w == "" {
			if f := strings.Fields(p.Title); len(f) > 0 {
				w = strings.ToLower(f[0])
			}
		}
		if w != "" && engage.Fold(w) != engage.Fold(term) {
			votes[w]++
			if votes[w] > votes[best] {
				best = w
			}
		}
	}
	out.Synonym = best
	if len(out.Products) == 0 {
		return nil
	}
	return out
}

// searchPreview shows what a shopper would get for q with the saved rules.
func (s *Server) searchPreview(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	q := catalog.BrowseQuery{Q: r.URL.Query().Get("q"), Limit: 12}
	s.searchQuery(t, &q)
	res := catalog.Browse(t.Catalog.Products(), t.Taxo, offerOf(t, "mx"), q)
	items := make([]map[string]any, 0, len(res.Items))
	pinned := map[string]bool{}
	for _, id := range q.Pinned {
		pinned[id] = true
	}
	for _, p := range res.Items {
		items = append(items, map[string]any{"id": p.ID, "title": p.Title, "handle": p.Handle, "brand": p.Brand, "pinned": pinned[p.ID]})
	}
	redirect, _ := t.Search().RedirectFor(q.Q)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"total": res.Total, "items": items, "alternatives": nonNil(q.Alt), "redirect": redirect})
}

// ---------- admin: reviews and wish lists ----------

type reviewRow struct {
	store.Review
	ProductTitle  string `json:"productTitle"`
	ProductHandle string `json:"productHandle"`
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	q := r.URL.Query()
	limit := queryLimit(r, 30, 100)
	var before time.Time
	if c := q.Get("cursor"); c != "" {
		before, _ = time.Parse(time.RFC3339Nano, c)
	}
	status := q.Get("status")
	if status != "" && status != "pending" && status != "approved" && status != "rejected" {
		apierr.BadRequest(w, "Estado desconocido")
		return
	}
	list, counts, err := s.DB.Reviews(r.Context(), tenant, status, q.Get("product"), before, limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	t, _ := s.Proj.Tenant(tenant)
	rows := make([]reviewRow, 0, len(list))
	for _, v := range list {
		row := reviewRow{Review: v, ProductTitle: "(producto eliminado)"}
		if t != nil {
			if p, err := t.Catalog.Get(v.ProductID); err == nil {
				row.ProductTitle, row.ProductHandle = p.Title, p.Handle
			}
		}
		rows = append(rows, row)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": rows, "nextCursor": next, "counts": counts})
}

func (s *Server) moderateReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string  `json:"status"`
		Reply  *string `json:"reply"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Status != "" && body.Status != "approved" && body.Status != "rejected" && body.Status != "pending" {
		apierr.BadRequest(w, "Estado desconocido")
		return
	}
	if body.Reply != nil {
		v := strings.TrimSpace(*body.Reply)
		if utf8.RuneCountInString(v) > 1000 {
			apierr.BadRequest(w, "La respuesta admite hasta 1000 caracteres")
			return
		}
		body.Reply = &v
	}
	pr := principal(r)
	v, err := s.DB.ModerateReview(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), body.Status, body.Reply)
	if s.storeError(w, err, "Reseña no encontrada") {
		return
	}
	s.engage.reviews.Delete(pr.Tenant + "|" + v.ProductID)
	httpx.JSON(w, r, http.StatusOK, v)
}

func (s *Server) deleteReview(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	product, err := s.DB.DeleteReview(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"))
	if s.storeError(w, err, "Reseña no encontrada") {
		return
	}
	s.engage.reviews.Delete(pr.Tenant + "|" + product)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) topWishes(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	list, err := s.DB.TopWishes(r.Context(), tenant, 50)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	t, _ := s.Proj.Tenant(tenant)
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		row := map[string]any{"productId": x.ProductID, "customers": x.Customers, "title": "(producto eliminado)", "handle": "", "available": int64(0)}
		if t != nil {
			if p, err := t.Catalog.Get(x.ProductID); err == nil {
				var avail int64
				for _, v := range p.Variants {
					avail += t.Inventory.Available(v.SKU)
				}
				row["title"], row["handle"], row["available"] = p.Title, p.Handle, avail
			}
		}
		out = append(out, row)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// ---------- storefront ----------

func siteParam(r *http.Request) string {
	site := r.URL.Query().Get("site")
	if site == "" || len(site) > 60 || strings.ContainsAny(site, "/?#.") {
		return "main"
	}
	return site
}

type cardJSON struct {
	ID        string `json:"id"`
	Handle    string `json:"handle"`
	Title     string `json:"title"`
	Brand     string `json:"brand"`
	URL       string `json:"url"`
	Price     string `json:"price"`
	CompareAt string `json:"compareAt,omitempty"`
	SoldOut   bool   `json:"soldOut"`
	Img       string `json:"img"` // ready-to-insert <picture>/<img> markup, or ""
	Initial   string `json:"initial"`
	Tone      int    `json:"tone"`
}

func cardJSONOf(t *projection.Tenant, pd pageData, p *catalog.Product, of catalog.Offer) cardJSON {
	c := shopCard(t, pd, p, of, true)
	return cardJSON{ID: p.ID, Handle: p.Handle, Title: p.Title, Brand: p.Brand, URL: pd.Base + "/p/" + p.Handle, Price: c.Price, CompareAt: c.CompareAt,
		SoldOut: c.SoldOut, Img: string(c.Img), Initial: c.Initial, Tone: c.Tone}
}

// shopSuggest answers the search box while the shopper types.
func (s *Server) shopSuggest(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, suggestLimiter) {
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	pd := pageData{Tenant: t.ID, Base: "/s/" + t.ID + "/" + siteParam(r), Channel: "mx"}
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	out := map[string]any{"products": []cardJSON{}, "categories": []map[string]string{}, "total": 0}
	if utf8.RuneCountInString(text) >= 2 && len(text) <= 80 {
		q := catalog.BrowseQuery{Q: text, Limit: 6}
		s.searchQuery(t, &q)
		of := offerOf(t, pd.Channel)
		res := s.browse.browse(t.ID, t.Version(), "s\x00"+pd.Channel+"\x00"+text+"\x00r="+ruleKey(q)+s.semKey(t.ID), func() catalog.BrowseResult {
			s.narrow(t, &q)
			return catalog.Browse(t.Catalog.ProductsIn(q.Only), t.Taxo, of, q)
		})
		res = liveOnly(t.Catalog, res)
		cards := make([]cardJSON, 0, len(res.Items))
		for i, p := range res.Items {
			cards = append(cards, cardJSONOf(t, pd, p, res.Offers[i]))
		}
		cats := []map[string]string{}
		f := engage.Fold(text)
		for _, c := range t.Taxo.Categories() {
			if c.Active && c.Handle != "" && strings.Contains(engage.Fold(c.Name), f) && len(cats) < 4 {
				cats = append(cats, map[string]string{"name": c.Name, "url": pd.Base + "/c/" + c.Handle})
			}
		}
		out["products"], out["categories"], out["total"] = cards, cats, res.Total
		if u, ok := t.Search().RedirectFor(text); ok {
			out["redirect"] = u
		}
	}
	w.Header().Set("Cache-Control", "public, max-age=20")
	httpx.JSON(w, r, http.StatusOK, out)
}

// shopCards returns cards for a list of handles (favorites kept in the browser).
func (s *Server) shopCards(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) {
		return
	}
	var body struct {
		Handles []string `json:"handles"`
		Site    string   `json:"site"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Handles) > 100 {
		body.Handles = body.Handles[:100]
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	site := body.Site
	if site == "" || len(site) > 60 || strings.ContainsAny(site, "/?#.") {
		site = "main"
	}
	pd := pageData{Tenant: t.ID, Base: "/s/" + t.ID + "/" + site, Channel: "mx"}
	of := offerOf(t, pd.Channel)
	cards := []cardJSON{}
	for _, h := range body.Handles {
		if p, ok := t.Catalog.ByHandle(h); ok {
			cards = append(cards, cardJSONOf(t, pd, &p, of(&p)))
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": cards})
}

// productReviews returns the public reviews of a product, cached for a moment.
func (s *Server) productReviews(ctx context.Context, tenant, productID string) store.ProductReviews {
	key := tenant + "|" + productID
	if v, ok := s.engage.reviews.Load(key); ok {
		if c := v.(cachedReviews); time.Since(c.at) < 30*time.Second {
			return c.data
		}
	}
	data, err := s.DB.PublicReviews(ctx, tenant, productID, 20)
	if err != nil {
		s.Log.Warn("reviews unavailable", "tenant", tenant, "err", err)
		return store.ProductReviews{Items: []store.Review{}}
	}
	s.engage.reviews.Store(key, cachedReviews{at: time.Now(), data: data})
	return data
}

func (s *Server) shopReviews(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) {
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	p, ok := t.Catalog.ByHandle(r.PathValue("handle"))
	if !ok {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.JSON(w, r, http.StatusOK, s.productReviews(r.Context(), t.ID, p.ID))
}

func (s *Server) shopReviewCreate(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, reviewLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	var body struct {
		Rating int    `json:"rating"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Name   string `json:"name"`
		Email  string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	p, ok := t.Catalog.ByHandle(r.PathValue("handle"))
	if !ok {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	body.Title, body.Body, body.Name = strings.TrimSpace(body.Title), strings.TrimSpace(body.Body), strings.TrimSpace(body.Name)
	email := strings.ToLower(strings.TrimSpace(body.Email))
	var errs []apierr.FieldError
	add := func(f, m string) { errs = append(errs, apierr.FieldError{Field: f, Code: "invalid", Message: m}) }
	if body.Rating < 1 || body.Rating > 5 {
		add("rating", "Elige de 1 a 5 estrellas")
	}
	if n := utf8.RuneCountInString(body.Name); n < 2 || n > 60 {
		add("name", "Escribe tu nombre")
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 200 {
		add("email", "Escribe un correo válido")
	}
	if utf8.RuneCountInString(body.Title) > 100 {
		add("title", "El título admite hasta 100 caracteres")
	}
	if n := utf8.RuneCountInString(body.Body); n < 10 || n > 2000 {
		add("body", "Cuéntanos un poco más (entre 10 y 2000 caracteres)")
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, errs[0].Message, errs...)
		return
	}
	hash, err := s.Keys.Blind(t.ID, "email", email)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	skus := make([]string, 0, len(p.Variants))
	for _, v := range p.Variants {
		skus = append(skus, v.SKU)
	}
	v, err := s.DB.CreateReview(r.Context(), t.ID, store.Review{ProductID: p.ID, Rating: body.Rating, Title: body.Title, Body: body.Body, Author: body.Name},
		hash, hex.EncodeToString(hash), skus)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"status": v.Status, "verified": v.Verified})
}

func (s *Server) shopWishlist(w http.ResponseWriter, r *http.Request) {
	customer, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	ids, err := s.DB.Wishes(r.Context(), t.ID, customer)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	handles := []string{}
	for _, id := range ids {
		if p, err := t.Catalog.Get(id); err == nil && p.Status == "active" {
			handles = append(handles, p.Handle)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"handles": handles})
}

func (s *Server) shopWishSet(w http.ResponseWriter, r *http.Request) {
	customer, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	p, found := t.Catalog.ByHandle(r.PathValue("handle"))
	if !found {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	if err := s.DB.SetWish(r.Context(), t.ID, customer, p.ID, r.Method == http.MethodPut); err != nil {
		if err == store.ErrConflict {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "wishlist_full", Detail: "Tu lista de favoritos está llena"})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shopFavorites renders the favorites page; the browser fills it in.
func (s *Server) shopFavorites(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pd.Title, pd.NoIndex = "Favoritos", true
	s.renderShop(w, "favorites", pd, false)
}
