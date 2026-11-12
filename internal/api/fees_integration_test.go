package api

import (
	"context"
	"testing"
)

// Extra charges per order and import duties: configured, shown in the cart,
// kept on the order, and given back when the order is cancelled.
func TestOrderFeesAndDuties(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("PUT", "/v1/settings/fees", e.adminA, m{"items": []m{{"name": "Raro", "kind": "magic", "active": true}}}); st != 400 {
		t.Fatalf("invalid fee accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/fees", e.sfA, m{"items": []m{}}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed fees: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/fees", e.adminA, m{"items": []m{{"id": "manejo", "name": "Manejo", "kind": "fixed", "amount": 2500, "active": true}}})
	e.drain()
	sim := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}})
	tot := sim["totals"].(map[string]any)
	if tot["fees"].(map[string]any)["amount"].(float64) != 2500 || len(sim["fees"].([]any)) != 1 {
		t.Fatalf("cart fees = %v %v", tot["fees"], sim["fees"])
	}
	add := func(k string) float64 { return tot[k].(map[string]any)["amount"].(float64) }
	if add("grand") != add("items")-add("discounts")+add("shipping")+add("taxAdjust")+add("fees") {
		t.Fatalf("totals do not add up: %v", tot)
	}
	// Another store does not get them.
	other := e.must(200, "POST", "/s/store-b/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}})
	if other["fees"] != nil {
		t.Fatalf("store B charged store A's fee: %v", other["fees"])
	}
	// The order keeps them; cancelling gives everything back.
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "cargos@cliente.example", "name": "Cargos"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "fees-key-1")
	if st != 201 || o["fees"].(float64) != 2500 || len(o["feeLines"].([]any)) != 1 || o["grandTotal"].(float64) != add("grand") {
		t.Fatalf("order = %d fees=%v grand=%v want %v %v", st, o["fees"], o["grandTotal"], add("grand"), o)
	}
	id := o["id"].(string)
	e.must(200, "POST", "/v1/orders/"+id+"/cancel", e.adminA, m{"reason": "prueba"})
	got := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if got["refundedTotal"].(float64) != o["grandTotal"].(float64) {
		t.Fatalf("cancel refunded %v of %v", got["refundedTotal"], o["grandTotal"])
	}
	// Turning the fee off removes it at once.
	e.must(200, "PUT", "/v1/settings/fees", e.adminA, m{"items": []m{{"id": "manejo", "name": "Manejo", "kind": "fixed", "amount": 2500, "active": false}}})
	e.drain()
	if s2 := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}}); s2["fees"] != nil {
		t.Fatalf("an inactive fee was charged: %v", s2["fees"])
	}

	// Duties: a US market charges 10% on products made in China, 0 on Mexican ones.
	e.must(200, "PUT", "/v1/product-classes/P1", e.adminA, m{"hsCode": "6109.10", "origin": "cn"})
	if st, _ := e.do("PUT", "/v1/product-classes/P1", e.adminA, m{"hsCode": "12"}); st != 400 {
		t.Fatalf("bad tariff code accepted: %d", st)
	}
	// Saving only the tax class keeps the customs data.
	e.must(200, "PUT", "/v1/product-classes/P1", e.adminA, m{"taxClass": "general"})
	cls := e.must(200, "GET", "/v1/product-classes", e.adminA, nil)
	found := false
	for _, x := range cls["items"].([]any) {
		c := x.(map[string]any)
		found = found || (c["productId"] == "P1" && c["hsCode"] == "6109.10" && c["origin"] == "CN")
	}
	if !found {
		t.Fatalf("customs data lost: %v", cls)
	}
	if st, _ := e.do("PUT", "/v1/channels/us", e.adminA, m{"currency": "USD", "market": true, "country": "US", "priceFrom": "mx", "rateMicros": 58000, "dutyBps": 20000}); st != 400 {
		t.Fatalf("duty over 100%% accepted: %d", st)
	}
	e.must(204, "PUT", "/v1/channels/us", e.adminA, m{"name": "Estados Unidos", "currency": "USD", "country": "US", "market": true, "priceFrom": "mx", "rateMicros": 58000,
		"taxExclusive": true, "taxBps": 800, "dutyBps": 1000, "dutyRules": []m{{"hsPrefix": "6109", "bps": 1500}}})
	e.drain()
	us := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "us", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}}})
	fees := us["fees"].([]any)
	if len(fees) != 1 || fees[0].(map[string]any)["kind"] != "duty" || fees[0].(map[string]any)["amount"].(float64) <= 0 {
		t.Fatalf("US duties = %v", us["fees"])
	}
	// The rate of the tariff code wins: 15% of the items after discounts.
	ut := us["totals"].(map[string]any)
	items := ut["items"].(map[string]any)["amount"].(float64) - ut["discounts"].(map[string]any)["amount"].(float64)
	if d := fees[0].(map[string]any)["amount"].(float64); d != float64(int64(items*1500+5000)/10000) {
		t.Fatalf("duty %v for items %v", d, items)
	}
	// A fixed fee is written in pesos: in the dollar market it is converted, not charged as dollars.
	e.must(200, "PUT", "/v1/settings/fees", e.adminA, m{"items": []m{{"id": "empaque", "name": "Empaque", "kind": "fixed", "amount": 3500, "active": true}}})
	e.drain()
	us = e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "us", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}}})
	for _, x := range us["fees"].([]any) {
		f := x.(map[string]any)
		if f["kind"] == "fee" && f["amount"].(float64) != 203 { // 35 MXN x 0.058 = 2.03 USD
			t.Fatalf("fee in the dollar market = %v cents, want 203", f["amount"])
		}
	}
	var unbalanced int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("unbalanced ledger: %d", unbalanced)
	}
}
