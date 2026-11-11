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
