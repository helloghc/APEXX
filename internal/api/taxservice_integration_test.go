package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/taxprovider"
)

// An external tax service computes the cart's tax; when it is slow or
// broken Apexx's own rules answer, and the order is charged what was shown.
func TestExternalTaxService(t *testing.T) {
	e := setup(t)
	var mode atomic.Int32 // 0 = 10%, 1 = hang, 2 = error
	var calls atomic.Int32
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if carriers.Sign(e.server.taxSecret("store-a"), r.Header.Get("X-Apexx-Timestamp"), body) != r.Header.Get("X-Apexx-Signature") {
			w.WriteHeader(401)
			return
		}
		switch mode.Load() {
		case 1:
			time.Sleep(2 * time.Second)
		case 2:
			w.WriteHeader(500)
			return
		}
		var in taxprovider.Request
		_ = json.Unmarshal(body, &in)
		var out taxprovider.Response
		for _, p := range in.Parts {
			out.Lines = append(out.Lines, taxprovider.Line{Class: p.Class, Label: "Impuesto externo 10%", RateBps: 1000, Tax: p.Amount * 1000 / 11000})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer app.Close()
	taxHTTPClient = app.Client()
	defer func() { taxHTTPClient = nil }()
	if st, _ := e.do("PUT", "/v1/settings/tax-service", e.adminA, m{"active": true, "endpoint": "http://inseguro.example"}); st != 400 {
		t.Fatalf("http endpoint accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/tax-service", e.sfA, m{"active": false}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed the tax service: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/tax-service", e.adminA, m{"active": true, "name": "TaxApp", "endpoint": app.URL})
	if g := e.must(200, "GET", "/v1/settings/tax-service", e.adminA, nil); len(g["secret"].(string)) != 64 {
		t.Fatalf("settings = %v", g)
	}
	sim := func(cp string) map[string]any {
		return e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": cp, "items": []m{{"sku": "S1", "qty": 1}}})["tax"].(map[string]any)
	}
	tax := sim("53129")
	if !strings.Contains(tax["reason"].(string), "TaxApp") || tax["lines"].([]any)[0].(map[string]any)["rateBps"].(float64) != 1000 {
		t.Fatalf("external tax = %v", tax)
	}
	// Store B does not use store A's service.
	if b := e.must(200, "POST", "/s/store-b/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}})["tax"].(map[string]any); strings.Contains(b["reason"].(string), "TaxApp") {
		t.Fatal("store B used store A's tax service")
	}
	// Cached: a second identical cart does not call it again.
	before := calls.Load()
	sim("53129")
	if calls.Load() != before {
		t.Fatal("the answer was not cached")
	}
	// A hanging service: Apexx's own rules answer in under a second.
	mode.Store(1)
	start := time.Now()
	if tax := sim("06600"); strings.Contains(tax["reason"].(string), "TaxApp") || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("hanging service: %v after %v", tax["reason"], time.Since(start))
	}
	// A broken service: same.
	mode.Store(2)
	if tax := sim("01000"); strings.Contains(tax["reason"].(string), "TaxApp") {
		t.Fatal("a failing service was used")
	}
	// The order charges what the cart showed with the service.
	mode.Store(0)
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "tax@cliente.example", "name": "Tax"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "taxsvc-key-1")
	if st != 201 || o["taxIncluded"].(float64) != tax["total"].(float64) {
		t.Fatalf("order tax %v, cart tax %v (%d)", o["taxIncluded"], tax["total"], st)
	}
	// Turned off: own rules again.
	e.must(200, "PUT", "/v1/settings/tax-service", e.adminA, m{"active": false, "endpoint": app.URL})
	if tax := sim("53129"); strings.Contains(tax["reason"].(string), "TaxApp") {
		t.Fatal("still using the service after turning it off")
	}
}
