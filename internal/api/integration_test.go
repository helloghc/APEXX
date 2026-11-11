package api

// Integration tests against a real PostgreSQL. They run when
// APEXX_TEST_ADMIN_URL points to a server where the user can create databases
// (CI starts one; locally: postgres://postgres@127.0.0.1:5432/postgres).

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/resilience"
	"github.com/helloghc/apexx/internal/seed"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
)

type env struct {
	t       *testing.T
	db      *store.DB
	keys    *crypto.Keyring
	proj    *projection.Projector
	sandbox *payments.Sandbox
	srv     *httptest.Server
	newSrv  func() *httptest.Server
	adminA  string // admin key of tenant "a"
	sfA     string // storefront key of tenant "a"
	adminB  string
	orders  *orders.Service
	server  *Server
}

func setup(t *testing.T) *env {
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

	e := &env{t: t, db: db, keys: keys, sandbox: payments.NewSandbox()}
	for _, tn := range []string{"a", "b"} {
		w, _ := keys.NewWrappedKey("store-" + tn)
		if err := db.CreateTenant(ctx, "store-"+tn, tn, w); err != nil {
			t.Fatal(err)
		}
		if err := keys.Load("store-"+tn, w); err != nil {
			t.Fatal(err)
		}
		if err := db.SetChannel(ctx, "store-"+tn, "test", "mx", seed.Channels()["mx"]); err != nil {
			t.Fatal(err)
		}
	}
	e.adminA, _ = db.CreateAPIKey(ctx, "store-a", "admin", "admin")
	e.sfA, _ = db.CreateAPIKey(ctx, "store-a", "sf", "storefront")
	e.adminB, _ = db.CreateAPIKey(ctx, "store-b", "admin", "admin")
	cmp := money.New(12000, "MXN")
	for i := 1; i <= 3; i++ {
		sku := fmt.Sprintf("S%d", i)
		if _, err := db.UpsertProduct(ctx, "store-a", "test", catalog.Product{ID: fmt.Sprintf("P%d", i), Handle: fmt.Sprintf("p%d", i), Title: fmt.Sprintf("Producto %d", i),
			Variants: []catalog.Variant{{SKU: sku, Title: "Única", WeightGrams: 500}}}, 0); err != nil {
			t.Fatal(err)
		}
		_ = db.SetStock(ctx, "store-a", "test", sku, "w1", 10)
		_ = db.SetPrices(ctx, "store-a", "test", "mx", sku, []pricing.Tier{{MinQty: 1, Unit: money.New(10000, "MXN"), CompareAt: &cmp}})
	}
	e.newSrv = func() *httptest.Server {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		proj := projection.New(db, sysURL.String(), log)
		if err := proj.Load(ctx); err != nil {
			t.Fatal(err)
		}
		e.proj = proj
		pa := &PaymentAccounts{DB: db, Keys: keys, Sandbox: e.sandbox, Breakers: &resilience.Breakers{Threshold: 5, Cooldown: time.Second}}
		lib, _ := builder.NewLibrary(builder.DefaultSections()...)
		DefaultHome = seed.HomeTemplate
		s := &Server{DB: db, Proj: proj, Auth: &auth.Service{DB: db, Keys: keys}, Keys: keys, Library: lib, Log: log, Payments: pa,
			Orders:   &orders.Service{DB: db, Keys: keys, Projector: proj, Accounts: pa.Resolve},
			Webhooks: &webhooks.Service{DB: db, Keys: keys, Proj: proj, AllowPrivate: true}}
		e.orders, e.server = s.Orders, s
		srv := httptest.NewServer(s.Routes())
		t.Cleanup(srv.Close)
		return srv
	}
	e.srv = e.newSrv()
	return e
}

func (e *env) do(method, path, key string, body any, hdr ...string) (int, map[string]any) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if m == nil {
		m = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, m
}

