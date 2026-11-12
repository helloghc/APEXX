package api

import (
	"net/http"
	"strings"
	"testing"
)

// Markets: a second country with its own currency, prices converted from the
// main channel, tax added on top, and the order reversed in that currency.
func TestMarkets(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	// Validation: a market needs a source channel that exists and a rate.
	for _, bad := range []m{
		{"currency": "USD", "market": true, "country": "US", "priceFrom": "nope", "rateMicros": 58000},
		{"currency": "USD", "market": true, "country": "US", "priceFrom": "mx"},
		{"currency": "USD", "market": true, "priceFrom": "mx", "rateMicros": 58000},
		{"currency": "USD", "market": true, "country": "US", "priceFrom": "mx", "rateMicros": 58000, "rounding": "raro"},
		{"currency": "USD", "market": true, "country": "US", "priceFrom": "us", "rateMicros": 58000},
	} {
		if st, _ := e.do("PUT", "/v1/channels/us", e.adminA, bad); st != 400 {
			t.Fatalf("invalid market %v accepted: %d", bad, st)
		}
	}
	e.must(204, "PUT", "/v1/channels/us", e.adminA, m{"name": "Estados Unidos", "currency": "USD", "country": "US", "market": true, "language": "en",
		"priceFrom": "mx", "rateMicros": 58000, "rounding": "99", "taxExclusive": true, "taxBps": 800, "maxQtyPerItem": 50})
	// A private wholesale channel, not offered to shoppers.
	e.must(204, "PUT", "/v1/channels/mayoreo", e.adminA, m{"currency": "MXN", "taxBps": 1600, "maxQtyPerItem": 500})
	e.drain()

	get := func(path string) string {
		t.Helper()
		_, b, _ := e.raw(http.MethodGet, path, "", nil, "")
		return string(b)
	}
	// The catalog in the US market shows dollars and links keep the market.
	page := get("/s/store-a/main/c?mk=us")
	if !strings.Contains(page, "USD") || strings.Contains(page, " MXN</p>") || !strings.Contains(page, `data-mk="us"`) || !strings.Contains(page, "mk=us") {
		i := strings.Index(page, " MXN</p>")
		t.Fatalf("US catalog: dollars=%v mk=%v; MXN at: %s", strings.Contains(page, "USD"), strings.Contains(page, `data-mk="us"`), page[max(0, i-300):min(len(page), i+20)])
	}
	if main := get("/s/store-a/main/c"); !strings.Contains(main, "MXN") || !strings.Contains(main, `class="markets"`) || !strings.Contains(main, `hreflang="es-US"`) {
		t.Fatal("main catalog lost its currency or the country selector")
	}
	if get("/s/store-a/main/c?mk=mayoreo") != get("/s/store-a/main/c") {
		t.Fatal("a private channel was reachable from the store address")
	}
	// Designer pages ask for prices in the market currency.
	st, pr := e.do("GET", "/s/store-a/api/prices?channel=us&handles=p1", "", nil)
	if st != 200 || !strings.Contains(pr["items"].(map[string]any)["p1"].(map[string]any)["price"].(string), "USD") {
		t.Fatalf("prices endpoint = %d %v", st, pr)
	}
	if st, _ := e.do("GET", "/s/store-a/api/prices?channel=mayoreo&handles=p1", "", nil); st != 400 {
		t.Fatalf("wholesale prices exposed to shoppers: %d", st)
	}

	// The cart in dollars: tax is added on top and the totals add up.
	cartBody := func(ch string) m {
		return m{"channel": ch, "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}}
	}
	st, sim := e.do("POST", "/s/store-a/api/simulate", "", cartBody("us"))
	if st != 200 {
		t.Fatalf("simulate us = %d %v", st, sim)
	}
	tot := sim["totals"].(map[string]any)
	amt := func(k string) float64 { return tot[k].(map[string]any)["amount"].(float64) }
	if tot["grand"].(map[string]any)["currency"] != "USD" || amt("taxAdjust") <= 0 ||
		amt("grand") != amt("items")-amt("discounts")+amt("shipping")+amt("taxAdjust") || amt("taxAdjust") != amt("taxIncluded") {
		t.Fatalf("US totals do not add up: %v", tot)
	}
	if st, _ := e.do("POST", "/s/store-a/api/simulate", "", cartBody("mayoreo")); st != 400 {
		t.Fatalf("a shopper simulated the wholesale channel: %d", st)
	}
	buy := func(ch, key string) (int, m) {
		b := cartBody(ch)
		b["customer"], b["paymentMethod"] = m{"email": "us@example.com", "name": "Pat Smith", "address": "1 Main St"}, "pm_card_visa"
		return e.do("POST", "/s/store-a/api/checkout", "", b, "Idempotency-Key", key)
	}
	if st, _ := buy("mayoreo", "market-key-0"); st != 400 {
		t.Fatalf("a shopper bought through the wholesale channel: %d", st)
	}
	stock := e.available("S1")
	st, o := buy("us", "market-key-1")
	if st != 201 || o["currency"] != "USD" || o["grandTotal"].(float64) != amt("grand") {
		t.Fatalf("US order = %d %v", st, o)
	}
	// Reversal: cancelling returns every cent in dollars and the stock.
	refunded := e.sandbox.Refunded()
	e.must(200, "POST", "/v1/orders/"+o["id"].(string)+"/cancel", e.adminA, m{"reason": "prueba", "notify": false})
	e.drain()
	if got := e.sandbox.Refunded() - refunded; got != int64(o["grandTotal"].(float64)) || e.available("S1") != stock {
		t.Fatalf("US cancel refunded %d of %v, stock %v of %v", got, o["grandTotal"], e.available("S1"), stock)
	}

	// A tax-exempt company pays the price without its tax.
	plain := e.simulate(m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}})["totals"].(map[string]any)
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Exenta", "terms": "net30", "creditLimit": 10000000, "taxExempt": true, "buyerEmails": []string{"x@exenta.example"}})["id"].(string)
	if st, _ := e.do("PUT", "/v1/companies/"+co, e.adminA, m{"name": "Exenta", "terms": "net30", "creditLimit": 10000000, "taxExempt": true, "taxExemptUntil": "31/12/2027"}); st != 400 {
		t.Fatalf("bad exemption date accepted: %d", st)
	}
	st, eo := e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "x@exenta.example", "name": "Exenta"}, "paymentMethod": "credit", "companyId": co}, "Idempotency-Key", "market-key-2")
	gross := plain["grand"].(map[string]any)["amount"].(float64)
	tax := plain["taxIncluded"].(map[string]any)["amount"].(float64)
	if st != 201 || eo["grandTotal"].(float64) != gross-tax {
		t.Fatalf("exempt order = %d grand=%v, want %v", st, eo["grandTotal"], gross-tax)
	}
	// An exemption that already ended no longer applies.
	e.must(200, "PUT", "/v1/companies/"+co, e.adminA, m{"name": "Exenta", "terms": "net30", "creditLimit": 10000000, "taxExempt": true, "taxExemptUntil": "2020-01-01", "buyerEmails": []string{"x@exenta.example"}})
	st, eo = e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "x@exenta.example", "name": "Exenta"}, "paymentMethod": "credit", "companyId": co}, "Idempotency-Key", "market-key-3")
	if st != 201 || eo["grandTotal"].(float64) != gross {
		t.Fatalf("expired exemption = %d grand=%v, want %v", st, eo["grandTotal"], gross)
	}
	// Another store is untouched.
	if strings.Contains(get("/s/store-b/main/c?mk=us"), "USD") {
		t.Fatal("the US market of one store appeared in another")
	}
}

