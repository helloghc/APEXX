package api

// Storefront catalog: listing with facets and sort, category and collection
// pages, and the product page with gallery, specifications and related
// products. Everything is rendered on the server from the in-memory
// projection; every filter lives in the URL, so any view can be shared.

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/projection"
)

const shopPageSize = 24

// browseLimiter bounds listing requests per visitor: each one scans the catalog.
var browseLimiter = &shopLimiter{n: 300}

// imgHTML renders a responsive image: WebP source when it exists, srcset with
// real widths, and width/height so the page does not shift while it loads.
func imgHTML(mv mediaView, class, sizes string, lazy bool, extra string) template.HTML {
	if mv.URL == "" || len(mv.Variants) == 0 {
		return ""
	}
	// width/height of the largest fallback rendition give the aspect ratio.
	w, h := mv.Width, mv.Height
	for _, r := range mv.Variants {
		if r.Format != "webp" && strings.HasSuffix(mv.URL, "/"+r.Key) {
			w, h = r.Width, r.Height
		}
	}
	e := html.EscapeString
	var b strings.Builder
	if mv.WebpSrcset != "" {
		b.WriteString(`<picture><source type="image/webp" srcset="` + e(mv.WebpSrcset) + `" sizes="` + e(sizes) + `">`)
	}
	b.WriteString(`<img class="` + e(class) + `" src="` + e(mv.URL) + `" srcset="` + e(mv.Srcset) + `" sizes="` + e(sizes) +
		`" width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `" alt="` + e(mv.Alt) + `"`)
	if lazy {
		b.WriteString(` loading="lazy"`)
	} else {
		b.WriteString(` fetchpriority="high"`)
	}
	b.WriteString(` decoding="async"` + extra + `>`)
	if mv.WebpSrcset != "" {
		b.WriteString(`</picture>`)
	}
	return template.HTML(b.String()) // #nosec G203 -- every dynamic value is escaped above
}

const cardSizes = "(min-width:1050px) 25vw, (min-width:700px) 33vw, 50vw"

// coverHTML returns the card image of a product, or "" when it has none.
func coverHTML(t *projection.Tenant, p *catalog.Product, lazy bool) template.HTML {
	if len(p.Media) == 0 {
		return ""
	}
	m, ok := t.Taxo.Media(p.Media[0])
	if !ok {
		return ""
	}
	return imgHTML(viewMedia(t.ID, m), "pcard-media ph", cardSizes, lazy, "")
}

// shopCard builds a product card with the lowest price among its variants.
func shopCard(t *projection.Tenant, pd pageData, p *catalog.Product, of catalog.Offer, lazy bool) cardView {
	c := cardOf(t, pd.Channel, *p)
	c.Base = pd.Base
	if of.HasPrice {
		cur := money.Currency("MXN")
		if ch, ok := t.Channel(pd.Channel); ok && ch.Currency != "" {
			cur = ch.Currency
		}
		c.Price, c.CompareAt = pesos(money.New(of.Price, cur)), ""
		if of.CompareAt > of.Price {
			c.CompareAt = pesos(money.New(of.CompareAt, cur))
		}
	}
	c.Img = coverHTML(t, p, lazy)
	return c
}

type crumb struct{ Label, URL string }

type facetLink struct {
	Label    string
	Count    int
	URL      string
	Selected bool
}

type facetGroup struct {
	Label string
	Links []facetLink
}

type kv struct{ Name, Value string }

type browsePage struct {
	pageData
	Eyebrow, Heading, Intro string
	Crumbs                  []crumb
	Products                []cardView
	Total                   int
	Groups                  []facetGroup
	Sorts                   []facetLink
	Active                  []facetLink // selected filters, each links to the view without it
	ClearURL                string
	PriceAction             string
	PriceKeep               []kv // other parameters the price form must keep
	PriceMin, PriceMax      string
	PriceHint               string
	ShowFilters             bool
	Prev, Next              string
	Page, Pages             int
}

var sortParam = map[string]string{"": catalog.SortRelevance, "relevancia": catalog.SortRelevance, "precio-asc": catalog.SortPriceAsc,
	"precio-desc": catalog.SortPriceDesc, "novedades": catalog.SortNewest}

func pesosPlain(cents int64) string {
	if cents%100 == 0 {
		return strconv.FormatInt(cents/100, 10)
	}
	return catalog.FormatAmount(cents)
}

// browseState is the set of filters of a listing, parsed from the URL.
type browseState struct {
	path   string // path of the page: …/c, …/c/{handle} or …/col/{handle}
	q      string
	brands []string
	attrs  map[string][]string
	min    *int64
	max    *int64
	sort   string // URL value
	page   int
}

func parseBrowse(r *http.Request, path string, t *projection.Tenant) browseState {
	v := r.URL.Query()
	st := browseState{path: path, attrs: map[string][]string{}, page: 1}
	st.q = strings.TrimSpace(v.Get("q"))
	if len(st.q) > 100 {
		st.q = st.q[:100]
	}
	seen := map[string]bool{}
	for _, b := range v["marca"] {
		if b != "" && len(b) <= 120 && !seen[b] && len(st.brands) < 20 {
			seen[b] = true
			st.brands = append(st.brands, b)
		}
	}
	for _, a := range t.Taxo.Attributes() {
		if !a.Filterable {
			continue
		}
		for _, val := range v["a."+a.ID] {
			if val != "" && len(val) <= 500 && len(st.attrs[a.ID]) < 20 && !contains(st.attrs[a.ID], val) {
				st.attrs[a.ID] = append(st.attrs[a.ID], val)
			}
		}
	}
	if n, ok := catalog.ParseAmount(v.Get("min")); ok {
		st.min = &n
	}
	if n, ok := catalog.ParseAmount(v.Get("max")); ok {
		st.max = &n
	}
	if st.min != nil && st.max != nil && *st.min > *st.max {
		st.min, st.max = st.max, st.min
	}
	if _, ok := sortParam[v.Get("orden")]; ok {
		st.sort = v.Get("orden")
	}
	if n, err := strconv.Atoi(v.Get("pagina")); err == nil && n > 1 && n <= 10000 {
		st.page = n
	}
	return st
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// url renders the state as a URL; mod may change a copy first. Parameters are
// written in a fixed order so equal views have equal URLs (cache friendly).
func (st browseState) url(mod func(*browseState)) string {
	c := st
	c.brands = append([]string{}, st.brands...)
	c.attrs = map[string][]string{}
	for k, vs := range st.attrs {
		c.attrs[k] = append([]string{}, vs...)
	}
	if mod != nil {
		c.page = 1 // any change of filters goes back to the first page
		mod(&c)
	}
	var parts []string
	add := func(k, v string) { parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v)) }
	if c.q != "" {
		add("q", c.q)
	}
	sort.Strings(c.brands)
	for _, b := range c.brands {
		add("marca", b)
	}
	keys := make([]string, 0, len(c.attrs))
	for k := range c.attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vs := c.attrs[k]
		sort.Strings(vs)
		for _, v := range vs {
			add("a."+k, v)
		}
	}
	if c.min != nil {
		add("min", pesosPlain(*c.min))
	}
	if c.max != nil {
		add("max", pesosPlain(*c.max))
	}
	if c.sort != "" && c.sort != "relevancia" {
		add("orden", c.sort)
	}
	if c.page > 1 {
		add("pagina", strconv.Itoa(c.page))
	}
	if len(parts) == 0 {
		return c.path
	}
	return c.path + "?" + strings.Join(parts, "&")
}

