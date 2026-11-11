package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/reports"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

// beacon posts a raw analytics beacon like navigator.sendBeacon does (text/plain).
func (e *env) beacon(tenant, body string, hdr ...string) (int, string) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.srv.URL+"/s/"+tenant+"/api/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("User-Agent", browserUA)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (e *env) eventCount(tenant string) int {
	var n int
	if err := e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM analytics_events WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestBeaconEndpoint(t *testing.T) {
	e := setup(t)
	ok := `{"c":"mx","r":"www.google.com","e":[{"t":"pv","p":"/s/store-a/main/p/p1"},{"t":"ac","s":"S1","q":1}]}`
	if c, body := e.beacon("store-a", ok); c != 204 || body != "" {
		t.Fatalf("valid beacon: %d %q", c, body)
	}
	if n := e.eventCount("store-a"); n != 2 {
		t.Fatalf("stored %d events, want 2", n)
	}
	var typ, product string
	_ = e.db.System.QueryRow(context.Background(), `SELECT type, product_id FROM analytics_events WHERE tenant_id='store-a' AND sku=''`).Scan(&typ, &product)
	if typ != "product_view" || product != "P1" {
		t.Errorf("a product page view must resolve the product from the catalog: %s %s", typ, product)
	}
	// Validation: problem+json with stable codes; nothing stored.
	for name, body := range map[string]string{
		"unknown field":   `{"e":[{"t":"pv","p":"/x"}],"email":"ana@example.com"}`,
		"bad type":        `{"e":[{"t":"login","p":"/x"}]}`,
		"unknown channel": `{"c":"us","e":[{"t":"pv","p":"/x"}]}`,
		"empty":           `{}`,
		"garbage":         `not json`,
	} {
		c, raw := e.beacon("store-a", body)
		if c != 400 || !strings.Contains(raw, `"code"`) {
			t.Errorf("%s: %d %s", name, c, raw)
		}
	}
	if c, _ := e.beacon("store-a", `{"e":[{"t":"pv","p":"/x"}],"r":"`+strings.Repeat("a", 9000)+`"}`); c != 400 {
		t.Errorf("oversized body: %d", c)
	}
	if c, _ := e.beacon("no-such-store", ok); c != 404 {
		t.Errorf("unknown store: %d", c)
	}
	// Privacy signals, bots and cross-site callers: accepted or refused, never stored.
	for name, h := range map[string][]string{
		"DNT": {"DNT", "1"}, "GPC": {"Sec-GPC", "1"}, "bot": {"User-Agent", "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)"}, "curl": {"User-Agent", "curl/8.5.0"},
	} {
		if c, _ := e.beacon("store-a", ok, h...); c != 204 {
			t.Errorf("%s: %d, want 204", name, c)
		}
	}
	if c, _ := e.beacon("store-a", ok, "Sec-Fetch-Site", "cross-site"); c != 403 {
		t.Errorf("cross-site beacon: %d, want 403", c)
	}
	if n := e.eventCount("store-a"); n != 2 {
		t.Fatalf("dropped or invalid beacons stored events: %d", n)
	}
	if n := e.eventCount("store-b"); n != 0 {
		t.Fatalf("store-b got %d events", n)
	}
	// Rate limit: 120 beacons per minute per store and address.
	limited := 0
	for i := 0; i < 130; i++ {
		if c, _ := e.beacon("store-a", `{"e":[{"t":"pv","p":"/s/store-a/main/c"}]}`); c == 429 {
			limited++
		}
	}
	if limited < 10 || limited > 30 {
		t.Errorf("rate limit let through the wrong amount: %d of 130 limited", limited)
	}
	// The limit is per store: the same address can still reach store-b.
	if c, _ := e.beacon("store-b", `{"e":[{"t":"pv","p":"/s/store-b/main/c"}]}`); c != 204 {
		t.Errorf("store-b after store-a was limited: %d", c)
	}
	// No raw address or user agent in the table.
	var dump string
	_ = e.db.System.QueryRow(context.Background(), `SELECT string_agg(row_to_json(x)::text, ' ') FROM analytics_events x`).Scan(&dump)
	if strings.Contains(dump, "127.0.0.1") || strings.Contains(dump, "Chrome") || strings.Contains(dump, "Mozilla") {
		t.Error("raw IP or user agent stored")
	}
}

