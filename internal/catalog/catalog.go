// Package catalog stores products and variants with one canonical model and
// cursor pagination without an offset ceiling (audit finding H3: VTEX search
// stops at 2,500 results).
package catalog

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/events"
)

// TopicChanged is published when a product is created or updated.
const TopicChanged = "catalog.product.changed"

// ErrNotFound is returned for unknown ids.
var ErrNotFound = errors.New("catalog: not found")

// Variant is a sellable SKU.
type Variant struct {
	SKU         string            `json:"sku"`
	Title       string            `json:"title"`
	Options     map[string]string `json:"options,omitempty"`
	Barcode     string            `json:"barcode,omitempty"`
	WeightGrams int64             `json:"weightGrams"`
	MediaID     string            `json:"mediaId,omitempty"` // image of this variant
}

// Option is a product option such as "Talla" with its values in display order.
type Option struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// Related lists products shown next to this one.
type Related struct {
	Similar     []string `json:"similar,omitempty"`
	Accessories []string `json:"accessories,omitempty"`
}

// Product groups variants.
type Product struct {
	ID          string            `json:"id"`
	Handle      string            `json:"handle"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Brand       string            `json:"brand,omitempty"`
	Categories  []string          `json:"categories,omitempty"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Status      string            `json:"status"` // active | draft | archived
	Variants    []Variant         `json:"variants"`
	Version     int64             `json:"version"`
	UpdatedAt   time.Time         `json:"updatedAt"`

	// Advanced catalog. Categories holds category ids (legacy rows may hold
	// names or handles; the taxonomy resolves them). Attributes maps an
	// attribute definition id to its value.
	BrandID        string     `json:"brandId,omitempty"`
	Type           string     `json:"type,omitempty"`
	Tags           []string   `json:"tags,omitempty"`
	Options        []Option   `json:"options,omitempty"`
	SEOTitle       string     `json:"seoTitle,omitempty"`
	SEODescription string     `json:"seoDescription,omitempty"`
	PublishAt      *time.Time `json:"publishAt,omitempty"` // a draft becomes active at this time
	Media          []string   `json:"media,omitempty"`     // ordered media ids; the first is the cover
	Related        Related    `json:"related"`
	CreatedAt      time.Time  `json:"createdAt"`

	hay []string // folded search words, computed on Upsert
}

// Store keeps products ordered by id.
type Store struct {
	mu    sync.RWMutex
	byID  map[string]*Product
	byHdl map[string]string // handle -> product id
	bySKU map[string]string // sku -> product id
	ids   []string          // sorted
	bus   *events.Bus
}

// New creates an empty store.
func New(bus *events.Bus) *Store {
	return &Store{byID: map[string]*Product{}, byHdl: map[string]string{}, bySKU: map[string]string{}, bus: bus}
}

// Upsert creates or replaces a product. A zero UpdatedAt means "now".
func (s *Store) Upsert(p Product) {
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = time.Now().UTC()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = p.UpdatedAt
	}
	if p.Status == "" {
		p.Status = "active"
	}
	p.hay = haystack(&p)
	s.mu.Lock()
	if old, ok := s.byID[p.ID]; ok {
		for _, v := range old.Variants {
			delete(s.bySKU, v.SKU)
		}
		if s.byHdl[old.Handle] == p.ID {
			delete(s.byHdl, old.Handle)
		}
	} else {
		i := sort.SearchStrings(s.ids, p.ID)
		s.ids = append(s.ids, "")
		copy(s.ids[i+1:], s.ids[i:])
		s.ids[i] = p.ID
	}
	cp := p
	s.byID[p.ID] = &cp
	s.byHdl[p.Handle] = p.ID
	for _, v := range p.Variants {
		s.bySKU[v.SKU] = p.ID
	}
	s.mu.Unlock()
	if s.bus != nil {
		s.bus.Publish(events.Event{Topic: TopicChanged, Key: p.ID})
	}
}

// Get returns a product by id.
func (s *Store) Get(id string) (Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	if !ok {
		return Product{}, ErrNotFound
	}
	return *p, nil
}

// VariantBySKU returns a variant and its product.
func (s *Store) VariantBySKU(sku string) (Variant, Product, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pid, ok := s.bySKU[sku]
	if !ok {
		return Variant{}, Product{}, ErrNotFound
	}
	p := s.byID[pid]
	for _, v := range p.Variants {
		if v.SKU == sku {
			return v, *p, nil
		}
	}
	return Variant{}, Product{}, ErrNotFound
}

// List returns up to limit products with id greater than after, plus the last id
// returned when more remain.
func (s *Store) List(after string, limit int) ([]Product, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i := sort.SearchStrings(s.ids, after)
	if i < len(s.ids) && s.ids[i] == after {
		i++
	}
	out := make([]Product, 0, limit)
	for ; i < len(s.ids) && len(out) < limit; i++ {
		out = append(out, *s.byID[s.ids[i]])
	}
	next := ""
	if i < len(s.ids) && len(out) > 0 {
		next = out[len(out)-1].ID
	}
	return out, next
}

// Count returns the number of products.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.ids)
}

// Delete removes a product (it was deleted from the database).
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.byID[id]
	if !ok {
		return
	}
	for _, v := range old.Variants {
		delete(s.bySKU, v.SKU)
	}
	if s.byHdl[old.Handle] == id {
		delete(s.byHdl, old.Handle)
	}
	delete(s.byID, id)
	if i := sort.SearchStrings(s.ids, id); i < len(s.ids) && s.ids[i] == id {
		s.ids = append(s.ids[:i], s.ids[i+1:]...)
	}
}

// Products returns every product in id order. The pointed-to products are
// never modified after Upsert (each Upsert stores a fresh copy), so callers
// may read them without holding a lock but must not write to them.
func (s *Store) Products() []*Product {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Product, len(s.ids))
	for i, id := range s.ids {
		out[i] = s.byID[id]
	}
	return out
}

// IDByHandle returns the id of the product with handle h, in any status.
func (s *Store) IDByHandle(h string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byHdl[h]
	return id, ok
}

// ProductOfSKU returns the id of the product that owns sku.
func (s *Store) ProductOfSKU(sku string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.bySKU[sku]
	return id, ok
}
