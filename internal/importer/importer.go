package importer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// Writer is the subset of store.DB the importer needs.
type Writer interface {
	UpsertProduct(ctx context.Context, tenant, actor string, p catalog.Product, expectedVersion int64) (int64, error)
	SetPrices(ctx context.Context, tenant, actor, channel, sku string, tiers []pricing.Tier) error
	SetStock(ctx context.Context, tenant, actor, sku, location string, onHand int64) error
}

// Channel maps a VTEX trade policy to an Apexx channel and currency.
type Channel struct {
	ID       string
	Currency money.Currency
}

// ParseChannelMap parses "1=mx:MXN,2=id:IDR".
func ParseChannelMap(s string) (map[string]Channel, error) {
	out := map[string]Channel{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tp, rest, ok := strings.Cut(part, "=")
		ch, cur, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 || tp == "" || ch == "" || len(cur) != 3 {
			return nil, fmt.Errorf("channel map %q: use tradePolicy=channel:CUR", part)
		}
		out[tp] = Channel{ID: ch, Currency: money.Currency(strings.ToUpper(cur))}
	}
	if len(out) == 0 {
		return nil, errors.New("channel map is empty")
	}
	return out, nil
}

// Options configure an import run.
type Options struct {
	Tenant     string
	Channels   map[string]Channel // trade policy id -> channel
	Limit      int                // 0 = whole catalog
	Workers    int
	DryRun     bool
	Actor      string
	UnlimitedQ int64 // on-hand used for VTEX "unlimited" stock
}

// Report summarizes a run.
type Report struct {
	Products     int64            `json:"products"`
	SKUs         int64            `json:"skus"`
	Prices       int64            `json:"priceSets"`
	StockRows    int64            `json:"stockRows"`
	Skipped      int64            `json:"productsWithErrors"`
	Errors       map[string]int64 `json:"errorsByType"`
	Requests     int64            `json:"vtexRequests"`
	Retries      int64            `json:"vtexRetries"`
	Duration     string           `json:"duration"`
	ProductsPerS float64          `json:"productsPerSecond"`
	CatalogTotal int              `json:"vtexCatalogTotal"`
	DryRun       bool             `json:"dryRun"`
}

// Importer runs one migration.
type Importer struct {
	VTEX *Client
	W    Writer
	Log  *slog.Logger

	products, skus, prices, stock, skipped atomic.Int64
	errMu                                  sync.Mutex
	errs                                   map[string]int64
}

func (im *Importer) fail(kind string) {
	im.errMu.Lock()
	im.errs[kind]++
	im.errMu.Unlock()
}

type job struct {
	productID int64
	skuIDs    []int64
}

// Run walks the VTEX catalog page by page and imports each product.
func (im *Importer) Run(ctx context.Context, o Options) (Report, error) {
	if o.Workers <= 0 {
		o.Workers = 8
	}
	if o.Actor == "" {
		o.Actor = "importer:vtex"
	}
	if o.UnlimitedQ == 0 {
		o.UnlimitedQ = 1_000_000
	}
	im.errs = map[string]int64{}
	start := time.Now()
	jobs := make(chan job, o.Workers*4)
	var wg sync.WaitGroup
	for i := 0; i < o.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := im.importOne(ctx, o, j); err != nil {
					im.skipped.Add(1)
					im.Log.Warn("product skipped", "vtexProduct", j.productID, "err", err)
				}
				if n := im.products.Load() + im.skipped.Load(); n%100 == 0 {
					im.Log.Info("progress", "done", n, "elapsed", time.Since(start).Round(time.Second).String())
				}
			}
		}()
	}

	total, err := im.enqueue(ctx, o, jobs)
	close(jobs)
	wg.Wait()
	dur := time.Since(start)
	rep := Report{
		Products: im.products.Load(), SKUs: im.skus.Load(), Prices: im.prices.Load(), StockRows: im.stock.Load(),
		Skipped: im.skipped.Load(), Errors: im.errs, Requests: im.VTEX.Requests, Retries: im.VTEX.Retries,
		Duration: dur.Round(time.Millisecond).String(), CatalogTotal: total, DryRun: o.DryRun,
	}
	if s := dur.Seconds(); s > 0 {
		rep.ProductsPerS = math.Round(float64(rep.Products)/s*10) / 10
	}
	if err == nil {
		err = ctx.Err()
	}
	return rep, err
}

