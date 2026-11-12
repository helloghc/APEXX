package api

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Recommendations and learning search: products bought or viewed together
// show next to each other, a cancelled order stops counting, the merchant can
// exclude products or turn it off, and search results rise with what shoppers
// choose, always below the merchant's pins. Nothing crosses stores.
func TestRecommendationsAndLearningSearch(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	page := func(handle string) string {
		st, b, _ := e.raw("GET", "/s/store-a/main/p/"+handle, "", nil, "")
		if st != 200 {
			t.Fatalf("page %s = %d", handle, st)
		}
		return string(b)
	}
	learn := func() {
		if _, err := e.server.RunLearning(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(page("p1"), "data-also-bought") {
		t.Fatal("recommendations without data")
	}
	var ids []string
	for i := 0; i < 2; i++ {
		_, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}, {"sku": "S2", "qty": 1}},
			"customer": m{"email": fmt.Sprintf("c%d@example.com", i), "name": "C"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("recs-order-%d", i))
		if o["status"] != "paid" {
			t.Fatalf("order = %v", o)
		}
		ids = append(ids, o["id"].(string))
	}
	learn()
	learn() // the second round sees the orders changed since the first
	if p := page("p1"); !strings.Contains(p, "data-also-bought") || !strings.Contains(p, "/p/p2") {
		t.Fatal("bought together not recommended")
	}
	r := e.must(200, "GET", "/v1/products/P1/recommendations", e.adminA, nil)["items"].([]any)
	if len(r) != 1 || r[0].(map[string]any)["id"] != "P2" || r[0].(map[string]any)["buys"].(float64) != 2 {
		t.Fatalf("explained = %v", r)
	}
	// Seen together in one visit.
	for i := 0; i < 3; i++ {
		if st, _ := e.do("POST", "/s/store-a/api/views", "", m{"p": "p1", "seen": []string{"p3", "p404"}}); st != 204 {
			t.Fatalf("views = %d", st)
		}
	}
	e.do("POST", "/s/store-b/api/views", "", m{"p": "p1", "seen": []string{"p3"}}) // another store's handles
	learn()
	r = e.must(200, "GET", "/v1/products/P1/recommendations", e.adminA, nil)["items"].([]any)
	if len(r) != 2 || r[1].(map[string]any)["id"] != "P3" || r[1].(map[string]any)["views"].(float64) != 3 {
		t.Fatalf("with views = %v", r)
	}
	if _, c := e.do("GET", "/s/store-a/api/recommendations?sku=S1,S2", "", nil); len(c["items"].([]any)) != 1 || c["items"].([]any)[0].(map[string]any)["handle"] != "p3" {
		t.Fatalf("cart = %v", c)
	}
	// Cancelled orders stop counting.
	for _, id := range ids {
		e.must(200, "POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "prueba"})
	}
	learn()
	r = e.must(200, "GET", "/v1/products/P1/recommendations", e.adminA, nil)["items"].([]any)
	if len(r) != 1 || r[0].(map[string]any)["id"] != "P3" {
		t.Fatalf("after cancel = %v", r)
	}
	// The merchant excludes a product, or turns recommendations off.
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, m{"exclude": []string{"P3"}})
	if strings.Contains(page("p1"), "data-also-bought") {
		t.Fatal("excluded product recommended")
	}
	if st, _ := e.do("PUT", "/v1/ai/settings", e.adminA, m{"exclude": []string{"NOPE"}}); st != 400 {
		t.Fatalf("unknown product excluded: %d", st)
	}
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, m{"recsOff": true})
	if strings.Contains(page("p1"), "data-also-bought") {
		t.Fatal("recommendations while off")
	}
	if g := e.must(200, "GET", "/v1/ai/settings", e.adminB, nil); g["settings"].(map[string]any)["recsOff"] != nil {
		t.Fatalf("store B sees A's settings: %v", g)
	}

	// Learning search.
	first := func() string {
		_, s := e.do("GET", "/s/store-a/api/suggest?q=producto", "", nil)
		return s["products"].([]any)[0].(map[string]any)["handle"].(string)
	}
	if f := first(); f != "p1" {
		t.Fatalf("first before learning = %s", f)
	}
	for i := 0; i < 4; i++ {
		e.do("POST", "/s/store-a/api/search-click", "", m{"q": "Producto", "p": "p3", "kind": "click"})
	}
	e.do("POST", "/s/store-a/api/search-click", "", m{"q": "producto", "p": "p3", "kind": "cart"})
	learn()
	if f := first(); f != "p3" {
		t.Fatalf("first after shoppers chose p3 = %s", f)
	}
	l := e.must(200, "GET", "/v1/search/learned?q=PRODUCTO", e.adminA, nil)["items"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["clicks"].(float64) != 4 || l[0].(map[string]any)["carts"].(float64) != 1 || l[0].(map[string]any)["boost"].(float64) <= 0 {
		t.Fatalf("learned = %v", l)
	}
	// Pins still win; turning learning off restores the order.
	cfg := e.must(200, "GET", "/v1/search/config", e.adminA, nil)["config"].(map[string]any)
	cfg["pins"] = []m{{"term": "producto", "productIds": []string{"P2"}}}
	e.must(200, "PUT", "/v1/search/config", e.adminA, cfg)
	e.drain()
	if f := first(); f != "p2" {
		t.Fatalf("pin lost to learning: %s", f)
	}
	cfg["pins"], cfg["noLearning"] = []m{}, true
	e.must(200, "PUT", "/v1/search/config", e.adminA, cfg)
	e.drain()
	if f := first(); f != "p1" {
		t.Fatalf("learning off still reorders: %s", f)
	}
	var leaked int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM product_coviews WHERE tenant_id='store-b'`).Scan(&leaked)
	if leaked != 0 {
		t.Fatalf("store B got %d pairs from A's handles", leaked)
	}
}