func (e *env) drain() {
	if err := e.proj.Drain(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) available(sku string) float64 {
	e.drain()
	_, m := e.do("GET", "/v1/inventory/"+sku, e.adminA, nil)
	v, _ := m["available"].(float64)
	return v
}

func TestTenantIsolation(t *testing.T) {
	e := setup(t)
	if c, _ := e.do("GET", "/v1/products/P1", e.adminB, nil); c != 404 {
		t.Fatalf("store-b read store-a product: %d", c)
	}
	_, m := e.do("GET", "/v1/products", e.adminB, nil)
	if m["total"].(float64) != 0 {
		t.Fatalf("store-b sees %v products", m["total"])
	}
	// The application role cannot see rows without a tenant set, nor API key hashes.
	var n int
	if err := e.db.App.QueryRow(context.Background(), "SELECT count(*) FROM products").Scan(&n); err != nil || n != 0 {
		t.Fatalf("app role without tenant saw %d products (err %v)", n, err)
	}
	if _, err := e.db.App.Exec(context.Background(), "SELECT 1 FROM api_keys"); err == nil {
		t.Fatal("app role can read api_keys")
	}
}

func TestCheckoutLifecycleAndReversion(t *testing.T) {
	e := setup(t)
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 2}},
		"customer": map[string]any{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
	c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "order-key-1")
	if c != 201 || o["status"] != "paid" {
		t.Fatalf("checkout %d %v", c, o)
	}
	if got := e.available("S1"); got != 8 {
		t.Fatalf("available after sale %v, want 8", got)
	}
	// Same key: same order, no second charge.
	calls := e.sandbox.Calls
	c2, o2 := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "order-key-1")
	if c2 != 201 || o2["id"] != o["id"] || e.sandbox.Calls != calls {
		t.Fatalf("replay: %d %v calls %d→%d", c2, o2["id"], calls, e.sandbox.Calls)
	}
	// Same key, different body.
	body2 := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 1}},
		"customer": map[string]any{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
	if c3, _ := e.do("POST", "/v1/checkout", e.sfA, body2, "Idempotency-Key", "order-key-1"); c3 != 422 {
		t.Fatalf("key reuse: %d", c3)
	}
	// Declined card releases the reservation.
	body2["paymentMethod"] = "pm_card_chargeDeclined"
	if c4, m := e.do("POST", "/v1/checkout", e.sfA, body2, "Idempotency-Key", "order-key-2"); c4 != 402 || m["code"] != "payment_declined" {
		t.Fatalf("declined: %d %v", c4, m)
	}
	if got := e.available("S1"); got != 8 {
		t.Fatalf("available after decline %v, want 8", got)
	}
	// Overselling is refused.
	body2["paymentMethod"] = "pm_card_visa"
	body2["items"] = []map[string]any{{"sku": "S1", "qty": 9}}
	if c5, _ := e.do("POST", "/v1/checkout", e.sfA, body2, "Idempotency-Key", "order-key-3"); c5 != 409 {
		t.Fatalf("oversell: %d", c5)
	}
	// Cancel reverts everything: refund, restock, ledger balanced.
	id := o["id"].(string)
	if c6, m := e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, map[string]any{"reason": "test"}); c6 != 200 || m["status"] != "refunded" {
		t.Fatalf("cancel %d %v", c6, m)
	}
	if got := e.available("S1"); got != 10 {
		t.Fatalf("available after refund %v, want 10", got)
	}
	if e.sandbox.Refunded() != int64(o["grandTotal"].(float64)) {
		t.Fatalf("refunded %d, want %v", e.sandbox.Refunded(), o["grandTotal"])
	}
	var unbalanced int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("%d unbalanced ledger transactions", unbalanced)
	}
	if c7, _ := e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, nil); c7 != 409 {
		t.Fatalf("second cancel: %d", c7)
	}
	// Customer data is encrypted at rest.
	var raw []byte
	_ = e.db.System.QueryRow(context.Background(), `SELECT customer FROM orders WHERE id=$1`, id).Scan(&raw)
	if bytes.Contains(raw, []byte("ana@example.com")) {
		t.Fatal("customer email stored in clear text")
	}
	// Invoices are issued by the worker.
	if _, err := (&orders.Service{DB: e.db, Keys: e.keys, Projector: e.proj, Accounts: func(ctx context.Context, tn string) (orders.Account, error) {
		return orders.Account{Provider: e.sandbox}, nil
	}}).IssueInvoices(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPendingPaymentIsReconciled(t *testing.T) {
	e := setup(t)
	e.sandbox.FailNext = fmt.Errorf("network down")
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S2", "qty": 1}},
		"customer": map[string]any{"email": "x@example.com", "name": "X"}, "paymentMethod": "pm_card_visa"}
	c, m := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "pending-key-1")
	if c != 502 || m["code"] != "payment_pending" {
		t.Fatalf("want pending, got %d %v", c, m)
	}
	if got := e.available("S2"); got != 9 {
		t.Fatalf("reservation should hold 1 unit, available %v", got)
	}
	svc := &orders.Service{DB: e.db, Keys: e.keys, Projector: e.proj, Accounts: func(ctx context.Context, tn string) (orders.Account, error) {
		return orders.Account{Provider: e.sandbox}, nil
	}}
	n, err := svc.Reconcile(context.Background(), 0)
	if err != nil || n != 1 {
		t.Fatalf("reconcile n=%d err=%v", n, err)
	}
	_, o := e.do("GET", "/v1/orders/"+m["orderId"].(string), e.adminA, nil)
	if o["status"] != "cancelled" {
		t.Fatalf("no charge found → order must be cancelled, got %v", o["status"])
	}
	if got := e.available("S2"); got != 10 {
		t.Fatalf("reservation not released, available %v", got)
	}
}

