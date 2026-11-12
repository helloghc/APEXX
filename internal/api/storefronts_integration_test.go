package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Storefronts: a second site copies the design of the first, opens in its
// own market (dollars, English), a domain can point to a site and a market,
// the address still overrides, and nothing crosses to another store.
func TestStorefrontsAndMarketDomains(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.must(204, "PUT", "/v1/channels/us", e.adminA, m{"name": "Estados Unidos", "currency": "USD", "country": "US", "market": true, "language": "en",
		"priceFrom": "mx", "rateMicros": 58000, "rounding": "99", "taxExclusive": true, "taxBps": 800, "maxQtyPerItem": 50})
	e.must(200, "PUT", "/v1/settings/languages", e.adminA, m{"enabled": []string{"en"}})
	e.drain()
	get := func(path, host string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}

	for _, bad := range []m{{"id": "X Y", "name": "Mala"}, {"id": "outlet", "name": ""}, {"id": "outlet", "name": "Outlet", "market": "marte"}, {"id": "outlet", "name": "Outlet", "copyFrom": "nope"}, {"id": "api", "name": "Api"}} {
		if st, _ := e.do("POST", "/v1/storefronts", e.adminA, bad); st != 400 {
			t.Fatalf("accepted %v: %d", bad, st)
		}
	}
	r := e.must(201, "POST", "/v1/storefronts", e.adminA, m{"id": "usa", "name": "Tienda USA", "market": "us", "copyFrom": "main"})
	if items := r["items"].([]any); len(items) != 2 || items[1].(map[string]any)["market"] != "us" {
		t.Fatalf("storefronts = %v", items)
	}
	if st, _ := e.do("POST", "/v1/storefronts", e.adminA, m{"id": "usa", "name": "Otra"}); st != 409 {
		t.Fatalf("duplicate storefront: %d", st)
	}
	// The copy has the same design and publishes on its own.
	e.must(200, "POST", "/v1/sites/usa/publish", e.adminA, nil)
	e.drain()
	_, mainPage := get("/s/store-a/main/c", "")
	st, usPage := get("/s/store-a/usa/c", "")
	if st != 200 || !strings.Contains(usPage, "USD") || !strings.Contains(usPage, `lang="en"`) || !strings.Contains(mainPage, "MXN") {
		t.Fatalf("usa storefront = %d dollars=%v english=%v", st, strings.Contains(usPage, "USD"), strings.Contains(usPage, `lang="en"`))
	}
	// The address still chooses: ?mk=mx shows pesos on the US storefront.
	if _, p := get("/s/store-a/usa/c?mk=mx", ""); !strings.Contains(p, "MXN") {
		t.Fatal("?mk=mx did not override the storefront market")
	}
	// A domain of the main site that opens in dollars.
	d := e.must(201, "POST", "/v1/domains", e.adminA, m{"hostname": "tienda-us.example.com", "site": "main", "market": "us"})
	if d["market"] != "us" {
		t.Fatalf("domain = %v", d)
	}
	if st, _ := e.do("POST", "/v1/domains", e.adminA, m{"hostname": "x.example.com", "market": "marte"}); st != 400 {
		t.Fatalf("unknown market on a domain: %d", st)
	}
	_, _ = e.db.System.Exec(ctx, `UPDATE domains SET status='verified', verified_at=now() WHERE tenant_id='store-a'`)
	e.must(204, "PUT", "/v1/domains/"+d["id"].(string)+"/market", e.adminA, m{"market": "us"}) // reloads the host map
	e.drain()
	if st, p := get("/c", "tienda-us.example.com"); st != 200 || !strings.Contains(p, "USD") {
		t.Fatalf("market domain = %d dollars=%v", st, strings.Contains(p, "USD"))
	}
	e.must(204, "PUT", "/v1/domains/"+d["id"].(string)+"/market", e.adminA, m{"market": ""})
	e.drain()
	if _, p := get("/c", "tienda-us.example.com"); !strings.Contains(p, "MXN") {
		t.Fatal("domain kept dollars after its market was cleared")
	}
	// A storefront with domains is not deleted; the main one never; store B sees none.
	e.must(201, "POST", "/v1/domains", e.adminA, m{"hostname": "usa.example.com", "site": "usa"})
	if st, _ := e.do("DELETE", "/v1/storefronts/usa", e.adminA, nil); st != 409 {
		t.Fatalf("deleted a storefront with domains: %d", st)
	}
	if st, _ := e.do("DELETE", "/v1/storefronts/main", e.adminA, nil); st != 409 {
		t.Fatalf("deleted the main storefront: %d", st)
	}
	if items := e.must(200, "GET", "/v1/storefronts", e.adminB, nil)["items"].([]any); len(items) > 1 {
		t.Fatalf("store B sees store A's storefronts: %v", items)
	}
	if st, _ := e.do("PUT", "/v1/storefronts/usa", e.adminB, m{"name": "Robada"}); st != 404 {
		t.Fatalf("store B renamed store A's storefront: %d", st)
	}
	var dom string
	_ = e.db.System.QueryRow(ctx, `SELECT id FROM domains WHERE hostname='usa.example.com'`).Scan(&dom)
	e.must(204, "DELETE", "/v1/domains/"+dom, e.adminA, nil)
	e.must(204, "DELETE", "/v1/storefronts/usa", e.adminA, nil)
	if st, _ := get("/s/store-a/usa/c", ""); st != 404 {
		t.Fatalf("deleted storefront still serves: %d", st)
	}
}
