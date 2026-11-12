package reports

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/seed"
	"github.com/helloghc/apexx/internal/store"
)

// ---------- unit ----------

func TestParseBeacon(t *testing.T) {
	ok := `{"c":"mx","r":"www.google.com","e":[{"t":"pv","p":"/s/a/main/p/zapato?utm=x#top"},{"t":"ac","s":"SKU-1","q":2},{"t":"pu","o":"ord_0123456789abcdef0123","v":12900}]}`
	b, err := ParseBeacon(strings.NewReader(ok))
	if err != nil {
		t.Fatalf("valid beacon rejected: %v", err)
	}
	if b.Events[0].Path != "/s/a/main/p/zapato" {
		t.Fatalf("query string and fragment must be dropped, got %q", b.Events[0].Path)
	}
	bad := map[string]string{
		"not json":            `{`,
		"unknown field":       `{"e":[{"t":"pv","p":"/x"}],"email":"a@b.c"}`,
		"unknown event field": `{"e":[{"t":"pv","p":"/x","ip":"1.2.3.4"}]}`,
		"no events":           `{"e":[]}`,
		"unknown type":        `{"e":[{"t":"xx"}]}`,
		"path not absolute":   `{"e":[{"t":"pv","p":"javascript:alert(1)"}]}`,
		"path with space":     `{"e":[{"t":"pv","p":"/a b"}]}`,
		"path with email":     `{"e":[{"t":"pv","p":"/u/ana@example.com"}]}`,
		"path too long":       `{"e":[{"t":"pv","p":"/` + strings.Repeat("a", 300) + `"}]}`,
		"referrer with path":  `{"r":"google.com/search?q=secret","e":[{"t":"pv","p":"/x"}]}`,
		"channel":             `{"c":"MX; DROP","e":[{"t":"pv","p":"/x"}]}`,
		"qty zero":            `{"e":[{"t":"ac","s":"A","q":0}]}`,
		"qty huge":            `{"e":[{"t":"ac","s":"A","q":100000}]}`,
		"sku control char":    `{"e":[{"t":"ac","s":"A\u0001","q":1}]}`,
		"bad order":           `{"e":[{"t":"pu","o":"1 OR 1=1","v":1}]}`,
		"negative value":      `{"e":[{"t":"pu","o":"ord_0123456789","v":-5}]}`,
		"mixed fields":        `{"e":[{"t":"pv","p":"/x","s":"A"}]}`,
		"too many":            `{"e":[` + strings.TrimSuffix(strings.Repeat(`{"t":"pv","p":"/x"},`, MaxBeaconEvents+1), ",") + `]}`,
		"too large":           `{"e":[{"t":"pv","p":"/x"}],"r":"` + strings.Repeat("a", MaxBeaconBytes) + `"}`,
	}
	for name, body := range bad {
		var ve *ValidationError
		if _, err := ParseBeacon(strings.NewReader(body)); !errors.As(err, &ve) || len(ve.Fields) == 0 {
			t.Errorf("%s: expected a validation error, got %v", name, err)
		}
	}
}

func TestBotsAndDevices(t *testing.T) {
	chrome := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	iphone := "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"
	ipad := "Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/604.1"
	for _, ua := range []string{chrome, iphone, ipad} {
		if IsBot(ua) {
			t.Errorf("browser flagged as bot: %s", ua)
		}
	}
	for _, ua := range []string{"", "curl/8.5.0", "Go-http-client/2.0", "python-requests/2.31",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/130.0.0.0 Safari/537.36",
		"facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)", "Mozilla/5.0 " + strings.Repeat("x", 600)} {
		if !IsBot(ua) {
			t.Errorf("bot not detected: %q", ua)
		}
	}
	if Device(chrome) != "desktop" || Device(iphone) != "mobile" || Device(ipad) != "tablet" {
		t.Errorf("device classes: %s %s %s", Device(chrome), Device(iphone), Device(ipad))
	}
}

func TestParseRange(t *testing.T) {
	// 2026-03-10 03:30 UTC is still March 9 in Mexico City (UTC-6).
	now := time.Date(2026, 3, 10, 3, 30, 0, 0, time.UTC)
	r, err := ParseRange("today", "", "", now)
	if err != nil || r.FromDay() != "2026-03-09" || r.ToDay() != "2026-03-09" || r.Days() != 1 {
		t.Fatalf("today: %+v %v", r.Info(), err)
	}
	if got := r.From.UTC().Format(time.RFC3339); got != "2026-03-09T06:00:00Z" {
		t.Fatalf("the store day starts at 06:00 UTC, got %s", got)
	}
	r, _ = ParseRange("7d", "", "", now)
	if r.FromDay() != "2026-03-03" || r.ToDay() != "2026-03-09" || r.Days() != 7 {
		t.Fatalf("7d: %+v", r.Info())
	}
	p := r.Previous()
	if p.FromDay() != "2026-02-24" || p.ToDay() != "2026-03-02" || p.Days() != 7 {
		t.Fatalf("previous: %+v", p.Info())
	}
	r, _ = ParseRange("lastmonth", "", "", now)
	if r.FromDay() != "2026-02-01" || r.ToDay() != "2026-02-28" {
		t.Fatalf("lastmonth: %+v", r.Info())
	}
	r, err = ParseRange("custom", "2026-01-01", "2026-01-31", now)
	if err != nil || r.Days() != 31 {
		t.Fatalf("custom: %+v %v", r.Info(), err)
	}
	for _, c := range [][3]string{{"custom", "2026-02-01", "2026-01-01"}, {"custom", "x", "y"}, {"custom", "2020-01-01", "2026-01-01"}, {"nope", "", ""}} {
		if _, err := ParseRange(c[0], c[1], c[2], now); !errors.Is(err, ErrRange) {
			t.Errorf("%v: expected ErrRange, got %v", c, err)
		}
	}
	if DefaultGrain(Range{From: r.From, To: r.From.AddDate(0, 0, 1)}) != GrainHour || bucketOfDay("2026-03-11", GrainWeek) != "2026-03-09" || bucketOfDay("2026-03-11", GrainMonth) != "2026-03" {
		t.Error("grain helpers")
	}
}

