package api

import (
	"context"
	"testing"
)

// Point of sale: the counter's units go first, cash gives change and must
// cover the total, a terminal needs its reference, and the sale is an
// ordinary paid order handed over at once.
func TestPointOfSale(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, id := range []string{"w1", "tienda-centro"} {
		if st, b := e.do("PUT", "/v1/locations/"+id, e.adminA, m{"name": id, "kind": "store", "active": true}); st >= 300 {
			t.Fatalf("location %s = %d %v", id, st, b)
		}
	}
	_ = e.db.SetStock(ctx, "store-a", "test", "S1", "tienda-centro", 4)
	e.drain()
	if r := e.must(200, "GET", "/v1/pos/lookup?q=S1&location=tienda-centro", e.adminA, nil); r["exact"] != true || r["items"].([]any)[0].(map[string]any)["here"].(float64) != 4 {
		t.Fatalf("lookup = %v", r)
	}
	if st, _ := e.do("POST", "/v1/pos/sales", e.sfA, m{"location": "tienda-centro"}); st != 401 && st != 403 {
		t.Fatalf("storefront key sold at the counter: %d", st)
	}
	if st, b := e.do("POST", "/v1/pos/sales", e.adminA, m{"location": "tienda-centro", "items": []m{{"sku": "S1", "qty": 2}}, "payment": m{"method": "cash", "tendered": 100}}); st != 400 {
		t.Fatalf("short cash accepted: %d %v", st, b)
	}
	if st, _ := e.do("POST", "/v1/pos/sales", e.adminA, m{"location": "tienda-centro", "items": []m{{"sku": "S1", "qty": 1}}, "payment": m{"method": "terminal"}}); st != 400 {
		t.Fatalf("terminal without reference accepted: %d", st)
	}
	r := e.must(201, "POST", "/v1/pos/sales", e.adminA, m{"location": "tienda-centro", "items": []m{{"sku": "S1", "qty": 2}}, "payment": m{"method": "cash", "tendered": 50000}})
	o := r["order"].(map[string]any)
	if r["change"].(float64) != 50000-o["grandTotal"].(float64) || o["fulfillmentStatus"] != "fulfilled" || o["paymentStatus"] != "paid" || o["source"] != "pos" {
		t.Fatalf("sale = %v (change %v)", o, r["change"])
	}
	// The two units came from the counter's location, not from the warehouse.
	var centro, w1 int64
	_ = e.db.System.QueryRow(ctx, `SELECT on_hand FROM stock_levels WHERE tenant_id='store-a' AND sku='S1' AND location_id='tienda-centro'`).Scan(&centro)
	_ = e.db.System.QueryRow(ctx, `SELECT on_hand FROM stock_levels WHERE tenant_id='store-a' AND sku='S1' AND location_id='w1'`).Scan(&w1)
	if centro != 2 || w1 != 10 {
		t.Fatalf("stock after counter sale: centro=%d w1=%d", centro, w1)
	}
	// The short-cash attempt left no open draft behind.
	var open int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM draft_orders WHERE tenant_id='store-a' AND status IN ('open','sent')`).Scan(&open)
	if open != 0 {
		t.Fatalf("drafts left open: %d", open)
	}
	if st, _ := e.do("POST", "/v1/pos/sales", e.adminB, m{"location": "tienda-centro", "items": []m{{"sku": "S1", "qty": 1}}, "payment": m{"method": "cash", "tendered": 100000}}); st != 400 {
		t.Fatalf("store B sold from store A's location: %d", st)
	}
}
