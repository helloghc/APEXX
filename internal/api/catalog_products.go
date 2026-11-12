package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

const (
	defaultChannel = "mx"
	lowStock       = 5
)

// settle applies pending outbox events before answering, so the caller reads
// its own write on this node (other nodes follow within milliseconds).
func (s *Server) settle(ctx context.Context) {
	if s.Proj == nil {
		return
	}
	if err := s.Proj.Drain(context.WithoutCancel(ctx)); err != nil {
		s.Log.Warn("settle after write", "err", err)
	}
}

// offerOf returns the storefront offer of a product in a channel: the lowest
// priced variant and whether anything can be bought now.
func offerOf(t *projection.Tenant, ch string) catalog.OfferFunc {
	return func(p *catalog.Product) catalog.Offer {
		var o catalog.Offer
		for _, v := range p.Variants {
			q, ok := t.Pricing.Resolve(ch, v.SKU, 1)
			if !ok {
				continue
			}
			if !o.HasPrice || q.Unit.Amount < o.Price {
				o.Price, o.CompareAt = q.Unit.Amount, 0
				if q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
					o.CompareAt = q.CompareAt.Amount
				}
			}
			o.HasPrice = true
			if t.Inventory.Available(v.SKU) > 0 {
				o.InStock = true
			}
		}
		return o
	}
}

// productRow is a product as the admin lists it: the product plus live stock,
// price range and cover image.
type productRow struct {
	catalog.Product
	Available    map[string]int64 `json:"available"`
	Stock        int64            `json:"stock"`
	VariantCount int              `json:"variantCount"`
	PriceMin     *int64           `json:"priceMin"`
	PriceMax     *int64           `json:"priceMax"`
	Currency     string           `json:"currency"`
	Thumb        *mediaView       `json:"thumb"`
}

func (s *Server) rowOf(t *projection.Tenant, p *catalog.Product) productRow {
	r := productRow{Product: *p, Available: make(map[string]int64, len(p.Variants)), VariantCount: len(p.Variants), Currency: "MXN"}
	for _, v := range p.Variants {
		a := t.Inventory.Available(v.SKU)
		r.Available[v.SKU] = a
		r.Stock += a
		if q, ok := t.Pricing.Resolve(defaultChannel, v.SKU, 1); ok {
			amt := q.Unit.Amount
			r.Currency = string(q.Unit.Currency)
			if r.PriceMin == nil || amt < *r.PriceMin {
				r.PriceMin = &amt
			}
			if r.PriceMax == nil || amt > *r.PriceMax {
				x := amt
				r.PriceMax = &x
			}
		}
	}
	if len(p.Media) > 0 {
		if m, ok := t.Taxo.Media(p.Media[0]); ok {
			mv := viewMedia(t.ID, m)
			r.Thumb = &mv
		}
	}
	return r
}

// productFilter is the filter of the admin list and of the CSV export.
type productFilter struct {
	q, status, category, brand, stock, typ, tag string
}

func filterFromQuery(r *http.Request) productFilter {
	q := r.URL.Query()
	return productFilter{q: strings.TrimSpace(q.Get("q")), status: q.Get("status"), category: q.Get("category"),
		brand: q.Get("brand"), stock: q.Get("stock"), typ: q.Get("type"), tag: q.Get("tag")}
}

func (f productFilter) validate() []apierr.FieldError {
	var errs []apierr.FieldError
	switch f.status {
	case "", "active", "draft", "archived":
	default:
		errs = append(errs, apierr.FieldError{Field: "status", Code: "invalid", Message: "status: active, draft o archived"})
	}
	switch f.stock {
	case "", "in", "out", "low":
	default:
		errs = append(errs, apierr.FieldError{Field: "stock", Code: "invalid", Message: "stock: in, out o low"})
	}
	if len(f.q) > 100 {
		errs = append(errs, apierr.FieldError{Field: "q", Code: "too_long", Message: "La búsqueda admite hasta 100 caracteres"})
	}
	return errs
}

func totalStock(t *projection.Tenant, p *catalog.Product) int64 {
	var n int64
	for _, v := range p.Variants {
		n += t.Inventory.Available(v.SKU)
	}
	return n
}