func TestTeamPermissions(t *testing.T) {
	e := setup(t)
	a := &auth.Service{DB: e.db}
	if _, err := a.CreateMember(context.Background(), "store-a", "test", "owner@a.test", "Owner", "owner-password-123", nil, true); err != nil {
		t.Fatal(err)
	}
	_, login := e.do("POST", "/v1/auth/login", "", map[string]any{"store": "store-a", "email": "owner@a.test", "password": "owner-password-123"})
	owner := login["token"].(string)
	if c, _ := e.do("POST", "/v1/team/members", owner, map[string]any{"email": "ed@a.test", "name": "Ed", "password": "editor-password-1",
		"permissions": map[string]string{"catalog": "edit", "orders": "read"}}); c != 201 {
		t.Fatalf("create member %d", c)
	}
	_, l2 := e.do("POST", "/v1/auth/login", "", map[string]any{"store": "store-a", "email": "ed@a.test", "password": "editor-password-1"})
	ed := l2["token"].(string)
	cases := []struct {
		method, path string
		body         any
		want         int
	}{
		{"PUT", "/v1/products/P1", map[string]any{"handle": "p1", "title": "Nuevo", "variants": []map[string]any{{"sku": "S1", "title": "Única"}}}, 200},
		{"PUT", "/v1/prices/mx/S1", map[string]any{"tiers": []map[string]any{{"minQty": 1, "unit": map[string]any{"amount": 1, "currency": "MXN"}}}}, 403},
		{"POST", "/v1/sites/main/publish", nil, 403},
		{"POST", "/v1/team/members", map[string]any{}, 403},
		{"GET", "/v1/orders", nil, 200},
	}
	for _, c := range cases {
		if got, _ := e.do(c.method, c.path, ed, c.body); got != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.path, got, c.want)
		}
	}
	if c, _ := e.do("POST", "/v1/auth/login", "", map[string]any{"store": "store-a", "email": "ed@a.test", "password": "wrong-password-1"}); c != 401 {
		t.Fatalf("bad password %d", c)
	}
}

