package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/carriers"
)

// Connected carriers: live rates with a fallback, labels bought from the
// order (and cancelled when the shipment cannot be saved), tracking that
// delivers, and returns the customer opens from their account with a
// prepaid label.
func TestCarriersLabelsTrackingAndReturns(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/shipping/defaults", e.adminA, nil)
	if st, _ := e.do("POST", "/v1/shipping/carriers", e.adminA, m{"name": "App", "active": true, "provider": "http", "endpoint": "http://inseguro.example"}); st != 400 {
		t.Fatalf("http endpoint accepted: %d", st)
	}
	st, c := e.do("POST", "/v1/shipping/carriers", e.adminA, m{"name": "sbx", "active": true, "provider": "sandbox", "trackingUrl": "https://rastreo.example/?g={tracking}"})
	if st >= 300 || c["id"] != "sbx" {
		t.Fatalf("create carrier = %d %v", st, c)
	}
	option := m{"name": "Estándar en vivo", "kind": "delivery", "zoneId": "mexico", "profileId": "default", "carrierId": "sbx", "active": true, "position": 3,
		"rate": m{"type": "live", "amount": 15000}, "estimate": m{"minDays": 3, "maxDays": 6, "cutoff": "14:00", "weekend": "none"}}
	e.must(200, "PUT", "/v1/shipping/options/mx-estandar", e.adminA, option)
	e.drain()
	quote := func(cp string) map[string]any {
		sim := e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": cp, "items": []m{{"sku": "S1", "qty": 1}}, "shippingOption": "mx-estandar"})
		for _, q := range sim["shippingOptions"].([]any) {
			if q.(map[string]any)["id"] == "mx-estandar" {
				return q.(map[string]any)
			}
		}
		t.Fatalf("no live option in %v", sim["shippingOptions"])
		return nil
	}
	// 500 g -> 1 kg: $99 + $15 from the sandbox carrier.
	if q := quote("64000"); q["price"].(float64) != 11400 || !strings.Contains(q["priceReason"].(string), "en vivo") {
		t.Fatalf("live rate = %v %v", q["price"], q["priceReason"])
	}
	// The carrier does not cover the postal code: fallback rate, the sale goes on.
	if q := quote("00000"); q["price"].(float64) != 15000 || !strings.Contains(q["priceReason"].(string), "respaldo") {
		t.Fatalf("fallback = %v %v", q["price"], q["priceReason"])
	}
	// A carrier that hangs never makes the cart slow.
	start := time.Now()
	if q := quote("99999"); q["price"].(float64) != 15000 || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("hanging carrier: %v in %v", q["price"], time.Since(start))
	}

	// A paid order; the label is bought from the panel.
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "64000", "items": []m{{"sku": "S1", "qty": 2}},
		"customer": m{"email": "guias@cliente.example", "name": "Guías", "address": "Av. Constitución 100, Monterrey"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "label-key-1")
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	id := o["id"].(string)
	if st, _ := e.do("POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"buyLabel": true, "carrierId": "estafeta", "notify": false}); st != 400 {
		t.Fatalf("label from a carrier that is not connected: %d", st)
	}
	f := e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"items": []m{{"line": 1, "qty": 1}}, "buyLabel": true, "carrierId": "sbx", "notify": false})
	ful := f["fulfillments"].([]any)[0].(map[string]any)
	if !strings.HasPrefix(ful["trackingNumber"].(string), "SBX") || ful["labelCost"].(float64) <= 0 || !strings.Contains(ful["trackingUrl"].(string), ful["trackingNumber"].(string)) {
		t.Fatalf("fulfillment with label = %v", ful)
	}
	// Tracking marks it delivered.
	if n, err := e.server.RunTracking(ctx); err != nil || n != 1 {
		t.Fatalf("tracking delivered %d (%v)", n, err)
	}
	if n, _ := e.server.RunTracking(ctx); n != 0 {
		t.Fatal("tracking asked again within the hour")
	}
	got := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if got["fulfillments"].([]any)[0].(map[string]any)["status"] != "delivered" {
		t.Fatalf("not delivered: %v", got["fulfillments"])
	}

	// Self-service return: off until the store turns it on.
	cid := got["customerId"].(string)
	_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, cid)
	if err := e.server.Customers.SetPassword(ctx, "store-a", cid, "Una-clave-larga-2026", ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", "guias@cliente.example", "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	as := func(path string, body any) (int, m) {
		return e.do("POST", path, "", body, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL)
	}
	ret := m{"items": []m{{"line": 1, "qty": 1, "reason": "size"}}, "note": "me quedó chica"}
	if st, _ := as("/s/store-a/api/account/orders/"+id+"/returns", ret); st != 403 {
		t.Fatalf("self-service while off = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/returns", e.adminA, m{"selfService": true, "windowDays": 30, "carrierId": "estafeta"}); st != 400 {
		t.Fatalf("returns carrier not connected accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/returns", e.adminA, m{"selfService": true, "windowDays": 30, "carrierId": "sbx"})
	if st, _ := e.do("POST", "/s/store-a/api/account/orders/"+id+"/returns", "", ret, "Origin", e.srv.URL); st != 401 {
		t.Fatalf("return without a session = %d", st)
	}
	if st, _ := e.do("POST", "/s/store-b/api/account/orders/"+id+"/returns", "", ret, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL); st != 401 && st != 404 {
		t.Fatalf("return through another store = %d", st)
	}
	if st, _ := as("/s/store-a/api/account/orders/"+id+"/returns", m{"items": []m{{"line": 1, "qty": 2, "reason": "size"}}}); st != 400 {
		t.Fatalf("returning units never shipped = %d", st)
	}
	st, rr := as("/s/store-a/api/account/orders/"+id+"/returns", ret)
	if st != 201 || !strings.HasPrefix(rr["labelTracking"].(string), "SBX") {
		t.Fatalf("self-service return = %d %v", st, rr)
	}
	rets := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)["returns"].([]any)
	if len(rets) != 1 || rets[0].(map[string]any)["requestedBy"] != "customer" {
		t.Fatalf("returns = %v", rets)
	}
	// Outside the window it is refused.
	_, _ = e.db.System.Exec(ctx, `UPDATE fulfillments SET shipped_at=now() - interval '40 days' WHERE order_id=$1`, id)
	if st, _ := as("/s/store-a/api/account/orders/"+id+"/returns", ret); st != 409 {
		t.Fatalf("return after the window = %d", st)
	}
}

// A carrier app that refuses or fails: the label is not left bought.
func TestCarrierAppLabelReversal(t *testing.T) {
	e := setup(t)
	var cancels, labels atomic.Int32
	app := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/rates":
			_ = json.NewEncoder(w).Encode(map[string]any{"rates": []carriers.Rate{{Service: "s", Amount: 7700, Days: 2}}})
		case "/labels":
			labels.Add(1)
			_ = json.NewEncoder(w).Encode(carriers.Label{TrackingNumber: "APP1", LabelURL: "https://app.example/l.pdf", Cost: 7700})
		case "/cancel":
			cancels.Add(1)
			_ = body
		}
	}))
	defer app.Close()
	carrierHTTPClient = app.Client()
	defer func() { carrierHTTPClient = nil }()
	e.must(200, "POST", "/v1/shipping/defaults", e.adminA, nil)
	if st, c := e.do("POST", "/v1/shipping/carriers", e.adminA, m{"name": "app", "active": true, "provider": "http", "endpoint": app.URL}); st >= 300 || c["id"] != "app" {
		t.Fatalf("create app carrier = %d %v", st, c)
	}
	conn := e.must(200, "GET", "/v1/shipping/carriers/app/connection", e.adminA, nil)
	if len(conn["secret"].(string)) != 64 {
		t.Fatalf("connection = %v", conn)
	}
	if st, _ := e.do("GET", "/v1/shipping/carriers/app/connection", e.adminB, nil); st != 404 {
		t.Fatalf("store B read store A's carrier secret: %d", st)
	}
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "64000", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "app@cliente.example", "name": "App"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "label-key-2")
	if st != 201 {
		t.Fatalf("checkout = %d", st)
	}
	// A shipment that cannot be saved (line 9 does not exist) cancels the label it bought.
	if st, _ := e.do("POST", "/v1/orders/"+o["id"].(string)+"/fulfillments", e.adminA, m{"items": []m{{"line": 9, "qty": 1}}, "buyLabel": true, "carrierId": "app", "notify": false}); st != 400 {
		t.Fatalf("bad shipment = %d", st)
	}
	if labels.Load() != 1 || cancels.Load() != 1 {
		t.Fatalf("labels %d, cancels %d", labels.Load(), cancels.Load())
	}
}
