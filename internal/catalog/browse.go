package catalog

import (
	"sort"
	"strconv"
	"strings"
)

// Sort orders of a storefront listing.
const (
	SortRelevance = "relevance"
	SortPriceAsc  = "price_asc"
	SortPriceDesc = "price_desc"
	SortNewest    = "newest"
)

// Offer is what a shopper sees of a product in a channel: its price (lowest
// among its variants) and whether any variant can be bought now.
type Offer struct {
	Price     int64
	CompareAt int64
	HasPrice  bool
	InStock   bool
}

// OfferFunc resolves the offer of a product.
type OfferFunc func(p *Product) Offer

// BrowseQuery describes one storefront listing.
type BrowseQuery struct {
	Q          string
	CategoryID string      // restrict to this category and its descendants
	Collection *Collection // restrict to this collection
	Brands     []string    // selected brand keys (see Taxonomy.BrandKey)
	PriceMin   *int64      // minor units, inclusive
	PriceMax   *int64
	Attrs      map[string][]string // attribute id -> selected values
	Sort       string
	Offset     int
	Limit      int
	// Alt holds alternative phrasings of Q (synonyms): a product matches when
	// it matches Q or any of them.
	Alt []string
	// Exclude hides these product ids from the results.
	Exclude map[string]bool
	// Pinned product ids come first, in this order, when no sort is chosen.
	Pinned []string
	// Boost raises products shoppers chose for this search, below the pins.
	Boost map[string]int
	// Only, when not nil, limits the scan to these product ids: the caller
	// already knows (from the word index) which products can match Q, its
	// synonyms or the pins. Every other check still runs on them.
	Only map[string]bool
}

// FacetValue is one choice of a facet with the number of products it would show.
type FacetValue struct {
	Value    string
	Label    string
	Count    int
	Selected bool
}

// Facet is a filterable attribute with its values.
type Facet struct {
	ID     string
	Label  string
	Values []FacetValue
}

// BrowseResult is a page of products plus the facets of the whole result.
type BrowseResult struct {
	Items      []*Product
	Offers     []Offer // parallel to Items
	Total      int
	Categories []FacetValue // child categories of the current one; Value is the handle
	Brands     []FacetValue
	Attrs      []Facet
	PriceFloor int64 // lowest and highest price among products matching every filter but price
	PriceCeil  int64
}

