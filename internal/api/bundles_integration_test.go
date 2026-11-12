package api

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// Kits: stock comes from the components; a sale, a cancellation, an edit and
// a return move the components; concurrent checkouts never oversell; another
// store cannot touch them.
func TestBundlesStockFromComponents(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.db.UpsertProduct(ctx, "store-a", "test", catalog.Product{ID: "K", Handle: "kit", Title: "Kit de regalo",
		Variants: []catalog.Variant{{SKU: "K1", Title: "Única", WeightGrams: 1500}}}, 0); err != nil {
		t.Fatal(err)
	}
	_ = e.db.SetPrices(ctx, "store-a", "test", "mx", "K1", []pricing.Tier{{MinQty: 1, Unit: money.New(25000, "MXN")}})
	e.drain()

	// Validation.
	for _, bad := range []m{
		{"components": []m{{"sku": "K1", "qty": 1}}},
		{"components": []m{{"sku": "NOPE", "qty": 1}}},
		{"components": []m{{"sku": "S1", "qty": 0}}},
		{"components": []m{{"sku": "S1", "qty": 1}, {"sku": "S1", "qty": 1}}},
	} {
		if st, b := e.do("PUT", "/v1/bundles/K1", e.adminA, bad); st != 400 {
			t.Fatalf("invalid kit %v accepted: %d %v", bad, st, b)
		}
	}
	if st, _ := e.do("PUT", "/v1/bundles/K1", e.sfA, m{"components": []m{{"sku": "S1", "qty": 1}}}); st != 401 && st != 403 {
		t.Fatalf("storefront key edited a kit: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/bundles/K1", e.adminB, m{"components": []m{{"sku": "S1", "qty": 1}}}); st != 404 {
		t.Fatalf("store B edited a kit of store A: %d", st)
	}

	// 2 × S1 + 1 × S2, with 10 of each: 5 kits.
	e.must(200, "PUT", "/v1/bundles/K1", e.adminA, m{"components": []m{{"sku": "S1", "qty": 2}, {"sku": "S2", "qty": 1}}})
	if e.available("K1") != 5 {
		t.Fatalf("kit available = %v, want 5", e.available("K1"))
	}
	if b := e.must(200, "GET", "/v1/bundles/K1", e.adminA, nil); b["available"].(float64) != 5 || len(b["components"].([]any)) != 2 {
		t.Fatalf("kit view = %v", b)
	}
	if b := e.must(200, "GET", "/v1/bundles", e.adminB, nil); len(b["items"].([]any)) != 0 {
		t.Fatalf("store B sees store A's kits: %v", b)
	}
	// A kit inside a kit, and a component turned into a kit, are refused.
	if st, _ := e.do("PUT", "/v1/bundles/S3", e.adminA, m{"components": []m{{"sku": "K1", "qty": 1}}}); st != 400 {
		t.Fatalf("nested kit accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/bundles/S1", e.adminA, m{"components": []m{{"sku": "S3", "qty": 1}}}); st != 400 {
		t.Fatalf("a component became a kit: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/inventory/K1/w1", e.adminA, m{"onHand": 50}); st != 409 {
		t.Fatalf("stock set directly on a kit: %d", st)
	}
	// The storefront sees it as available and the cart prices it.
	sim := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "K1", "qty": 2}}})
	if sim["totals"].(map[string]any)["items"].(map[string]any)["amount"].(float64) != 50000 {
		t.Fatalf("kit cart = %v", sim["totals"])
	}

	n := 0
	buy := func(qty int) (int, m) {
		n++
		return e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "K1", "qty": qty}},
			"customer": m{"email": "kits@cliente.example", "name": "Kits"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("kit-order-%d", n))
	}
	st, o := buy(2)
	if st != 201 {
		t.Fatalf("kit checkout = %d %v", st, o)
	}
	if e.available("S1") != 6 || e.available("S2") != 8 || e.available("K1") != 3 {
		t.Fatalf("after selling 2 kits: S1=%v S2=%v K1=%v", e.available("S1"), e.available("S2"), e.available("K1"))
	}
	if st, b := buy(4); st == 201 {
		t.Fatalf("sold 4 kits with stock for 3: %v", b)
	}
	if e.available("S1") != 6 {
		t.Fatalf("a refused kit order kept stock: S1=%v", e.available("S1"))
	}
	// Changing the kit later does not change what this order gives back.
	e.must(200, "PUT", "/v1/bundles/K1", e.adminA, m{"components": []m{{"sku": "S1", "qty": 1}}})
	e.drain()
	// Edit: remove one kit -> 2 × S1 and 1 × S2 come back.
	id := o["id"].(string)
	e.must(200, "POST", "/v1/orders/"+id+"/edit", e.adminA, m{"remove": []m{{"line": 1, "qty": 1}}, "reason": "ya no lo quiere"})
	if e.available("S1") != 8 || e.available("S2") != 9 {
		t.Fatalf("after removing 1 kit: S1=%v S2=%v", e.available("S1"), e.available("S2"))
	}
	// Ship the other one and take it back in good condition.
	e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"items": []m{{"line": 1, "qty": 1}}, "carrier": "DHL", "trackingNumber": "K1", "notify": false})
	r := e.must(201, "POST", "/v1/orders/"+id+"/returns", e.adminA, m{"items": []m{{"line": 1, "qty": 1, "reason": "changed_mind"}}})
	e.must(200, "POST", "/v1/returns/"+r["id"].(string)+"/receive", e.adminA, m{"items": []m{{"line": 1, "restock": true}}})
	if e.available("S1") != 10 || e.available("S2") != 10 {
		t.Fatalf("after the return: S1=%v S2=%v", e.available("S1"), e.available("S2"))
	}
	// Cancelling an order of the new composition puts back only S1.
	st, o2 := buy(3)
	if st != 201 || e.available("S1") != 7 || e.available("S2") != 10 {
		t.Fatalf("new composition: %d S1=%v S2=%v", st, e.available("S1"), e.available("S2"))
	}
	e.must(200, "POST", "/v1/orders/"+o2["id"].(string)+"/cancel", e.adminA, m{"reason": "prueba"})
	if e.available("S1") != 10 {
		t.Fatalf("after cancel: S1=%v", e.available("S1"))
	}

	// Concurrency: stock for 3 kits, 8 buyers at once -> exactly 3 orders.
	_ = e.db.SetStock(ctx, "store-a", "test", "S1", "w1", 3)
	e.drain()
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, _ := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "K1", "qty": 1}},
				"customer": m{"email": "kits@cliente.example", "name": "Kits"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("kit-race-%d", i))
			if st == 201 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 3 || e.available("S1") != 0 || e.available("K1") != 0 {
		t.Fatalf("race: %d orders, S1=%v K1=%v", ok, e.available("S1"), e.available("K1"))
	}

	// Removing the kit turns it back into a normal SKU.
	e.must(204, "DELETE", "/v1/bundles/K1", e.adminA, nil)
	if b := e.must(200, "GET", "/v1/bundles", e.adminA, nil); len(b["items"].([]any)) != 0 {
		t.Fatalf("kit not removed: %v", b)
	}
}
