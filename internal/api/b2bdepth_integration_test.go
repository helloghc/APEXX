package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/orders"
)

// Approvals in levels, monthly caps per buyer and branch, branches with their
// own prices, substitutes, expiry, and the company's own administrators.
func TestB2BLevelsCapsBranchesAndPortal(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(201, "POST", "/v1/price-lists", e.adminA, m{"id": "norte", "name": "Sucursal Norte", "kind": "override", "customerGroup": "norte", "priority": 10,
		"items": []m{{"sku": "S1", "amount": 6000}}})
	e.drain()
	people := []string{"ana@grupo.example", "beto@grupo.example", "jefa@grupo.example", "dir@grupo.example"}
	// A level-2 rule cannot be saved while nobody approves level 2.
	if st, b := e.do("POST", "/v1/companies", e.adminA, m{"name": "Grupo Niveles", "terms": "net30", "creditLimit": 5000000, "approvalThreshold": 50000,
		"approvalRules": []m{{"minTotal": 30000, "level": 2}}, "buyerEmails": people[:2], "approverEmails": people[2:]}); st != 400 || !strings.Contains(b["detail"].(string), "nivel 2") {
		t.Fatalf("level-2 rule without a level-2 approver = %d %v", st, b)
	}
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Grupo Niveles", "terms": "net30", "creditLimit": 5000000, "approvalThreshold": 50000,
		"buyerEmails": people[:2], "approverEmails": people[2:]})
	id := co["id"].(string)
	if co["approvalDays"].(float64) != 7 {
		t.Fatalf("default approval days = %v", co["approvalDays"])
	}
	if st, _ := e.do("PUT", "/v1/companies/"+id, e.adminA, m{"name": "Grupo Niveles", "terms": "net30", "creditLimit": 5000000, "approvalRules": []m{{"minTotal": 1, "level": 9}}}); st != 400 {
		t.Fatalf("rule with level 9 = %d", st)
	}
	locs := e.must(200, "PUT", "/v1/companies/"+id+"/locations", e.adminA, m{"items": []m{{"name": "Norte", "priceGroup": "norte", "address": m{"city": "Monterrey", "evil": "x"}}}})["items"].([]any)
	norte := locs[0].(map[string]any)
	if norte["address"].(map[string]any)["evil"] != nil {
		t.Fatal("unknown address keys are kept")
	}
	loc := norte["id"].(string)
	members := []m{
		{"email": "ana@grupo.example", "monthlyLimit": 25000, "location": loc},
		{"email": "beto@grupo.example"},
		{"email": "jefa@grupo.example", "approver": true, "level": 1, "admin": true},
		{"email": "dir@grupo.example", "approver": true, "level": 2},
	}
	if st, _ := e.do("PUT", "/v1/companies/"+id+"/members", e.adminA, m{"items": append([]m{{"email": "x@grupo.example", "location": "loc_nope"}}, members...)}); st != 400 {
		t.Fatalf("unknown branch = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/companies/"+id+"/members", e.adminB, m{"items": members}); st != 404 {
		t.Fatalf("store B edited store A's company = %d", st)
	}
	e.must(200, "PUT", "/v1/companies/"+id+"/members", e.adminA, m{"items": members})
	e.must(200, "PUT", "/v1/companies/"+id, e.adminA, m{"name": "Grupo Niveles", "terms": "net30", "creditLimit": 5000000, "approvalThreshold": 50000,
		"approvalRules": []m{{"minTotal": 30000, "level": 2}}})
	// Every level the company asks for needs someone to approve it.
	if st, b := e.do("PUT", "/v1/companies/"+id+"/members", e.adminA, m{"items": members[:3]}); st != 400 || !strings.Contains(b["detail"].(string), "nivel 2") {
		t.Fatalf("rules without a level-2 approver = %d %v", st, b)
	}
	// A branch with buyers cannot be removed.
	if st, _ := e.do("PUT", "/v1/companies/"+id+"/locations", e.adminA, m{"items": []m{}}); st != 400 {
		t.Fatalf("removed a branch with buyers = %d", st)
	}
	// The old buyer list (panel's company form) keeps levels, caps and branches.
	e.must(200, "PUT", "/v1/companies/"+id, e.adminA, m{"name": "Grupo Niveles", "terms": "net30", "creditLimit": 5000000, "approvalThreshold": 50000,
		"approvalRules": []m{{"minTotal": 30000, "level": 2}}, "buyerEmails": people[:2], "approverEmails": people[2:]})
	got := e.must(200, "GET", "/v1/companies/"+id+"/members", e.adminA, nil)["items"].([]any)
	for _, x := range got {
		mm := x.(map[string]any)
		if mm["email"] == "ana@grupo.example" && (mm["monthlyLimit"].(float64) != 25000 || mm["location"] != loc) {
			t.Fatalf("saving the company form wiped Ana's cap or branch: %v", mm)
		}
		if mm["email"] == "dir@grupo.example" && mm["level"].(float64) != 2 {
			t.Fatalf("saving the company form wiped the level: %v", mm)
		}
	}

	access := func(email string) *orders.CompanyAccess {
		a, err := e.orders.CompanyForEmail(ctx, "store-a", email)
		if err != nil || a == nil {
			t.Fatalf("access %s: %v", email, err)
		}
		return a
	}
	buy := func(email, key string, qty int64) orders.Result {
		req := orders.CheckoutRequest{Channel: "mx", PostalCode: "53129", Items: []cart.Item{{SKU: "S1", Qty: qty}}, Customer: orders.Customer{Email: email, Name: strings.Split(email, "@")[0]},
			PaymentMethod: "credit", Company: access(email)}
		res, err := e.orders.Checkout(ctx, "store-a", "storefront:web", key, req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// The branch buys at its own price; under her cap Ana buys without waiting.
	ana := access("ana@grupo.example")
	if ana.PriceGroup != "norte" || ana.LocationName != "Norte" {
		t.Fatalf("branch access = %+v", ana)
	}
	if res := buy("ana@grupo.example", "lv-1", 1); res.Status != 201 || !strings.Contains(string(res.Body), `"itemsTotal":6000`) {
		t.Fatalf("Ana under her cap = %d %s", res.Status, res.Body)
	}
	if a := access("ana@grupo.example"); a.MonthSpent <= 0 || a.LocationSpent != a.MonthSpent {
		t.Fatalf("month spent = %d / branch %d", a.MonthSpent, a.LocationSpent)
	}
	// Over her monthly cap: a level-1 request.
	stock := e.available("S1")
	res := buy("ana@grupo.example", "lv-2", 2)
	if res.Status != 202 || !strings.Contains(string(res.Body), "tope mensual") {
		t.Fatalf("Ana over her cap = %d %s", res.Status, res.Body)
	}
	if e.available("S1") != stock {
		t.Fatal("a waiting request took stock")
	}

	// Ana's session in the store (portal): she sees her request and cancels it.
	session := func(email string) func(method, path string, body any) (int, m) {
		e.drain()
		cid := ""
		if items := e.must(200, "GET", "/v1/customers?q="+email, e.adminA, nil)["items"].([]any); len(items) > 0 {
			cid = items[0].(map[string]any)["id"].(string)
		} else {
			c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
			if err != nil {
				t.Fatal(err)
			}
			cid = c.ID
		}
		c := struct{ ID string }{cid}
		if _, err := e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, c.ID); err != nil {
			t.Fatal(err)
		}
		if err := e.server.Customers.SetPassword(ctx, "store-a", c.ID, "Una-clave-larga-2026", ""); err != nil {
			t.Fatal(err)
		}
		token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
		if err != nil {
			t.Fatal(err)
		}
		return func(method, path string, body any) (int, m) {
			return e.do(method, path, "", body, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL)
		}
	}
	asAna, asJefa := session("ana@grupo.example"), session("jefa@grupo.example")
	st, p := asAna("GET", "/s/store-a/api/account/company", nil)
	if st != 200 || p["members"] != nil || len(p["requests"].([]any)) != 1 {
		t.Fatalf("Ana's portal = %d %v", st, p)
	}
	reqID := p["requests"].([]any)[0].(map[string]any)["id"].(string)
	if st, _ := asAna("PUT", "/s/store-a/api/account/company/members", m{"items": members}); st != 403 {
		t.Fatalf("a buyer changed the members = %d", st)
	}
	if st, _ := asJefa("POST", "/s/store-a/api/account/company/requests/"+reqID+"/cancel", nil); st != 409 {
		t.Fatalf("someone else cancelled Ana's request = %d", st)
	}
	if st, _ := asAna("POST", "/s/store-a/api/account/company/requests/"+reqID+"/cancel", nil); st != 204 {
		t.Fatalf("Ana cancels = %d", st)
	}
	if st, _ := asAna("POST", "/s/store-a/api/account/company/requests/"+reqID+"/cancel", nil); st != 409 {
		t.Fatalf("cancel twice = %d", st)
	}
	if st, _ := e.do("GET", "/s/store-b/api/account/company", "", nil, "Cookie", "apexx_shopper=x", "Origin", e.srv.URL); st != 401 {
		t.Fatalf("another store's portal = %d", st)
	}

	// Beto buys 40,000: the rule asks for level 2. Level 1 approves, then level 2.
	res = buy("beto@grupo.example", "lv-3", 4)
	if res.Status != 202 {
		t.Fatalf("Beto's purchase = %d %s", res.Status, res.Body)
	}
	setToken := func(tok string) {
		if _, err := e.db.System.Exec(ctx, `UPDATE purchase_requests SET token_hash=$1 WHERE status='pending'`, orders.HashToken(tok)); err != nil {
			t.Fatal(err)
		}
	}
	setToken("nivel-1")
	if b := e.must(200, "POST", "/s/store-a/api/approval/nivel-1/approve", "", nil); b["status"] != "pending" {
		t.Fatalf("level 1 = %v", b)
	}
	if e.available("S1") != stock {
		t.Fatal("level 1 alone placed the order")
	}
	if st, _ := e.do("POST", "/s/store-a/api/approval/nivel-1/approve", "", nil); st != 404 {
		t.Fatalf("the level-1 link still works = %d", st)
	}
	var lvl, step int
	_ = e.db.System.QueryRow(ctx, `SELECT level, step_level FROM purchase_requests WHERE status='pending'`).Scan(&lvl, &step)
	if lvl != 1 || step != 2 {
		t.Fatalf("level=%d step=%d", lvl, step)
	}
	// The director gets the new link by email.
	dirHash, _ := e.orders.Keys.Blind("store-a", "email", "dir@grupo.example")
	var mails int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE to_hash=$1 AND template='purchase_approval'`, dirHash).Scan(&mails)
	if mails != 1 {
		t.Fatalf("level-2 emails = %d", mails)
	}
	// Reversal: level 2 rejects; nothing is placed.
	setToken("nivel-2")
	e.must(200, "POST", "/s/store-a/api/approval/nivel-2/reject", "", nil)
	if e.available("S1") != stock {
		t.Fatal("a rejected request took stock")
	}

	// Again, and both levels approve at once on the last step: exactly one order.
	if res := buy("beto@grupo.example", "lv-4", 4); res.Status != 202 {
		t.Fatalf("second request = %d", res.Status)
	}
	setToken("otra-1")
	e.must(200, "POST", "/s/store-a/api/approval/otra-1/approve", "", nil)
	setToken("otra-2")
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = e.do("POST", "/s/store-a/api/approval/otra-2/approve", "", nil)
		}(i)
	}
	wg.Wait()
	if e.available("S1") != stock-4 {
		t.Fatalf("concurrent final approvals: stock %v -> %v (codes %v)", stock, e.available("S1"), codes)
	}
	betoHash, _ := e.orders.Keys.Blind("store-a", "email", "beto@grupo.example")
	var charged int64
	_ = e.db.System.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM company_entries WHERE buyer_hash=$1 AND kind='charge'`, betoHash).Scan(&charged)
	if charged <= 0 {
		t.Fatal("the approved order does not count for the buyer who asked")
	}

	// A substitute: the director is away; the level-2 link goes to the boss.
	members[3]["awayUntil"] = time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	members[3]["substitute"] = "jefa@grupo.example"
	// The company administrator manages members from the store, but cannot
	// remove her own administration.
	noAdmin := []m{members[0], members[1], {"email": "jefa@grupo.example", "approver": true, "level": 1}, members[3]}
	if st, _ := asJefa("PUT", "/s/store-a/api/account/company/members", m{"items": noAdmin}); st != 400 {
		t.Fatalf("admin removed herself = %d", st)
	}
	if st, b := asJefa("PUT", "/s/store-a/api/account/company/members", m{"items": members}); st != 200 {
		t.Fatalf("admin saves members = %d %v", st, b)
	}
	st, p = asJefa("GET", "/s/store-a/api/account/company", nil)
	if st != 200 || len(p["members"].([]any)) != 4 || len(p["locations"].([]any)) != 1 || len(p["requests"].([]any)) < 3 {
		t.Fatalf("admin portal = %d %v", st, p)
	}
	jefaHash, _ := e.orders.Keys.Blind("store-a", "email", "jefa@grupo.example")
	var before int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE to_hash=$1 AND template='purchase_approval'`, jefaHash).Scan(&before)
	if res := buy("beto@grupo.example", "lv-5", 4); res.Status != 202 {
		t.Fatalf("third request = %d", res.Status)
	}
	setToken("tres-1")
	e.must(200, "POST", "/s/store-a/api/approval/tres-1/approve", "", nil)
	var after, dirAfter int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE to_hash=$1 AND template='purchase_approval'`, jefaHash).Scan(&after)
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE to_hash=$1 AND template='purchase_approval'`, dirHash).Scan(&dirAfter)
	if after != before+2 || dirAfter != 2 {
		t.Fatalf("substitute: boss mails %d -> %d, director %d", before, after, dirAfter)
	}

	// Expiry: a request nobody decides closes, and its link cannot place it.
	if _, err := e.db.System.Exec(ctx, `UPDATE purchase_requests SET expires_at=now() - interval '1 minute', token_hash=$1 WHERE status='pending'`, orders.HashToken("vencida")); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.do("POST", "/s/store-a/api/approval/vencida/approve", "", nil); st != 409 {
		t.Fatalf("approving an expired request = %d", st)
	}
	if n, err := e.orders.ExpirePurchaseRequests(ctx); err != nil || n != 1 {
		t.Fatalf("expired = %d %v", n, err)
	}
	if e.available("S1") != stock-4 {
		t.Fatal("expiry changed stock")
	}
	reqs := e.must(200, "GET", "/v1/companies/"+id+"/requests", e.adminA, nil)["items"].([]any)
	statuses := map[string]int{}
	for _, x := range reqs {
		statuses[x.(map[string]any)["status"].(string)]++
	}
	if statuses["cancelled"] != 1 || statuses["rejected"] != 1 || statuses["approved"] != 1 || statuses["expired"] != 1 {
		t.Fatalf("request statuses = %v", statuses)
	}
	// Nobody to approve: the purchase does not wait for nobody.
	e.must(200, "PUT", "/v1/companies/"+id+"/members", e.adminA, m{"items": []m{{"email": "ana@grupo.example", "location": loc}, {"email": "beto@grupo.example"},
		{"email": "jefa@grupo.example", "approver": true, "level": 2, "admin": true}}})
	if _, err := e.db.System.Exec(ctx, `UPDATE company_buyers SET approver=false`); err != nil {
		t.Fatal(err)
	}
	if res := buy("beto@grupo.example", "lv-6", 4); res.Status != 409 || !strings.Contains(string(res.Body), "no_approver") {
		t.Fatalf("no approver = %d %s", res.Status, res.Body)
	}
}

// A company with a negotiated catalog sees and buys only what its price list prices.
func TestCompanyRestrictedCatalog(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("POST", "/v1/companies", e.adminA, m{"name": "Sin grupo", "catalogRestricted": true}); st != 400 {
		t.Fatalf("restricted catalog without a price group = %d", st)
	}
	e.must(201, "POST", "/v1/price-lists", e.adminA, m{"id": "cerrado", "name": "Catálogo cerrado", "kind": "override", "customerGroup": "cerrado", "priority": 10,
		"items": []m{{"sku": "S1", "amount": 9000}}})
	e.drain()
	e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Catálogo Cerrado", "priceGroup": "cerrado", "catalogRestricted": true, "buyerEmails": []string{"cerrado@empresa.example"}})
	a, err := e.orders.CompanyForEmail(ctx, "store-a", "cerrado@empresa.example")
	if err != nil || a == nil || !a.CatalogRestricted {
		t.Fatalf("access = %+v %v", a, err)
	}
	req := orders.CheckoutRequest{Channel: "mx", PostalCode: "53129", Items: []cart.Item{{SKU: "S2", Qty: 1}}, Customer: orders.Customer{Email: "cerrado@empresa.example", Name: "Cerrado"},
		PaymentMethod: "pm_card_visa", Company: a}
	stock := e.available("S2")
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "cat-1", req); res.Status == 201 {
		t.Fatalf("bought a product outside the catalog: %s", res.Body)
	}
	if e.available("S2") != stock {
		t.Fatal("a refused product took stock")
	}
	req.Items = []cart.Item{{SKU: "S1", Qty: 1}}
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "cat-2", req); res.Status != 201 || !strings.Contains(string(res.Body), `"itemsTotal":9000`) {
		t.Fatalf("product in the catalog = %d %s", res.Status, res.Body)
	}
	// Everyone else still buys S2.
	req.Company, req.Customer.Email, req.Items = nil, "otro@cliente.example", []cart.Item{{SKU: "S2", Qty: 1}}
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "cat-3", req); res.Status != 201 {
		t.Fatalf("a regular shopper = %d %s", res.Status, res.Body)
	}
}

// A company pays its credit automatically: reminder before the due date, one
// charge on it (never twice), three declines turn it off; only its
// administrator saves the card.
func TestCompanyAutopay(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Paga Sola", "terms": "net30", "creditLimit": 1000000, "buyerEmails": []string{"admin@pagasola.example", "compras@pagasola.example"}})
	id := co["id"].(string)
	e.must(200, "PUT", "/v1/companies/"+id+"/members", e.adminA, m{"items": []m{{"email": "admin@pagasola.example", "admin": true}, {"email": "compras@pagasola.example"}}})
	a, _ := e.orders.CompanyForEmail(ctx, "store-a", "compras@pagasola.example")
	req := orders.CheckoutRequest{Channel: "mx", PostalCode: "53129", Items: []cart.Item{{SKU: "S1", Qty: 1}}, Customer: orders.Customer{Email: "compras@pagasola.example", Name: "Compras"}, PaymentMethod: "credit", Company: a}
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "ap-1", req); res.Status != 201 {
		t.Fatalf("credit order = %d %s", res.Status, res.Body)
	}
	// Reminder: the charge falls due in two days.
	if _, err := e.db.System.Exec(ctx, `UPDATE company_entries SET due_at=now() + interval '2 days' WHERE company_id=$1 AND kind='charge'`, id); err != nil {
		t.Fatal(err)
	}
	adminHash, _ := e.orders.Keys.Blind("store-a", "email", "admin@pagasola.example")
	buyerHash, _ := e.orders.Keys.Blind("store-a", "email", "compras@pagasola.example")
	count := func(tpl string, h []byte) int {
		var n int
		_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE template=$1 AND to_hash=$2`, tpl, h).Scan(&n)
		return n
	}
	for i := 0; i < 2; i++ {
		if _, err := e.orders.RemindCompanyDues(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if count("company_due", adminHash) != 1 || count("company_due", buyerHash) != 0 {
		t.Fatalf("reminders: admin %d buyer %d", count("company_due", adminHash), count("company_due", buyerHash))
	}
	// Only the administrator saves the card, from the store.
	session := func(email string) func(method, path string, body any, hdr ...string) (int, m) {
		e.drain()
		c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
		cid := c.ID
		if err != nil {
			cid = e.must(200, "GET", "/v1/customers?q="+email, e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)
		}
		_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, cid)
		if err := e.server.Customers.SetPassword(ctx, "store-a", cid, "Una-clave-larga-2026", ""); err != nil {
			t.Fatal(err)
		}
		token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
		if err != nil {
			t.Fatal(err)
		}
		return func(method, path string, body any, hdr ...string) (int, m) {
			h := append([]string{"Cookie", "apexx_shopper=" + token}, hdr...)
			if len(hdr) == 0 {
				h = append(h, "Origin", e.srv.URL)
			}
			return e.do(method, path, "", body, h...)
		}
	}
	asAdmin, asBuyer := session("admin@pagasola.example"), session("compras@pagasola.example")
	if st, _ := asBuyer("PUT", "/s/store-a/api/account/company/autopay", m{"paymentMethod": "pm_card_visa"}); st != 403 {
		t.Fatalf("a buyer saved the company card = %d", st)
	}
	if st, _ := asAdmin("PUT", "/s/store-a/api/account/company/autopay", m{"paymentMethod": "pm_card_visa"}, "Origin", "https://evil.example"); st != 403 {
		t.Fatalf("cross-site save = %d", st)
	}
	if st, _ := asAdmin("PUT", "/s/store-a/api/account/company/autopay", m{"paymentMethod": "pm_card_chargeDeclined"}); st != 400 {
		t.Fatalf("declined card saved = %d", st)
	}
	if st, b := asAdmin("PUT", "/s/store-a/api/account/company/autopay", m{"paymentMethod": "pm_card_visa"}); st != 200 || b["on"] != true {
		t.Fatalf("save card = %d %v", st, b)
	}
	// Not due yet: nothing is charged.
	if n, _ := e.orders.RunAutopay(ctx); n != 0 {
		t.Fatalf("charged before the due date: %d", n)
	}
	// Due date passed: one charge, and the balance is paid.
	if _, err := e.db.System.Exec(ctx, `UPDATE company_entries SET due_at=now() - interval '1 hour' WHERE company_id=$1 AND kind='charge'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE companies SET autopay_last_try=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := e.orders.RunAutopay(ctx); err != nil || n != 1 {
		t.Fatalf("autopay = %d %v", n, err)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE companies SET autopay_last_try=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.orders.RunAutopay(ctx); n != 0 {
		t.Fatal("charged twice")
	}
	got := e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)["company"].(map[string]any)
	if got["balance"].(float64) != 0 || got["overdue"].(float64) != 0 {
		t.Fatalf("after autopay: balance %v overdue %v", got["balance"], got["overdue"])
	}
	// A card that stops passing: three declines turn it off and tell the admin.
	if res, _ := e.orders.Checkout(ctx, "store-a", "storefront:web", "ap-2", req); res.Status != 201 {
		t.Fatalf("second order = %d", res.Status)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE company_entries SET due_at=now() - interval '1 hour' WHERE company_id=$1 AND kind='charge'`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.System.Exec(ctx, `UPDATE companies SET autopay_method='pm_card_chargeDeclined' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		_, _ = e.db.System.Exec(ctx, `UPDATE companies SET autopay_last_try=NULL WHERE id=$1`, id)
		if _, err := e.orders.RunAutopay(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ap := e.must(200, "GET", "/v1/companies/"+id+"/autopay", e.adminA, nil)
	if ap["on"] != false || ap["failures"].(float64) != 3 || count("company_autopay_failed", adminHash) != 3 {
		t.Fatalf("after declines: %v, emails %d", ap, count("company_autopay_failed", adminHash))
	}
	if b := e.must(200, "GET", "/v1/companies/"+id, e.adminA, nil)["company"].(map[string]any); b["overdue"].(float64) <= 0 {
		t.Fatal("a declined charge lowered the balance")
	}
	// The store turns it off; another store cannot see it.
	if st, _ := e.do("GET", "/v1/companies/"+id+"/autopay", e.adminB, nil); st != 404 {
		t.Fatalf("store B reads autopay = %d", st)
	}
	e.must(204, "DELETE", "/v1/companies/"+id+"/autopay", e.adminA, nil)
	var unbalanced int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM (SELECT tx_id FROM ledger_entries GROUP BY tx_id HAVING sum(amount) <> 0) x`).Scan(&unbalanced)
	if unbalanced != 0 {
		t.Fatalf("unbalanced ledger transactions: %d", unbalanced)
	}
}