// MatchesCollection reports whether p belongs to c.
func (t *Taxonomy) MatchesCollection(c *Collection, p *Product, cats map[string]bool, o Offer) bool {
	if c.Kind == CollectionManual {
		for _, id := range c.ProductIDs {
			if id == p.ID {
				return true
			}
		}
		return false
	}
	r := c.Rules
	if r.Empty() {
		return false
	}
	var checks []bool
	if len(r.BrandIDs) > 0 {
		id := p.BrandID
		if id == "" && p.Brand != "" {
			if b, ok := t.BrandByName(p.Brand); ok {
				id = b.ID
			}
		}
		checks = append(checks, id != "" && contains(r.BrandIDs, id))
	}
	if len(r.CategoryIDs) > 0 {
		ok := false
		for _, id := range r.CategoryIDs {
			ok = ok || cats[id]
		}
		checks = append(checks, ok)
	}
	if len(r.Tags) > 0 {
		ok := false
		for _, want := range r.Tags {
			for _, have := range p.Tags {
				ok = ok || fold(want) == fold(have)
			}
		}
		checks = append(checks, ok)
	}
	if r.PriceMin != nil || r.PriceMax != nil {
		ok := o.HasPrice && (r.PriceMin == nil || o.Price >= *r.PriceMin) && (r.PriceMax == nil || o.Price <= *r.PriceMax)
		checks = append(checks, ok)
	}
	if r.Match == "any" {
		for _, c := range checks {
			if c {
				return true
			}
		}
		return false
	}
	for _, c := range checks {
		if !c {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// attrFacetValue normalizes the stored value of an attribute for faceting.
func attrFacetValue(a AttributeDef, raw string) (value, label string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	switch a.Kind {
	case AttrBoolean:
		if raw == "true" {
			return "true", "Sí", true
		}
		return "false", "No", true
	case AttrNumber:
		if a.Unit != "" {
			return raw, raw + " " + a.Unit, true
		}
	}
	return raw, raw, true
}

type cand struct {
	p     *Product
	offer Offer
	score int
	order int // position in a manual collection
}

// Browse filters, counts facets, sorts and pages the active products.
// Facet counts are disjunctive: the counts of a facet ignore the selections
// made in that same facet, so choosing "Nike" still shows how many "Adidas"
// products the other filters leave.
func Browse(products []*Product, t *Taxonomy, offer OfferFunc, q BrowseQuery) BrowseResult {
	qs := words(q.Q)
	alts := make([][]string, 0, len(q.Alt))
	for _, a := range q.Alt {
		if w := words(a); len(w) > 0 {
			alts = append(alts, w)
		}
	}
	pinPos := map[string]int{}
	for i, id := range q.Pinned {
		pinPos[id] = len(q.Pinned) - i
	}
	var subtree map[string]bool
	if q.CategoryID != "" {
		subtree = t.Subtree(q.CategoryID)
	}
	manualPos := map[string]int{}
	if q.Collection != nil && q.Collection.Kind == CollectionManual {
		for i, id := range q.Collection.ProductIDs {
			manualPos[id] = i
		}
	}
	var facetAttrs []AttributeDef
	for _, a := range t.Attributes() {
		if a.Filterable {
			facetAttrs = append(facetAttrs, a)
		}
	}
	brandSel := map[string]bool{}
	for _, b := range q.Brands {
		brandSel[b] = true
	}
	// Facet groups: 0 = brand, 1 = price, 2.. = attributes.
	nGroups := 2 + len(facetAttrs)
	brandCount := map[string]int{}
	brandLabel := map[string]string{}
	attrCount := make([]map[string]int, len(facetAttrs))
	attrLabel := make([]map[string]string, len(facetAttrs))
	for i := range facetAttrs {
		attrCount[i], attrLabel[i] = map[string]int{}, map[string]string{}
	}
	childCount := map[string]int{}
	children := t.Children(q.CategoryID)
	childOf := map[string]string{} // any category id in a child's subtree -> child id
	for _, c := range children {
		if !c.Active {
			continue
		}
		for id := range t.Subtree(c.ID) {
			childOf[id] = c.ID
		}
	}

	res := BrowseResult{}
	var hits []cand
	pass := make([]bool, nGroups)
	priceSeen := false
	for _, p := range products {
		if q.Only != nil && !q.Only[p.ID] {
			continue
		}
		if p.Status != "active" {
			continue
		}
		if q.Exclude[p.ID] {
			continue
		}
		if len(qs) > 0 && !matchesAll(qs, p.hay) {
			ok := pinPos[p.ID] > 0 // a pinned product shows even when its text does not match
			for _, a := range alts {
				if matchesAll(a, p.hay) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		cats := t.ProductCategories(p)
		if subtree != nil {
			in := false
			for id := range cats {
				if subtree[id] {
					in = true
					break
				}
			}
			if !in {
				continue
			}
		}
		of := offer(p)
		if q.Collection != nil && !t.MatchesCollection(q.Collection, p, cats, of) {
			continue
		}
		bkey, blabel := t.BrandKey(p)
		pass[0] = len(brandSel) == 0 || brandSel[bkey]
		pass[1] = (q.PriceMin == nil && q.PriceMax == nil) ||
			(of.HasPrice && (q.PriceMin == nil || of.Price >= *q.PriceMin) && (q.PriceMax == nil || of.Price <= *q.PriceMax))
		vals := make([]string, len(facetAttrs))
		labels := make([]string, len(facetAttrs))
		for i, a := range facetAttrs {
			v, l, ok := attrFacetValue(a, p.Attributes[a.ID])
			if ok {
				vals[i], labels[i] = v, l
			}
			sel := q.Attrs[a.ID]
			pass[2+i] = len(sel) == 0 || (ok && contains(sel, v))
		}
		failed, failedAt := 0, -1
		for i, ok := range pass {
			if !ok {
				failed++
				failedAt = i
			}
		}
		if failed > 1 {
			continue
		}
		// Counts of a group include products that fail only that group.
		if failed == 0 || failedAt == 0 {
			if bkey != "" {
				brandCount[bkey]++
				brandLabel[bkey] = blabel
			}
		}
		if (failed == 0 || failedAt == 1) && of.HasPrice {
			if !priceSeen || of.Price < res.PriceFloor {
				res.PriceFloor = of.Price
			}
			if !priceSeen || of.Price > res.PriceCeil {
				res.PriceCeil = of.Price
			}
			priceSeen = true
		}
		for i := range facetAttrs {
			if (failed == 0 || failedAt == 2+i) && vals[i] != "" {
				attrCount[i][vals[i]]++
				attrLabel[i][vals[i]] = labels[i]
			}
		}
		if failed != 0 {
			continue
		}
		counted := map[string]bool{}
		for id := range cats {
			if c, ok := childOf[id]; ok && !counted[c] {
				counted[c] = true
				childCount[c]++
			}
		}
		c := cand{p: p, offer: of}
		if len(qs) > 0 {
			tw := words(p.Title)
			for _, tok := range qs {
				c.score++
				if tokenMatches(tok, tw) {
					c.score += 2
				}
			}
		}
		c.score += pinPos[p.ID]*1000 + q.Boost[p.ID]
		if pos, ok := manualPos[p.ID]; ok {
			c.order = pos
		}
		hits = append(hits, c)
	}

	switch q.Sort {
	case SortPriceAsc, SortPriceDesc:
		desc := q.Sort == SortPriceDesc
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i], hits[j]
			if a.offer.HasPrice != b.offer.HasPrice {
				return a.offer.HasPrice // products without price go last
			}
			if a.offer.Price != b.offer.Price {
				return (a.offer.Price < b.offer.Price) != desc
			}
			return a.p.ID < b.p.ID
		})
	case SortNewest:
		sort.SliceStable(hits, func(i, j int) bool {
			if !hits[i].p.CreatedAt.Equal(hits[j].p.CreatedAt) {
				return hits[i].p.CreatedAt.After(hits[j].p.CreatedAt)
			}
			return hits[i].p.ID > hits[j].p.ID
		})
	default:
		sort.SliceStable(hits, func(i, j int) bool {
			a, b := hits[i], hits[j]
			if a.score != b.score {
				return a.score > b.score
			}
			if a.order != b.order {
				return a.order < b.order
			}
			if a.offer.InStock != b.offer.InStock {
				return a.offer.InStock // what can be bought now goes first
			}
			return a.p.ID < b.p.ID
		})
	}

	res.Total = len(hits)
	lo := min(max(q.Offset, 0), len(hits))
	hi := len(hits)
	if q.Limit > 0 {
		hi = min(lo+q.Limit, len(hits))
	}
	for _, c := range hits[lo:hi] {
		res.Items = append(res.Items, c.p)
		res.Offers = append(res.Offers, c.offer)
	}

	for _, c := range children {
		if n := childCount[c.ID]; n > 0 {
			res.Categories = append(res.Categories, FacetValue{Value: c.Handle, Label: c.Name, Count: n})
		}
	}
	for k, n := range brandCount {
		res.Brands = append(res.Brands, FacetValue{Value: k, Label: brandLabel[k], Count: n, Selected: brandSel[k]})
	}
	sort.Slice(res.Brands, func(i, j int) bool { return fold(res.Brands[i].Label) < fold(res.Brands[j].Label) })
	for i, a := range facetAttrs {
		if len(attrCount[i]) == 0 {
			continue
		}
		f := Facet{ID: a.ID, Label: a.Name}
		for v, n := range attrCount[i] {
			f.Values = append(f.Values, FacetValue{Value: v, Label: attrLabel[i][v], Count: n, Selected: contains(q.Attrs[a.ID], v)})
		}
		sortFacetValues(a, f.Values)
		res.Attrs = append(res.Attrs, f)
	}
	return res
}

// sortFacetValues orders select values as defined, numbers numerically and
// everything else alphabetically.
func sortFacetValues(a AttributeDef, vs []FacetValue) {
	switch a.Kind {
	case AttrSelect:
		pos := map[string]int{}
		for i, o := range a.Options {
			pos[o] = i + 1
		}
		sort.Slice(vs, func(i, j int) bool {
			pi, pj := pos[vs[i].Value], pos[vs[j].Value]
			if pi != pj && pi != 0 && pj != 0 {
				return pi < pj
			}
			if (pi == 0) != (pj == 0) {
				return pi != 0
			}
			return vs[i].Value < vs[j].Value
		})
	case AttrNumber:
		sort.Slice(vs, func(i, j int) bool {
			x, _ := strconv.ParseFloat(vs[i].Value, 64)
			y, _ := strconv.ParseFloat(vs[j].Value, 64)
			return x < y
		})
	case AttrBoolean:
		sort.Slice(vs, func(i, j int) bool { return vs[i].Value > vs[j].Value }) // Sí, No
	default:
		sort.Slice(vs, func(i, j int) bool { return fold(vs[i].Label) < fold(vs[j].Label) })
	}
}

// ValidateAttributeValue checks raw against the definition and returns the
// canonical stored form ("true"/"false" for booleans, plain decimals for
// numbers, the exact option for selects).
func ValidateAttributeValue(a AttributeDef, raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if len(raw) > 500 {
		return "", "El valor no puede pasar de 500 caracteres"
	}
	switch a.Kind {
	case AttrNumber:
		f, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64)
		if err != nil || f != f || f > 1e15 || f < -1e15 {
			return "", a.Name + " debe ser un número"
		}
		return strconv.FormatFloat(f, 'f', -1, 64), ""
	case AttrBoolean:
		switch fold(raw) {
		case "true", "si", "1", "yes", "verdadero":
			return "true", ""
		case "false", "no", "0", "falso":
			return "false", ""
		}
		return "", a.Name + " debe ser sí o no"
	case AttrSelect:
		for _, o := range a.Options {
			if fold(o) == fold(raw) {
				return o, ""
			}
		}
		return "", a.Name + " debe ser una de: " + strings.Join(a.Options, ", ")
	}
	return raw, ""
}
