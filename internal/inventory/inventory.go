// Package inventory is the single source of truth for stock. Availability
// shown in listings, search and checkout is always read from here or from a
// cache invalidated by its events, so the store never shows 100 units while
// checkout can only allocate 14 (audit finding H1).
package inventory

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/events"
)

// TopicChanged is published whenever available stock for a SKU changes.
const TopicChanged = "inventory.changed"

// ErrInsufficient means a reservation could not be fully satisfied.
var ErrInsufficient = errors.New("inventory: insufficient stock")

// Level is the stock of one SKU at one location.
type Level struct {
	LocationID string `json:"locationId"`
	OnHand     int64  `json:"onHand"`
	Reserved   int64  `json:"reserved"`
}

// Available is what can still be sold.
func (l Level) Available() int64 {
	if a := l.OnHand - l.Reserved; a > 0 {
		return a
	}
	return 0
}

// Snapshot is the public view of a SKU's stock.
type Snapshot struct {
	SKU       string    `json:"sku"`
	Available int64     `json:"available"`
	Levels    []Level   `json:"levels"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Components is set for a kit: its stock is derived from these SKUs.
	Components []Component `json:"components,omitempty"`
	// Preorder is set while the SKU is sold before it is in stock.
	Preorder *Preorder `json:"preorder,omitempty"`
}

// Preorder is how a SKU on preorder is sold: from a pool of units (Left < 0
// means no limit), shipping on ShipsOn, with DepositBps of the price paid now.
type Preorder struct {
	ShipsOn    string `json:"shipsOn"`
	DepositBps int    `json:"depositBps"`
	Left       int64  `json:"left"`
}

// Unlimited is the availability shown for a preorder without a limit.
const Unlimited = 999999

// Component is one SKU (and how many units) inside a kit.
type Component struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

type reservation struct {
	sku, location string
	qty           int64
	expires       time.Time
}

// Service stores stock levels and reservations.
type Service struct {
	mu           sync.Mutex
	levels       map[string]map[string]*Level // sku -> location -> level
	updated      map[string]time.Time
	reservations map[string][]reservation // holder (cart id) -> reservations
	bundles      map[string][]Component   // kit sku -> components
	preorders    map[string]Preorder      // sku on preorder
	bus          *events.Bus
	now          func() time.Time
}

// New creates an empty inventory.
func New(bus *events.Bus) *Service {
	return &Service{
		levels:       map[string]map[string]*Level{},
		updated:      map[string]time.Time{},
		reservations: map[string][]reservation{},
		bundles:      map[string][]Component{},
		preorders:    map[string]Preorder{},
		bus:          bus,
		now:          time.Now,
	}
}

// Set overwrites on-hand stock of sku at location.
func (s *Service) Set(sku, location string, onHand int64) {
	s.mu.Lock()
	l := s.level(sku, location)
	l.OnHand = onHand
	snap := s.snapshotLocked(sku)
	s.mu.Unlock()
	s.publish(snap)
}

// Load replaces every level of sku with the given values (used by the
// projector to mirror the database, which owns reservations).
func (s *Service) Load(sku string, levels []Level) {
	s.mu.Lock()
	m := map[string]*Level{}
	for _, l := range levels {
		cp := l
		m[l.LocationID] = &cp
	}
	s.levels[sku] = m
	s.updated[sku] = s.now()
	snap := s.snapshotLocked(sku)
	s.mu.Unlock()
	s.publish(snap)
}

// Get returns the snapshot for sku. ok is false if the SKU has no stock records,
// so callers can return 404 instead of a silent zero (VTEX returns 200 + 0).
func (s *Service) Get(sku string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	if p, ok := s.preorders[sku]; ok {
		snap := Snapshot{SKU: sku, UpdatedAt: s.updated[sku].UTC(), Available: p.Left, Preorder: &p}
		if p.Left < 0 {
			snap.Available = Unlimited
		}
		return snap, true
	}
	if comps, ok := s.bundles[sku]; ok {
		return s.bundleLocked(sku, comps), true
	}
	if _, ok := s.levels[sku]; !ok {
		return Snapshot{}, false
	}
	return s.snapshotLocked(sku), true
}

// SetBundle makes sku a kit built from comps; nil or empty turns it back into
// a normal SKU. A kit is sellable while every component is: its availability
// is the smallest whole number of kits the components can still form.
func (s *Service) SetBundle(sku string, comps []Component) {
	s.mu.Lock()
	if len(comps) == 0 {
		delete(s.bundles, sku)
	} else {
		s.bundles[sku] = append([]Component(nil), comps...)
	}
	s.updated[sku] = s.now()
	s.mu.Unlock()
}

// SetPreorder puts sku on preorder (nil takes it off).
func (s *Service) SetPreorder(sku string, p *Preorder) {
	s.mu.Lock()
	if p == nil {
		delete(s.preorders, sku)
	} else {
		s.preorders[sku] = *p
	}
	s.updated[sku] = s.now()
	s.mu.Unlock()
}

// PreorderOf returns the preorder of sku, if it is on one.
func (s *Service) PreorderOf(sku string) (Preorder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.preorders[sku]
	return p, ok
}

// Bundle returns the components of a kit (nil for a normal SKU).
func (s *Service) Bundle(sku string) []Component {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Component(nil), s.bundles[sku]...)
}

func (s *Service) bundleLocked(sku string, comps []Component) Snapshot {
	snap := Snapshot{SKU: sku, UpdatedAt: s.updated[sku].UTC(), Components: append([]Component(nil), comps...)}
	first := true
	for _, c := range comps {
		if c.Qty <= 0 {
			continue
		}
		var avail int64
		for _, l := range s.levels[c.SKU] {
			avail += l.Available()
		}
		if n := avail / c.Qty; first || n < snap.Available {
			snap.Available = n
		}
		if t := s.updated[c.SKU]; t.After(snap.UpdatedAt) {
			snap.UpdatedAt = t.UTC()
		}
		first = false
	}
	return snap
}

// Available returns sellable units for sku across locations.
func (s *Service) Available(sku string) int64 {
	snap, _ := s.Get(sku)
	return snap.Available
}

// Reserve holds qty units of sku for holder until ttl expires. It allocates
// from locations in id order and is all-or-nothing.
func (s *Service) Reserve(holder, sku string, qty int64, ttl time.Duration) error {
	s.mu.Lock()
	s.expireLocked()
	locs := s.levels[sku]
	var total int64
	for _, l := range locs {
		total += l.Available()
	}
	if total < qty {
		s.mu.Unlock()
		return ErrInsufficient
	}
	ids := make([]string, 0, len(locs))
	for id := range locs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	left := qty
	exp := s.now().Add(ttl)
	for _, id := range ids {
		if left == 0 {
			break
		}
		l := locs[id]
		take := min(l.Available(), left)
		if take == 0 {
			continue
		}
		l.Reserved += take
		left -= take
		s.reservations[holder] = append(s.reservations[holder], reservation{sku: sku, location: id, qty: take, expires: exp})
	}
	snap := s.snapshotLocked(sku)
	s.updated[sku] = s.now()
	s.mu.Unlock()
	s.publish(snap)
	return nil
}

// Release drops every reservation held by holder.
func (s *Service) Release(holder string) {
	s.mu.Lock()
	touched := s.releaseLocked(holder)
	snaps := make([]Snapshot, 0, len(touched))
	for sku := range touched {
		snaps = append(snaps, s.snapshotLocked(sku))
	}
	s.mu.Unlock()
	for _, sn := range snaps {
		s.publish(sn)
	}
}

func (s *Service) releaseLocked(holder string) map[string]bool {
	touched := map[string]bool{}
	for _, r := range s.reservations[holder] {
		if l := s.levels[r.sku][r.location]; l != nil {
			l.Reserved -= r.qty
			touched[r.sku] = true
		}
	}
	delete(s.reservations, holder)
	return touched
}

func (s *Service) expireLocked() {
	now := s.now()
	for holder, rs := range s.reservations {
		if len(rs) > 0 && now.After(rs[0].expires) {
			s.releaseLocked(holder)
		}
	}
}

func (s *Service) level(sku, location string) *Level {
	m := s.levels[sku]
	if m == nil {
		m = map[string]*Level{}
		s.levels[sku] = m
	}
	l := m[location]
	if l == nil {
		l = &Level{LocationID: location}
		m[location] = l
	}
	s.updated[sku] = s.now()
	return l
}

func (s *Service) snapshotLocked(sku string) Snapshot {
	snap := Snapshot{SKU: sku, UpdatedAt: s.updated[sku].UTC()}
	for _, l := range s.levels[sku] {
		snap.Levels = append(snap.Levels, *l)
		snap.Available += l.Available()
	}
	sort.Slice(snap.Levels, func(i, j int) bool { return snap.Levels[i].LocationID < snap.Levels[j].LocationID })
	return snap
}

func (s *Service) publish(snap Snapshot) {
	if s.bus != nil {
		s.bus.Publish(events.Event{Topic: TopicChanged, Key: snap.SKU, Data: snap})
	}
}

// StockCounts counts SKUs with stock rows by what they have left (on hand
// minus reserved, summed over locations): low is 1..threshold, out is 0 or
// less. Same numbers as the report's SQL, from memory.
func (s *Service) StockCounts(threshold int64) (low, out int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, locs := range s.levels {
		if len(locs) == 0 {
			continue
		}
		var avail int64
		for _, l := range locs {
			avail += l.OnHand - l.Reserved
		}
		switch {
		case avail <= 0:
			out++
		case avail <= threshold:
			low++
		}
	}
	return low, out
}