// ---------- integration ----------

type fixture struct {
	t    *testing.T
	db   *store.DB
	keys *crypto.Keyring
	svc  *Service
	n    int64
}

func setup(t *testing.T) *fixture {
	admin := os.Getenv("APEXX_TEST_ADMIN_URL")
	if admin == "" {
		t.Skip("APEXX_TEST_ADMIN_URL not set")
	}
	ctx := context.Background()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	dbName := "apexx_test_" + hex.EncodeToString(b)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
		_ = conn.Close(context.Background())
	})
	u, _ := url.Parse(admin)
	u.Path = "/" + dbName
	if _, err := store.Migrate(ctx, u.String(), "test-app-pass", "test-sys-pass"); err != nil {
		t.Fatal(err)
	}
	appURL, sysURL := *u, *u
	appURL.User = url.UserPassword("apexx_app", "test-app-pass")
	sysURL.User = url.UserPassword("apexx_system", "test-sys-pass")
	db, err := store.Open(ctx, appURL.String(), sysURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	mk := make([]byte, 32)
	_, _ = rand.Read(mk)
	keys, _ := crypto.NewKeyring(hex.EncodeToString(mk))
	for _, tn := range []string{"store-a", "store-b"} {
		w, _ := keys.NewWrappedKey(tn)
		if err := db.CreateTenant(ctx, tn, tn, w); err != nil {
			t.Fatal(err)
		}
		if err := keys.Load(tn, w); err != nil {
			t.Fatal(err)
		}
		if err := db.SetChannel(ctx, tn, "test", "mx", seed.Channels()["mx"]); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 4; i++ {
			sku := fmt.Sprintf("S%d", i)
			if _, err := db.UpsertProduct(ctx, tn, "test", catalog.Product{ID: fmt.Sprintf("P%d", i), Handle: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("Producto %d", i),
				Brand: []string{"Norte", "Sierra"}[i%2], Categories: []string{"calzado"}, Variants: []catalog.Variant{{SKU: sku, Title: "Única"}}}, 0); err != nil {
				t.Fatal(err)
			}
			_ = db.SetStock(ctx, tn, "test", sku, "w1", int64(i*3-3)) // S1 out, S2 low (3), S3 6, S4 9
			_ = db.SetPrices(ctx, tn, "test", "mx", sku, []pricing.Tier{{MinQty: 1, Unit: money.New(int64(i)*10000, "MXN")}})
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &fixture{t: t, db: db, keys: keys, svc: &Service{DB: db, Keys: keys, Log: log}, n: 1}
}

func mx(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, Zone)
	if err != nil {
		panic(err)
	}
	return t
}

type ord struct {
	id, email            string
	paid                 time.Time // zero = never paid
	gross, disc, ship    int64
	qty                  int64
	sku                  string
	promo                string
	unpaidStatus, status string
}

// order writes an order the way the checkout does: order, line, events, payment.
func (f *fixture) order(tenant string, o ord) string {
	f.t.Helper()
	ctx := context.Background()
	f.n++
	id := fmt.Sprintf("ord_%s%06d", strings.ReplaceAll(tenant, "-", ""), f.n)
	if o.sku == "" {
		o.sku = "S1"
	}
	if o.qty == 0 {
		o.qty = 1
	}
	cust, _ := json.Marshal(map[string]string{"email": o.email, "name": "Ana Prueba"})
	enc, err := f.keys.Encrypt(tenant, cust, id)
	if err != nil {
		f.t.Fatal(err)
	}
	grand := o.gross - o.disc + o.ship
	created := o.paid.Add(-10 * time.Second)
	status := "paid"
	if o.paid.IsZero() {
		created, status = mx("2026-03-09 12:00:00"), "cancelled"
	}
	promos := `[]`
	if o.promo != "" {
		promos = fmt.Sprintf(`[{"id":%q,"name":"Promo","applied":true,"discount":{"amount":%d,"currency":"MXN"}},{"id":"otra","name":"No aplica","applied":false,"discount":{"amount":0,"currency":"MXN"}}]`, o.promo, o.disc)
	}
	sys := f.db.System
	exec := func(q string, args ...any) {
		f.t.Helper()
		if _, err := sys.Exec(ctx, q, args...); err != nil {
			f.t.Fatal(err)
		}
	}
	exec(`INSERT INTO orders (tenant_id, id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, version, created_at, updated_at)
		VALUES ($1,$2,$3,'mx',$4,'MXN',$5,$6,$7,$8,$9,$10,2,$11,$11)`, tenant, id, f.n, status, o.gross, o.disc, o.ship, grand*16/116, grand, enc, created)
	exec(`INSERT INTO order_lines (tenant_id, order_id, line, sku, title, qty, unit, total) VALUES ($1,$2,1,$3,'Producto',$4,$5,$6)`, tenant, id, o.sku, o.qty, o.gross/o.qty, o.gross)
	exec(`INSERT INTO order_events (tenant_id, order_id, seq, at, actor, type, to_status, data) VALUES ($1,$2,1,$3,'t','order.created','pending_payment',$4::jsonb)`,
		tenant, id, created, `{"promotions":`+promos+`}`)
	if o.paid.IsZero() {
		exec(`INSERT INTO order_events (tenant_id, order_id, seq, at, actor, type, from_status, to_status) VALUES ($1,$2,2,$3,'t','payment.failed','pending_payment','cancelled')`, tenant, id, created)
		return id
	}
	exec(`INSERT INTO order_events (tenant_id, order_id, seq, at, actor, type, from_status, to_status) VALUES ($1,$2,2,$3,'t','payment.captured','pending_payment','paid')`, tenant, id, o.paid)
	exec(`INSERT INTO payments (tenant_id, id, order_id, provider, status, amount, currency) VALUES ($1,$2,$3,'sandbox','captured',$4,'MXN')`, tenant, "pay_"+id, id, grand)
	return id
}

// refund marks an order refunded at a given instant, like orders.Cancel does.
func (f *fixture) refund(tenant, id string, at time.Time) {
	f.t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`UPDATE payments SET refunded = amount, status = 'refunded' WHERE tenant_id=$1 AND order_id=$2`,
		`UPDATE orders SET status = 'refunded', version = version + 1, updated_at = now() WHERE tenant_id=$1 AND id=$2`,
	} {
		if _, err := f.db.System.Exec(ctx, q, tenant, id); err != nil {
			f.t.Fatal(err)
		}
	}
	if _, err := f.db.System.Exec(ctx, `INSERT INTO order_events (tenant_id, order_id, seq, at, actor, type, from_status, to_status) VALUES ($1,$2,3,$3,'t','order.refunded','paid','refunded')`, tenant, id, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) sync() {
	f.t.Helper()
	if _, err := f.svc.SyncOrders(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) sales(tenant, from, to string) SalesReport {
	f.t.Helper()
	r, err := ParseRange("custom", from, to, time.Now())
	if err != nil {
		f.t.Fatal(err)
	}
	rep, err := f.svc.SalesReport(context.Background(), tenant, Filter{Range: r}, GrainDay)
	if err != nil {
		f.t.Fatal(err)
	}
	return rep
}

func TestSalesRollupsRefundsAndDayBoundaries(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// Two orders one second apart across midnight in Mexico City (06:00 UTC).
	late := f.order("store-a", ord{email: "ana@example.com", paid: mx("2026-03-09 23:59:59"), gross: 100000, disc: 10000, ship: 9900, qty: 2, promo: "promo-10"})
	f.order("store-a", ord{email: "Ana@Example.com ", paid: mx("2026-03-10 00:00:00"), gross: 50000, ship: 9900, sku: "S2"})
	f.order("store-a", ord{email: "luis@example.com", paid: mx("2026-03-10 13:15:00"), gross: 30000, sku: "S3"})
	f.order("store-a", ord{email: "nadie@example.com", gross: 77700})                                         // never paid: not a sale
	f.order("store-b", ord{email: "ana@example.com", paid: mx("2026-03-09 10:00:00"), gross: 999900, qty: 1}) // other store
	f.sync()

	rep := f.sales("store-a", "2026-03-09", "2026-03-10")
	if len(rep.Series) != 2 {
		t.Fatalf("series: %+v", rep.Series)
	}
	d9, d10 := rep.Series[0].Sales, rep.Series[1].Sales
	if d9.Orders != 1 || d9.Gross != 100000 || d9.Discounts != 10000 || d9.Shipping != 9900 || d9.Units != 2 || d9.Net != 99900 || d9.Tax != 99900*16/116 {
		t.Errorf("March 9 (order paid 23:59:59 local): %+v", d9)
	}
	if d10.Orders != 2 || d10.Gross != 80000 || d10.Net != 89900 || d10.AOV != 44950 {
		t.Errorf("March 10 (order paid 00:00:00 local): %+v", d10)
	}
	// The same e-mail (different case and spaces) is one customer: new on the 9th, returning on the 10th.
	if d9.NewCustomers != 1 || d9.ReturningCustomers != 0 || d10.NewCustomers != 1 || d10.ReturningCustomers != 1 {
		t.Errorf("new/returning: day9=%d/%d day10=%d/%d", d9.NewCustomers, d9.ReturningCustomers, d10.NewCustomers, d10.ReturningCustomers)
	}
	if rep.Totals.Orders != 3 || rep.Totals.Gross != 180000 || rep.Totals.Refunds != 0 || rep.Totals.Net != 189800 {
		t.Errorf("totals: %+v", rep.Totals)
	}
	// Hour of day in store time: 23h, 0h and 13h.
	for h, want := range map[int]int64{23: 1, 0: 1, 13: 1, 6: 0, 5: 0} {
		if rep.ByHour[h].Orders != want {
			t.Errorf("hour %d: %d orders, want %d", h, rep.ByHour[h].Orders, want)
		}
	}
	// 2026-03-09 is a Monday (slot 0), the 10th a Tuesday.
	if rep.ByWeekday[0].Orders != 1 || rep.ByWeekday[1].Orders != 2 {
		t.Errorf("weekday: %+v", rep.ByWeekday[:2])
	}

	// A refund two days later lowers the net of the refund day, not of the sale day.
	f.refund("store-a", late, mx("2026-03-11 18:30:00"))
	f.sync()
	rep = f.sales("store-a", "2026-03-09", "2026-03-11")
	d9, d11 := rep.Series[0].Sales, rep.Series[2].Sales
	if d9.Gross != 100000 || d9.Refunds != 0 || d9.Net != 99900 {
		t.Errorf("sale day must not change after the refund: %+v", d9)
	}
	if d11.Orders != 0 || d11.Refunds != 99900 || d11.RefundCount != 1 || d11.Net != -99900 {
		t.Errorf("refund day: %+v", d11)
	}
	if rep.Totals.Refunds != 99900 || rep.Totals.Net != 189800-99900 || rep.Totals.Orders != 3 {
		t.Errorf("totals after refund: %+v", rep.Totals)
	}
	// Comparison: the previous period of [10, 11] is [8, 9].
	rep = f.sales("store-a", "2026-03-10", "2026-03-11")
	if rep.PrevTotals.Orders != 1 || rep.PrevTotals.Gross != 100000 {
		t.Errorf("previous period: %+v", rep.PrevTotals)
	}

	// Hourly and daily rollups agree with each other and with the facts.
	var hourly, daily, facts int64
	q := func(dst *int64, sql string) {
		if err := f.db.System.QueryRow(ctx, sql).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	q(&hourly, `SELECT sum(gross - discounts + shipping - refunds) FROM analytics_sales_hourly WHERE tenant_id='store-a'`)
	q(&daily, `SELECT sum(gross - discounts + shipping - refunds) FROM analytics_sales_daily WHERE tenant_id='store-a'`)
	q(&facts, `SELECT sum(total - refunded) FROM analytics_orders WHERE tenant_id='store-a' AND paid_at IS NOT NULL`)
	if hourly != daily || daily != facts || facts != 189800-99900 {
		t.Errorf("rollups disagree: hourly=%d daily=%d facts=%d", hourly, daily, facts)
	}

	// Syncing again changes nothing, and Rebuild reproduces the same numbers.
	f.sync()
	before := f.sales("store-a", "2026-03-01", "2026-03-31")
	if _, err := f.svc.Rebuild(ctx, "store-a"); err != nil {
		t.Fatal(err)
	}
	after := f.sales("store-a", "2026-03-01", "2026-03-31")
	if fmt.Sprint(before.Series) != fmt.Sprint(after.Series) || before.Totals != after.Totals {
		t.Errorf("rebuild changed the numbers:\n%+v\n%+v", before.Totals, after.Totals)
	}

	// Products, promotions and customers come from the same facts.
	r, _ := ParseRange("custom", "2026-03-09", "2026-03-11", time.Now())
	pr, err := f.svc.ProductsReport(ctx, "store-a", Filter{Range: r}, "product", 10)
	if err != nil || len(pr.Rows) != 3 || pr.Rows[0].Key != "P1" || pr.Rows[0].Revenue != 100000 || pr.Rows[0].Units != 2 || pr.Totals.Revenue != 180000 {
		t.Errorf("products: %+v %v", pr, err)
	}
	br, _ := f.svc.ProductsReport(ctx, "store-a", Filter{Range: r}, "brand", 10)
	if len(br.Rows) != 2 || br.Rows[0].Name != "Sierra" || br.Rows[0].Revenue != 130000 {
		t.Errorf("brands: %+v", br.Rows)
	}
	pm, err := f.svc.PromotionsReport(ctx, "store-a", Filter{Range: r})
	if err != nil || len(pm.Rows) != 1 || pm.Rows[0].ID != "promo-10" || pm.Rows[0].Discount != 10000 || pm.Rows[0].Orders != 1 || pm.Rows[0].Revenue != 99900 {
		t.Errorf("promotions: %+v %v", pm.Rows, err)
	}
	cr, err := f.svc.CustomersReport(ctx, "store-a", Filter{Range: r})
	if err != nil || cr.Customers != 2 || cr.NewCustomers != 2 || cr.Returning != 1 || cr.NewOrders != 2 || cr.ReturningOrders != 1 || cr.Frequency[0] != 1 || cr.Frequency[1] != 1 {
		t.Errorf("customers: %+v %v", cr, err)
	}
	for _, c := range cr.Top {
		if strings.Contains(c.Customer, "@") || len(c.Customer) != 10 {
			t.Errorf("customers are identified by a short hash, got %q", c.Customer)
		}
	}
	inv, err := f.svc.InventoryReport(ctx, "store-a", "", "", 5, 100)
	if err != nil || inv.SKUs != 4 || inv.OutOfStock != 1 || inv.LowStock != 1 || inv.Units != 18 || inv.Value != 3*20000+6*30000+9*40000 {
		t.Errorf("inventory: %+v %v", inv, err)
	}
	if low, _ := f.svc.InventoryReport(ctx, "store-a", "mx", "low", 5, 100); len(low.Rows) != 1 || low.Rows[0].SKU != "S2" {
		t.Errorf("low stock filter: %+v", low.Rows)
	}

	// The dashboard of the other store only sees its own order.
	db, err := f.svc.Dashboard(ctx, "store-b", Filter{Range: r})
	if err != nil || db.Totals.Orders != 1 || db.Totals.Gross != 999900 || len(db.RecentOrders) != 1 {
		t.Errorf("store-b dashboard: %+v %v", db.Totals, err)
	}
}

// event inserts a raw storefront event at a given instant.
func (f *fixture) event(tenant string, visitor byte, at time.Time, typ, product, device, referrer, order string) {
	f.t.Helper()
	v := make([]byte, 16)
	v[0] = visitor
	if _, err := f.db.System.Exec(context.Background(), `INSERT INTO analytics_events (tenant_id, at, type, visitor, channel, product_id, device, referrer, order_id)
		VALUES ($1,$2,$3,$4,'mx',$5,$6,$7,$8)`, tenant, at, typ, v, product, device, referrer, order); err != nil {
		f.t.Fatal(err)
	}
}

func TestFunnelMath(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	day := "2026-11-03"
	f.svc.Now = func() time.Time { return mx(day + " 20:00:00") }
	at := func(hm string) time.Time { return mx(day + " " + hm + ":00") }
	// 1: full funnel. 2: product + cart. 3: product only. 4: bounce.
	// 5: went straight to checkout (cart kept from another day). 6: other device, bounce.
	f.event("store-a", 1, at("10:00"), EvPageView, "", "mobile", "www.google.com", "")
	f.event("store-a", 1, at("10:01"), EvProductView, "P1", "mobile", "", "")
	f.event("store-a", 1, at("10:02"), EvAddToCart, "P1", "mobile", "", "")
	f.event("store-a", 1, at("10:03"), EvCheckoutStart, "", "mobile", "", "")
	f.event("store-a", 1, at("10:04"), EvPurchase, "", "mobile", "", "ord_abcdef123456")
	f.event("store-a", 2, at("11:00"), EvProductView, "P1", "mobile", "instagram.com", "")
	f.event("store-a", 2, at("11:01"), EvAddToCart, "P1", "mobile", "", "")
	f.event("store-a", 3, at("12:00"), EvProductView, "P2", "mobile", "", "")
	f.event("store-a", 3, at("12:05"), EvProductView, "P2", "mobile", "", "")
	f.event("store-a", 4, at("12:00"), EvPageView, "", "mobile", "", "")
	f.event("store-a", 5, at("13:00"), EvCheckoutStart, "", "mobile", "", "")
	f.event("store-a", 6, at("13:00"), EvPageView, "", "desktop", "www.google.com", "")
	// The same visitor hash on the next store day is a different session; 23:30 belongs to its own day.
	f.event("store-a", 1, mx("2026-11-04 00:10:00"), EvPageView, "", "mobile", "", "")
	f.event("store-a", 9, mx("2026-11-02 23:30:00"), EvPageView, "", "mobile", "", "")
	f.event("store-b", 1, at("10:00"), EvPageView, "", "mobile", "", "")
	if _, err := f.svc.RollupTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := ParseRange("custom", day, day, f.svc.now())
	rep, err := f.svc.FunnelReport(ctx, "store-a", Filter{Range: r})
	if err != nil {
		t.Fatal(err)
	}
	want := Traffic{Sessions: 6, Product: 4, Cart: 3, Checkout: 2, Purchase: 1, PageViews: 9}
	if rep.Totals != want {
		t.Fatalf("funnel totals:\n got %+v\nwant %+v", rep.Totals, want)
	}
	tt := rep.Totals
	if !(tt.Sessions >= tt.Product && tt.Product >= tt.Cart && tt.Cart >= tt.Checkout && tt.Checkout >= tt.Purchase) {
		t.Errorf("funnel must never grow downstream: %+v", tt)
	}
	if tt.ConversionBps() != 1666 {
		t.Errorf("conversion = 1 of 6 sessions = 16.66%%, got %d bps", tt.ConversionBps())
	}
	if rep.PrevTotals.Sessions != 1 {
		t.Errorf("previous day has one session (23:30 local), got %+v", rep.PrevTotals)
	}
	dev := map[string]Traffic{}
	for _, d := range rep.ByDevice {
		dev[d.Device] = d.Traffic
	}
	if dev["mobile"].Sessions != 5 || dev["desktop"].Sessions != 1 || dev["mobile"].Purchase != 1 {
		t.Errorf("by device: %+v", dev)
	}
	refs := map[string]ReferrerRow{}
	for _, x := range rep.Referrers {
		refs[x.Referrer] = x
	}
	if refs["www.google.com"].Sessions != 2 || refs["www.google.com"].Purchases != 1 || refs["instagram.com"].Sessions != 1 || refs[""].Sessions != 3 {
		t.Errorf("referrers: %+v", refs)
	}
	if len(rep.TopProducts) != 2 || rep.TopProducts[0].Key != "P1" || rep.TopProducts[0].Views != 2 || rep.TopProducts[0].Visitors != 2 || rep.TopProducts[0].CartVisitors != 2 || rep.TopProducts[1].Views != 2 || rep.TopProducts[1].Visitors != 1 {
		t.Errorf("top products: %+v", rep.TopProducts)
	}
	// Two days: sessions add up per day (a visitor on two days is two sessions).
	r2, _ := ParseRange("custom", day, "2026-11-04", f.svc.now())
	rep2, _ := f.svc.FunnelReport(ctx, "store-a", Filter{Range: r2})
	if rep2.Totals.Sessions != 7 || len(rep2.Series) != 2 || rep2.Series[1].Traffic.Sessions != 1 {
		t.Errorf("two days: %+v", rep2.Totals)
	}
	// Rolling up again is idempotent.
	if _, err := f.svc.RollupTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	rep3, _ := f.svc.FunnelReport(ctx, "store-a", Filter{Range: r})
	if rep3.Totals != want {
		t.Errorf("second rollup changed totals: %+v", rep3.Totals)
	}
}

type fakeCatalog struct{}

func (fakeCatalog) ProductByHandle(h string) (string, bool) { return "P1", h == "p1" }
func (fakeCatalog) ProductBySKU(s string) (string, bool)    { return "P1", s == "S1" }

const (
	testUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	testIP = "203.0.113.77"
)

func TestBeaconPrivacy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := mx("2026-11-03 12:00:00")
	f.svc.Now = func() time.Time { return now }
	b := Beacon{Channel: "mx", Referrer: "www.google.com", Events: []BeaconEvent{
		{Type: "pv", Path: "/s/store-a/main/index"}, {Type: "pv", Path: "/s/store-a/main/p/p1"}, {Type: "pv", Path: "/s/store-a/main/p/no-existe"},
		{Type: "pv", Path: "/s/store-a/main/checkout"}, {Type: "ac", SKU: "S1", Qty: 2}, {Type: "ac", SKU: "NOPE", Qty: 1},
		{Type: "pu", Order: "ord_0123456789abcdef", Value: 12900}}}
	c := Client{IP: testIP, UserAgent: testUA}
	out, err := f.svc.Ingest(ctx, "store-a", b, c, fakeCatalog{})
	if err != nil || out.Stored != 6 {
		t.Fatalf("ingest: %+v %v (a SKU of another store is skipped)", out, err)
	}
	types := map[string]int{}
	rows, _ := f.db.System.Query(ctx, `SELECT type, product_id FROM analytics_events WHERE tenant_id='store-a'`)
	for rows.Next() {
		var ty, p string
		_ = rows.Scan(&ty, &p)
		types[ty+"/"+p]++
	}
	if types["page_view/"] != 2 || types["product_view/P1"] != 1 || types["checkout_start/"] != 1 || types["add_to_cart/P1"] != 1 || types["purchase/"] != 1 {
		t.Errorf("classification: %v", types)
	}

	// Nothing that identifies the person is stored anywhere in the database.
	var dump string
	for _, tbl := range []string{"analytics_events", "analytics_salts", "analytics_traffic_hourly", "analytics_funnel_daily"} {
		var s string
		if err := f.db.System.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(x)::text, ' '), '') FROM `+tbl+` x`).Scan(&s); err != nil {
			t.Fatal(err)
		}
		dump += s
	}
	for _, secret := range []string{testIP, "203.0.113", "Macintosh", "Chrome/130", "Mozilla"} {
		if strings.Contains(dump, secret) {
			t.Errorf("raw %q found in stored analytics", secret)
		}
	}
	var cols string
	_ = f.db.System.QueryRow(ctx, `SELECT string_agg(column_name, ',') FROM information_schema.columns WHERE table_name LIKE 'analytics_%'`).Scan(&cols)
	for _, bad := range []string{"ip", "user_agent", "email", "cookie"} {
		for _, c := range strings.Split(cols, ",") {
			if c == bad {
				t.Errorf("analytics tables must not have a %q column", bad)
			}
		}
	}

	visitors := func(tenant string) []string {
		rows, err := f.db.System.Query(ctx, `SELECT DISTINCT encode(visitor, 'hex') FROM analytics_events WHERE tenant_id=$1`, tenant)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := pgx.CollectRows(rows, pgx.RowTo[string])
		sort.Strings(v)
		return v
	}
	one := Beacon{Events: []BeaconEvent{{Type: "pv", Path: "/s/x/main/c"}}}
	// Same person, same day → same visitor. Another service instance (another node) agrees.
	other := &Service{DB: f.db, Keys: f.keys, Now: f.svc.Now}
	if _, err := other.Ingest(ctx, "store-a", one, c, fakeCatalog{}); err != nil {
		t.Fatal(err)
	}
	va := visitors("store-a")
	if len(va) != 1 || len(va[0]) != 32 {
		t.Fatalf("one visitor expected, got %v", va)
	}
	// Same person in another store → unrelated hash (no cross-store tracking).
	if _, err := f.svc.Ingest(ctx, "store-b", one, c, fakeCatalog{}); err != nil {
		t.Fatal(err)
	}
	if vb := visitors("store-b"); len(vb) != 1 || vb[0] == va[0] {
		t.Errorf("visitor hash must differ between stores: %v %v", va, vb)
	}
	// Another address or browser → another visitor.
	_, _ = f.svc.Ingest(ctx, "store-a", one, Client{IP: "203.0.113.78", UserAgent: testUA}, fakeCatalog{})
	_, _ = f.svc.Ingest(ctx, "store-a", one, Client{IP: testIP, UserAgent: testUA + " Edg/130"}, fakeCatalog{})
	if got := visitors("store-a"); len(got) != 3 {
		t.Errorf("three visitors expected, got %d", len(got))
	}
	// Next store day: the salt rotates, so yesterday's visitor cannot be recognised.
	now = mx("2026-11-04 00:00:01")
	_, _ = f.svc.Ingest(ctx, "store-a", one, c, fakeCatalog{})
	if got := visitors("store-a"); len(got) != 4 {
		t.Errorf("the visitor hash must change every day, got %d distinct", len(got))
	}
	// Do Not Track / Global Privacy Control and bots: accepted, nothing stored.
	var before, after int
	_ = f.db.System.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&before)
	for _, cl := range []Client{{IP: testIP, UserAgent: testUA, DNT: true}, {IP: testIP, UserAgent: "curl/8.5.0"}, {IP: testIP, UserAgent: ""},
		{IP: testIP, UserAgent: "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"}} {
		out, err := f.svc.Ingest(ctx, "store-a", one, cl, fakeCatalog{})
		if err != nil || out.Stored != 0 || out.Dropped == "" {
			t.Errorf("%+v must be dropped: %+v %v", cl, out, err)
		}
	}
	_ = f.db.System.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&after)
	if before != after {
		t.Errorf("dropped requests stored %d events", after-before)
	}
	// Salts older than two days are destroyed.
	now = mx("2026-11-07 09:00:00")
	if _, err := f.svc.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	var salts int
	_ = f.db.System.QueryRow(ctx, `SELECT count(*) FROM analytics_salts`).Scan(&salts)
	if salts != 0 {
		t.Errorf("%d old salts survived the purge", salts)
	}
}

func TestRetentionAndAppendOnly(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.event("store-a", 1, time.Now().Add(-100*24*time.Hour), EvPageView, "", "mobile", "", "")
	f.event("store-a", 2, time.Now().Add(-89*24*time.Hour), EvPageView, "", "mobile", "", "")
	f.event("store-a", 3, time.Now(), EvPageView, "", "mobile", "", "")
	if _, err := f.db.System.Exec(ctx, `UPDATE analytics_events SET path = '/x'`); err == nil {
		t.Error("raw events must be append-only (UPDATE succeeded)")
	}
	if _, err := f.db.System.Exec(ctx, `DELETE FROM analytics_events WHERE at > now() - interval '1 day'`); err == nil {
		t.Error("raw events inside the retention window must not be deletable")
	}
	n, err := f.svc.Purge(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge removed %d events (want only the 100-day-old one): %v", n, err)
	}
	var left int
	_ = f.db.System.QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&left)
	if left != 2 {
		t.Errorf("%d events left, want 2", left)
	}
}

func TestRLSIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.order("store-a", ord{email: "ana@example.com", paid: mx("2026-03-09 10:00:00"), gross: 100000, disc: 5000, promo: "p"})
	f.sync()
	f.svc.Now = func() time.Time { return mx("2026-03-09 12:00:00") }
	if _, err := f.svc.Ingest(ctx, "store-a", Beacon{Events: []BeaconEvent{{Type: "pv", Path: "/s/store-a/main/p/p1"}}}, Client{IP: testIP, UserAgent: testUA}, fakeCatalog{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.RollupTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	tables := []string{"analytics_events", "analytics_traffic_hourly", "analytics_funnel_daily", "analytics_product_traffic_daily", "analytics_referrer_daily",
		"analytics_orders", "analytics_order_promos", "analytics_sales_hourly", "analytics_sales_daily", "analytics_sku_daily", "analytics_promo_daily"}
	count := func(tx pgx.Tx, tbl string) int {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("%s: %v", tbl, err)
		}
		return n
	}
	// Store A sees its rows in every table; store B sees none of them.
	_ = f.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
		for _, tbl := range tables {
			if count(tx, tbl) == 0 {
				t.Errorf("%s: store-a should have rows (the fixture did not fill it)", tbl)
			}
		}
		return nil
	})
	_ = f.db.Tx(ctx, "store-b", func(tx pgx.Tx) error {
		for _, tbl := range tables {
			if n := count(tx, tbl); n != 0 {
				t.Errorf("%s: store-b can read %d rows of store-a", tbl, n)
			}
		}
		return nil
	})
	// Without a tenant the application role sees nothing at all.
	for _, tbl := range append(tables, "analytics_salts", "analytics_state") {
		var n int
		if err := f.db.App.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s without tenant: %d rows, err %v", tbl, n, err)
		}
	}
	// Store B cannot write rows for store A, nor change them.
	err := f.db.Tx(ctx, "store-b", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO analytics_sales_daily (tenant_id, day, channel, currency, gross) VALUES ('store-a', '2026-03-09', 'mx', 'MXN', 1)`)
		return err
	})
	if err == nil {
		t.Error("store-b inserted a rollup row for store-a")
	}
	_ = f.db.Tx(ctx, "store-b", func(tx pgx.Tx) error {
		tag, _ := tx.Exec(ctx, `UPDATE analytics_orders SET gross = 0`)
		if tag.RowsAffected() != 0 {
			t.Error("store-b updated facts of store-a")
		}
		return nil
	})
	// The application role can never read or write a salt.
	if _, err := f.db.App.Exec(ctx, `INSERT INTO analytics_salts (day, salt) VALUES ('2020-01-01', '\x00')`); err == nil {
		t.Error("the application role wrote a salt")
	}
	// Reports through the service are scoped too.
	r, _ := ParseRange("custom", "2026-03-09", "2026-03-09", time.Now())
	a, _ := f.svc.Dashboard(ctx, "store-a", Filter{Range: r})
	b, _ := f.svc.Dashboard(ctx, "store-b", Filter{Range: r})
	if a.Totals.Orders != 1 || a.Traffic.Sessions != 1 || b.Totals.Orders != 0 || b.Traffic.Sessions != 0 || len(b.RecentOrders) != 0 {
		t.Errorf("dashboards: a=%+v/%+v b=%+v/%+v", a.Totals, a.Traffic, b.Totals, b.Traffic)
	}
	al, _, err := f.svc.AuditList(ctx, "store-b", AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range al {
		if strings.Contains(e.Target, "store-a") {
			t.Errorf("store-b sees activity of store-a: %+v", e)
		}
	}
}