func (f productFilter) apply(t *projection.Tenant) []*catalog.Product {
	only := t.Catalog.Matching(f.q) // nil: no text to match
	var subtree map[string]bool
	if f.category != "" && f.category != "none" {
		subtree = t.Taxo.Subtree(f.category)
	}
	var out []*catalog.Product
	for _, p := range t.Catalog.Products() {
		if f.status != "" && p.Status != f.status {
			continue
		}
		if f.typ != "" && p.Type != f.typ {
			continue
		}
		if f.tag != "" {
			has := false
			for _, x := range p.Tags {
				has = has || x == f.tag
			}
			if !has {
				continue
			}
		}
		switch f.brand {
		case "":
		case "none":
			if p.BrandID != "" || p.Brand != "" {
				continue
			}
		default:
			id := p.BrandID
			if id == "" && p.Brand != "" {
				if b, ok := t.Taxo.BrandByName(p.Brand); ok {
					id = b.ID
				}
			}
			if id != f.brand {
				continue
			}
		}
		if f.category != "" {
			cats := t.Taxo.ProductCategories(p)
			if f.category == "none" {
				if len(cats) > 0 {
					continue
				}
			} else {
				in := false
				for id := range cats {
					in = in || subtree[id]
				}
				if !in {
					continue
				}
			}
		}
		if f.stock != "" {
			n := totalStock(t, p)
			if (f.stock == "in" && n <= 0) || (f.stock == "out" && n > 0) || (f.stock == "low" && (n <= 0 || n > lowStock)) {
				continue
			}
		}
		if only != nil && !only[p.ID] {
			continue
		}
		out = append(out, p)
	}
	return out
}

var productSorts = map[string]bool{"": true, "id": true, "title": true, "updated": true, "created": true, "stock": true, "price": true, "status": true}

func sortProducts(t *projection.Tenant, ps []*catalog.Product, key string) {
	desc := strings.HasPrefix(key, "-")
	key = strings.TrimPrefix(key, "-")
	if key == "" || key == "id" {
		if desc {
			for i, j := 0, len(ps)-1; i < j; i, j = i+1, j-1 {
				ps[i], ps[j] = ps[j], ps[i]
			}
		}
		return
	}
	var num map[string]int64
	switch key {
	case "stock":
		num = make(map[string]int64, len(ps))
		for _, p := range ps {
			num[p.ID] = totalStock(t, p)
		}
	case "price":
		num = make(map[string]int64, len(ps))
		of := offerOf(t, defaultChannel)
		for _, p := range ps {
			num[p.ID] = of(p).Price
		}
	}
	less := func(a, b *catalog.Product) int {
		switch key {
		case "title":
			return strings.Compare(catalog.Fold(a.Title), catalog.Fold(b.Title))
		case "updated":
			return a.UpdatedAt.Compare(b.UpdatedAt)
		case "created":
			return a.CreatedAt.Compare(b.CreatedAt)
		case "status":
			return strings.Compare(a.Status, b.Status)
		}
		switch x, y := num[a.ID], num[b.ID]; {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	sort.SliceStable(ps, func(i, j int) bool {
		c := less(ps[i], ps[j])
		if c == 0 {
			return ps[i].ID < ps[j].ID
		}
		return (c < 0) != desc
	})
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 250 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "limit", Code: "out_of_range", Message: "limit debe estar entre 1 y 250"})
			return
		}
		limit = n
	}
	after, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "cursor", Code: "invalid", Message: "cursor inválido"})
		return
	}
	f := filterFromQuery(r)
	errs := f.validate()
	sortKey := r.URL.Query().Get("sort")
	if !productSorts[strings.TrimPrefix(sortKey, "-")] {
		errs = append(errs, apierr.FieldError{Field: "sort", Code: "invalid", Message: "sort: title, updated, created, stock, price o status (con - para descendente)"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Filtros inválidos", errs...)
		return
	}
	// Filtered and sorted once per projection version and filter; the pages
	// of a 50 000 product list then cost a slice, not a scan and a sort.
	key := "admin\x00" + strings.Join([]string{f.q, f.status, f.category, f.brand, f.stock, f.typ, f.tag, sortKey}, "\x00")
	ps := s.browse.browse(t.ID, t.Version(), key, func() catalog.BrowseResult {
		ps := f.apply(t)
		sortProducts(t, ps, sortKey)
		return catalog.BrowseResult{Items: ps}
	}).Items
	// The cursor is the id of the last product of the previous page.
	start := 0
	if after != "" {
		for i, p := range ps {
			if p.ID == after {
				start = i + 1
				break
			}
		}
	}
	end := min(start+limit, len(ps))
	fields := httpx.Fields(r)
	out := make([]any, 0, end-start)
	for _, p := range ps[start:end] {
		out = append(out, httpx.Project(s.rowOf(t, p), fields))
	}
	next := ""
	if end < len(ps) && end > start {
		next = ps[end-1].ID
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "nextCursor": httpx.EncodeCursor(next), "total": len(ps), "catalogTotal": t.Catalog.Count()})
}

