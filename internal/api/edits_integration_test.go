package api

import (
	"context"
	"testing"
)

// Editing a paid order before it ships, and exchanging a shipped item for
// another: stock, money and the ledger end where they should, and extra
// charges survive the edit.
func TestOrderEditAndExchange(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "PUT", "/v1/settings/fees", e.adminA, m{"items": []m{{"id": "manejo", "name": "Manejo", "kind": "fixed", "amount": 2500, "active": true}}})
	e.drain()
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}},
		"customer": m{"email": "edita@cliente.example", "name": "Edita"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "edit-key-1")
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	id := o["id"].(string)
	grand := o["grandTotal"].(float64)
	s1, s2 := e.available("S1"), e.available("S2")
	// Preview changes nothing.
	pv := e.must(200, "POST", "/v1/orders/"+id+"/edit", e.adminA, m{"remove": []m{{"line": 1, "qty": 1}}, "add": []m{{"sku": "S2", "qty": 1}}, "reason": "cambio de talla", "dryRun": true})
	if e.available("S1") != s1 || e.available("S2") != s2 {
		t.Fatal("a preview moved stock")
	}
	if st, _ := e.do("POST", "/v1/orders/"+id+"/edit", e.adminB, m{"remove": []m{{"line": 1, "qty": 1}}}); st != 404 {
		t.Fatalf("store B edited store A's order: %d", st)
	}
	if st, _ := e.do("POST", "/v1/orders/"+id+"/edit", e.adminA, m{"remove": []m{{"line": 1, "qty": 2}}}); st != 400 {
		t.Fatalf("an edit emptied the order: %d", st)
	}
	res := e.must(200, "POST", "/v1/orders/"+id+"/edit", e.adminA, m{"remove": []m{{"line": 1, "qty": 1}}, "add": []m{{"sku": "S2", "qty": 1}}, "reason": "cambio de talla"})
	if res["difference"] != pv["difference"] {
		t.Fatalf("preview %v and edit %v differ", pv["difference"], res["difference"])
	}
	if e.available("S1") != s1+1 || e.available("S2") != s2-1 {
		t.Fatalf("stock after edit: S1 %v->%v, S2 %v->%v", s1, e.available("S1"), s2, e.available("S2"))
	}
	got := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if got["fees"].(float64) != 2500 {
		t.Fatalf("the edit dropped the fee: %v", got["fees"])
	}
	after := res["after"].(map[string]any)
	if after["grandTotal"].(float64) != after["itemsTotal"].(float64)-after["discounts"].(float64)+after["shipping"].(float64)+2500 {
		t.Fatalf("edited total lost the fee: %v", after)
	}
	if d := res["difference"].(float64); got["grandTotal"].(float64) != grand+d {
		t.Fatalf("grand %v + difference %v != %v", grand, d, got["grandTotal"])
	}

	// Exchange: ship it, return one S1 for one S2.
	e.must(201, "POST", "/v1/orders/"+id+"/fulfillments", e.adminA, m{"items": []m{{"line": 1, "qty": 1}, {"line": 2, "qty": 1}}, "carrier": "DHL", "trackingNumber": "X1", "notify": false})
	s2 = e.available("S2")
	ex := e.must(201, "POST", "/v1/orders/"+id+"/returns", e.adminA, m{"items": []m{{"line": 1, "qty": 1, "reason": "size"}}, "note": "otra talla", "exchange": []m{{"sku": "S2", "qty": 1}}})
	if ex["kind"] != "exchange" || e.available("S2") != s2-1 {
		t.Fatalf("exchange = %v, S2 %v -> %v", ex["kind"], s2, e.available("S2"))
	}
	// An exchange cannot be cancelled once the new item is part of the order.
	if st, _ := e.do("POST", "/v1/returns/"+ex["id"].(string)+"/cancel", e.adminA, nil); st != 409 {
		t.Fatalf("cancel an exchange = %d", st)
	}
	// Receiving the returned unit puts it back in stock and completes the exchange.
	s1 = e.available("S1")
	rec := e.must(200, "POST", "/v1/returns/"+ex["id"].(string)+"/receive", e.adminA, m{"items": []m{{"line": 1, "restock": true}}})["return"].(map[string]any)
	if e.available("S1") != s1+1 || (rec["status"] != "completed" && rec["status"] != "received") {
		t.Fatalf("received exchange: status %v, S1 %v -> %v", rec["status"], s1, e.available("S1"))
	}
	final := e.must(200, "GET", "/v1/orders/"+id, e.adminA, nil)
	if final["fees"].(float64) != 2500 {
		t.Fatalf("the exchange dropped the fee: %v", final["fees"])
	}
	var unbalanced int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("unbalanced ledger: %d", unbalanced)
	}
}
