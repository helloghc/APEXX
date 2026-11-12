package api

import (
	"context"

	"github.com/helloghc/apexx/internal/orders"
	"net/http"
	"strings"
	"testing"
)

// Negotiable quotes: request, offer, changes, acceptance on credit, and the
// rules that keep it safe (one order per quote, expiry, isolation).
func TestQuotes(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	req := func(body m) (int, m) {
		return e.do("POST", "/s/store-a/api/quotes", "", body, "Origin", e.srv.URL)
	}
	if st, _ := req(m{"channel": "mx", "items": []m{}, "customer": m{"email": "q@example.com", "name": "Q"}}); st != 400 {
		t.Fatalf("empty quote accepted: %d", st)
	}
	if st, _ := req(m{"channel": "mayoreo", "items": []m{{"sku": "S1", "qty": 1}}, "customer": m{"email": "q@example.com", "name": "Q"}}); st != 400 {
		t.Fatalf("quote on a private channel accepted: %d", st)
	}
	st, r := req(m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 3, "unit": 1}}, "customer": m{"email": "q@example.com", "name": "Quique Compras"}, "message": "¿Precio por 3?"})
	if st != 201 || r["url"] == nil {
		t.Fatalf("request quote = %d %v", st, r)
	}
	token := r["url"].(string)[strings.LastIndex(r["url"].(string), "/")+1:]
	list := e.must(200, "GET", "/v1/quotes?status=requested", e.adminA, nil)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("requested quotes = %d", len(list))
	}
	q := list[0].(map[string]any)
	id := q["id"].(string)
	if q["items"].([]any)[0].(map[string]any)["unit"] != nil {
		t.Fatal("the buyer set their own price")
	}
	// The buyer page works before the offer and shows no personal data.
	_, page, _ := e.raw(http.MethodGet, "/s/store-a/main/cotizacion/"+token, "", nil, "")
	if !strings.Contains(string(page), "Cotización #") || strings.Contains(string(page), "q@example.com") {
		t.Fatal("buyer page before the offer")
	}
	if st, _ := e.do("POST", "/s/store-a/api/quotes/"+token+"/accept", "", m{}, "Origin", e.srv.URL); st != 409 {
		t.Fatalf("accepting before an offer = %d", st)
	}
	// The store answers: $70 a piece, free shipping, valid 7 days.
	if st, _ := e.do("POST", "/v1/quotes/"+id+"/offer", e.adminA, m{"items": []m{{"sku": "S1", "qty": 3}}, "validDays": 7}); st != 400 {
		t.Fatalf("offer without prices = %d", st)
	}
	off := e.must(200, "POST", "/v1/quotes/"+id+"/offer", e.adminA, m{"items": []m{{"sku": "S1", "qty": 3, "unit": 7000}}, "shipping": 0, "validDays": 7, "message": "Precio especial"})
	if off["status"] != "offered" || off["offer"].(map[string]any)["grandTotal"].(float64) != 21000 {
		t.Fatalf("offer = %v", off)
	}
	// Every offer keeps the buyer's link.
	if got := off["url"].(string)[strings.LastIndex(off["url"].(string), "/")+1:]; got != token {
		t.Fatal("the offer changed the buyer's link")
	}
	// The buyer asks for changes, the store answers again.
	if st, _ := e.do("POST", "/s/store-a/api/quotes/"+token+"/changes", "", m{"message": ""}, "Origin", e.srv.URL); st != 400 {
		t.Fatalf("changes without a message = %d", st)
	}
	if st, b := e.do("POST", "/s/store-a/api/quotes/"+token+"/changes", "", m{"message": "¿Y si llevo 3 con envío incluido?"}, "Origin", e.srv.URL); st != 200 {
		t.Fatalf("ask for changes = %d %v", st, b)
	}
	e.must(200, "POST", "/v1/quotes/"+id+"/offer", e.adminA, m{"items": []m{{"sku": "S1", "qty": 3, "unit": 6800}}, "validDays": 7})
	// Accepting by card: the buyer gets the payment link.
	stock := e.available("S1")
	st, acc := e.do("POST", "/s/store-a/api/quotes/"+token+"/accept", "", m{}, "Origin", e.srv.URL)
	if st != 200 || acc["status"] != "accepted" || acc["payUrl"] == "" {
		t.Fatalf("accept = %v", acc)
	}
	// A second click cannot accept twice.
	if st, _ := e.do("POST", "/s/store-a/api/quotes/"+token+"/accept", "", m{}, "Origin", e.srv.URL); st != 409 {
		t.Fatalf("second accept = %d", st)
	}
	if e.available("S1") != stock {
		t.Fatal("stock moved before payment by card")
	}
	got := e.must(200, "GET", "/v1/quotes/"+id, e.adminA, nil)
	hist := got["history"].([]any)
	if len(hist) != 5 { // requested, offered, changes, offered, accepted
		t.Fatalf("history = %v", hist)
	}

	// Expiry closes an open offer and its draft.
	st, r = req(m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 1}}, "customer": m{"email": "q2@example.com", "name": "Otra"}})
	if st != 201 {
		t.Fatalf("second request = %d", st)
	}
	id2 := e.must(200, "GET", "/v1/quotes?status=requested", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)
	o2 := e.must(200, "POST", "/v1/quotes/"+id2+"/offer", e.adminA, m{"items": []m{{"sku": "S1", "qty": 1, "unit": 5000}}, "validDays": 1})
	tok2 := o2["url"].(string)[strings.LastIndex(o2["url"].(string), "/")+1:]
	if _, err := e.db.System.Exec(ctx, `UPDATE quotes SET expires_at = now() - interval '1 minute' WHERE id=$1`, id2); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.do("POST", "/s/store-a/api/quotes/"+tok2+"/accept", "", m{}, "Origin", e.srv.URL); st != 409 {
		t.Fatalf("accepting an expired offer = %d", st)
	}
	if n, err := e.orders.ExpireQuotes(ctx); err != nil || n != 1 {
		t.Fatalf("expire = %d %v", n, err)
	}
	if e.must(200, "GET", "/v1/quotes/"+id2, e.adminA, nil)["status"] != "expired" {
		t.Fatal("expired quote not closed")
	}
	// A company on terms accepts and the order is placed on its credit at once.
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Cotiza SA", "terms": "net30", "creditLimit": 10000000, "buyerEmails": []string{"compras@cotiza.example"}})["id"].(string)
	acc3, err := e.orders.CompanyAccessByID(ctx, "store-a", co)
	if err != nil {
		t.Fatal(err)
	}
	q3, err := e.orders.RequestQuote(ctx, "store-a", orders.QuoteRequest{Channel: "mx", Items: []orders.ItemInput{{SKU: "S1", Qty: 2}}, Customer: orders.Customer{Email: "compras@cotiza.example", Name: "Compras"}, Company: acc3})
	if err != nil {
		t.Fatal(err)
	}
	unit := int64(6000)
	o3, err := e.orders.OfferQuote(ctx, "store-a", "test", q3.ID, orders.QuoteOffer{Items: []orders.ItemInput{{SKU: "S1", Qty: 2, Unit: &unit}}, ValidDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	tok3 := o3.URL[strings.LastIndex(o3.URL, "/")+1:]
	before := e.available("S1")
	done, pay, err := e.orders.AnswerQuote(ctx, "store-a", tok3, "accept", "", acc3)
	if err != nil || pay != "" || done.OrderID == "" {
		t.Fatalf("accept on credit: %v pay=%q order=%q", err, pay, done.OrderID)
	}
	e.drain()
	ord := e.must(200, "GET", "/v1/orders/"+done.OrderID, e.adminA, nil)
	if ord["status"] != "paid" || ord["grandTotal"].(float64) != 12000 || e.available("S1") != before-2 {
		t.Fatalf("credit order: status=%v total=%v stock %v→%v", ord["status"], ord["grandTotal"], before, e.available("S1"))
	}
	if bal := e.must(200, "GET", "/v1/companies/"+co, e.adminA, nil)["company"].(map[string]any)["balance"].(float64); bal != 12000 {
		t.Fatalf("company balance = %v", bal)
	}
	if _, _, err := e.orders.AnswerQuote(ctx, "store-a", tok3, "accept", "", acc3); err == nil {
		t.Fatal("a quote was accepted twice")
	}
	// Isolation: another store sees nothing; the storefront key cannot offer.
	if st, _ := e.do("GET", "/v1/quotes/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the quote: %d", st)
	}
	if st, _ := e.do("POST", "/v1/quotes/"+id+"/offer", e.sfA, m{"items": []m{{"sku": "S1", "qty": 1, "unit": 1}}, "validDays": 1}); st != 403 && st != 401 {
		t.Fatalf("storefront key made an offer: %d", st)
	}
	if st, _ := e.do("POST", "/s/store-b/api/quotes/"+token+"/accept", "", m{}, "Origin", e.srv.URL); st != 404 {
		t.Fatalf("token used in another store: %d", st)
	}
}