type priceView struct {
	Amount    int64  `json:"amount"`
	CompareAt *int64 `json:"compareAt"`
	Currency  string `json:"currency"`
}

type relatedView struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status string     `json:"status"`
	Thumb  *mediaView `json:"thumb"`
}

// productDetail is everything the product editor needs in one response.
type productDetail struct {
	productRow
	Pricing    map[string]priceView         `json:"pricing"`
	Levels     map[string][]inventory.Level `json:"levels"`
	Locations  []string                     `json:"locations"`
	MediaItems map[string]mediaView         `json:"mediaItems"`
	RelatedOf  map[string]relatedView       `json:"relatedItems"`
}

func (s *Server) detailOf(ctx context.Context, t *projection.Tenant, p *catalog.Product) productDetail {
	d := productDetail{productRow: s.rowOf(t, p), Pricing: map[string]priceView{}, Levels: map[string][]inventory.Level{},
		MediaItems: map[string]mediaView{}, RelatedOf: map[string]relatedView{}}
	for _, v := range p.Variants {
		for _, tier := range t.Pricing.Tiers(defaultChannel, v.SKU) {
			if tier.MinQty == 1 {
				pv := priceView{Amount: tier.Unit.Amount, Currency: string(tier.Unit.Currency)}
				if tier.CompareAt != nil {
					c := tier.CompareAt.Amount
					pv.CompareAt = &c
				}
				d.Pricing[v.SKU] = pv
			}
		}
		if snap, ok := t.Inventory.Get(v.SKU); ok {
			d.Levels[v.SKU] = snap.Levels
		}
		if m, ok := t.Taxo.Media(v.MediaID); ok {
			d.MediaItems[m.ID] = viewMedia(t.ID, m)
		}
	}
	for _, id := range p.Media {
		if m, ok := t.Taxo.Media(id); ok {
			d.MediaItems[id] = viewMedia(t.ID, m)
		}
	}
	for _, id := range append(append([]string{}, p.Related.Similar...), p.Related.Accessories...) {
		if rp, err := t.Catalog.Get(id); err == nil {
			row := s.rowOf(t, &rp)
			d.RelatedOf[id] = relatedView{ID: id, Title: rp.Title, Status: rp.Status, Thumb: row.Thumb}
		}
	}
	locs, err := s.DB.Locations(ctx, t.ID)
	if err != nil {
		s.Log.Warn("list locations", "err", err)
	}
	if len(locs) == 0 {
		locs = []string{"principal"}
	}
	d.Locations = locs
	return d
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	p, err := t.Catalog.Get(r.PathValue("id"))
	if err != nil {
		apierr.NotFound(w, "Producto "+r.PathValue("id")+" no encontrado")
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(p.Version, 10)+`"`)
	if fields := httpx.Fields(r); len(fields) > 0 {
		httpx.JSON(w, r, http.StatusOK, httpx.Project(s.detailOf(r.Context(), t, &p), fields))
		return
	}
	writeJSON(w, http.StatusOK, s.detailOf(r.Context(), t, &p))
}

// productBody is the body of POST and PUT: the product plus, optionally, the
// price and stock of its variants so the whole grid saves in one transaction.
type productBody struct {
	catalog.Product
	Prices map[string]store.PriceInput `json:"prices"`
	Stock  map[string]map[string]int64 `json:"stock"`
}

var (
	skuRe      = regexp.MustCompile(`^[^\s/|]{1,80}$`)
	locationRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	productIDR = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
)

func fe(field, code, msg string) apierr.FieldError {
	return apierr.FieldError{Field: field, Code: code, Message: msg}
}

func cleanList(in []string, maxItems, maxLen int) ([]string, bool) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" || seen[catalog.Fold(x)] {
			continue
		}
		if len(x) > maxLen {
			return nil, false
		}
		seen[catalog.Fold(x)] = true
		out = append(out, x)
	}
	return out, len(out) <= maxItems
}

