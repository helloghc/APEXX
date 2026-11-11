// Package pricing resolves the unit price of a SKU for a sales channel and
// quantity. Tier prices (VTEX "fixedPrices" with minQuantity) are first-class.
package pricing

import (
	"sort"
	"sync"
	"time"

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
	List      string       `json:"priceList,omitempty"`     // id of the price list that set this price
	ListName  string       `json:"priceListName,omitempty"` // and its name, to explain the price
}

// List is a price list: a scheduled override or percentage adjustment for a
// channel and (later) a customer group. Among the lists that apply to a SKU
// the one with the highest priority wins.
type List struct {
	ID            string
	Name          string
	Active        bool
	Channel       string // "" = every channel
	CustomerGroup string // "" = every customer
	Kind          string // override | adjust
	AdjustBps     int64  // adjust: -1000 = 10% cheaper
	All           bool   // adjust: applies to the whole catalog
	Priority      int
	StartsAt      *time.Time
	EndsAt        *time.Time
	Items         map[string]int64 // sku -> override amount (ignored for adjust)
}

// Applies reports whether the list is in force for channel, group and now.
func (l *List) Applies(channel, group string, now time.Time) bool {
	if !l.Active || (l.Channel != "" && l.Channel != channel) || (l.CustomerGroup != "" && l.CustomerGroup != group) {
		return false
	}
	if l.StartsAt != nil && now.Before(*l.StartsAt) {
		return false
	}
	return l.EndsAt == nil || !now.After(*l.EndsAt)
}

// Service holds price tiers per channel and SKU.
type Service struct {
	mu     sync.RWMutex
	prices map[string]map[string][]Tier // channel -> sku -> tiers sorted by MinQty
	lists  []*List                      // sorted by priority desc, id
}

// SetList adds or replaces a price list.
func (s *Service) SetList(l List) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeList(l.ID)
	cp := l
	s.lists = append(s.lists, &cp)
	sort.SliceStable(s.lists, func(i, j int) bool {
		if s.lists[i].Priority != s.lists[j].Priority {
			return s.lists[i].Priority > s.lists[j].Priority
		}
		return s.lists[i].ID < s.lists[j].ID
	})
}

// RemoveList deletes a price list.
func (s *Service) RemoveList(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeList(id)
}

func (s *Service) removeList(id string) {
	kept := s.lists[:0:0]
	for _, l := range s.lists {
		if l.ID != id {
			kept = append(kept, l)
		}
	}
	s.lists = kept
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

// Tiers returns a copy of the tiers of sku in channel.
func (s *Service) Tiers(channel, sku string) []Tier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Tier(nil), s.prices[channel][sku]...)
}

// Resolve picks the tier with the highest MinQty <= qty and applies the price
// list in force right now, if any (no customer group).
func (s *Service) Resolve(channel, sku string, qty int64) (Quote, bool) {
	return s.ResolveAt(channel, sku, qty, "", time.Now())
}

// Base returns the price before price lists.
func (s *Service) Base(channel, sku string, qty int64) (Quote, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.base(channel, sku, qty)
}

func (s *Service) base(channel, sku string, qty int64) (Quote, bool) {
	tiers := s.prices[channel][sku]
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

// ResolveAt resolves the price for a customer group at a given instant.
// Schedules are checked at read time, so a list starts and ends exactly on
// time without any job or cache to refresh.
func (s *Service) ResolveAt(channel, sku string, qty int64, group string, now time.Time) (Quote, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q, ok := s.base(channel, sku, qty)
	if !ok {
		return q, false
	}
	for _, l := range s.lists {
		if !l.Applies(channel, group, now) {
			continue
		}
		unit := q.Unit.Amount
		if l.Kind == "override" {
			amt, has := l.Items[sku]
			if !has {
				continue
			}
			unit = amt
		} else {
			if _, has := l.Items[sku]; !has && !l.All {
				continue
			}
			unit = q.Unit.Amount + money.New(q.Unit.Amount, "").Percent(abs(l.AdjustBps)).Amount*sign(l.AdjustBps)
			if unit < 0 {
				unit = 0
			}
		}
		if unit < q.Unit.Amount {
			// The regular price becomes the crossed-out reference.
			ref := q.Unit
			if q.CompareAt != nil && q.CompareAt.Amount > ref.Amount {
				ref = *q.CompareAt
			}
			q.CompareAt = &ref
		} else if q.CompareAt != nil && q.CompareAt.Amount <= unit {
			q.CompareAt = nil
		}
		q.Unit.Amount, q.List, q.ListName = unit, l.ID, l.Name
		return q, true
	}
	return q, true
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int64) int64 {
	if v < 0 {
		return -1
	}
	return 1
}