// enqueue pages through GetProductAndSkuIds (250 per page, no 2,500 ceiling).
func (im *Importer) enqueue(ctx context.Context, o Options, jobs chan<- job) (int, error) {
	const page = 250
	sent, total := 0, 0
	for from := 1; ; from += page {
		to := from + page - 1
		if o.Limit > 0 && to > o.Limit {
			to = o.Limit
		}
		p, err := im.VTEX.ProductPage(ctx, from, to)
		if err != nil {
			return total, fmt.Errorf("list products %d-%d: %w", from, to, err)
		}
		total = p.Range.Total
		ids := make([]int64, 0, len(p.Data))
		for k := range p.Data {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			select {
			case jobs <- job{productID: id, skuIDs: p.Data[strconv.FormatInt(id, 10)]}:
				sent++
			case <-ctx.Done():
				return total, ctx.Err()
			}
		}
		if to >= total || len(p.Data) == 0 || (o.Limit > 0 && to >= o.Limit) {
			return total, nil
		}
	}
}

// Cents converts a VTEX decimal amount to minor units.
func Cents(v float64) int64 { return int64(math.Round(v * 100)) }

func (im *Importer) importOne(ctx context.Context, o Options, j job) error {
	vp, err := im.VTEX.Product(ctx, j.productID)
	if err != nil {
		im.fail("product_fetch")
		return err
	}
	type skuData struct {
		sku   SKU
		price *Price
		inv   *Inventory
	}
	var data []skuData
	for _, id := range j.skuIDs {
		s, err := im.VTEX.SKU(ctx, id)
		if err != nil {
			im.fail("sku_fetch")
			continue
		}
		d := skuData{sku: s}
		if pr, err := im.VTEX.Price(ctx, id); err == nil {
			d.price = &pr
		} else if errors.Is(err, ErrNotFound) {
			im.fail("price_not_found")
		} else {
			im.fail("price_fetch")
		}
		if inv, err := im.VTEX.Inventory(ctx, id); err == nil {
			d.inv = &inv
		} else {
			im.fail("inventory_fetch")
		}
		data = append(data, d)
	}
	skus := make([]SKU, len(data))
	for i, d := range data {
		skus[i] = d.sku
	}
	p := MapProduct(vp, skus)

	if !o.DryRun {
		if _, err := im.W.UpsertProduct(ctx, o.Tenant, o.Actor, p, 0); err != nil {
			if strings.Contains(err.Error(), "duplicate key") && strings.Contains(err.Error(), "handle") {
				p.Handle = p.Handle + "-" + strconv.FormatInt(vp.ID, 10)
				_, err = im.W.UpsertProduct(ctx, o.Tenant, o.Actor, p, 0)
			}
			if err != nil {
				im.fail("product_write")
				return err
			}
		}
	}
	im.products.Add(1)
	im.skus.Add(int64(len(p.Variants)))

	for _, d := range data {
		sku := strconv.FormatInt(d.sku.ID, 10)
		if d.price != nil {
			for tp, ch := range o.Channels {
				tiers := MapPrices(*d.price, tp, ch.Currency)
				if len(tiers) == 0 {
					continue
				}
				if !o.DryRun {
					if err := im.W.SetPrices(ctx, o.Tenant, o.Actor, ch.ID, sku, tiers); err != nil {
						im.fail("price_write")
						continue
					}
				}
				im.prices.Add(1)
			}
		}
		if d.inv != nil {
			for _, b := range d.inv.Balance {
				q := b.TotalQuantity
				if b.HasUnlimitedQuantity {
					q = o.UnlimitedQ
				}
				if q < 0 {
					q = 0
				}
				if !o.DryRun {
					if err := im.W.SetStock(ctx, o.Tenant, o.Actor, sku, b.WarehouseID, q); err != nil {
						im.fail("stock_write")
						continue
					}
				}
				im.stock.Add(1)
			}
		}
	}
	return nil
}