// validateProduct normalizes b in place and returns every problem found.
func validateProduct(t *projection.Tenant, b *productBody) []apierr.FieldError {
	p := &b.Product
	var errs []apierr.FieldError
	p.Title, p.Handle = strings.TrimSpace(p.Title), strings.TrimSpace(p.Handle)
	if p.Title == "" || len(p.Title) > 200 {
		errs = append(errs, fe("title", "required", "Escribe un nombre de hasta 200 caracteres"))
	}
	if !catalog.ValidHandle(p.Handle) {
		errs = append(errs, fe("handle", "invalid", "La dirección solo admite minúsculas, números y guiones (por ejemplo tenis-pacifico)"))
	}
	if len(p.Description) > 20000 {
		errs = append(errs, fe("description", "too_long", "La descripción admite hasta 20,000 caracteres"))
	}
	switch p.Status {
	case "":
		p.Status = "active"
	case "active", "draft", "archived":
	default:
		errs = append(errs, fe("status", "invalid", "status: active, draft o archived"))
	}
	if p.PublishAt != nil {
		if p.Status != "draft" {
			errs = append(errs, fe("publishAt", "invalid", "Solo un borrador puede tener fecha de publicación"))
		} else if p.PublishAt.After(time.Now().AddDate(2, 0, 0)) {
			errs = append(errs, fe("publishAt", "out_of_range", "La fecha de publicación no puede pasar de dos años"))
		}
	}
	if len(p.SEOTitle) > 120 {
		errs = append(errs, fe("seoTitle", "too_long", "El título SEO admite hasta 120 caracteres"))
	}
	if len(p.SEODescription) > 320 {
		errs = append(errs, fe("seoDescription", "too_long", "La descripción SEO admite hasta 320 caracteres"))
	}
	p.Type = strings.TrimSpace(p.Type)
	if len(p.Type) > 60 {
		errs = append(errs, fe("type", "too_long", "El tipo admite hasta 60 caracteres"))
	}
	var ok bool
	if p.Tags, ok = cleanList(p.Tags, 50, 60); !ok {
		errs = append(errs, fe("tags", "invalid", "Hasta 50 etiquetas de 60 caracteres cada una"))
	}

	// Brand and categories.
	if p.BrandID != "" {
		if br, found := t.Taxo.Brand(p.BrandID); found {
			p.Brand = br.Name
		} else {
			errs = append(errs, fe("brandId", "not_found", "La marca no existe"))
		}
	} else if len(p.Brand) > 120 {
		errs = append(errs, fe("brand", "too_long", "La marca admite hasta 120 caracteres"))
	}
	if len(p.Categories) > 20 {
		errs = append(errs, fe("categories", "too_many", "Un producto admite hasta 20 categorías"))
	}
	cats := make([]string, 0, len(p.Categories))
	seenCat := map[string]bool{}
	for _, c := range p.Categories {
		c = strings.TrimSpace(c)
		if id, found := t.Taxo.ResolveCategory(c); found {
			c = id
		}
		if c != "" && len(c) <= 120 && !seenCat[c] {
			seenCat[c] = true
			cats = append(cats, c)
		}
	}
	p.Categories = cats

	// Attributes: values of defined attributes are validated and stored in
	// their canonical form; other keys (imported data) pass through.
	for k, v := range p.Attributes {
		if strings.TrimSpace(v) == "" {
			delete(p.Attributes, k)
			continue
		}
		if a, found := t.Taxo.Attribute(k); found {
			cv, msg := catalog.ValidateAttributeValue(a, v)
			if msg != "" {
				errs = append(errs, fe("attributes."+k, "invalid", msg))
				continue
			}
			p.Attributes[k] = cv
		} else if len(k) > 80 || len(v) > 2000 {
			errs = append(errs, fe("attributes."+k, "too_long", "Atributo demasiado largo"))
		}
	}
	if len(p.Attributes) > 100 {
		errs = append(errs, fe("attributes", "too_many", "Un producto admite hasta 100 atributos"))
	}

	// Options and variants.
	if opts, err := catalog.NormalizeOptions(p.Options); err != nil {
		var me *catalog.MatrixError
		errors.As(err, &me)
		errs = append(errs, fe(me.Field, me.Code, me.Message))
	} else {
		p.Options = opts
	}
	if len(p.Variants) == 0 {
		errs = append(errs, fe("variants", "required", "Un producto necesita al menos una variante con SKU"))
	}
	if len(p.Variants) > catalog.MaxVariants {
		errs = append(errs, fe("variants", "too_many", fmt.Sprintf("Un producto admite hasta %d variantes", catalog.MaxVariants)))
	}
	skus := map[string]bool{}
	for i := range p.Variants {
		v := &p.Variants[i]
		f := "variants[" + strconv.Itoa(i) + "]"
		v.SKU, v.Barcode = strings.TrimSpace(v.SKU), strings.TrimSpace(v.Barcode)
		switch {
		case !skuRe.MatchString(v.SKU):
			errs = append(errs, fe(f+".sku", "invalid", "El SKU es obligatorio, sin espacios ni diagonales, hasta 80 caracteres"))
		case skus[v.SKU]:
			errs = append(errs, fe(f+".sku", "duplicate", "El SKU "+v.SKU+" está repetido"))
		default:
			if owner, taken := t.Catalog.ProductOfSKU(v.SKU); taken && owner != p.ID {
				errs = append(errs, fe(f+".sku", "taken", "El SKU "+v.SKU+" ya pertenece a otro producto"))
			}
		}
		skus[v.SKU] = true
		if v.WeightGrams < 0 || v.WeightGrams > 10_000_000 {
			errs = append(errs, fe(f+".weightGrams", "out_of_range", "El peso va de 0 a 10,000,000 g"))
		}
		if len(v.Barcode) > 60 {
			errs = append(errs, fe(f+".barcode", "too_long", "El código de barras admite hasta 60 caracteres"))
		}
		if v.MediaID != "" {
			if _, found := t.Taxo.Media(v.MediaID); !found {
				errs = append(errs, fe(f+".mediaId", "not_found", "La imagen de la variante no existe"))
			}
		}
		if len(p.Options) > 0 {
			v.Title = catalog.VariantTitle(p.Options, v.Options)
		} else {
			if v.Title = strings.TrimSpace(v.Title); v.Title == "" {
				v.Title = catalog.DefaultVariantTi
			}
			if len(v.Title) > 120 || len(v.Options) > 10 {
				errs = append(errs, fe(f+".title", "too_long", "El nombre de la variante admite hasta 120 caracteres"))
			}
		}
	}
	if len(errs) == 0 && len(p.Options) > 0 {
		if err := catalog.ValidateVariantOptions(p.Options, p.Variants); err != nil {
			var me *catalog.MatrixError
			errors.As(err, &me)
			errs = append(errs, fe(me.Field, me.Code, me.Message))
		}
	}

	// Gallery and related products.
	if len(p.Media) > 50 {
		errs = append(errs, fe("media", "too_many", "La galería admite hasta 50 imágenes"))
	}
	seenMedia := map[string]bool{}
	media := make([]string, 0, len(p.Media))
	for i, id := range p.Media {
		if seenMedia[id] {
			continue
		}
		seenMedia[id] = true
		if _, found := t.Taxo.Media(id); !found {
			errs = append(errs, fe("media["+strconv.Itoa(i)+"]", "not_found", "Una imagen de la galería ya no existe en la biblioteca"))
			continue
		}
		media = append(media, id)
	}
	p.Media = media
	for name, list := range map[string]*[]string{"related.similar": &p.Related.Similar, "related.accessories": &p.Related.Accessories} {
		clean := make([]string, 0, len(*list))
		seen := map[string]bool{}
		for _, id := range *list {
			if id == p.ID || seen[id] {
				continue
			}
			seen[id] = true
			if _, err := t.Catalog.Get(id); err != nil {
				errs = append(errs, fe(name, "not_found", "El producto relacionado "+id+" no existe"))
				continue
			}
			clean = append(clean, id)
		}
		if len(clean) > 20 {
			errs = append(errs, fe(name, "too_many", "Hasta 20 productos relacionados por lista"))
		}
		*list = clean
	}

	// Prices and stock of the variants.
	currency := "MXN"
	if ch, found := t.Channel(defaultChannel); found && ch.Currency != "" {
		currency = string(ch.Currency)
	}
	for sku, pr := range b.Prices {
		f := "prices." + sku
		if !skus[sku] {
			errs = append(errs, fe(f, "not_found", "El precio es de un SKU que no está en el producto: "+sku))
			continue
		}
		if pr.Currency == "" {
			pr.Currency = currency
		}
		if pr.Amount < 0 || pr.Amount > 1e13 || len(pr.Currency) != 3 {
			errs = append(errs, fe(f, "invalid", "Precio inválido para "+sku))
		}
		if pr.CompareAt != nil && *pr.CompareAt < pr.Amount {
			errs = append(errs, fe(f+".compareAt", "invalid", "El precio anterior de "+sku+" no puede ser menor que el precio"))
		}
		b.Prices[sku] = pr
	}
	for sku, locs := range b.Stock {
		if !skus[sku] {
			errs = append(errs, fe("stock."+sku, "not_found", "Las existencias son de un SKU que no está en el producto: "+sku))
			continue
		}
		if len(locs) > 0 && len(t.Inventory.Bundle(sku)) > 0 {
			errs = append(errs, fe("stock."+sku, "kit", "El SKU "+sku+" es un kit: sus existencias salen de sus componentes"))
			continue
		}
		for loc, n := range locs {
			if !locationRe.MatchString(loc) || n < 0 || n > 1e9 {
				errs = append(errs, fe("stock."+sku+"."+loc, "invalid", "Existencias inválidas para "+sku))
			}
		}
	}
	return errs
}