func TestAuditListFilters(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		_ = f.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
			return store.Audit(ctx, tx, "store-a", "member:m_ana", fmt.Sprintf("order.%s", []string{"created", "refunded"}[i%2]), fmt.Sprintf("ord_%d", i), map[string]any{"i": i})
		})
	}
	all, next, err := f.svc.AuditList(ctx, "store-a", AuditFilter{Limit: 5})
	if err != nil || len(all) != 5 || next == 0 || all[0].Seq < all[4].Seq {
		t.Fatalf("first page: %d items, next %d, %v", len(all), next, err)
	}
	rest, next2, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Before: next})
	if next2 != 0 || len(rest) == 0 || rest[0].Seq >= next {
		t.Errorf("second page: %d items, next %d", len(rest), next2)
	}
	byPrefix, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Action: "order"})
	exact, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Action: "order.refunded"})
	byActor, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Actor: "member:m_ana"})
	byTarget, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Target: "ORD_3"})
	wild, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{Target: "%"})
	future, _, _ := f.svc.AuditList(ctx, "store-a", AuditFilter{From: time.Now().Add(time.Hour)})
	if len(byPrefix) != 7 || len(exact) != 3 || len(byActor) != 7 || len(byTarget) != 1 || len(wild) != 0 || len(future) != 0 {
		t.Errorf("filters: prefix=%d exact=%d actor=%d target=%d wildcard=%d future=%d", len(byPrefix), len(exact), len(byActor), len(byTarget), len(wild), len(future))
	}
	actors, actions, total, err := f.svc.AuditFacets(ctx, "store-a")
	if err != nil || total < 7 || len(actions) < 2 || len(actors) < 1 {
		t.Errorf("facets: %v %v %d %v", actors, actions, total, err)
	}
	if h := f.svc.Health(ctx, "store-a"); !h.DB.OK || h.DB.PoolMax == 0 || h.Stock.Out != 1 || h.Stock.Low != 1 {
		t.Errorf("health: %+v", h)
	}
}