// B2B quantity rules and volume prices.
func TestQuantityRulesAndVolumePrices(t *testing.T) {
	e := setup(t)
	if st, _ := e.do("PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 10, "max": 5}}}); st != 400 {
		t.Fatalf("max below min accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 1, "step": 1}, {"group": "mayoreo", "min": 6, "step": 6}}})
	e.drain()
	sim := func(qty int) m {
		return e.simulate(m{"channel": "mx", "items": []m{{"sku": "S1", "qty": qty}}})
	}
	// The general rule allows anything; a company of the wholesale group buys in boxes of 6.
	if q := sim(2)["lines"].([]any)[0].(map[string]any)["qty"].(float64); q != 2 {
		t.Fatalf("general rule changed the quantity: %v", q)
	}
	e.must(200, "PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 3, "step": 1}}})
	e.drain()
	s := sim(1)
	if q := s["lines"].([]any)[0].(map[string]any)["qty"].(float64); q != 3 {
		t.Fatalf("minimum not applied: %v", q)
	}
	found := false
	for _, x := range s["messages"].([]any) {
		found = found || x.(map[string]any)["code"] == "qty_min"
	}
	if !found {
		t.Fatal("the cart does not explain the minimum")
	}
	// Lowering to the stock (10) keeps whole boxes of 6 and never goes below the minimum.
	e.must(200, "PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 6, "step": 6}}})
	e.drain()
	if q := sim(12)["lines"].([]any)[0].(map[string]any)["qty"].(float64); q != 6 {
		t.Fatalf("stock adjustment broke the box of 6: %v", q)
	}
	e.must(200, "PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 12, "step": 1}}})
	e.drain()
	s12 := sim(12)
	if q := s12["lines"].([]any)[0].(map[string]any)["qty"].(float64); q != 0 {
		t.Fatalf("a minimum above the stock still sold: %v", q)
	}
	e.must(200, "PUT", "/v1/products/P1/quantity-rules", e.adminA, m{"rules": []m{{"min": 3, "step": 1}}})
	e.drain()
	// Checkout refuses a quantity below the minimum.
	if st, b := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "min@example.com", "name": "Min", "address": "x"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "qty-rule-key-1"); st != 409 || b["code"] != "cart_changed" {
		t.Fatalf("checkout below the minimum = %d %v", st, b)
	}

	// Volume prices: $90 from 4 pieces, $80 from 8, in a fixed-price list.
	base := sim(3)["lines"].([]any)[0].(map[string]any)["unit"].(map[string]any)["amount"].(float64)
	if st, _ := e.do("POST", "/v1/price-lists", e.adminA, m{"name": "Volumen", "kind": "override", "status": "active",
		"items": []m{{"sku": "S1", "amount": base, "minQty": 1}, {"sku": "S1", "amount": 9000, "minQty": 4}, {"sku": "S1", "amount": 9500, "minQty": 4}}}); st != 400 {
		t.Fatalf("repeated volume step accepted: %d", st)
	}
	e.must(201, "POST", "/v1/price-lists", e.adminA, m{"name": "Volumen", "kind": "override", "status": "active",
		"items": []m{{"sku": "S1", "amount": base, "minQty": 1}, {"sku": "S1", "amount": 9000, "minQty": 4}, {"sku": "S1", "amount": 8000, "minQty": 8}}})
	e.drain()
	unit := func(q int) float64 {
		return sim(q)["lines"].([]any)[0].(map[string]any)["unit"].(map[string]any)["amount"].(float64)
	}
	if unit(3) != base || unit(4) != 9000 || unit(9) != 8000 {
		t.Fatalf("volume prices: 3=%v 4=%v 9=%v", unit(3), unit(4), unit(9))
	}
}
