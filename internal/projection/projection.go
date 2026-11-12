// Package projection replaces the "indexer" found in VTEX and Magento. The
// database is the only source of truth; every write also appends an outbox
// event in the same transaction. The projector consumes the outbox (LISTEN/
// NOTIFY plus a polling fallback) and updates per-tenant in-memory read
// models immediately, entity by entity. There is no full reindex, no batch
// window and no stale cache TTL: freshness is measured per event and exposed
// as a metric.
package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/promostore"
	"github.com/helloghc/apexx/internal/store"
)

// Tenant holds the read models of one tenant.
type Tenant struct {
	ID        string
	Catalog   *catalog.Store
	Inventory *inventory.Service
	Pricing   *pricing.Service
	Cart      *cart.Service
	Taxo      *catalog.Taxonomy // categories, brands, collections, attributes, media

	mu sync.RWMutex // guards Cart.Channels, Cart.Promos and Cart.Logistics swaps
	siteState

	version atomic.Uint64 // grows with every event applied to the tenant
}

// Version grows every time an event of the tenant is applied. A reader can
// keep a result computed at one version and reuse it until the number moves.
func (t *Tenant) Version() uint64 { return t.version.Load() }

// Promos returns the promotion engine of the tenant. The engine is safe for
// concurrent use and is updated promotion by promotion as events arrive.
func (t *Tenant) Promos() *promo.Engine { return t.Cart.Promos }

// Simulate runs a cart simulation with a consistent view of channels and promotions.
func (t *Tenant) Simulate(req cart.Request) (cart.Result, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// Lines bought on subscription get the store's subscription discount. A
	// plan the store does not offer for that product is cleared, so callers
	// can trust Items[i].Plan after the call.
	cfg := t.Subscriptions()
	var skus []string
	for i := range req.Items {
		if req.Items[i].Plan == "" {
			continue
		}
		_, p, err := t.Catalog.VariantBySKU(req.Items[i].SKU)
		if err != nil || !cfg.Allows(p.ID, req.Items[i].Plan) {
			req.Items[i].Plan = ""
			continue
		}
		skus = append(skus, req.Items[i].SKU)
	}
	if len(skus) > 0 && cfg.DiscountBps > 0 {
		req.Extra = append(append([]promo.Promotion{}, req.Extra...), promo.Promotion{ID: "subscription", Name: "Suscríbete y ahorra", Type: promo.TypeItemPercent,
			Status: "active", Bps: cfg.DiscountBps, Target: promo.Target{SKUs: skus}})
	}
	return t.Cart.Simulate(req)
}

// Channel returns a channel configuration.
func (t *Tenant) Channel(id string) (cart.Channel, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	c, ok := t.Cart.Channels[id]
	return c, ok
}

