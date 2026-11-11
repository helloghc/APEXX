package api

import (
	"strings"
	"testing"
)

// A seller earns on its own lines only, at the commission of the sale;
// refunds take it back in proportion; its people see just their portal.
func TestMarketplaceSellers(t *testing.T) {
	e := setup(t)
	if st, _ := e.do("POST", "/v1/sellers", e.adminA, m{"name": "", "commissionBps": 2000}); st != 400 {
		t.Fatalf("seller without a name = %d", st)
	}
	sel := e.must(201, "POST", "/v1/sellers", e.adminA, m{"name": "Artesanías Luna", "email": "luna@example.com", "commissionBps": 2000})
	id := sel["id"].(string)
	other := e.must(201, "POST", "/v1/sellers", e.adminA, m{"name": "Otro Vendedor", "commissionBps": 1000})["id"].(string)
	if st, _ := e.do("PUT", "/v1/sellers/"+id+"/products", e.adminA, m{"productIds": []string{"NOPE"}}); st != 400 {
		t.Fatalf("unknown product assigned: %d", st)
	}
	e.must(204, "PUT", "/v1/sellers/"+id+"/products", e.adminA, m{"productIds": []string{"P1"}})
	e.drain()
	balance := func() (float64, float64, float64) {
		t.Helper()
		s := e.must(200, "GET", "/v1/sellers/"+id, e.adminA, nil)["seller"].(map[string]any)
		return s["sales"].(float64), s["commission"].(float64), s["balance"].(float64)
	}
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 2}, {"sku": "S2", "qty": 1}},
		"customer": m{"email": "comprador@example.com", "name": "Carla Compradora", "address": "Calle 1"}, "paymentMethod": "pm_card_visa"}
	_, o := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "mkt-key-1")
	orderID, grand := o["id"].(string), o["grandTotal"].(float64)
	if g, c, b := balance(); g != 20000 || c != 4000 || b != 16000 {
		t.Fatalf("after the sale: sales=%v commission=%v balance=%v", g, c, b)
	}
	// A later commission change does not rewrite what was already sold.
	e.must(200, "PUT", "/v1/sellers/"+id, e.adminA, m{"name": "Artesanías Luna", "commissionBps": 5000})
	// Half of the order refunded: the seller keeps half.
	e.must(201, "POST", "/v1/orders/"+orderID+"/refunds", e.adminA, m{"amount": grand / 2, "reason": "prueba", "notify": false})
	if g, c, b := balance(); g != 10000 || c != 2000 || b != 8000 {
		t.Fatalf("after refunding half: sales=%v commission=%v balance=%v", g, c, b)
	}
	// Payouts cannot exceed what is owed and need a reference.
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/entries", e.adminA, m{"kind": "payout", "amount": 9000, "note": "SPEI 1"}); st != 400 {
		t.Fatalf("payout above the balance = %d", st)
	}
	if st, _ := e.do("POST", "/v1/sellers/"+id+"/entries", e.adminA, m{"kind": "payout", "amount": 5000}); st != 400 {
		t.Fatalf("payout without reference = %d", st)
	}
	e.must(204, "POST", "/v1/sellers/"+id+"/entries", e.adminA, m{"kind": "payout", "amount": 5000, "note": "SPEI 123"})
	if _, _, b := balance(); b != 3000 {
		t.Fatalf("balance after the payout = %v", b)
	}
	if st, _ := e.do("GET", "/v1/sellers/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the seller: %d", st)
	}

	// The seller's person: invited, signs in, sees only the portal.
	inv := e.must(201, "POST", "/v1/sellers/"+id+"/invite", e.adminA, m{"email": "persona@luna.example", "name": "Lucía Luna"})
	link := inv["link"].(string)
	e.must(201, "POST", "/v1/invitations/"+link[strings.LastIndex(link, "/")+1:]+"/accept", "", m{"name": "Lucía Luna", "password": "Una-clave-larga-2026"})
	_, lg := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "persona@luna.example", "password": "Una-clave-larga-2026"})
	tok := lg["token"].(string)
	for _, path := range []string{"/v1/products", "/v1/orders", "/v1/sellers", "/v1/customers", "/v1/team/members"} {
		if st, _ := e.do("GET", path, tok, nil); st != 403 {
			t.Fatalf("seller person opened %s: %d", path, st)
		}
	}
	if st, _ := e.do("GET", "/v1/seller/me", e.adminA, nil); st != 403 {
		t.Fatalf("store key opened the seller portal: %d", st)
	}
	me := e.must(200, "GET", "/v1/seller/me", tok, nil)
	if ps := me["products"].([]any); len(ps) != 1 || ps[0].(map[string]any)["id"] != "P1" || me["seller"].(map[string]any)["balance"].(float64) != 3000 {
		t.Fatalf("portal = %v", me)
	}
	// A second order to ship: the portal shows only the seller's line, with the address but not the email.
	body["items"] = []m{{"sku": "S1", "qty": 1}, {"sku": "S2", "qty": 1}}
	_, o2 := e.do("POST", "/v1/checkout", e.sfA, body, "Idempotency-Key", "mkt-key-2")
	lines := e.must(200, "GET", "/v1/seller/orders?pending=1", tok, nil)["items"].([]any)
	var mine map[string]any
	for _, x := range lines {
		l := x.(map[string]any)
		if l["sku"] != "S1" {
			t.Fatalf("the portal shows a line of another owner: %v", l)
		}
		if l["orderId"] == o2["id"] {
			mine = l
		}
	}
	if mine == nil || mine["customer"].(map[string]any)["name"] != "Carla Compradora" || mine["customer"].(map[string]any)["email"] != "" {
		t.Fatalf("line to ship = %v", mine)
	}
	e.must(201, "POST", "/v1/seller/orders/"+o2["id"].(string)+"/ship", tok, m{"carrier": "Estafeta", "trackingNumber": "LUNA123"})
	if st, _ := e.do("POST", "/v1/seller/orders/"+o2["id"].(string)+"/ship", tok, m{"carrier": "Estafeta", "trackingNumber": "LUNA124"}); st != 409 {
		t.Fatalf("shipping twice = %d", st)
	}
	full := e.must(200, "GET", "/v1/orders/"+o2["id"].(string), e.adminA, nil)
	if full["fulfillmentStatus"] != "partial" { // the store still ships its own line
		t.Fatalf("order after the seller shipped = %v", full["fulfillmentStatus"])
	}
	// Paused seller: portal closed. Products moved to another seller stop showing.
	e.must(200, "PUT", "/v1/sellers/"+id, e.adminA, m{"name": "Artesanías Luna", "commissionBps": 5000, "status": "paused"})
	if st, b := e.do("GET", "/v1/seller/me", tok, nil); st != 403 || b["code"] != "seller_paused" {
		t.Fatalf("paused seller portal = %d %v", st, b)
	}
	e.must(204, "PUT", "/v1/sellers/"+other+"/products", e.adminA, m{"productIds": []string{"P1"}})
	if s := e.must(200, "GET", "/v1/sellers/"+id, e.adminA, nil); len(s["products"].([]any)) != 0 {
		t.Fatalf("a product belongs to two sellers: %v", s["products"])
	}
	// Cancelling the first order takes back what was left of it, from the seller who sold it.
	before, _, _ := balance()
	e.must(200, "POST", "/v1/orders/"+orderID+"/cancel", e.adminA, m{"reason": "prueba"})
	if g, _, _ := balance(); g != before-10000 {
		t.Fatalf("after cancelling: sales=%v, before %v", g, before)
	}
}
