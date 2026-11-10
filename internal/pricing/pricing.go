// Package pricing resolves the unit price of a SKU for a sales channel and
// quantity. Tier prices (VTEX "fixedPrices" with minQuantity) are first-class.
package pricing

import (
	"sort"
	"sync"

	"github.com/helloghc/apexx/internal/money"
)

// Tier is a unit price that applies from MinQty units upward.
type Tier struct {
	MinQty    int64        `json:"minQty"`
	Unit      money.Money  `json:"unit"`
	CompareAt *money.Money `json:"compareAt,omitempty"` // "list price" shown crossed out
}

// Quote is the resolved price for a quantity.
type Quote struct {
	Unit      money.Money  `json:"unit"`
	CompareAt *money.Money `json:"compareAt,omitempty"`
	TierMin   int64        `json:"tierMinQty"`
}

// Service holds price tiers per channel and SKU.
type Service struct {
	mu     sync.RWMutex
	prices map[string]map[string][]Tier // channel -> sku -> tiers sorted by MinQty
}

// New creates an empty price book.
func New() *Service { return &Service{prices: map[string]map[string][]Tier{}} }

// Set replaces the tiers of sku in channel.
func (s *Service) Set(channel, sku string, tiers ...Tier) {
	t := append([]Tier(nil), tiers...)
	sort.Slice(t, func(i, j int) bool { return t[i].MinQty < t[j].MinQty })
	s.mu.Lock()
	if s.prices[channel] == nil {
		s.prices[channel] = map[string][]Tier{}
	}
	s.prices[channel][sku] = t
	s.mu.Unlock()
}

// Resolve picks the tier with the highest MinQty <= qty.
func (s *Service) Resolve(channel, sku string, qty int64) (Quote, bool) {
	s.mu.RLock()
	tiers := s.prices[channel][sku]
	s.mu.RUnlock()
	var best *Tier
	for i := range tiers {
		if tiers[i].MinQty <= qty {
			best = &tiers[i]
		}
	}
	if best == nil {
		return Quote{}, false
	}
	return Quote{Unit: best.Unit, CompareAt: best.CompareAt, TierMin: best.MinQty}, true
}