// saveProduct validates and writes; create distinguishes POST from PUT.
func (s *Server) saveProduct(w http.ResponseWriter, r *http.Request, id string, create bool) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var b productBody
	if !decode(w, r, &b) {
		return
	}
	b.ID = id
	pr := principal(r)
	if len(b.Prices) > 0 && !pr.Permissions.Allows("pricing", auth.Edit) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Tu acceso a precios no permite cambiarlos; guarda sin precios o pide permiso"})
		return
	}
	if len(b.Stock) > 0 && !pr.Permissions.Allows("inventory", auth.Edit) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Tu acceso a inventario no permite cambiar existencias; guarda sin existencias o pide permiso"})
		return
	}
	if errs := validateProduct(t, &b); len(errs) > 0 {
		apierr.BadRequest(w, "El producto tiene errores", errs...)
		return
	}
	var expected int64
	if m := strings.Trim(r.Header.Get("If-Match"), `"`); m != "" {
		expected, _ = strconv.ParseInt(m, 10, 64)
	}
	ver, err := s.DB.SaveProduct(r.Context(), pr.Tenant, pr.Actor(),
		store.ProductWrite{Product: b.Product, Channel: defaultChannel, Prices: b.Prices, Stock: b.Stock, Create: create}, expected)
	var ref *store.RefError
	switch {
	case errors.Is(err, store.ErrVersion):
		apierr.Write(w, apierr.Problem{Status: http.StatusPreconditionFailed, Code: "version_conflict", Detail: "El producto cambió desde que lo abriste. Recarga para ver la versión actual."})
		return
	case errors.Is(err, store.ErrConflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "La dirección o un SKU ya existe en otro producto"})
		return
	case errors.As(err, &ref):
		apierr.BadRequest(w, "El producto tiene errores", fe(ref.Field, "not_found", "No existe: "+ref.ID))
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	w.Header().Set("ETag", `"`+strconv.FormatInt(ver, 10)+`"`)
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, map[string]any{"id": b.ID, "version": ver})
}