// TestDashboardPerformance seeds 50,000 orders spread over a year and
// measures the home dashboard. Run with -v to see the percentiles.
func TestDashboardPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds 50k orders")
	}
	f := setup(t)
	ctx := context.Background()
	const orders = 50000
	start := time.Now()
	if err := f.svc.SeedDemo(ctx, "store-a", "test", DemoOptions{Orders: orders, Days: 365, Sessions: 2, Seed: 7}); err != nil {
		t.Fatal(err)
	}
	seeded := time.Since(start)
	// A second store with its own history must not slow the first one down or leak into it.
	if err := f.svc.SeedDemo(ctx, "store-b", "test", DemoOptions{Orders: 2000, Days: 60, Sessions: 0, Seed: 8}); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	n, err := f.svc.SyncOrders(ctx)
	if err != nil || n != orders+2000 {
		t.Fatalf("sync: %d orders, %v", n, err)
	}
	if _, err := f.svc.RollupTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	synced := time.Since(start)
	for _, q := range []string{"VACUUM ANALYZE"} {
		if _, err := f.db.System.Exec(ctx, q); err != nil {
			t.Log("analyze:", err)
		}
	}
	// The incremental sync gave the same result a full rebuild would.
	var facts, paid, rollup int64
	_ = f.db.System.QueryRow(ctx, `SELECT count(*), count(paid_at) FROM analytics_orders WHERE tenant_id='store-a'`).Scan(&facts, &paid)
	_ = f.db.System.QueryRow(ctx, `SELECT sum(orders) FROM analytics_sales_daily WHERE tenant_id='store-a'`).Scan(&rollup)
	if facts != orders || paid != rollup || paid < orders*9/10 {
		t.Fatalf("facts=%d paid=%d rollup orders=%d", facts, paid, rollup)
	}

	presets := []string{"today", "7d", "30d", "90d", "12m"}
	var all []time.Duration
	per := map[string][]time.Duration{}
	for i := 0; i < 60; i++ {
		for _, p := range presets {
			r, _ := ParseRange(p, "", "", time.Now())
			s0 := time.Now()
			d, err := f.svc.Dashboard(ctx, "store-a", Filter{Range: r})
			el := time.Since(s0)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 && p == "12m" && (d.Totals.Orders != paid || len(d.TopProducts) == 0 || len(d.RecentOrders) != 8) {
				t.Fatalf("12m dashboard: %d orders (want %d), %d products, %d recent", d.Totals.Orders, paid, len(d.TopProducts), len(d.RecentOrders))
			}
			all = append(all, el)
			per[p] = append(per[p], el)
		}
	}
	pctl := func(d []time.Duration, q float64) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[int(float64(len(d)-1)*q)]
	}
	t.Logf("seed %d orders: %s; first sync + rollups: %s", orders, seeded.Round(time.Millisecond), synced.Round(time.Millisecond))
	for _, p := range presets {
		t.Logf("dashboard %-5s p50=%s p95=%s", p, pctl(per[p], .5).Round(100*time.Microsecond), pctl(per[p], .95).Round(100*time.Microsecond))
	}
	p95 := pctl(all, .95)
	t.Logf("dashboard over %d orders, %d calls: p50=%s p95=%s max=%s", orders, len(all), pctl(all, .5).Round(100*time.Microsecond), p95.Round(100*time.Microsecond), pctl(all, 1).Round(100*time.Microsecond))
	if p95 > 250*time.Millisecond {
		t.Errorf("dashboard p95 = %s on %d orders; budget is 250 ms", p95, orders)
	}
	// The heaviest report stays interactive too.
	r, _ := ParseRange("12m", "", "", time.Now())
	s0 := time.Now()
	if _, err := f.svc.CustomersReport(ctx, "store-a", Filter{Range: r}); err != nil {
		t.Fatal(err)
	}
	t.Logf("customers report, 12 months: %s", time.Since(s0).Round(time.Millisecond))
	s0 = time.Now()
	if _, err := f.svc.ProductsReport(ctx, "store-a", Filter{Range: r}, "category", 100); err != nil {
		t.Fatal(err)
	}
	t.Logf("products by category, 12 months: %s", time.Since(s0).Round(time.Millisecond))
}