// MapProduct converts a VTEX product and its SKUs into an Apexx product.
func MapProduct(vp Product, skus []SKU) catalog.Product {
	p := catalog.Product{
		ID:          "vtex-" + strconv.FormatInt(vp.ID, 10),
		Handle:      slug(vp.LinkID),
		Title:       strings.TrimSpace(vp.Name),
		Description: vp.Description,
		Status:      "active",
		Attributes:  map[string]string{"vtexProductId": strconv.FormatInt(vp.ID, 10)},
	}
	if p.Handle == "" {
		p.Handle = "p-" + strconv.FormatInt(vp.ID, 10)
	}
	if p.Title == "" {
		p.Title = vp.Title
	}
	if p.Title == "" {
		p.Title = "Producto " + strconv.FormatInt(vp.ID, 10)
	}
	if !vp.IsActive {
		p.Status = "draft"
	}
	if vp.RefID != "" {
		p.Attributes["refId"] = vp.RefID
	}
	cats := map[string]bool{}
	for _, s := range skus {
		if p.Brand == "" {
			p.Brand = s.BrandName
		}
		for _, c := range s.ProductCategories {
			if !cats[c] {
				cats[c] = true
				p.Categories = append(p.Categories, c)
			}
		}
		title := strings.TrimSpace(s.SkuName)
		if title == "" {
			title = s.NameComplete
		}
		opts := map[string]string{}
		for _, sp := range s.SkuSpecifications {
			if len(sp.FieldValues) > 0 {
				opts[sp.FieldName] = sp.FieldValues[0]
			}
		}
		if !s.IsActive {
			opts["vtexActive"] = "false"
		}
		p.Variants = append(p.Variants, catalog.Variant{
			SKU: strconv.FormatInt(s.ID, 10), Title: title, Options: opts,
			Barcode: s.AlternateIDs.Ean, WeightGrams: int64(math.Round(s.Dimension.Weight)),
		})
	}
	sort.Strings(p.Categories)
	return p
}

// MapPrices builds Apexx tiers for one trade policy. Fixed prices of that
// policy win; otherwise the base price applies with the list price as the
// crossed-out "compare at" when it is higher.
func MapPrices(pr Price, tradePolicy string, cur money.Currency) []pricing.Tier {
	byQty := map[int64]pricing.Tier{}
	for _, f := range pr.FixedPrices {
		if f.TradePolicyID != tradePolicy {
			continue
		}
		q := f.MinQuantity
		if q < 1 {
			q = 1
		}
		t := pricing.Tier{MinQty: q, Unit: money.New(Cents(f.Value), cur)}
		if f.ListPrice != nil && Cents(*f.ListPrice) > t.Unit.Amount {
			c := money.New(Cents(*f.ListPrice), cur)
			t.CompareAt = &c
		}
		byQty[q] = t
	}
	if _, ok := byQty[1]; !ok {
		base := pr.BasePrice
		if base == nil && pr.CostPrice != nil {
			v := *pr.CostPrice
			if pr.Markup != nil {
				v *= 1 + *pr.Markup/100
			}
			base = &v
		}
		if base != nil {
			t := pricing.Tier{MinQty: 1, Unit: money.New(Cents(*base), cur)}
			if pr.ListPrice != nil && Cents(*pr.ListPrice) > t.Unit.Amount {
				c := money.New(Cents(*pr.ListPrice), cur)
				t.CompareAt = &c
			}
			byQty[1] = t
		} else if len(byQty) > 0 {
			// No base price: the lowest fixed tier also covers single units.
			var minQ int64 = math.MaxInt64
			for q := range byQty {
				minQ = min(minQ, q)
			}
			t := byQty[minQ]
			t.MinQty = 1
			byQty[1] = t
		}
	}
	out := make([]pricing.Tier, 0, len(byQty))
	for _, t := range byQty {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MinQty < out[j].MinQty })
	return out
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
