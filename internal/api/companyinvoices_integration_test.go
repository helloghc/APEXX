package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Company invoices: each order on credit is an invoice; money received
// without an invoice pays the oldest first; the buyer pays chosen invoices
// online once per key, a declined card changes nothing, the receipt is a
// PDF, the ledger balances and other companies and stores see nothing.
func TestCompanyInvoicesOnline(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Facturas SA", "terms": "net30", "creditLimit": 5000000, "buyerEmails": []string{"pagos@facturas.example"}})["id"].(string)
	other := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Otra SA", "terms": "net30", "creditLimit": 5000000, "buyerEmails": []string{"pagos@otra.example"}})["id"].(string)
	var orders []m
	for i := 0; i < 3; i++ {
		st, o := e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": i + 1}},
			"customer": m{"email": "pagos@facturas.example", "name": "Pagos"}, "paymentMethod": "credit", "companyId": co}, "Idempotency-Key", fmt.Sprintf("invoice-order-%d", i))
		if st != 201 {
			t.Fatalf("order %d = %d %v", i, st, o)
		}
		orders = append(orders, o)
	}
	inv := func() m { return e.must(200, "GET", "/v1/companies/"+co+"/invoices", e.adminA, nil) }
	var owed float64
	for _, o := range orders {
		owed += o["grandTotal"].(float64)
	}
	if r := inv(); len(r["items"].([]any)) != 3 || r["open"].(float64) != owed {
		t.Fatalf("invoices = %v (owed %v)", r, owed)
	}
	// A transfer without an invoice pays the oldest first.
	first := orders[0]["grandTotal"].(float64)
	e.must(204, "POST", "/v1/companies/"+co+"/entries", e.adminA, m{"kind": "payment", "amount": int(first), "note": "Transferencia"})
	if r := inv(); r["open"].(float64) != owed-first {
		t.Fatalf("open after transfer = %v", r["open"])
	}
	email := "pagos@facturas.example"
	var cid string
	_ = e.db.System.QueryRow(ctx, `SELECT customer_id FROM orders WHERE id=$1`, orders[0]["id"]).Scan(&cid)
	if cid == "" {
		c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
		if err != nil {
			t.Fatal(err)
		}
		cid = c.ID
	}
	_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, cid)
	_ = e.server.Customers.SetPassword(ctx, "store-a", cid, "Una-clave-larga-2026", "")
	tok, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	as := func(method, path string, body any, extra ...string) (int, m) {
		return e.do(method, path, "", body, append([]string{"Cookie", "apexx_shopper=" + tok, "Origin", e.srv.URL}, extra...)...)
	}
	ids := []string{orders[1]["id"].(string), orders[2]["id"].(string)}
	if st, r := as("POST", "/s/store-a/api/account/company/invoices/pay", m{"orders": ids, "paymentMethod": "pm_card_chargeDeclined"}, "Idempotency-Key", "pay-declined-1"); st != 409 || r["code"] != "payment_declined" {
		t.Fatalf("declined = %d %v", st, r)
	}
	if r := inv(); r["open"].(float64) != owed-first {
		t.Fatalf("a declined card changed the balance: %v", r["open"])
	}
	st, rc := as("POST", "/s/store-a/api/account/company/invoices/pay", m{"orders": ids, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "pay-invoices-0001")
	if st != 201 || rc["amount"].(float64) != owed-first || len(rc["invoices"].([]any)) != 2 {
		t.Fatalf("pay = %d %v", st, rc)
	}
	// The same key never charges twice; paying paid invoices is refused.
	calls := e.sandbox.Calls
	if st, again := as("POST", "/s/store-a/api/account/company/invoices/pay", m{"orders": ids, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "pay-invoices-0001"); st != 201 || again["id"] != rc["id"] || e.sandbox.Calls != calls {
		t.Fatalf("retry = %d %v (charged again: %v)", st, again["id"], e.sandbox.Calls != calls)
	}
	if st, r := as("POST", "/s/store-a/api/account/company/invoices/pay", m{"orders": ids, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "pay-invoices-0002"); st != 409 || r["code"] != "nothing_open" {
		t.Fatalf("paying paid invoices = %d %v", st, r)
	}
	if r := inv(); r["open"].(float64) != 0 || e.must(200, "GET", "/v1/companies/"+co, e.adminA, nil)["company"].(map[string]any)["balance"].(float64) != 0 {
		t.Fatalf("after paying everything: %v", r["open"])
	}
	// The receipt PDF, for the buyer and the store.
	get := func(path, key, cookie string) (int, []byte) {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	rid := rc["id"].(string)
	if st, b := get("/s/store-a/api/account/company/receipts/"+rid+"/pdf", "", "apexx_shopper="+tok); st != 200 || !bytes.HasPrefix(b, []byte("%PDF-")) || !bytes.Contains(b, []byte("Recibo de pago")) {
		t.Fatalf("buyer receipt = %d", st)
	}
	if st, _ := get("/v1/companies/"+co+"/receipts/"+rid+"/pdf", e.adminA, ""); st != 200 {
		t.Fatalf("store receipt = %d", st)
	}
	if st, _ := get("/v1/companies/"+other+"/receipts/"+rid+"/pdf", e.adminA, ""); st != 404 {
		t.Fatalf("receipt under another company: %d", st)
	}
	if st, _ := e.do("GET", "/v1/companies/"+co+"/invoices", e.adminB, nil); st != 404 {
		t.Fatalf("store B read the invoices: %d", st)
	}
	var bad int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries WHERE tenant_id='store-a' GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&bad)
	if bad != 0 {
		t.Fatalf("%d unbalanced ledger transactions", bad)
	}
}