// ChannelIDs lists the configured sales channels.
func (t *Tenant) ChannelIDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.Cart.Channels))
	for id := range t.Cart.Channels {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Stats describes projection freshness.
type Stats struct {
	Applied   int64   `json:"eventsApplied"`
	LastID    int64   `json:"lastEventId"`
	LastLagMs float64 `json:"lastLagMs"`
	MaxLagMs  float64 `json:"maxLagMs"`
	AvgLagMs  float64 `json:"avgLagMs"`
	Errors    int64   `json:"errors"`
	Tenants   int     `json:"tenants"`
	LoadedAt  string  `json:"loadedAt"`
}

// Projector keeps every tenant's read models in sync with the database.
type Projector struct {
	pre      map[[2]string]*catalog.Product // products prefetched for the batch being drained (drain only)
	preStock map[[2]string]stockPre         // stock of SKUs prefetched for the batch (drain only)
	prePrice map[[3]string][]pricing.Tier   // price tiers (tenant, channel, sku) prefetched for the batch
	db       *store.DB
	ps       *promostore.Store
	listen   string // connection string for LISTEN (system role)
	log      *slog.Logger
	mu       sync.RWMutex
	tenants  map[string]*Tenant
	cursor   atomic.Int64

	applied  atomic.Int64
	errs     atomic.Int64
	lagSumUs atomic.Int64
	lastLag  atomic.Int64
	maxLag   atomic.Int64
	loadedAt time.Time
	wake     chan struct{}
	drainMu  sync.Mutex // one drain at a time: Run and Drain callers share the cursor

	hostIdx hostIndex

	// liveRates answers carrier rates for a store (set by the server; nil
	// until then, and carts fall back to their fixed rates).
	liveRates atomic.Pointer[LiveRateFunc]
	extTax    atomic.Pointer[ExternalTaxFunc]
	extRules  atomic.Pointer[ExternalRulesFunc]

	hooksMu sync.RWMutex
	hooks   map[string][]func(tenant, key string)
}

// On registers fn for events of topic that the projector itself does not
// handle (for example site releases, so every node drops its cached site).
func (p *Projector) On(topic string, fn func(tenant, key string)) {
	p.hooksMu.Lock()
	if p.hooks == nil {
		p.hooks = map[string][]func(string, string){}
	}
	p.hooks[topic] = append(p.hooks[topic], fn)
	p.hooksMu.Unlock()
}

// New creates a projector.
func New(db *store.DB, listenURL string, log *slog.Logger) *Projector {
	return &Projector{db: db, ps: &promostore.Store{DB: db}, listen: listenURL, log: log, tenants: map[string]*Tenant{}, wake: make(chan struct{}, 1)}
}

// Tenant returns the read models of id.
func (p *Projector) Tenant(id string) (*Tenant, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.tenants[id]
	return t, ok
}

// TenantIDs lists the stores loaded in memory.
func (p *Projector) TenantIDs() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.tenants))
	for id := range p.tenants {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Stats returns freshness statistics.
func (p *Projector) Stats() Stats {
	p.mu.RLock()
	n := len(p.tenants)
	p.mu.RUnlock()
	applied := p.applied.Load()
	s := Stats{
		Applied: applied, LastID: p.cursor.Load(), Errors: p.errs.Load(), Tenants: n,
		LastLagMs: float64(p.lastLag.Load()) / 1000, MaxLagMs: float64(p.maxLag.Load()) / 1000,
		LoadedAt: p.loadedAt.UTC().Format(time.RFC3339),
	}
	if applied > 0 {
		s.AvgLagMs = float64(p.lagSumUs.Load()) / float64(applied) / 1000
	}
	return s
}

// Load builds every tenant's read models from the database. It records the
// outbox position first, so no event committed during the load is lost.
func (p *Projector) Load(ctx context.Context) error {
	start, err := p.db.OutboxMax(ctx)
	if err != nil {
		return err
	}
	ids, err := p.db.Tenants(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := p.loadTenant(ctx, id); err != nil {
			return fmt.Errorf("load tenant %s: %w", id, err)
		}
	}
	if err := p.loadHosts(ctx); err != nil {
		return err
	}
	p.cursor.Store(start)
	p.loadedAt = time.Now()
	return nil
}

func (p *Projector) loadTenant(ctx context.Context, id string) error {
	t := &Tenant{ID: id, Catalog: catalog.New(nil), Inventory: inventory.New(nil), Pricing: pricing.New(), Taxo: catalog.NewTaxonomy()}
	if err := p.loadTaxonomy(ctx, t); err != nil {
		return err
	}
	t.Cart = &cart.Service{Catalog: t.Catalog, Inventory: t.Inventory, Pricing: t.Pricing, Channels: map[string]cart.Channel{}, Promos: promo.NewEngine(), Taxo: t.Taxo}
	t.Cart.ExternalTax = func(country, postal, currency string, exclusive bool, parts []logistics.TaxPart) (logistics.TaxResult, bool) {
		if fn := p.extTax.Load(); fn != nil {
			return (*fn)(id, country, postal, currency, exclusive, parts)
		}
		return logistics.TaxResult{}, false
	}
	t.Cart.ExternalRules = func(in cart.RulesInput) (cart.RulesResult, bool) {
		if fn := p.extRules.Load(); fn != nil {
			return (*fn)(id, in)
		}
		return cart.RulesResult{}, false
	}
	t.Cart.LiveRate = func(carrierID, country, postal string, weight, value int64) (int64, int, bool) {
		if fn := p.liveRates.Load(); fn != nil {
			return (*fn)(id, carrierID, country, postal, weight, value)
		}
		return 0, 0, false
	}
	// One query per kind for the whole store (not one per product or SKU).
	if err := p.db.EachProduct(ctx, id, t.Catalog.Upsert); err != nil {
		return err
	}
	stock, err := p.db.AllStock(ctx, id)
	if err != nil {
		return err
	}
	for sku, levels := range stock {
		t.Inventory.Load(sku, levels)
	}
	kits, err := p.db.LoadBundles(ctx, id, "")
	if err != nil {
		return err
	}
	for sku, comps := range kits {
		t.Inventory.SetBundle(sku, comps)
	}
	pres, err := p.db.Preorders(ctx, id, "", true)
	if err != nil {
		return err
	}
	for _, pr := range pres {
		t.Inventory.SetPreorder(pr.SKU, preorderView([]store.Preorder{pr}))
	}
	prices, err := p.db.AllPrices(ctx, id)
	if err != nil {
		return err
	}
	for k, tiers := range prices {
		t.Pricing.Set(k[0], k[1], tiers...)
	}
	if err := p.applyChannels(ctx, t); err != nil {
		return err
	}
	if err := p.loadPromotions(ctx, t); err != nil {
		return err
	}
	if err := p.applySettings(ctx, t, ""); err != nil {
		return err
	}
	p.mu.Lock()
	p.tenants[id] = t
	p.mu.Unlock()
	return nil
}

// prefetchProducts loads, with one query per store, every product the
// batch of events touches, so applying 500 product events does not read the
// database 500 times. Only the drain goroutine uses p.pre.
type stockPre struct {
	levels []inventory.Level
	kit    []inventory.Component
	pre    []store.Preorder
}

func (p *Projector) prefetchProducts(ctx context.Context, evs []store.OutboxEvent) error {
	if err := p.prefetchStockAndPrices(ctx, evs); err != nil {
		return err
	}
	p.pre = nil
	ids := map[string][]string{}
	seen := map[[2]string]bool{}
	for _, e := range evs {
		if e.Topic != store.TopicProduct || seen[[2]string{e.Tenant, e.Key}] {
			continue
		}
		seen[[2]string{e.Tenant, e.Key}] = true
		ids[e.Tenant] = append(ids[e.Tenant], e.Key)
	}
	if len(ids) == 0 {
		return nil
	}
	p.pre = map[[2]string]*catalog.Product{}
	for tenant, list := range ids {
		for _, id := range list {
			p.pre[[2]string{tenant, id}] = nil // nil: not found (deleted)
		}
		if err := p.db.EachProductIn(ctx, tenant, list, func(prod catalog.Product) {
			p.pre[[2]string{tenant, prod.ID}] = &prod
		}); err != nil {
			return err
		}
	}
	return nil
}

// prefetchStockAndPrices does for stock and price events what
// prefetchProducts does for products: one query per kind and store.
func (p *Projector) prefetchStockAndPrices(ctx context.Context, evs []store.OutboxEvent) error {
	p.preStock, p.prePrice = nil, nil
	skus := map[string][]string{}
	prices := map[string][][2]string{}
	seenS, seenP := map[[2]string]bool{}, map[[3]string]bool{}
	for _, e := range evs {
		switch e.Topic {
		case store.TopicStock:
			if k := [2]string{e.Tenant, e.Key}; !seenS[k] {
				seenS[k] = true
				skus[e.Tenant] = append(skus[e.Tenant], e.Key)
			}
		case store.TopicPrice:
			ch, sku, _ := strings.Cut(e.Key, "|")
			if k := [3]string{e.Tenant, ch, sku}; !seenP[k] {
				seenP[k] = true
				prices[e.Tenant] = append(prices[e.Tenant], [2]string{ch, sku})
			}
		}
	}
	if len(skus) > 0 {
		p.preStock = map[[2]string]stockPre{}
		for tenant, list := range skus {
			levels, err := p.db.StockFor(ctx, tenant, list)
			if err != nil {
				return err
			}
			kits, err := p.db.BundlesFor(ctx, tenant, list)
			if err != nil {
				return err
			}
			pres, err := p.db.PreordersFor(ctx, tenant, list)
			if err != nil {
				return err
			}
			for _, sku := range list {
				p.preStock[[2]string{tenant, sku}] = stockPre{levels[sku], kits[sku], pres[sku]}
			}
		}
	}
	if len(prices) > 0 {
		p.prePrice = map[[3]string][]pricing.Tier{}
		for tenant, keys := range prices {
			got, err := p.db.PricesFor(ctx, tenant, keys)
			if err != nil {
				return err
			}
			for _, k := range keys {
				p.prePrice[[3]string{tenant, k[0], k[1]}] = got[k] // nil: no tiers left
			}
		}
	}
	return nil
}

func (p *Projector) applyProduct(ctx context.Context, t *Tenant, id string) error {
	if pre, ok := p.pre[[2]string{t.ID, id}]; ok {
		delete(p.pre, [2]string{t.ID, id}) // later events of the batch read fresh
		if pre == nil {
			t.Catalog.Delete(id)
		} else {
			t.Catalog.Upsert(*pre)
		}
		return nil
	}
	prod, err := p.db.LoadProduct(ctx, t.ID, id)
	if err == store.ErrNotFound {
		t.Catalog.Delete(id) // deleted: drop it from the read model
		return nil
	}
	if err != nil {
		return err
	}
	t.Catalog.Upsert(prod)
	return nil
}

// loadTaxonomy fills the taxonomy of a tenant at start-up.
func (p *Projector) loadTaxonomy(ctx context.Context, t *Tenant) error {
	for _, topic := range []string{store.TopicCategory, store.TopicBrand, store.TopicCollection, store.TopicAttribute, store.TopicMedia} {
		if err := p.applyTaxonomy(ctx, t, topic, ""); err != nil {
			return err
		}
	}
	return nil
}

// applyTaxonomy reloads one entity (or all of a kind when id is empty) and
// removes it from memory when it no longer exists in the database.
func (p *Projector) applyTaxonomy(ctx context.Context, t *Tenant, topic, id string) error {
	switch topic {
	case store.TopicCategory:
		rows, err := p.db.Categories(ctx, t.ID, id)
		if err != nil {
			return err
		}
		if id != "" && len(rows) == 0 {
			t.Taxo.DeleteCategory(id)
		}
		for _, r := range rows {
			t.Taxo.SetCategory(r)
		}
	case store.TopicBrand:
		rows, err := p.db.Brands(ctx, t.ID, id)
		if err != nil {
			return err
		}
		if id != "" && len(rows) == 0 {
			t.Taxo.DeleteBrand(id)
		}
		for _, r := range rows {
			t.Taxo.SetBrand(r)
		}
	case store.TopicCollection:
		rows, err := p.db.Collections(ctx, t.ID, id)
		if err != nil {
			return err
		}
		if id != "" && len(rows) == 0 {
			t.Taxo.DeleteCollection(id)
		}
		for _, r := range rows {
			t.Taxo.SetCollection(r)
		}
	case store.TopicAttribute:
		rows, err := p.db.Attributes(ctx, t.ID, id)
		if err != nil {
			return err
		}
		if id != "" && len(rows) == 0 {
			t.Taxo.DeleteAttribute(id)
		}
		for _, r := range rows {
			t.Taxo.SetAttribute(r)
		}
	case store.TopicMedia:
		rows, err := p.db.Media(ctx, t.ID, id)
		if err != nil {
			return err
		}
		if id != "" && len(rows) == 0 {
			t.Taxo.DeleteMedia(id)
		}
		for _, r := range rows {
			t.Taxo.SetMedia(r)
		}
	}
	return nil
}

func (p *Projector) applyStock(ctx context.Context, t *Tenant, sku string) error {
	if pre, ok := p.preStock[[2]string{t.ID, sku}]; ok {
		delete(p.preStock, [2]string{t.ID, sku}) // a later event of the batch reads fresh
		t.Inventory.Load(sku, pre.levels)
		t.Inventory.SetBundle(sku, pre.kit)
		t.Inventory.SetPreorder(sku, preorderView(pre.pre))
		return nil
	}
	levels, err := p.db.LoadStock(ctx, t.ID, sku)
	if err != nil {
		return err
	}
	t.Inventory.Load(sku, levels)
	kits, err := p.db.LoadBundles(ctx, t.ID, sku)
	if err != nil {
		return err
	}
	t.Inventory.SetBundle(sku, kits[sku])
	pre, err := p.db.Preorders(ctx, t.ID, sku, true)
	if err != nil {
		return err
	}
	t.Inventory.SetPreorder(sku, preorderView(pre))
	return nil
}

func preorderView(ps []store.Preorder) *inventory.Preorder {
	if len(ps) == 0 || !ps[0].Active {
		return nil
	}
	p := ps[0]
	left := int64(-1)
	if p.MaxUnits > 0 {
		left = max(p.MaxUnits-p.Sold, 0)
	}
	return &inventory.Preorder{ShipsOn: p.ShipsOn, DepositBps: p.DepositBps, Left: left}
}

func (p *Projector) applyPrice(ctx context.Context, t *Tenant, channel, sku string) error {
	if tiers, ok := p.prePrice[[3]string{t.ID, channel, sku}]; ok {
		delete(p.prePrice, [3]string{t.ID, channel, sku})
		t.Pricing.Set(channel, sku, tiers...)
		return nil
	}
	tiers, err := p.db.LoadPrices(ctx, t.ID, channel, sku)
	if err != nil {
		return err
	}
	t.Pricing.Set(channel, sku, tiers...)
	return nil
}

func (p *Projector) applyChannels(ctx context.Context, t *Tenant) error {
	raw, err := p.db.Channels(ctx, t.ID)
	if err != nil {
		return err
	}
	chs := map[string]cart.Channel{}
	for id, b := range raw {
		var c cart.Channel
		if err := json.Unmarshal(b, &c); err != nil {
			return fmt.Errorf("channel %s: %w", id, err)
		}
		c.ID = id
		chs[id] = c
	}
	conv := map[string]pricing.Conversion{}
	for id, c := range chs {
		if c.PriceFrom != "" && c.PriceFrom != id && c.RateMicros > 0 {
			conv[id] = pricing.Conversion{From: c.PriceFrom, Currency: c.Currency, RateMicros: c.RateMicros, Rounding: c.Rounding}
		}
	}
	t.Pricing.SetConversions(conv)
	t.mu.Lock()
	t.Cart.Channels = chs
	t.mu.Unlock()
	return nil
}

// Markets lists the channels shoppers can pick in the storefront, the main
// channel first.
func (t *Tenant) Markets(main string) []cart.Channel {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []cart.Channel
	if c, ok := t.Cart.Channels[main]; ok {
		out = append(out, c)
	}
	ids := make([]string, 0, len(t.Cart.Channels))
	for id, c := range t.Cart.Channels {
		if c.Market && id != main {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, t.Cart.Channels[id])
	}
	return out
}

// loadPromotions fills the engine with every promotion, campaign and price
// list of the tenant (initial load only; afterwards each event updates one).
func (p *Projector) loadPromotions(ctx context.Context, t *Tenant) error {
	ps, err := p.ps.LoadPromotions(ctx, t.ID)
	if err != nil {
		return err
	}
	for _, l := range ps {
		t.Cart.Promos.Upsert(l.Promotion, l.Uses)
	}
	if err := p.applyCampaign(ctx, t, ""); err != nil {
		return err
	}
	return p.applyPriceList(ctx, t, "")
}

// applyPromotion reloads one promotion (definition and use counter).
func (p *Projector) applyPromotion(ctx context.Context, t *Tenant, id string) error {
	l, found, err := p.ps.LoadPromotion(ctx, t.ID, id)
	if err != nil {
		return err
	}
	if !found {
		t.Cart.Promos.Remove(id)
		return nil
	}
	t.Cart.Promos.Upsert(l.Promotion, l.Uses)
	return nil
}

// applyCampaign reloads one campaign (or all with id "").
func (p *Projector) applyCampaign(ctx context.Context, t *Tenant, id string) error {
	cs, err := p.ps.LoadCampaigns(ctx, t.ID, id)
	if err != nil {
		return err
	}
	if id != "" && len(cs) == 0 {
		t.Cart.Promos.RemoveCampaign(id)
	}
	for _, c := range cs {
		t.Cart.Promos.SetCampaign(c)
	}
	return nil
}

// applyPriceList reloads one price list (or all with id "").
func (p *Projector) applyPriceList(ctx context.Context, t *Tenant, id string) error {
	ls, err := p.ps.LoadPriceLists(ctx, t.ID, id)
	if err != nil {
		return err
	}
	if id != "" && len(ls) == 0 {
		t.Pricing.RemoveList(id)
	}
	for _, l := range ls {
		t.Pricing.SetList(l)
	}
	return nil
}

// Run consumes the outbox until ctx is cancelled. It reconnects LISTEN on
// failure and always polls every second as a fallback.
func (p *Projector) Run(ctx context.Context) {
	go p.listenLoop(ctx)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	soon := time.NewTimer(time.Hour)
	defer soon.Stop()
	for {
		if err := p.drain(ctx); err != nil && ctx.Err() == nil {
			p.errs.Add(1)
			p.log.Error("projector drain", "err", err)
		}
		// Another node may hold the sequencer: if events are still waiting
		// for a position, look again in a few milliseconds, not in a second.
		if n, err := p.db.OutboxPending(ctx, 0); err == nil && n > 0 {
			soon.Reset(5 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		case <-tick.C:
		case <-soon.C:
		}
	}
}

// Drain applies every event committed before the call, so a caller reads
// its own writes (tests, and the API right after a write). If another node
// holds the sequencer at that moment, Drain waits for it, up to five seconds.
func (p *Projector) Drain(ctx context.Context) error {
	upTo, err := p.db.OutboxLastID(ctx)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	wait := 500 * time.Microsecond
	for {
		if err := p.drain(ctx); err != nil {
			return err
		}
		n, err := p.db.OutboxPending(ctx, upTo)
		if err != nil || n == 0 {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("projection: %d events still wait for a position", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if wait < 20*time.Millisecond {
			wait *= 2
		}
	}
}

func (p *Projector) drain(ctx context.Context) error {
	p.drainMu.Lock()
	defer p.drainMu.Unlock()
	for {
		if _, err := p.db.SequenceOutbox(ctx); err != nil {
			return err
		}
		evs, err := p.db.OutboxAfter(ctx, p.cursor.Load(), 500)
		if err != nil {
			return err
		}
		if len(evs) == 0 {
			return nil
		}
		if err := p.prefetchProducts(ctx, evs); err != nil {
			return err
		}
		for _, e := range evs {
			if err := p.apply(ctx, e); err != nil {
				p.errs.Add(1)
				p.log.Error("projector apply", "event", e.ID, "topic", e.Topic, "err", err)
				return err // retry from this event on the next round
			}
			lag := time.Since(e.CreatedAt).Microseconds()
			p.lastLag.Store(lag)
			p.lagSumUs.Add(lag)
			for {
				m := p.maxLag.Load()
				if lag <= m || p.maxLag.CompareAndSwap(m, lag) {
					break
				}
			}
			p.applied.Add(1)
			p.cursor.Store(e.Seq)
		}
	}
}

func (p *Projector) apply(ctx context.Context, e store.OutboxEvent) error {
	t, ok := p.Tenant(e.Tenant)
	if !ok {
		return p.loadTenant(ctx, e.Tenant)
	}
	defer t.version.Add(1)
	if err := p.applyTopic(ctx, t, e); err != nil {
		return err
	}
	// Hooks run after the event is applied, for every topic, so a hook
	// sees the read models already up to date.
	p.hooksMu.RLock()
	hs := p.hooks[e.Topic]
	p.hooksMu.RUnlock()
	for _, h := range hs {
		h(e.Tenant, e.Key)
	}
	return nil
}

func (p *Projector) applyTopic(ctx context.Context, t *Tenant, e store.OutboxEvent) error {
	switch e.Topic {
	case store.TopicProduct:
		return p.applyProduct(ctx, t, e.Key)
	case store.TopicStock:
		return p.applyStock(ctx, t, e.Key)
	case store.TopicPrice:
		ch, sku, _ := strings.Cut(e.Key, "|")
		return p.applyPrice(ctx, t, ch, sku)
	case store.TopicChannel:
		return p.applyChannels(ctx, t)
	case store.TopicPromotion:
		return p.applyPromotion(ctx, t, e.Key)
	case promostore.TopicCampaign:
		return p.applyCampaign(ctx, t, e.Key)
	case promostore.TopicPriceList:
		return p.applyPriceList(ctx, t, e.Key)
	case store.TopicCategory, store.TopicBrand, store.TopicCollection, store.TopicAttribute, store.TopicMedia:
		return p.applyTaxonomy(ctx, t, e.Topic, e.Key)
	case store.TopicSettings:
		return p.applySettings(ctx, t, e.Key)
	}
	return nil
}

func (p *Projector) listenLoop(ctx context.Context) {
	for ctx.Err() == nil {
		conn, err := pgx.Connect(ctx, p.listen)
		if err == nil {
			_, err = conn.Exec(ctx, "LISTEN apexx_outbox")
		}
		for err == nil {
			_, err = conn.WaitForNotification(ctx)
			if err == nil {
				select {
				case p.wake <- struct{}{}:
				default:
				}
			}
		}
		if conn != nil {
			_ = conn.Close(context.Background())
		}
		if ctx.Err() == nil {
			p.log.Warn("projector listen reconnecting", "err", err)
			time.Sleep(time.Second)
		}
	}
}

// CheckoutBlocks returns the blocks of the store's payment page.
func (t *Tenant) CheckoutBlocks() []cart.CheckoutBlock {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]cart.CheckoutBlock(nil), t.Cart.CheckoutBlocks...)
}

// CustomsOf returns the personalization fields of a product.
func (t *Tenant) CustomsOf(productID string) []cart.CustomField {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]cart.CustomField(nil), t.Cart.Customs[productID]...)
}

