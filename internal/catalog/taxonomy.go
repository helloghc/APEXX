package catalog

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Category is a node of the category tree.
type Category struct {
	ID             string `json:"id"`
	ParentID       string `json:"parentId"`
	Handle         string `json:"handle"`
	Name           string `json:"name"`
	Description    string `json:"description,omitempty"`
	SEOTitle       string `json:"seoTitle,omitempty"`
	SEODescription string `json:"seoDescription,omitempty"`
	Active         bool   `json:"active"`
	Position       int    `json:"position"`
}

// Brand is a product brand; its logo is an item of the media library.
type Brand struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	LogoMediaID string `json:"logoMediaId,omitempty"`
	Active      bool   `json:"active"`
}

// Collection kinds.
const (
	CollectionManual = "manual"
	CollectionRules  = "rules"
)

// CollectionRules selects products automatically. Empty groups are ignored;
// Match says whether a product must satisfy every group ("all") or any ("any").
type CollectionRuleSet struct {
	Match       string   `json:"match"` // all | any
	BrandIDs    []string `json:"brandIds,omitempty"`
	CategoryIDs []string `json:"categoryIds,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	PriceMin    *int64   `json:"priceMin,omitempty"` // minor units, inclusive
	PriceMax    *int64   `json:"priceMax,omitempty"`
}

// Empty reports whether no rule is set.
func (r CollectionRuleSet) Empty() bool {
	return len(r.BrandIDs) == 0 && len(r.CategoryIDs) == 0 && len(r.Tags) == 0 && r.PriceMin == nil && r.PriceMax == nil
}

// Collection groups products by hand or by rules.
type Collection struct {
	ID             string            `json:"id"`
	Handle         string            `json:"handle"`
	Title          string            `json:"title"`
	Description    string            `json:"description,omitempty"`
	Kind           string            `json:"kind"`
	ProductIDs     []string          `json:"productIds"`
	Rules          CollectionRuleSet `json:"rules"`
	SEOTitle       string            `json:"seoTitle,omitempty"`
	SEODescription string            `json:"seoDescription,omitempty"`
	Active         bool              `json:"active"`
}

// Attribute kinds.
const (
	AttrText    = "text"
	AttrNumber  = "number"
	AttrBoolean = "boolean"
	AttrSelect  = "select"
)

// AttributeDef defines a typed product specification.
type AttributeDef struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Options    []string `json:"options,omitempty"` // select only
	Unit       string   `json:"unit,omitempty"`    // number only, e.g. "cm"
	Filterable bool     `json:"filterable"`
	Position   int      `json:"position"`
}

// MediaVariant is one resized rendition of an image.
type MediaVariant struct {
	Width  int    `json:"w"`
	Height int    `json:"h"`
	Format string `json:"format"` // jpeg | png | webp
	Key    string `json:"key"`    // storage key, also the last part of its URL
	Bytes  int64  `json:"bytes"`
}

// Media is an image of the media library.
type Media struct {
	ID          string          `json:"id"`
	Filename    string          `json:"filename"`
	ContentType string          `json:"contentType"`
	Bytes       int64           `json:"bytes"`
	Width       int             `json:"width"`
	Height      int             `json:"height"`
	Alt         string          `json:"alt"`
	Variants    []MediaVariant  `json:"variants"`
	CreatedAt   time.Time       `json:"createdAt"`
	Edits       json.RawMessage `json:"edits,omitempty"` // non-destructive edits over the original
}

// Taxonomy is the in-memory read model of everything that organizes
// products: categories, brands, collections, attribute definitions and media.
// It is updated entity by entity as outbox events arrive.
type Taxonomy struct {
	mu       sync.RWMutex
	cats     map[string]Category
	catHdl   map[string]string   // handle -> id
	catName  map[string]string   // folded name -> id (legacy products store names)
	children map[string][]string // parent id -> child ids ordered by position
	brands   map[string]Brand
	brandHdl map[string]string
	brandNm  map[string]string // folded name -> id
	cols     map[string]Collection
	colHdl   map[string]string
	attrs    map[string]AttributeDef
	media    map[string]Media
}

// NewTaxonomy creates an empty taxonomy.
func NewTaxonomy() *Taxonomy {
	return &Taxonomy{
		cats: map[string]Category{}, catHdl: map[string]string{}, catName: map[string]string{}, children: map[string][]string{},
		brands: map[string]Brand{}, brandHdl: map[string]string{}, brandNm: map[string]string{},
		cols: map[string]Collection{}, colHdl: map[string]string{},
		attrs: map[string]AttributeDef{}, media: map[string]Media{},
	}
}

// ---------- categories ----------

func (t *Taxonomy) reindexCategories() {
	t.catHdl, t.catName, t.children = map[string]string{}, map[string]string{}, map[string][]string{}
	for id, c := range t.cats {
		t.catHdl[c.Handle] = id
		t.catName[fold(c.Name)] = id
		t.children[c.ParentID] = append(t.children[c.ParentID], id)
	}
	for _, ids := range t.children {
		sort.Slice(ids, func(i, j int) bool {
			a, b := t.cats[ids[i]], t.cats[ids[j]]
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			return a.Name < b.Name
		})
	}
}

// SetCategory creates or replaces a category.
func (t *Taxonomy) SetCategory(c Category) {
	t.mu.Lock()
	t.cats[c.ID] = c
	t.reindexCategories()
	t.mu.Unlock()
}

// DeleteCategory removes a category.
func (t *Taxonomy) DeleteCategory(id string) {
	t.mu.Lock()
	delete(t.cats, id)
	t.reindexCategories()
	t.mu.Unlock()
}

// Category returns a category by id.
func (t *Taxonomy) Category(id string) (Category, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.cats[id]
	return c, ok
}

// CategoryByHandle returns a category by handle.
func (t *Taxonomy) CategoryByHandle(h string) (Category, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.cats[t.catHdl[h]]
	return c, ok
}

// ResolveCategory accepts an id, a handle or a name and returns the category id.
func (t *Taxonomy) ResolveCategory(s string) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.resolveCategoryLocked(s)
}

func (t *Taxonomy) resolveCategoryLocked(s string) (string, bool) {
	if _, ok := t.cats[s]; ok {
		return s, true
	}
	if id, ok := t.catHdl[s]; ok {
		return id, true
	}
	id, ok := t.catName[fold(s)]
	return id, ok
}

// Categories returns the whole tree flattened in display order (depth first).
func (t *Taxonomy) Categories() []Category {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Category, 0, len(t.cats))
	seen := map[string]bool{}
	var walk func(parent string)
	walk = func(parent string) {
		for _, id := range t.children[parent] {
			if seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, t.cats[id])
			walk(id)
		}
	}
	walk("")
	// Orphans (parent deleted on another node a moment ago) still show up.
	for id, c := range t.cats {
		if !seen[id] {
			out = append(out, c)
		}
	}
	return out
}

// Children returns the direct children of parent ("" = roots) in order.
func (t *Taxonomy) Children(parent string) []Category {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Category, 0, len(t.children[parent]))
	for _, id := range t.children[parent] {
		out = append(out, t.cats[id])
	}
	return out
}

// Path returns the ancestors of id from the root down to id itself.
func (t *Taxonomy) Path(id string) []Category {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var rev []Category
	for i := 0; id != "" && i < 32; i++ {
		c, ok := t.cats[id]
		if !ok {
			break
		}
		rev = append(rev, c)
		id = c.ParentID
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Subtree returns id and every descendant id.
func (t *Taxonomy) Subtree(id string) map[string]bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := map[string]bool{}
	var walk func(string)
	walk = func(x string) {
		if out[x] {
			return
		}
		out[x] = true
		for _, c := range t.children[x] {
			walk(c)
		}
	}
	walk(id)
	return out
}

// ProductCategories resolves the categories of p to ids, including every
// ancestor, so a product in "Playeras" also belongs to "Ropa".
func (t *Taxonomy) ProductCategories(p *Product) map[string]bool {
	if len(p.Categories) == 0 {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := map[string]bool{}
	for _, raw := range p.Categories {
		id, ok := t.resolveCategoryLocked(raw)
		for i := 0; ok && id != "" && !out[id] && i < 32; i++ {
			out[id] = true
			id = t.cats[id].ParentID
			_, ok = t.cats[id]
		}
	}
	return out
}

// ---------- brands ----------

// SetBrand creates or replaces a brand.
func (t *Taxonomy) SetBrand(b Brand) {
	t.mu.Lock()
	if old, ok := t.brands[b.ID]; ok {
		delete(t.brandHdl, old.Handle)
		delete(t.brandNm, fold(old.Name))
	}
	t.brands[b.ID] = b
	t.brandHdl[b.Handle] = b.ID
	t.brandNm[fold(b.Name)] = b.ID
	t.mu.Unlock()
}

// DeleteBrand removes a brand.
func (t *Taxonomy) DeleteBrand(id string) {
	t.mu.Lock()
	if old, ok := t.brands[id]; ok {
		delete(t.brandHdl, old.Handle)
		delete(t.brandNm, fold(old.Name))
		delete(t.brands, id)
	}
	t.mu.Unlock()
}

// Brand returns a brand by id.
func (t *Taxonomy) Brand(id string) (Brand, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	b, ok := t.brands[id]
	return b, ok
}

// BrandByName returns a brand by its name, ignoring case and accents.
func (t *Taxonomy) BrandByName(name string) (Brand, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	b, ok := t.brands[t.brandNm[fold(name)]]
	return b, ok
}

// Brands returns every brand ordered by name.
func (t *Taxonomy) Brands() []Brand {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Brand, 0, len(t.brands))
	for _, b := range t.brands {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return fold(out[i].Name) < fold(out[j].Name) })
	return out
}

// BrandKey returns the stable facet key and label of the brand of p: the
// brand's handle when p points to a brand (by id or by name), otherwise a slug
// of the free-text brand name.
func (t *Taxonomy) BrandKey(p *Product) (key, label string) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if b, ok := t.brands[p.BrandID]; ok && p.BrandID != "" {
		return b.Handle, b.Name
	}
	if p.Brand == "" {
		return "", ""
	}
	if b, ok := t.brands[t.brandNm[fold(p.Brand)]]; ok {
		return b.Handle, b.Name
	}
	return Slug(p.Brand), p.Brand
}

// ---------- collections ----------

// SetCollection creates or replaces a collection.
func (t *Taxonomy) SetCollection(c Collection) {
	t.mu.Lock()
	if old, ok := t.cols[c.ID]; ok {
		delete(t.colHdl, old.Handle)
	}
	t.cols[c.ID] = c
	t.colHdl[c.Handle] = c.ID
	t.mu.Unlock()
}

// DeleteCollection removes a collection.
func (t *Taxonomy) DeleteCollection(id string) {
	t.mu.Lock()
	if old, ok := t.cols[id]; ok {
		delete(t.colHdl, old.Handle)
		delete(t.cols, id)
	}
	t.mu.Unlock()
}

// Collection returns a collection by id.
func (t *Taxonomy) Collection(id string) (Collection, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.cols[id]
	return c, ok
}

// CollectionByHandle returns a collection by handle.
func (t *Taxonomy) CollectionByHandle(h string) (Collection, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.cols[t.colHdl[h]]
	return c, ok
}

// Collections returns every collection ordered by title.
func (t *Taxonomy) Collections() []Collection {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Collection, 0, len(t.cols))
	for _, c := range t.cols {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return fold(out[i].Title) < fold(out[j].Title) })
	return out
}

// ---------- attributes ----------

// SetAttribute creates or replaces an attribute definition.
func (t *Taxonomy) SetAttribute(a AttributeDef) {
	t.mu.Lock()
	t.attrs[a.ID] = a
	t.mu.Unlock()
}

// DeleteAttribute removes an attribute definition.
func (t *Taxonomy) DeleteAttribute(id string) {
	t.mu.Lock()
	delete(t.attrs, id)
	t.mu.Unlock()
}

// Attribute returns a definition by id.
func (t *Taxonomy) Attribute(id string) (AttributeDef, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	a, ok := t.attrs[id]
	return a, ok
}

// Attributes returns every definition in display order.
func (t *Taxonomy) Attributes() []AttributeDef {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]AttributeDef, 0, len(t.attrs))
	for _, a := range t.attrs {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ---------- media ----------

// SetMedia creates or replaces a media item.
func (t *Taxonomy) SetMedia(m Media) {
	t.mu.Lock()
	t.media[m.ID] = m
	t.mu.Unlock()
}

// DeleteMedia removes a media item.
func (t *Taxonomy) DeleteMedia(id string) {
	t.mu.Lock()
	delete(t.media, id)
	t.mu.Unlock()
}

// Media returns a media item by id.
func (t *Taxonomy) Media(id string) (Media, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m, ok := t.media[id]
	return m, ok
}

// Counts returns how many entities of each kind are loaded.
func (t *Taxonomy) Counts() (categories, brands, collections, attributes, media int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.cats), len(t.brands), len(t.cols), len(t.attrs), len(t.media)
}

// ---------- helpers ----------

// Slug turns any text into a URL handle: "Tenis Pacífico 2" → "tenis-pacifico-2".
func Slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range fold(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			// letters outside a-z are dropped after folding
		default:
			dash = true
		}
	}
	return b.String()
}

// ValidHandle reports whether h is a well-formed handle.
func ValidHandle(h string) bool {
	return h != "" && len(h) <= 120 && Slug(h) == h
}

// Fold lowercases and strips accents; exported for callers that compare names.
func Fold(s string) string { return fold(strings.TrimSpace(s)) }
