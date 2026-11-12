package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/helloghc/apexx/internal/carriers"
)

// A third-party warehouse gets the paid orders of its location, signed and
// retried until it answers; a cancelled order gets a cancel request; the
// warehouse ships through the orders API.
func TestThirdPartyWarehouse(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var down atomic.Bool
	var got, cancels atomic.Int32
	var lastItems atomic.Value
	wh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if carriers.Sign(e.server.warehouseKey("store-a", "w1"), r.Header.Get("X-Apexx-Timestamp"), body) != r.Header.Get("X-Apexx-Signature") {
			w.WriteHeader(401)
			return
		}
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		var in struct {
			OrderID string           `json:"orderId"`
			Items   []map[string]any `json:"items"`
			ShipTo  map[string]any   `json:"shipTo"`
		}
		_ = json.Unmarshal(body, &in)
		if r.URL.Path == "/cancellations" {
			cancels.Add(1)
		} else {
			got.Add(1)
			lastItems.Store(in)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"reference": "WH-" + in.OrderID})
	}))
	defer wh.Close()
	warehouseHTTPClient = wh.Client()
	defer func() { warehouseHTTPClient = nil }()
	if st, _ := e.do("PUT", "/v1/locations/w1", e.adminA, m{"name": "w1", "kind": "warehouse", "active": true, "fulfillerEndpoint": "http://inseguro.example",
		"address": m{"line1": "Bodega 1", "city": "Tultitlán", "state": "MEX", "postalCode": "54900", "country": "MX"}}); st != 400 {
		t.Fatalf("http warehouse accepted: %d", st)
	}
	if st, b := e.do("PUT", "/v1/locations/w1", e.adminA, m{"name": "w1", "kind": "warehouse", "active": true, "fulfillerEndpoint": wh.URL,
		"address": m{"line1": "Bodega 1", "city": "Tultitlán", "state": "MEX", "postalCode": "54900", "country": "MX"}}); st >= 300 {
		t.Fatalf("warehouse location = %d %v", st, b)
	}
	buy := func(key string) string {
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}},
			"customer": m{"email": "3pl@cliente.example", "name": "Tres", "address": "Calle 1"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", key)
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
		return o["id"].(string)
	}
	// The warehouse is down: the request waits and is retried.
	down.Store(true)
	id := buy("wh-key-001")
	if _, err := e.server.RunWarehouseRequests(ctx); err != nil {
		t.Fatal(err)
	}
	r := e.must(200, "GET", "/v1/orders/"+id+"/warehouse-requests", e.adminA, nil)["items"].([]any)
	if len(r) != 1 || r[0].(map[string]any)["status"] != "pending" || r[0].(map[string]any)["attempts"].(float64) != 1 {
		t.Fatalf("after a failure: %v", r)
	}
	// Back up: delivered once.
	down.Store(false)
	_, _ = e.db.System.Exec(ctx, `UPDATE fulfillment_requests SET next_attempt=now()`)
	if n, _ := e.server.RunWarehouseRequests(ctx); n != 1 {
		t.Fatalf("delivered %d", n)
	}
	if n, _ := e.server.RunWarehouseRequests(ctx); n != 0 || got.Load() != 1 {
		t.Fatalf("delivered twice: %d", got.Load())
	}
	r = e.must(200, "GET", "/v1/orders/"+id+"/warehouse-requests", e.adminA, nil)["items"].([]any)
	if r[0].(map[string]any)["status"] != "accepted" || r[0].(map[string]any)["reference"] != "WH-"+id {
		t.Fatalf("accepted: %v", r)
	}
	if st, _ := e.do("GET", "/v1/orders/"+id+"/warehouse-requests", e.adminB, nil); st != 200 {
		t.Fatalf("store B list = %d", st)
	} else if b := e.must(200, "GET", "/v1/orders/"+id+"/warehouse-requests", e.adminB, nil); len(b["items"].([]any)) != 0 {
		t.Fatal("store B sees store A's warehouse requests")
	}
	// The warehouse ships with the orders API.
	e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"carrier": "DHL", "trackingNumber": "WH1", "notify": false})
	// A second order cancelled after the warehouse accepted it: cancel request.
	id2 := buy("wh-key-002")
	if n, _ := e.server.RunWarehouseRequests(ctx); n != 1 {
		t.Fatalf("second order delivered %d", n)
	}
	e.must(200, "POST", "/v1/orders/"+id2+"/cancel", e.adminA, m{"reason": "cliente"})
	if _, err := e.server.RunWarehouseRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if cancels.Load() != 1 {
		t.Fatalf("cancel requests = %d", cancels.Load())
	}
	// A third order cancelled before delivery: never sent.
	down.Store(true)
	id3 := buy("wh-key-003")
	e.must(200, "POST", "/v1/orders/"+id3+"/cancel", e.adminA, m{"reason": "cliente"})
	down.Store(false)
	_, _ = e.db.System.Exec(ctx, `UPDATE fulfillment_requests SET next_attempt=now()`)
	_, _ = e.server.RunWarehouseRequests(ctx)
	if got.Load() != 2 {
		t.Fatalf("a cancelled order reached the warehouse: %d", got.Load())
	}
}