func TestReportsAPIEndToEnd(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rep := &reports.Service{DB: e.db, Keys: e.keys}
	body := map[string]any{"channel": "mx", "postalCode": "53129", "items": []map[string]any{{"sku": "S1", "qty": 2}},
		"customer": map[string]any{"email": "ana@example.com", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
	c, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "reports-key-1")
	if c != 201 {
		t.Fatalf("checkout %d %v", c, o)
	}
	id := o["id"].(string)
	total := o["grandTotal"].(float64)
	if c, raw := e.beacon("store-a", `{"c":"mx","e":[{"t":"pv","p":"/s/store-a/main/p/p1"},{"t":"ac","s":"S1","q":2},{"t":"pv","p":"/s/store-a/main/checkout"},{"t":"pu","o":"`+id+`","v":1}]}`); c != 204 {
		t.Fatalf("beacon %d %s", c, raw)
	}
	if n, err := rep.SyncOrders(ctx); err != nil || n != 1 {
		t.Fatalf("sync: %d %v", n, err)
	}
	if _, err := rep.RollupTraffic(ctx); err != nil {
		t.Fatal(err)
	}
	c, d := e.do("GET", "/v1/reports/dashboard?range=today", e.adminA, nil)
	if c != 200 {
		t.Fatalf("dashboard %d %v", c, d)
	}
	tot := d["totals"].(map[string]any)
	if tot["orders"].(float64) != 1 || tot["net"].(float64) != total || tot["units"].(float64) != 2 || tot["newCustomers"].(float64) != 1 {
		t.Errorf("dashboard totals after a real checkout: %v (order total %v)", tot, total)
	}
	tr := d["traffic"].(map[string]any)
	if tr["sessions"].(float64) != 1 || tr["purchase"].(float64) != 1 || tr["addToCart"].(float64) != 1 {
		t.Errorf("dashboard funnel: %v", tr)
	}
	if top := d["topProducts"].([]any); len(top) != 1 || top[0].(map[string]any)["key"] != "P1" {
		t.Errorf("top products: %v", d["topProducts"])
	}
	if ro := d["recentOrders"].([]any); len(ro) != 1 || strings.Contains(strings.ToLower(toJSON(ro)), "ana@") {
		t.Errorf("recent orders must not carry personal data: %v", ro)
	}
	if d["range"].(map[string]any)["timezone"] != "America/Mexico_City" || len(d["series"].([]any)) != 24 {
		t.Errorf("today is 24 hourly buckets in Mexico City time: %v, %d buckets", d["range"], len(d["series"].([]any)))
	}

	// Cancelling refunds the order; the refund shows up after the next sync.
	if c, m := e.do("POST", "/v1/orders/"+id+"/cancel", e.adminA, map[string]any{"reason": "prueba"}); c != 200 {
		t.Fatalf("cancel %d %v", c, m)
	}
	if n, err := rep.SyncOrders(ctx); err != nil || n != 1 {
		t.Fatalf("sync after refund: %d %v", n, err)
	}
	_, s := e.do("GET", "/v1/reports/sales?range=7d", e.adminA, nil)
	st := s["totals"].(map[string]any)
	if st["refunds"].(float64) != total || st["net"].(float64) != 0 || st["orders"].(float64) != 1 || st["refundCount"].(float64) != 1 {
		t.Errorf("sales after the refund: %v", st)
	}

	// Every report answers JSON and CSV.
	for _, p := range []string{"sales", "products", "products?by=brand", "customers", "funnel", "promotions", "inventory", "inventory?state=low&threshold=20"} {
		sep := "?"
		if strings.Contains(p, "?") {
			sep = "&"
		}
		if c, m := e.do("GET", "/v1/reports/"+p, e.adminA, nil); c != 200 {
			t.Errorf("GET %s: %d %v", p, c, m)
		}
		req, _ := http.NewRequest("GET", e.srv.URL+"/v1/reports/"+p+sep+"format=csv", nil)
		req.Header.Set("Authorization", "Bearer "+e.adminA)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") ||
			!strings.HasPrefix(string(raw), "\xEF\xBB\xBF") || strings.Count(string(raw), "\r\n") < 1 {
			t.Errorf("CSV %s: %d %s %q", p, resp.StatusCode, resp.Header.Get("Content-Type"), string(raw[:min(80, len(raw))]))
		}
	}
	// Input validation.
	for _, p := range []string{"sales?range=siempre", "sales?range=custom&from=2026-02-01&to=2026-01-01", "sales?grain=minute", "inventory?state=x", "inventory?threshold=-1"} {
		if c, m := e.do("GET", "/v1/reports/"+p, e.adminA, nil); c != 400 || m["code"] != "validation_failed" {
			t.Errorf("GET %s: %d %v", p, c, m)
		}
	}

	// Permissions: the storefront key cannot read reports; another store sees only its own (empty) data.
	if c, _ := e.do("GET", "/v1/reports/dashboard", e.sfA, nil); c != 403 {
		t.Errorf("storefront key read the dashboard: %d", c)
	}
	if c, _ := e.do("GET", "/v1/reports/dashboard", "", nil); c != 401 {
		t.Errorf("anonymous dashboard: %d", c)
	}
	_, db := e.do("GET", "/v1/reports/dashboard?range=7d", e.adminB, nil)
	if db["totals"].(map[string]any)["orders"].(float64) != 0 || len(db["recentOrders"].([]any)) != 0 {
		t.Errorf("store-b dashboard shows data of store-a: %v", db["totals"])
	}
	// A member with reports:read sees reports but not operations; without it, nothing.
	c, m := e.do("POST", "/v1/team/members", e.adminA, map[string]any{"email": "analista@example.com", "name": "Analista", "password": "analista-password-1",
		"permissions": map[string]string{"reports": "read"}})
	if c != 201 {
		t.Fatalf("create member %d %v", c, m)
	}
	c, l := e.do("POST", "/v1/auth/login", "", map[string]any{"store": "store-a", "email": "analista@example.com", "password": "analista-password-1"})
	if c != 200 {
		t.Fatalf("login %d %v", c, l)
	}
	tok := l["token"].(string)
	if c, _ := e.do("GET", "/v1/reports/sales", tok, nil); c != 200 {
		t.Errorf("reports reader: %d", c)
	}
	for _, p := range []string{"/v1/ops/audit", "/v1/ops/status", "/v1/ops/audit/facets"} {
		if c, _ := e.do("GET", p, tok, nil); c != 403 {
			t.Errorf("reports reader reached %s: %d", p, c)
		}
	}
	if c, _ := e.do("POST", "/v1/reports/rebuild", tok, nil); c != 403 {
		t.Errorf("reports reader rebuilt analytics: %d", c)
	}

	// Operations (settings admin): activity with filters, facets, status, rebuild.
	c, a := e.do("GET", "/v1/ops/audit?action=order&limit=1", e.adminA, nil)
	items, _ := a["items"].([]any)
	if c != 200 || len(items) != 1 || a["nextCursor"] == nil {
		t.Fatalf("audit: %d %v", c, a)
	}
	first := items[0].(map[string]any)
	if first["action"] != "order.refunded" || first["target"] != id || first["hash"] == "" {
		t.Errorf("newest activity first: %v", first)
	}
	c, a2 := e.do("GET", "/v1/ops/audit?action=order&limit=1&cursor="+a["nextCursor"].(string), e.adminA, nil)
	if c != 200 || len(a2["items"].([]any)) == 0 || a2["items"].([]any)[0].(map[string]any)["seq"].(float64) >= items[0].(map[string]any)["seq"].(float64) {
		t.Errorf("audit second page: %d %v", c, a2)
	}
	if c, m := e.do("GET", "/v1/ops/audit?cursor=zzz", e.adminA, nil); c != 400 {
		t.Errorf("bad cursor: %d %v", c, m)
	}
	if c, m := e.do("GET", "/v1/ops/audit?target="+id+"&from=2020-01-01&to=2099-01-01", e.adminA, nil); c != 200 || len(m["items"].([]any)) < 3 {
		t.Errorf("audit by target: %d %v", c, m)
	}
	if c, m := e.do("GET", "/v1/ops/audit/facets", e.adminA, nil); c != 200 || len(m["actions"].([]any)) == 0 {
		t.Errorf("facets: %d %v", c, m)
	}
	c, sm := e.do("GET", "/v1/ops/status", e.adminA, nil)
	if c != 200 || sm["status"] == "down" || sm["health"].(map[string]any)["db"].(map[string]any)["ok"] != true || sm["http"].(map[string]any)["requests"].(float64) < 10 {
		t.Errorf("status: %d %v", c, sm)
	}
	if c, m := e.do("POST", "/v1/reports/rebuild", e.adminA, nil); c != 200 || m["orders"].(float64) != 1 {
		t.Errorf("rebuild: %d %v", c, m)
	}
	_, s2 := e.do("GET", "/v1/reports/sales?range=7d", e.adminA, nil)
	if toJSON(s2["totals"]) != toJSON(s["totals"]) {
		t.Errorf("rebuild changed the totals:\n%v\n%v", s["totals"], s2["totals"])
	}
	// /metrics exposes worker health for monitoring.
	resp, err := http.Get(e.srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(raw), "apexx_worker_runs_total") {
		t.Error("/metrics lacks worker metrics")
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