// QtyRulesOf returns the quantity rules of a product.
func (t *Tenant) QtyRulesOf(productID string) []cart.QtyRule {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]cart.QtyRule(nil), t.Cart.QtyRules[productID]...)
}

// LiveRateFunc prices a parcel with a store's connected carrier.
type LiveRateFunc func(tenant, carrierID, country, postal string, weightGrams, value int64) (amount int64, days int, ok bool)

// SetLiveRates installs the carrier rate function for every store.
func (p *Projector) SetLiveRates(fn LiveRateFunc) { p.liveRates.Store(&fn) }

// ExternalTaxFunc asks a store's tax service.
type ExternalTaxFunc func(tenant, country, postal, currency string, exclusive bool, parts []logistics.TaxPart) (logistics.TaxResult, bool)

// ExternalRulesFunc asks a store's checkout rules app.
type ExternalRulesFunc func(tenant string, in cart.RulesInput) (cart.RulesResult, bool)

// SetExternalRules installs the rules app function for every store.
func (p *Projector) SetExternalRules(fn ExternalRulesFunc) { p.extRules.Store(&fn) }

// SetExternalTax installs the tax service function for every store.
func (p *Projector) SetExternalTax(fn ExternalTaxFunc) { p.extTax.Store(&fn) }

// GiftOptions returns the store's gift setting.
func (t *Tenant) GiftOptions() cart.GiftOptions {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Cart.Gift
}