func toggle(list []string, v string) []string {
	out := list[:0:0]
	found := false
	for _, x := range list {
		if x == v {
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		out = append(out, v)
	}
	return out
}

func (st browseState) filtered() bool {
	return len(st.brands) > 0 || len(st.attrs) > 0 || st.min != nil || st.max != nil
}

// renderBrowse runs the listing and renders it. cat and col are optional.
func (s *Server) renderBrowse(w http.ResponseWriter, r *http.Request, t *projection.Tenant, pd pageData, st browseState, cat *catalog.Category, col *catalog.Collection) {
	q := catalog.BrowseQuery{Q: st.q, Brands: st.brands, PriceMin: st.min, PriceMax: st.max, Attrs: st.attrs,
		Sort: sortParam[st.sort], Offset: (st.page - 1) * shopPageSize, Limit: shopPageSize, Collection: col}
	if cat != nil {
		q.CategoryID = cat.ID
	}
	res := catalog.Browse(t.Catalog.Products(), t.Taxo, offerOf(t, pd.Channel), q)

	bp := browsePage{pageData: pd, Total: res.Total, Page: st.page, Pages: (res.Total + shopPageSize - 1) / shopPageSize}
	bp.Query = st.q
	bp.Crumbs = []crumb{{"Inicio", pd.Base + "/index"}, {"Productos", pd.Base + "/c"}}
	switch {
	case cat != nil:
		path := t.Taxo.Path(cat.ID)
		for _, c := range path[:max(len(path)-1, 0)] {
			bp.Crumbs = append(bp.Crumbs, crumb{c.Name, pd.Base + "/c/" + c.Handle})
		}
		bp.Eyebrow, bp.Heading, bp.Intro = "Categoría", cat.Name, cat.Description
		bp.Title, bp.Description = firstNonEmpty(cat.SEOTitle, cat.Name), firstNonEmpty(cat.SEODescription, cat.Description, cat.Name+" en "+pd.Store)
	case col != nil:
		bp.Eyebrow, bp.Heading, bp.Intro = "Colección", col.Title, col.Description
		bp.Title, bp.Description = firstNonEmpty(col.SEOTitle, col.Title), firstNonEmpty(col.SEODescription, col.Description, col.Title+" en "+pd.Store)
	case st.q != "":
		bp.Eyebrow, bp.Heading = "Búsqueda", "Resultados para “"+st.q+"”"
		bp.Title, bp.Description = "Buscar: "+st.q, "Catálogo de "+pd.Store
	default:
		bp.Eyebrow, bp.Heading = "Catálogo", "Todos los productos"
		bp.Title, bp.Description = "Productos", "Catálogo de "+pd.Store
	}
	bp.Canonical = st.path
	bp.NoIndex = st.q != "" || st.filtered() || st.sort != "" || st.page > 1

	for i, p := range res.Items {
		bp.Products = append(bp.Products, shopCard(t, pd, p, res.Offers[i], i >= 4))
	}

	// Facets. Categories navigate to their own page and keep the search.
	if len(res.Categories) > 0 {
		g := facetGroup{Label: "Categoría"}
		for _, c := range res.Categories {
			u := browseState{path: pd.Base + "/c/" + c.Value, q: st.q, sort: st.sort, page: 1}
			g.Links = append(g.Links, facetLink{Label: c.Label, Count: c.Count, URL: u.url(nil)})
		}
		bp.Groups = append(bp.Groups, g)
	}
	if len(res.Brands) > 1 || len(st.brands) > 0 {
		g := facetGroup{Label: "Marca"}
		for _, b := range res.Brands {
			key := b.Value
			g.Links = append(g.Links, facetLink{Label: b.Label, Count: b.Count, Selected: b.Selected,
				URL: st.url(func(c *browseState) { c.brands = toggle(c.brands, key) })})
			if b.Selected {
				bp.Active = append(bp.Active, facetLink{Label: b.Label, URL: st.url(func(c *browseState) { c.brands = toggle(c.brands, key) })})
			}
		}
		bp.Groups = append(bp.Groups, g)
	}
	for _, f := range res.Attrs {
		g := facetGroup{Label: f.Label}
		id := f.ID
		for _, v := range f.Values {
			val := v.Value
			u := st.url(func(c *browseState) {
				if vs := toggle(c.attrs[id], val); len(vs) > 0 {
					c.attrs[id] = vs
				} else {
					delete(c.attrs, id)
				}
			})
			g.Links = append(g.Links, facetLink{Label: v.Label, Count: v.Count, Selected: v.Selected, URL: u})
			if v.Selected {
				bp.Active = append(bp.Active, facetLink{Label: f.Label + ": " + v.Label, URL: u})
			}
		}
		bp.Groups = append(bp.Groups, g)
	}
	if st.min != nil || st.max != nil {
		label := "Precio"
		switch {
		case st.min != nil && st.max != nil:
			label = "$" + pesosPlain(*st.min) + " – $" + pesosPlain(*st.max)
		case st.min != nil:
			label = "Desde $" + pesosPlain(*st.min)
		default:
			label = "Hasta $" + pesosPlain(*st.max)
		}
		bp.Active = append(bp.Active, facetLink{Label: label, URL: st.url(func(c *browseState) { c.min, c.max = nil, nil })})
	}
	if len(bp.Active) > 0 {
		bp.ClearURL = st.url(func(c *browseState) { c.brands, c.attrs, c.min, c.max = nil, map[string][]string{}, nil, nil })
	}
	bp.PriceAction = st.path
	if st.q != "" {
		bp.PriceKeep = append(bp.PriceKeep, kv{"q", st.q})
	}
	for _, b := range st.brands {
		bp.PriceKeep = append(bp.PriceKeep, kv{"marca", b})
	}
	for k, vs := range st.attrs {
		for _, v := range vs {
			bp.PriceKeep = append(bp.PriceKeep, kv{"a." + k, v})
		}
	}
	if st.sort != "" {
		bp.PriceKeep = append(bp.PriceKeep, kv{"orden", st.sort})
	}
	if st.min != nil {
		bp.PriceMin = pesosPlain(*st.min)
	}
	if st.max != nil {
		bp.PriceMax = pesosPlain(*st.max)
	}
	if res.PriceCeil > 0 {
		bp.PriceHint = "De $" + pesosPlain(res.PriceFloor) + " a $" + pesosPlain(res.PriceCeil)
	}
	bp.ShowFilters = len(bp.Groups) > 0 || res.PriceCeil > res.PriceFloor || len(bp.Active) > 0

	for _, o := range []struct{ v, label string }{{"relevancia", "Relevancia"}, {"precio-asc", "Precio: menor a mayor"}, {"precio-desc", "Precio: mayor a menor"}, {"novedades", "Novedades"}} {
		val := o.v
		cur := st.sort
		if cur == "" {
			cur = "relevancia"
		}
		bp.Sorts = append(bp.Sorts, facetLink{Label: o.label, Selected: cur == val, URL: st.url(func(c *browseState) { c.sort = val })})
	}
	if st.page > 1 {
		prev := st
		prev.page = st.page - 1
		bp.Prev = prev.url(nil)
	}
	if st.page < bp.Pages {
		next := st
		next.page = st.page + 1
		bp.Next = next.url(nil)
	}
	if st.page > 1 && len(res.Items) == 0 {
		w.WriteHeader(http.StatusNotFound)
	}
	s.renderShop(w, "catalog", bp, st.q == "")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (s *Server) browseAllowed(w http.ResponseWriter, r *http.Request) bool {
	if browseLimiter.allow(s.clientIP(r)) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	http.Error(w, "Demasiadas solicitudes; espera un minuto", http.StatusTooManyRequests)
	return false
}

func (s *Server) shopCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.browseAllowed(w, r) {
		return
	}
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	s.renderBrowse(w, r, t, pd, parseBrowse(r, pd.Base+"/c", t), nil, nil)
}

// shopNotFound renders the catalog shell with a 404 and a way forward.
func (s *Server) shopNotFound(w http.ResponseWriter, pd pageData, what string) {
	w.WriteHeader(http.StatusNotFound)
	pd.Title, pd.NoIndex = what+" no encontrada", true
	s.renderShop(w, "catalog", browsePage{pageData: pd, Eyebrow: "Catálogo", Heading: what + " no encontrada",
		Crumbs: []crumb{{"Inicio", pd.Base + "/index"}, {"Productos", pd.Base + "/c"}}}, false)
}

func (s *Server) shopCategory(w http.ResponseWriter, r *http.Request) {
	if !s.browseAllowed(w, r) {
		return
	}
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	cat, found := t.Taxo.CategoryByHandle(r.PathValue("handle"))
	if found {
		// A category inside an inactive one is hidden too.
		for _, c := range t.Taxo.Path(cat.ID) {
			found = found && c.Active
		}
	}
	if !found {
		s.shopNotFound(w, pd, "Categoría")
		return
	}
	s.renderBrowse(w, r, t, pd, parseBrowse(r, pd.Base+"/c/"+cat.Handle, t), &cat, nil)
}

func (s *Server) shopCollection(w http.ResponseWriter, r *http.Request) {
	if !s.browseAllowed(w, r) {
		return
	}
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	col, found := t.Taxo.CollectionByHandle(r.PathValue("handle"))
	if !found || !col.Active {
		s.shopNotFound(w, pd, "Colección")
		return
	}
	s.renderBrowse(w, r, t, pd, parseBrowse(r, pd.Base+"/col/"+col.Handle, t), nil, &col)
}

// ---------- product page ----------

type variantView struct {
	SKU, Title, Price, CompareAt string
	Available                    int64
	Img, ImgSet                  string // image of the variant: the gallery shows it when chosen
}

type specView struct{ Name, Value string }

type productPage struct {
	pageData
	P struct {
		Title, Brand, BrandURL, Description, Initial string
		Tone                                         int
		Variants                                     []variantView
	}
	Crumbs           []crumb
	Cover            template.HTML
	Gallery          []template.HTML
	Specs            []specView
	Similar          []cardView
	Accessories      []cardView
	Price, CompareAt string
	FirstAvailable   int
	SoldOut          bool
	JSONLD           template.JS
}

const pdpSizes = "(min-width:900px) 55vw, 100vw"

func (s *Server) shopProduct(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	p, found := t.Catalog.ByHandle(r.PathValue("handle"))
	if !found {
		w.WriteHeader(http.StatusNotFound)
		pd.Title, pd.NoIndex = "Producto no encontrado", true
		s.renderShop(w, "catalog", browsePage{pageData: pd, Eyebrow: "Catálogo", Heading: "Producto no encontrado",
			Crumbs: []crumb{{"Inicio", pd.Base + "/index"}, {"Productos", pd.Base + "/c"}}}, false)
		return
	}
	pg := productPage{pageData: pd, FirstAvailable: -1, SoldOut: true}
	pg.P.Title, pg.P.Brand, pg.P.Description, pg.P.Initial, pg.P.Tone = p.Title, p.Brand, p.Description, initial(p.Title), builder.ToneOf(p.Title)
	if key, _ := t.Taxo.BrandKey(&p); key != "" {
		pg.P.BrandURL = pd.Base + "/c?marca=" + url.QueryEscape(key)
	}
	var images []string
	for i, v := range p.Variants {
		vv := variantView{SKU: v.SKU, Title: v.Title, Available: t.Inventory.Available(v.SKU)}
		if q, ok := t.Pricing.Resolve(pd.Channel, v.SKU, 1); ok {
			vv.Price = pesos(q.Unit)
			if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
				vv.CompareAt = pesos(*q.CompareAt)
			}
		} else {
			vv.Available = 0 // no price, not sellable
		}
		if m, ok := t.Taxo.Media(v.MediaID); ok {
			mv := viewMedia(t.ID, m)
			vv.Img, vv.ImgSet = mv.URL, mv.Srcset
		}
		if vv.Available > 0 && pg.FirstAvailable < 0 {
			pg.FirstAvailable, pg.SoldOut = i, false
		}
		pg.P.Variants = append(pg.P.Variants, vv)
	}
	if pg.FirstAvailable >= 0 {
		pg.Price, pg.CompareAt = pg.P.Variants[pg.FirstAvailable].Price, pg.P.Variants[pg.FirstAvailable].CompareAt
	} else if len(pg.P.Variants) > 0 {
		pg.Price = pg.P.Variants[0].Price
	}
	for i, id := range p.Media {
		m, ok := t.Taxo.Media(id)
		if !ok {
			continue
		}
		mv := viewMedia(t.ID, m)
		images = append(images, mv.URL)
		if i == 0 || pg.Cover == "" {
			pg.Cover = imgHTML(mv, "ph pdp-img", pdpSizes, false, ` data-pdp-img`)
		} else {
			pg.Gallery = append(pg.Gallery, imgHTML(mv, "ph pdp-img", "(min-width:900px) 27vw, 50vw", true, ""))
		}
	}
	pg.Crumbs = []crumb{{"Inicio", pd.Base + "/index"}, {"Productos", pd.Base + "/c"}}
	for _, raw := range p.Categories {
		if id, ok := t.Taxo.ResolveCategory(raw); ok {
			for _, c := range t.Taxo.Path(id) {
				if c.Active {
					pg.Crumbs = append(pg.Crumbs, crumb{c.Name, pd.Base + "/c/" + c.Handle})
				}
			}
			break
		}
	}
	for _, a := range t.Taxo.Attributes() {
		if _, label, ok := attrDisplay(a, p.Attributes[a.ID]); ok {
			pg.Specs = append(pg.Specs, specView{a.Name, label})
		}
	}
	of := offerOf(t, pd.Channel)
	related := func(ids []string) []cardView {
		var out []cardView
		for _, id := range ids {
			if rp, err := t.Catalog.Get(id); err == nil && rp.Status == "active" && len(out) < 8 {
				out = append(out, shopCard(t, pd, &rp, of(&rp), true))
			}
		}
		return out
	}
	pg.Similar, pg.Accessories = related(p.Related.Similar), related(p.Related.Accessories)

	ld := map[string]any{
		"@context": "https://schema.org", "@type": "Product", "name": p.Title, "brand": map[string]any{"@type": "Brand", "name": p.Brand},
		"sku": firstSKU(p), "offers": map[string]any{"@type": "Offer", "priceCurrency": "MXN", "price": priceNumber(t, pd.Channel, p),
			"availability": map[bool]string{true: "https://schema.org/OutOfStock", false: "https://schema.org/InStock"}[pg.SoldOut]},
	}
	if len(images) > 0 {
		ld["image"] = images
	}
	if p.Description != "" {
		ld["description"] = p.Description
	}
	b, _ := json.Marshal(ld)
	pg.JSONLD = template.JS(b) // #nosec G203 -- json.Marshal escapes <, > and &
	pg.Title = firstNonEmpty(p.SEOTitle, p.Title)
	pg.Description = firstNonEmpty(p.SEODescription, strings.TrimSpace(p.Brand+" "+p.Title))
	pg.Canonical = pd.Base + "/p/" + p.Handle
	s.renderShop(w, "product", pg, true)
}

// attrDisplay renders an attribute value for shoppers.
func attrDisplay(a catalog.AttributeDef, raw string) (value, label string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	switch a.Kind {
	case catalog.AttrBoolean:
		if raw == "true" {
			return raw, "Sí", true
		}
		return raw, "No", true
	case catalog.AttrNumber:
		if a.Unit != "" {
			return raw, raw + " " + a.Unit, true
		}
	}
	return raw, raw, true
}
