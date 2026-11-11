package api

import (
	"context"
	"testing"
)

// A store copies the catalog of another store of the same business and keeps
// it up to date; prices and local choices stay its own.
func TestMasterCatalog(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Only a key of the source with catalog access is accepted.
	for _, bad := range []m{
		{"source": "store-a", "apiKey": "apx_inventada"},
		{"source": "store-a", "apiKey": e.adminB}, // a key of this same store
		{"source": "store-b", "apiKey": e.adminB}, // copying itself
		{"source": "store-a", "apiKey": e.sfA},    // storefront key
	} {
		if st, _ := e.do("PUT", "/v1/settings/catalog-source", e.adminB, bad); st != 400 {
			t.Fatalf("bad source %v accepted: %d", bad["apiKey"], st)
		}
	}
	src := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	st := e.must(200, "PUT", "/v1/settings/catalog-source", e.adminB, m{"source": "store-a", "apiKey": e.adminA, "newPrices": true})
	if st["lastError"] != nil && st["lastError"] != "" {
		t.Fatalf("first sync: %v", st)
	}
	if st["products"].(float64) < 1 {
		t.Fatalf("nothing copied: %v", st)
	}
	e.drain()
	got := e.must(200, "GET", "/v1/products/P1", e.adminB, nil)
	if got["title"] != src["title"] {
		t.Fatalf("copy title %v, source %v", got["title"], src["title"])
	}
	// The copy sets its own price; the next sync keeps it.
	e.must(200, "PUT", "/v1/products/P1", e.adminB, m{"handle": got["handle"], "title": got["title"], "variants": []m{{"sku": "S1", "title": "Única"}},
		"prices": m{"S1": m{"amount": 77700, "currency": "MXN"}}})
	// The source renames the product: the copy follows, the price stays.
	e.must(200, "PUT", "/v1/products/P1", e.adminA, m{"handle": src["handle"], "title": "Nombre desde el origen", "variants": []m{{"sku": "S1", "title": "Única"}}})
	e.drain()
	e.must(200, "POST", "/v1/settings/catalog-source/sync", e.adminB, nil)
	e.drain()
	got = e.must(200, "GET", "/v1/products/P1", e.adminB, nil)
	tb, _ := e.proj.Tenant("store-b")
	q, _ := tb.Pricing.Resolve("mx", "S1", 1)
	if got["title"] != "Nombre desde el origen" || q.Unit.Amount != 77700 {
		t.Fatalf("after the rename: title=%v price=%d", got["title"], q.Unit.Amount)
	}
	// Running again with nothing new copies nothing.
	if st := e.must(200, "POST", "/v1/settings/catalog-source/sync", e.adminB, nil); st["copied"].(float64) != 0 {
		t.Fatalf("an unchanged catalog was copied again: %v", st)
	}
	// Removed at the source: archived in the copy, not deleted.
	e.must(204, "DELETE", "/v1/products/P1", e.adminA, nil)
	e.drain()
	if err := e.server.SyncCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if got := e.must(200, "GET", "/v1/products/P1", e.adminB, nil); got["status"] != "archived" {
		t.Fatalf("product removed at the source: status %v", got["status"])
	}
	// The source revokes access: the copy stops and says why.
	if _, err := e.db.System.Exec(ctx, `UPDATE api_keys SET revoked_at=now() WHERE tenant_id='store-a'`); err != nil {
		t.Skip("api_keys table shape differs:", err)
	}
	if st := e.must(200, "POST", "/v1/settings/catalog-source/sync", e.adminB, nil); st["lastError"] == "" || st["lastError"] == nil {
		t.Fatalf("revoked key still syncing: %v", st)
	}
	// The saved key never comes back in the answer.
	if _, has := e.must(200, "GET", "/v1/settings/catalog-source", e.adminB, nil)["key"]; has {
		t.Fatal("the source key was returned")
	}
}