func (s *Server) putProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !productIDR.MatchString(id) {
		apierr.BadRequest(w, "", fe("id", "invalid", "El id admite letras, números, guiones y puntos, hasta 80 caracteres"))
		return
	}
	s.saveProduct(w, r, id, false)
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	s.saveProduct(w, r, store.NewID("prod_"), true)
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	err := s.DB.DeleteProduct(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Producto no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bulkProducts(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var body struct {
		IDs          []string `json:"ids"`
		Action       string   `json:"action"`
		Status       string   `json:"status"`
		CategoryID   string   `json:"categoryId"`
		BrandID      string   `json:"brandId"`
		Percent      float64  `json:"percent"`
		KeepOldPrice bool     `json:"keepOldPrice"`
	}
	if !decode(w, r, &body) {
		return
	}
	var errs []apierr.FieldError
	if len(body.IDs) == 0 || len(body.IDs) > 500 {
		errs = append(errs, fe("ids", "out_of_range", "Selecciona entre 1 y 500 productos"))
	}
	op := store.BulkOp{Action: body.Action, Status: body.Status, CategoryID: body.CategoryID, BrandID: body.BrandID, KeepOld: body.KeepOldPrice, Channel: defaultChannel}
	pr := principal(r)
	switch body.Action {
	case "status":
		if body.Status != "active" && body.Status != "draft" && body.Status != "archived" {
			errs = append(errs, fe("status", "invalid", "status: active, draft o archived"))
		}
	case "category_add", "category_remove":
		if _, found := t.Taxo.Category(body.CategoryID); !found {
			errs = append(errs, fe("categoryId", "not_found", "La categoría no existe"))
		}
	case "brand":
		if body.BrandID != "" {
			if _, found := t.Taxo.Brand(body.BrandID); !found {
				errs = append(errs, fe("brandId", "not_found", "La marca no existe"))
			}
		}
	case "delete":
		// Same level as deleting one product.
		if !pr.Permissions.Allows("catalog", auth.Admin) {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: forbiddenDetail("catalog", "admin")})
			return
		}
		if len(errs) > 0 {
			apierr.BadRequest(w, "La edición masiva tiene errores", errs...)
			return
		}
		n, err := s.DB.DeleteProducts(r.Context(), pr.Tenant, pr.Actor(), body.IDs)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		s.settle(r.Context())
		httpx.JSON(w, r, http.StatusOK, map[string]any{"selected": len(body.IDs), "changed": n})
		return
	case "price_percent":
		if !pr.Permissions.Allows("pricing", auth.Edit) {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: forbiddenDetail("pricing", "edit")})
			return
		}
		op.Bps = int64(body.Percent*100 + map[bool]float64{true: 0.5, false: -0.5}[body.Percent >= 0])
		if body.Percent != body.Percent || op.Bps == 0 || op.Bps < -9000 || op.Bps > 100000 {
			errs = append(errs, fe("percent", "out_of_range", "El porcentaje va de -90 a 1000 y no puede ser 0"))
		}
	default:
		errs = append(errs, fe("action", "invalid", "action: status, category_add, category_remove, brand, price_percent o delete"))
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "La edición masiva tiene errores", errs...)
		return
	}
	n, err := s.DB.BulkProducts(r.Context(), pr.Tenant, pr.Actor(), body.IDs, op)
	var ref *store.RefError
	if errors.As(err, &ref) {
		apierr.BadRequest(w, "", fe(ref.Field, "not_found", "No existe: "+ref.ID))
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.settle(r.Context())
	httpx.JSON(w, r, http.StatusOK, map[string]any{"selected": len(body.IDs), "changed": n})
}