func TestBuilderSurvivesRestart(t *testing.T) {
	e := setup(t)
	if c, m := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, map[string]any{"ops": []map[string]any{{"op": "move", "from": "/regions/main/3", "path": "/regions/main/1"}}}); c != 200 {
		t.Fatalf("patch %d %v", c, m)
	}
	if c, _ := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 200 {
		t.Fatalf("publish %d", c)
	}
	page := func() string {
		resp, err := http.Get(e.srv.URL + "/s/store-a/main/index")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	before := page()
	if strings.Index(before, "sec_promos") > strings.Index(before, "sec_benefits") {
		t.Fatal("move not applied")
	}
	e.srv = e.newSrv() // a brand new process view: everything reloads from Postgres
	if after := page(); after != before {
		t.Fatal("published page changed after restart")
	}
	if c, _ := e.do("POST", "/v1/sites/main/rollback", e.adminA, nil); c != 409 {
		t.Fatalf("rollback with a single release should be 409, got %d", c)
	}
}

func TestFreshnessAndAudit(t *testing.T) {
	e := setup(t)
	start := time.Now()
	if c, _ := e.do("PUT", "/v1/prices/mx/S3", e.adminA, map[string]any{"tiers": []map[string]any{{"minQty": 1, "unit": map[string]any{"amount": 777, "currency": "MXN"}}}}); c != 204 {
		t.Fatalf("price %d", c)
	}
	e.drain()
	_, sim := e.do("POST", "/v1/carts/simulate", e.sfA, map[string]any{"channel": "mx", "items": []map[string]any{{"sku": "S3", "qty": 1}}})
	unit := sim["lines"].([]any)[0].(map[string]any)["unit"].(map[string]any)["amount"].(float64)
	if unit != 777 {
		t.Fatalf("price not visible: %v", unit)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("write→visible took %v", d)
	}
	_, v := e.do("GET", "/v1/audit/verify", e.adminA, nil)
	if v["intact"] != true {
		t.Fatalf("audit chain broken: %v", v)
	}
	if _, err := e.db.System.Exec(context.Background(), `UPDATE audit_log SET actor='x'`); err == nil {
		t.Fatal("audit log accepted an UPDATE")
	}
}

func TestTwoNodesSeeSamePublishedSite(t *testing.T) {
	e := setup(t)
	nodeA := e.srv
	projA := e.proj
	nodeB := e.newSrv()
	projB := e.proj
	get := func(srv *httptest.Server) string {
		resp, err := http.Get(srv.URL + "/s/store-a/main/index")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.Header.Get("ETag") + string(b)
	}
	// Publish v1 on node A and make node B load the site.
	e.srv = nodeA
	if c, _ := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 200 {
		t.Fatalf("publish v1: %d", c)
	}
	_ = projB.Drain(context.Background())
	first := get(nodeB)
	// Change and publish v2 on node A; node B must serve v2 once it applies the event.
	if c, _ := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, map[string]any{"ops": []map[string]any{{"op": "replace", "path": "/regions/main/0/settings/heading", "value": "Versión dos"}}}); c != 200 {
		t.Fatalf("patch: %d", c)
	}
	if c, _ := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 200 {
		t.Fatalf("publish v2: %d", c)
	}
	_ = projA.Drain(context.Background())
	_ = projB.Drain(context.Background())
	second := get(nodeB)
	if second == first || !strings.Contains(second, "Versión dos") {
		t.Fatal("node B kept serving the old revision")
	}
	// A stale edit from node B is rejected instead of overwriting node A's work.
	e.srv = nodeB
	if c, _ := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, map[string]any{"ops": []map[string]any{{"op": "replace", "path": "/name", "value": "B"}}, "ifVersion": 1}); c != 409 {
		t.Fatalf("stale edit across nodes: %d, want 409", c)
	}
}

