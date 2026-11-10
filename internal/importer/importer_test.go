package importer

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// fakeVTEX serves n products with one SKU each (sku id = 1000+product id).
func fakeVTEX(t *testing.T, n int, throttleEvery int64) (*httptest.Server, *atomic.Int64) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-VTEX-API-AppKey") != "k" || r.Header.Get("X-VTEX-API-AppToken") != "t" {
			w.WriteHeader(401)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("importer must be read-only, got %s %s", r.Method, r.URL.Path)
		}
		h := hits.Add(1)
		if throttleEvery > 0 && h%throttleEvery == 0 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/GetProductAndSkuIds"):
			from, _ := strconv.Atoi(r.URL.Query().Get("_from"))
			to, _ := strconv.Atoi(r.URL.Query().Get("_to"))
			if to-from+1 > 250 {
				w.WriteHeader(400)
				return
			}
			var parts []string
			for i := from; i <= to && i <= n; i++ {
				parts = append(parts, fmt.Sprintf(`"%d":[%d]`, i, 1000+i))
			}
			fmt.Fprintf(w, `{"data":{%s},"range":{"total":%d,"from":%d,"to":%d}}`, strings.Join(parts, ","), n, from, to)
		case strings.HasPrefix(p, "/api/catalog/pvt/product/"):
			id := strings.TrimPrefix(p, "/api/catalog/pvt/product/")
			fmt.Fprintf(w, `{"Id":%s,"Name":"Tenis %s","LinkId":"Tenis-%s","Description":"d","IsActive":%v,"RefId":"R%s"}`, id, id, id, id != "2", id)
		case strings.HasPrefix(p, "/api/catalog_system/pvt/sku/stockkeepingunitbyid/"):
			id := strings.TrimPrefix(p, "/api/catalog_system/pvt/sku/stockkeepingunitbyid/")
			fmt.Fprintf(w, `{"Id":%s,"IsActive":true,"SkuName":"Talla 25","BrandName":"Converse","ProductCategories":{"1":"Shoes","2":"Women"},
				"AlternateIds":{"Ean":"750%s"},"Dimension":{"weight":400},"SkuSpecifications":[{"FieldName":"Talla","FieldValues":["25"]}]}`, id, id)
		case strings.HasPrefix(p, "/pricing/prices/"):
			id := strings.TrimPrefix(p, "/pricing/prices/")
			if id == "1003" {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, `{"itemId":"x","listPrice":1932.91,"costPrice":1684.38,"markup":0,"basePrice":1684.38,
				"fixedPrices":[{"tradePolicyId":"1","value":1500.5,"listPrice":null,"minQuantity":10},{"tradePolicyId":"2","value":1674000,"listPrice":1921000,"minQuantity":1}]}`)
		case strings.HasPrefix(p, "/api/logistics/pvt/inventory/skus/"):
			io.WriteString(w, `{"skuId":"x","balance":[{"warehouseId":"1_1","totalQuantity":20,"reservedQuantity":6},{"warehouseId":"2_1","totalQuantity":0,"hasUnlimitedQuantity":true}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	return srv, &hits
}

type memWriter struct {
	mu       sync.Mutex
	products map[string]catalog.Product
	prices   map[string][]pricing.Tier
	stock    map[string]int64
}

func newMem() *memWriter {
	return &memWriter{products: map[string]catalog.Product{}, prices: map[string][]pricing.Tier{}, stock: map[string]int64{}}
}

func (m *memWriter) UpsertProduct(_ context.Context, _, _ string, p catalog.Product, _ int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.products[p.ID] = p
	return 1, nil
}

func (m *memWriter) SetPrices(_ context.Context, _, _, ch, sku string, t []pricing.Tier) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prices[ch+"/"+sku] = t
	return nil
}

func (m *memWriter) SetStock(_ context.Context, _, _, sku, loc string, q int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stock[sku+"@"+loc] = q
	return nil
}

func run(t *testing.T, n, limit int, throttle int64, dry bool) (Report, *memWriter) {
	srv, _ := fakeVTEX(t, n, throttle)
	defer srv.Close()
	c := NewClient("acct", "k", "t")
	c.BaseURL, c.PricingURL, c.RPS = srv.URL, srv.URL, 0
	mw := newMem()
	ch, _ := ParseChannelMap("1=mx:MXN,2=id:IDR")
	im := &Importer{VTEX: c, W: mw, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rep, err := im.Run(context.Background(), Options{Tenant: "t", Channels: ch, Limit: limit, Workers: 4, DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	return rep, mw
}

func TestWalksPastSearchCeiling(t *testing.T) {
	rep, mw := run(t, 2600, 0, 0, false) // more than VTEX search's 2,500 limit
	if rep.Products != 2600 || len(mw.products) != 2600 || rep.CatalogTotal != 2600 {
		t.Fatalf("imported %d/%d products (total %d)", rep.Products, len(mw.products), rep.CatalogTotal)
	}
}

func TestLimitAndDryRun(t *testing.T) {
	rep, mw := run(t, 600, 300, 0, true)
	if rep.Products != 300 || len(mw.products) != 0 {
		t.Fatalf("dry run: report %d, written %d", rep.Products, len(mw.products))
	}
}

func TestRetriesOn429(t *testing.T) {
	rep, mw := run(t, 40, 0, 7, false)
	if rep.Retries == 0 || rep.Products != 40 || len(mw.products) != 40 {
		t.Fatalf("retries=%d products=%d written=%d errors=%v", rep.Retries, rep.Products, len(mw.products), rep.Errors)
	}
}

func TestMapping(t *testing.T) {
	rep, mw := run(t, 3, 0, 0, false)
	p := mw.products["vtex-1"]
	if p.Handle != "tenis-1" || p.Title != "Tenis 1" || p.Brand != "Converse" || p.Status != "active" || len(p.Categories) != 2 {
		t.Fatalf("product mapped wrong: %+v", p)
	}
	v := p.Variants[0]
	if v.SKU != "1001" || v.Barcode != "7501001" || v.WeightGrams != 400 || v.Options["Talla"] != "25" {
		t.Fatalf("variant mapped wrong: %+v", v)
	}
	if mw.products["vtex-2"].Status != "draft" {
		t.Fatal("inactive VTEX product must import as draft")
	}
	mx := mw.prices["mx/1001"]
	if len(mx) != 2 || mx[0].MinQty != 1 || mx[0].Unit != money.New(168438, "MXN") || mx[0].CompareAt.Amount != 193291 ||
		mx[1].MinQty != 10 || mx[1].Unit.Amount != 150050 {
		t.Fatalf("mx tiers wrong: %+v", mx)
	}
	id := mw.prices["id/1001"]
	if len(id) != 1 || id[0].Unit.Amount != 167400000 || id[0].CompareAt.Amount != 192100000 {
		t.Fatalf("idr tiers wrong (fixed prices are decimals too): %+v", id)
	}
	if mw.stock["1001@1_1"] != 20 || mw.stock["1001@2_1"] != 1_000_000 {
		t.Fatalf("stock wrong: %v", mw.stock)
	}
	if rep.Errors["price_not_found"] != 1 || len(mw.prices) != 4 {
		t.Fatalf("SKU 1003 has no price: errors=%v prices=%d", rep.Errors, len(mw.prices))
	}
}

func TestCents(t *testing.T) {
	for in, want := range map[float64]int64{1684.38: 168438, 0.1 + 0.2: 30, 19.995: 2000, 1932.91: 193291} {
		if got := Cents(in); got != want {
			t.Errorf("Cents(%v)=%d want %d", in, got, want)
		}
	}
}

func TestParseChannelMap(t *testing.T) {
	if _, err := ParseChannelMap("1=mx"); err == nil {
		t.Fatal("currency is required")
	}
	m, err := ParseChannelMap("1=mx:mxn, 3=vn:VND")
	if err != nil || m["1"].Currency != "MXN" || m["3"].ID != "vn" {
		t.Fatalf("%v %v", m, err)
	}
}