// The operations counts are the same whether the rollup has run or not, and
// with new rows after its mark: rollup plus the rows after it, never twice.
func TestActivityRollupExact(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	write := func(i int) {
		_ = f.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
			if err := store.Audit(ctx, tx, "store-a", fmt.Sprintf("member:m_%d", i%3), []string{"order.created", "product.updated"}[i%2], fmt.Sprintf("x_%d", i), nil); err != nil {
				return err
			}
			return store.Emit(ctx, tx, "store-a", "test.topic", fmt.Sprintf("k%d", i), nil)
		})
	}
	counts := func() (int64, map[string]int64) {
		_, _ = f.db.SequenceOutbox(ctx)
		h := f.svc.Health(ctx, "store-a")
		actors, actions, _, err := f.svc.AuditFacets(ctx, "store-a")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]int64{}
		for _, a := range actors {
			m["actor "+a.Value] = a.Count
		}
		for _, a := range actions {
			m["action "+a.Value] = a.Count
		}
		return h.Outbox.Last24h, m
	}
	for i := 0; i < 12; i++ {
		write(i)
	}
	raw24, rawFacets := counts()
	if raw24 < 12 {
		t.Fatalf("without rollup: %d changes", raw24)
	}
	if _, err := f.svc.RollupActivity(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	r24, rFacets := counts()
	if r24 != raw24 || fmt.Sprint(rFacets) != fmt.Sprint(rawFacets) {
		t.Fatalf("after rollup: %d vs %d · %v vs %v", r24, raw24, rFacets, rawFacets)
	}
	for i := 12; i < 17; i++ {
		write(i)
	}
	n24, nFacets := counts()
	if n24 != raw24+5 || nFacets["actor member:m_0"] != rawFacets["actor member:m_0"]+2 {
		t.Fatalf("new rows after the mark: %d (want %d) · %v", n24, raw24+5, nFacets)
	}
	if _, err := f.svc.RollupActivity(ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	if a24, aFacets := counts(); a24 != n24 || fmt.Sprint(aFacets) != fmt.Sprint(nFacets) {
		t.Fatalf("second rollup changed the counts: %d vs %d · %v vs %v", a24, n24, aFacets, nFacets)
	}
}