func TestMasterKeyRotation(t *testing.T) {
	e := setup(t)
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S3", "qty": 1}},
		"customer": map[string]any{"email": "rot@example.com", "name": "Rot"}, "paymentMethod": "pm_card_visa"}
	_, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "rotation-1")
	mk := make([]byte, 32)
	_, _ = rand.Read(mk)
	newKR, _ := crypto.NewKeyring(hex.EncodeToString(mk))
	n, err := e.db.RotateTenantKeys(context.Background(), func(tn string, w []byte) ([]byte, error) { return crypto.Rewrap(e.keys, newKR, tn, w) })
	if err != nil || n != 2 {
		t.Fatalf("rotate n=%d err=%v", n, err)
	}
	w, _ := e.db.WrappedKey(context.Background(), "store-a")
	if err := newKR.Load("store-a", w); err != nil {
		t.Fatal(err)
	}
	if err := e.keys.Load("store-a", w); err == nil {
		t.Fatal("old master key still unwraps the rotated key")
	}
	var enc []byte
	_ = e.db.System.QueryRow(context.Background(), `SELECT customer FROM orders WHERE id=$1`, o["id"]).Scan(&enc)
	plain, err := newKR.Decrypt("store-a", enc, o["id"].(string))
	if err != nil || !strings.Contains(string(plain), "rot@example.com") {
		t.Fatalf("customer data unreadable after rotation: %v", err)
	}
}

func TestShopperStorefront(t *testing.T) {
	e := setup(t)
	if c, _ := e.do("POST", "/v1/sites/main/publish", e.adminA, nil); c != 200 {
		t.Fatalf("publish %d", c)
	}
	for _, p := range []string{"/s/store-a/main/index", "/s/store-a/main/c", "/s/store-a/main/c?q=prodcto", "/s/store-a/main/p/p1", "/s/store-a/main/cart", "/s/store-a/main/checkout", "/s/store-a/main/gracias", "/s/assets/shop.js"} {
		resp, err := http.Get(e.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
		if p == "/s/store-a/main/c?q=prodcto" && !strings.Contains(string(b), "Producto 1") {
			t.Fatal("typo search did not find products")
		}
	}
	if resp, _ := http.Get(e.srv.URL + "/s/store-a/main/p/no-existe"); resp.StatusCode != 404 {
		t.Fatalf("missing product: %d", resp.StatusCode)
	}
	if resp, _ := http.Get(e.srv.URL + "/s/store-z/main/c"); resp.StatusCode != 404 {
		t.Fatalf("unknown store: %d", resp.StatusCode)
	}
	// Public API: no key needed, same guarantees as the private checkout.
	c, sim := e.do("POST", "/s/store-a/api/simulate", "", map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 1}}})
	if c != 200 || sim["shipping"] == nil {
		t.Fatalf("public simulate %d %v", c, sim)
	}
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 1}},
		"customer": map[string]any{"email": "shop@example.com", "name": "Shop"}, "paymentMethod": "pm_card_visa"}
	c1, o1 := e.do("POST", "/s/store-a/api/checkout", "", body, "Idempotency-Key", "shop-key-0001")
	c2, o2 := e.do("POST", "/s/store-a/api/checkout", "", body, "Idempotency-Key", "shop-key-0001")
	if c1 != 201 || c2 != 201 || o1["id"] != o2["id"] {
		t.Fatalf("public checkout: %d %d %v %v", c1, c2, o1["id"], o2["id"])
	}
	if _, ok := o1["customer"]; ok {
		t.Fatal("public checkout response leaked customer data")
	}
	if c, _ := e.do("POST", "/s/store-a/api/checkout", "", body); c != 400 {
		t.Fatalf("checkout without Idempotency-Key: %d", c)
	}
	_, cfg := e.do("GET", "/s/store-a/api/config", "", nil)
	if cfg["mode"] != "sandbox" {
		t.Fatalf("config %v", cfg)
	}
}
