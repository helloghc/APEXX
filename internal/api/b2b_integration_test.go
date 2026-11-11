package api

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/orders"
)

// A company buys at its price and on credit up to its limit; refunds and
// payments lower what it owes; a public storefront key cannot use its credit.
func TestCompaniesAndCredit(t *testing.T) {
	e := setup(t)
	if st, _ := e.do("POST", "/v1/companies", e.adminA, m{"name": "Sin límite", "terms": "net30"}); st != 400 {
		t.Fatalf("credit terms without a limit = %d", st)
	}
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "ACME Industrial", "rfc": "AAA010101AAA", "terms": "net30", "creditLimit": 50000, "discountBps": 1000,
		"buyerEmails": []string{"Compras@acme.example", "jefe@acme.example"}})
	id := co["id"].(string)
	if co["buyers"].(float64) != 2 {
		t.Fatalf("buyers = %v", co["buyers"])
	}
	if st, b := e.do("POST", "/v1/companies", e.adminA, m{"name": "Otra", "buyerEmails": []string{"compras@acme.example"}}); st != 400 {
		t.Fatalf("a buyer in two companies = %d %v", st, b)
	}
	prepaid := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "De contado", "discountBps": 500})["id"].(string)
	n := 0
	checkout := func(key string, qty int, extra m) (int, m) {
		n++
		body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}}, "customer": m{"email": "compras@acme.example", "name": "Compras ACME"}, "paymentMethod": "credit", "companyId": id}
		for k, v := range extra {
			body[k] = v
		}
		return e.do("POST", "/v1/checkout", key, body, "Idempotency-Key", fmt.Sprintf("b2b-key-%d", n))
	}
	balance := func() float64 {
		t.Helper()
		return e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)["company"].(map[string]any)["balance"].(float64)
	}
	if st, _ := checkout(e.sfA, 1, nil); st != 403 {
		t.Fatalf("a storefront key bought on a company's credit: %d", st)
	}
	if st, b := checkout(e.adminA, 1, m{"companyId": ""}); st != 409 || b["code"] != "credit_unavailable" {
		t.Fatalf("credit without a company = %d %v", st, b)
	}
	if st, b := checkout(e.adminA, 1, m{"companyId": prepaid}); st != 409 || b["code"] != "credit_unavailable" {
		t.Fatalf("credit for a prepaid company = %d %v", st, b)
	}
	calls := e.sandbox.Calls
	st, o := checkout(e.adminA, 2, nil)
	if st != 201 || o["status"] != "paid" || o["discounts"].(float64) != 2000 || e.sandbox.Calls != calls {
		t.Fatalf("order on credit = %d status=%v discounts=%v charged=%v", st, o["status"], o["discounts"], e.sandbox.Calls != calls)
	}
	grand := o["grandTotal"].(float64)
	if got := balance(); got != grand {
		t.Fatalf("company owes %v after an order of %v", got, grand)
	}
	full := e.must(200, "GET", "/v1/orders/"+o["id"].(string), e.adminA, nil)
	if pay := full["payments"].([]any)[0].(map[string]any); pay["provider"] != "company_credit" || pay["amount"].(float64) != grand {
		t.Fatalf("payment of a credit order = %v", pay)
	}
	// Over the limit: refused, nothing is owed for it and stock is not held.
	stock := e.available("S1")
	if st, b := checkout(e.adminA, 3, nil); st != 409 || b["code"] != "credit_limit" {
		t.Fatalf("order over the credit limit = %d %v", st, b)
	}
	if balance() != grand || e.available("S1") != stock {
		t.Fatalf("a refused order changed the balance or the stock")
	}
	// A refund lowers the debt; so does a payment, which needs a reference.
	e.must(201, "POST", "/v1/orders/"+o["id"].(string)+"/refunds", e.adminA, m{"amount": 5000, "reason": "prueba", "notify": false})
	if got := balance(); got != grand-5000 {
		t.Fatalf("after a refund the company owes %v", got)
	}
	if st, _ := e.do("POST", "/v1/companies/"+id+"/entries", e.adminA, m{"kind": "payment", "amount": 10000}); st != 400 {
		t.Fatalf("payment without reference = %d", st)
	}
	e.must(204, "POST", "/v1/companies/"+id+"/entries", e.adminA, m{"kind": "payment", "amount": 10000, "note": "SPEI 778"})
	if got := balance(); got != grand-15000 {
		t.Fatalf("after a payment the company owes %v", got)
	}
	// The company price also applies when paying by card.
	st, card := checkout(e.adminA, 1, m{"paymentMethod": "pm_card_visa"})
	if st != 201 || card["discounts"].(float64) != 1000 || balance() != grand-15000 {
		t.Fatalf("card order of a company = %d discounts=%v", st, card["discounts"])
	}
	// A suspended company cannot buy as a company.
	e.must(200, "PUT", "/v1/companies/"+id, e.adminA, m{"name": "ACME Industrial", "terms": "net30", "creditLimit": 50000, "discountBps": 1000, "status": "suspended"})
	if st, b := checkout(e.adminA, 1, nil); st != 409 || b["code"] != "company_suspended" {
		t.Fatalf("suspended company = %d %v", st, b)
	}
	if st, _ := e.do("GET", "/v1/companies/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the company: %d", st)
	}
	var clear, unbalanced int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM company_buyers WHERE encode(email,'escape') LIKE '%acme%'`).Scan(&clear)
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if clear != 0 || unbalanced != 0 {
		t.Fatalf("buyers in clear: %d; unbalanced ledger transactions: %d", clear, unbalanced)
	}
}

// Price per product for a company, and purchases above the buyer's limit that
// wait for an approver and are placed exactly once.
func TestCompanyPriceListAndApprovals(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(201, "POST", "/v1/price-lists", e.adminA, m{"id": "acme", "name": "Precios ACME", "kind": "override", "customerGroup": "acme", "priority": 10,
		"items": []m{{"sku": "S1", "amount": 7000}}})
	if st, _ := e.do("POST", "/v1/companies", e.adminA, m{"name": "X", "terms": "net30", "creditLimit": 100000, "approvalThreshold": 1000, "buyerEmails": []string{"a@x.example"}}); st != 400 {
		t.Fatalf("approval threshold without approvers = %d", st)
	}
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "ACME", "terms": "net30", "creditLimit": 500000, "priceGroup": "acme", "approvalThreshold": 20000,
		"buyerEmails": []string{"compras@acme.example"}, "approverEmails": []string{"jefa@acme.example"}})
	id := co["id"].(string)
	e.drain()
	got := e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)
	if len(got["buyers"].([]any)) != 2 || len(got["approvers"].([]any)) != 1 {
		t.Fatalf("buyers=%v approvers=%v", got["buyers"], got["approvers"])
	}
	// Price per product: S1 costs 70 for the company, 100 for everyone else.
	body := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "customer": m{"email": "compras@acme.example", "name": "Carlos Compras"}, "paymentMethod": "credit", "companyId": id}
	st, o := e.do("POST", "/v1/checkout", e.adminA, body, "Idempotency-Key", "apr-key-1")
	if st != 201 || o["itemsTotal"].(float64) != 7000 {
		t.Fatalf("company price = %d items=%v %v", st, o["itemsTotal"], o["detail"])
	}
	// A buyer above the threshold: no order, a request for the approver.
	buyer, err := e.orders.CompanyForEmail(ctx, "store-a", "compras@acme.example")
	if err != nil || buyer == nil || buyer.Approver || buyer.ApprovalThreshold != 20000 {
		t.Fatalf("buyer access = %+v %v", buyer, err)
	}
	req := orders.CheckoutRequest{Channel: "mx", PostalCode: "53129", Items: []cart.Item{{SKU: "S1", Qty: 4}}, Customer: orders.Customer{Email: "compras@acme.example", Name: "Carlos Compras"}, PaymentMethod: "credit", Company: buyer}
	before := e.available("S1")
	res, err := e.orders.Checkout(ctx, "store-a", "storefront:web", "apr-key-2", req)
	if err != nil || res.Status != 202 || !strings.Contains(string(res.Body), "approval_required") {
		t.Fatalf("purchase above the threshold = %d %s %v", res.Status, res.Body, err)
	}
	if e.available("S1") != before {
		t.Fatal("a purchase waiting for approval took stock")
	}
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM purchase_requests WHERE status='pending'`).Scan(&n)
	if n != 1 {
		t.Fatalf("pending requests = %d", n)
	}
	var clear int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM purchase_requests WHERE encode(request,'escape') LIKE '%acme.example%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("the purchase request is stored in clear")
	}
	// The approver's link: a wrong token finds nothing; the right one shows and decides.
	if _, err := e.db.System.Exec(ctx, `UPDATE purchase_requests SET token_hash=$1`, orders.HashToken("enlace-de-prueba")); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.do("POST", "/s/store-a/api/approval/otro-enlace/approve", "", nil); st != 404 {
		t.Fatalf("approval with a wrong link = %d", st)
	}
	if st, _ := e.do("POST", "/s/store-b/api/approval/enlace-de-prueba/approve", "", nil); st != 404 {
		t.Fatalf("store B approved a request of store A: %d", st)
	}
	owed := e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)["company"].(map[string]any)["balance"].(float64)
	e.must(200, "POST", "/s/store-a/api/approval/enlace-de-prueba/approve", "", nil)
	if e.available("S1") != before-4 {
		t.Fatalf("approved purchase did not take stock: %v -> %v", before, e.available("S1"))
	}
	after := e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)["company"].(map[string]any)["balance"].(float64)
	if after <= owed {
		t.Fatalf("approved purchase is not on the company's credit: %v -> %v", owed, after)
	}
	if st, _ := e.do("POST", "/s/store-a/api/approval/enlace-de-prueba/approve", "", nil); st != 409 {
		t.Fatalf("approving twice = %d", st)
	}
	if e.available("S1") != before-4 {
		t.Fatal("approving twice placed two orders")
	}
	// An approver buys above the threshold without waiting.
	boss, _ := e.orders.CompanyForEmail(ctx, "store-a", "jefa@acme.example")
	req.Company, req.Customer = boss, orders.Customer{Email: "jefa@acme.example", Name: "Julia Jefa"}
	if res, err := e.orders.Checkout(ctx, "store-a", "storefront:web", "apr-key-3", req); err != nil || res.Status != 201 {
		t.Fatalf("approver's own purchase = %d %s %v", res.Status, res.Body, err)
	}
	// A rejected request places nothing.
	req.Company, req.Customer = buyer, orders.Customer{Email: "compras@acme.example", Name: "Carlos Compras"}
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "apr-key-4", req); res.Status != 202 {
		t.Fatalf("second request = %d", res.Status)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE purchase_requests SET token_hash=$1 WHERE status='pending'`, orders.HashToken("enlace-dos")); err != nil {
		t.Fatal(err)
	}
	stock := e.available("S1")
	e.must(200, "POST", "/s/store-a/api/approval/enlace-dos/reject", "", nil)
	if e.available("S1") != stock {
		t.Fatal("a rejected request took stock")
	}
}