// generateVariants builds the variant matrix for a set of options, keeping
// the variants that already exist. It writes nothing.
func (s *Server) generateVariants(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKUBase  string            `json:"skuBase"`
		Options  []catalog.Option  `json:"options"`
		Variants []catalog.Variant `json:"variants"`
	}
	if !decode(w, r, &body) {
		return
	}
	vs, err := catalog.GenerateVariants(body.SKUBase, body.Options, body.Variants)
	var me *catalog.MatrixError
	if errors.As(err, &me) {
		apierr.BadRequest(w, me.Message, fe(me.Field, me.Code, me.Message))
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	opts, _ := catalog.NormalizeOptions(body.Options)
	had := map[string]bool{}
	for _, v := range body.Variants {
		had[v.SKU] = true
	}
	now := map[string]bool{}
	created := []string{}
	for _, v := range vs {
		now[v.SKU] = true
		if !had[v.SKU] {
			created = append(created, v.SKU)
		}
	}
	removed := []string{}
	for _, v := range body.Variants {
		if !now[v.SKU] {
			removed = append(removed, v.SKU)
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"options": opts, "variants": vs, "created": created, "removed": removed})
}

// catalogSummary gives the counts the catalog screens show in their tabs.
func (s *Server) catalogSummary(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	by := map[string]int{}
	out, low := 0, 0
	for _, p := range t.Catalog.Products() {
		by[p.Status]++
		if p.Status != "archived" {
			switch n := totalStock(t, p); {
			case n <= 0:
				out++
			case n <= lowStock:
				low++
			}
		}
	}
	cats, brands, cols, attrs, media := t.Taxo.Counts()
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"products": t.Catalog.Count(), "byStatus": by, "outOfStock": out, "lowStock": low,
		"categories": cats, "brands": brands, "collections": cols, "attributes": attrs, "media": media,
	})
}
