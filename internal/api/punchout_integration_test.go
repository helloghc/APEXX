package api

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/cxml"
)

// Punchout: the purchasing system authenticates with identity and secret,
// gets a single-use start link, the buyer shops with company prices, the cart
// goes back as a PunchOutOrderMessage and the purchase order becomes one
// order on credit with its PO number, however many times it is sent. Wrong
// secrets, changed prices, other stores and insecure return URLs are refused.
func TestPunchout(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Compras SA", "terms": "net30", "creditLimit": 50000000, "discountBps": 1000, "buyerEmails": []string{"compras@punch.example"}})["id"].(string)
	if st, _ := e.do("PUT", "/v1/companies/"+co+"/punchout", e.adminA, m{"enabled": true, "identity": "AN0001", "buyerEmail": "nadie@punch.example"}); st != 400 {
		t.Fatalf("buyer outside the company = %d", st)
	}
	cfg := e.must(200, "PUT", "/v1/companies/"+co+"/punchout", e.adminA, m{"enabled": true, "identity": "AN0001", "buyerEmail": "compras@punch.example"})
	secret, _ := cfg["secret"].(string)
	if len(secret) < 32 || !strings.HasSuffix(cfg["setupUrl"].(string), "/punchout/store-a/setup") {
		t.Fatalf("config = %v", cfg)
	}
	if g := e.must(200, "GET", "/v1/companies/"+co+"/punchout", e.adminA, nil); g["secret"] != nil || g["hasSecret"] != true {
		t.Fatalf("the secret is shown again: %v", g)
	}
	if st, _ := e.do("GET", "/v1/companies/"+co+"/punchout", e.adminB, nil); st != 404 {
		t.Fatalf("store B read the punchout: %d", st)
	}
	// Saving without regenerating keeps the secret.
	e.must(200, "PUT", "/v1/companies/"+co+"/punchout", e.adminA, m{"enabled": true, "identity": "AN0001", "buyerEmail": "compras@punch.example"})

	post := func(path, body string) (int, string) {
		resp, err := http.Post(e.srv.URL+path, "text/xml", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	header := func(id, sec string) string {
		return `<Header><From><Credential domain="NetworkID"><Identity>` + id + `</Identity></Credential></From><To><Credential domain="NetworkID"><Identity>store-a</Identity></Credential></To><Sender><Credential domain="NetworkID"><Identity>` + id + `</Identity><SharedSecret>` + sec + `</SharedSecret></Credential><UserAgent>ERP</UserAgent></Sender></Header>`
	}
	setupDoc := func(sec, ret string) string {
		return `<?xml version="1.0"?><!DOCTYPE cXML SYSTEM "http://xml.cxml.org/schemas/cXML/1.2.014/cXML.dtd"><cXML payloadID="1@erp" timestamp="2026-01-01T00:00:00Z">` + header("AN0001", sec) +
			`<Request><PunchOutSetupRequest operation="create"><BuyerCookie>cookie-123</BuyerCookie><BrowserFormPost><URL>` + ret + `</URL></BrowserFormPost></PunchOutSetupRequest></Request></cXML>`
	}
	if st, b := post("/punchout/store-a/setup", setupDoc("malo", "https://erp.example/return")); st != 401 || !strings.Contains(b, `code="401"`) {
		t.Fatalf("wrong secret = %d %s", st, b)
	}
	if st, _ := post("/punchout/store-b/setup", setupDoc(secret, "https://erp.example/return")); st != 401 {
		t.Fatalf("the identity worked in store B: %d", st)
	}
	if st, _ := post("/punchout/store-a/setup", setupDoc(secret, "http://erp.example/return")); st != 400 {
		t.Fatalf("insecure return accepted: %d", st)
	}
	if st, _ := post("/punchout/store-a/setup", `<cXML><!ENTITY x SYSTEM "file:///etc/passwd"></cXML>`); st != 400 {
		t.Fatalf("broken document = %d", st)
	}
	st, b := post("/punchout/store-a/setup", setupDoc(secret, "https://erp.example/return"))
	d, err := cxml.Parse(strings.NewReader(b))
	_ = d
	i := strings.Index(b, "<URL>")
	if st != 200 || err != nil || i < 0 {
		t.Fatalf("setup = %d %s", st, b)
	}
	start := b[i+5 : strings.Index(b, "</URL>")]
	start = start[strings.Index(start, "/s/"):]
	// The start link opens a shopper session once.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noRedirect.Get(e.srv.URL + start)
	if err != nil || resp.StatusCode != 303 {
		t.Fatalf("start = %v %v", err, resp)
	}
	var shopper string
	for _, c := range resp.Cookies() {
		if c.Name == "apexx_shopper" {
			shopper = c.Value
		}
	}
	resp.Body.Close()
	if shopper == "" {
		t.Fatal("no shopper session")
	}
	if again, _ := noRedirect.Get(e.srv.URL + start); again.StatusCode != 410 {
		t.Fatalf("start link reused: %d", again.StatusCode)
	}
	// A normal shopper session cannot transfer.
	if st, _ := e.do("POST", "/s/store-a/api/punchout/transfer", "", m{"items": []m{{"sku": "S1", "qty": 2}}}, "Origin", e.srv.URL); st != 401 {
		t.Fatalf("transfer without session = %d", st)
	}
	st, tr := e.do("POST", "/s/store-a/api/punchout/transfer", "", m{"items": []m{{"sku": "S1", "qty": 2}}}, "Cookie", "apexx_shopper="+shopper, "Origin", e.srv.URL)
	if st != 200 || tr["next"] != "/s/store-a/punchout/return" {
		t.Fatalf("transfer = %d %v", st, tr)
	}
	// The return page posts the message to the purchasing system, and its
	// policy allows posting there and nowhere else.
	req, _ := http.NewRequest("GET", e.srv.URL+"/s/store-a/punchout/return", nil)
	req.Header.Set("Cookie", "apexx_shopper="+shopper)
	rr, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	csp := rr.Header.Get("Content-Security-Policy")
	if rr.StatusCode != 200 || !strings.Contains(csp, "form-action https://erp.example;") || !strings.Contains(string(page), `action="https://erp.example/return"`) {
		t.Fatalf("return = %d %s %s", rr.StatusCode, csp, page)
	}
	i = strings.Index(string(page), `name="cxml-urlencoded" value="`)
	msg := html.UnescapeString(strings.SplitN(string(page)[i+30:], `"`, 2)[0])
	if !strings.Contains(msg, "<BuyerCookie>cookie-123</BuyerCookie>") || !strings.Contains(msg, "<SupplierPartID>S1</SupplierPartID>") {
		t.Fatalf("message = %s", msg)
	}
	if rr, _ := http.Get(e.srv.URL + "/s/store-a/punchout/return"); rr.StatusCode != 404 {
		t.Fatalf("return page without session = %d", rr.StatusCode)
	}
	// The company price: list price minus 10 %.
	list := e.must(200, "POST", "/v1/carts/simulate", e.adminA, m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 1}}})["lines"].([]any)[0].(map[string]any)["unit"].(map[string]any)["amount"].(float64)
	want := int64(list) * 9 / 10
	unit := fmt.Sprintf("%d.%02d", want/100, want%100)
	if !strings.Contains(msg, ">"+unit+"</Money></UnitPrice>") {
		t.Fatalf("unit %s not in %s", unit, msg)
	}
	order := func(po, price string) (int, string) {
		return post("/punchout/store-a/order", `<?xml version="1.0"?><cXML payloadID="2@erp" timestamp="2026-01-01T00:00:00Z">`+header("AN0001", secret)+
			`<Request><OrderRequest><OrderRequestHeader orderID="`+po+`" type="new"><Total><Money currency="MXN">0</Money></Total><ShipTo><Address><Name>Almacén</Name><PostalAddress><Street>Calle 1</Street><City>CDMX</City><PostalCode>53129</PostalCode></PostalAddress></Address></ShipTo></OrderRequestHeader>`+
			`<ItemOut quantity="2" lineNumber="1"><ItemID><SupplierPartID>S1</SupplierPartID></ItemID><ItemDetail><UnitPrice><Money currency="MXN">`+price+`</Money></UnitPrice><Description>x</Description></ItemDetail></ItemOut></OrderRequest></Request></cXML>`)
	}
	if st, b := order("PO-1", "1.00"); st != 409 || !strings.Contains(b, "Price changed") {
		t.Fatalf("changed price = %d %s", st, b)
	}
	for k := 0; k < 3; k++ {
		if st, b := order("PO-1", unit); st != 200 || !strings.Contains(b, `code="200"`) {
			t.Fatalf("order %d = %d %s", k, st, b)
		}
	}
	var n int
	var po string
	var total int64
	_ = e.db.System.QueryRow(ctx, `SELECT count(*), max(po_number), max(grand_total) FROM orders o WHERE tenant_id='store-a' AND EXISTS (SELECT 1 FROM company_entries c WHERE c.tenant_id=o.tenant_id AND c.order_id=o.id AND c.company_id=$1)`, co).Scan(&n, &po, &total)
	if n != 1 || po != "PO-1" {
		t.Fatalf("orders = %d po %q", n, po)
	}
	c := e.must(200, "GET", "/v1/companies/"+co, e.adminA, nil)["company"].(map[string]any)
	if int64(c["balance"].(float64)) != total {
		t.Fatalf("balance %v, order %d", c["balance"], total)
	}
	// Disabled: nothing gets in.
	e.must(200, "PUT", "/v1/companies/"+co+"/punchout", e.adminA, m{"enabled": false, "identity": "AN0001", "buyerEmail": "compras@punch.example"})
	if st, _ := order("PO-2", unit); st != 401 {
		t.Fatalf("disabled punchout = %d", st)
	}
	var audits int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND action IN ('company.punchout','punchout.setup')`).Scan(&audits)
	if audits < 4 {
		t.Fatalf("audit entries = %d", audits)
	}
}
