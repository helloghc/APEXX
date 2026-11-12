package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Reorder lists and quick order pages.
func TestReorderListsAndQuickOrder(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	// A buyer of a company, signed in.
	if st, b := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "lista@empresa.example", "name": "Lista"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "reorder-key-1"); st != 201 {
		t.Fatalf("checkout = %d %v", st, b)
	}
	e.drain()
	e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Empresa Listas", "buyerEmails": []string{"lista@empresa.example", "otro@empresa.example"}})
	cid := e.must(200, "GET", "/v1/customers?q=lista@empresa.example", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)
	// The buyer already confirmed their email (they signed in with a code once).
	if _, err := e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	if err := e.server.Customers.SetPassword(ctx, "store-a", cid, "Una-clave-larga-2026", ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", "lista@empresa.example", "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	as := func(method, path string, body any) (int, m) {
		return e.do(method, path, "", body, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL)
	}
	if st, _ := e.do("POST", "/s/store-a/api/lists", "", m{"name": "x", "items": []m{{"sku": "S1", "qty": 1}}}, "Origin", e.srv.URL); st != 401 {
		t.Fatalf("list without signing in = %d", st)
	}
	if st, _ := as("POST", "/s/store-a/api/lists", m{"name": "Mensual", "items": []m{{"sku": "NOPE", "qty": 1}}}); st != 400 {
		t.Fatalf("unknown SKU in a list = %d", st)
	}
	st, b := as("POST", "/s/store-a/api/lists", m{"name": "Mensual", "items": []m{{"sku": "S1", "qty": 6}, {"sku": "S2", "qty": 2}}})
	if st != 200 {
		t.Fatalf("save list = %d %v", st, b)
	}
	listID := b["id"].(string)
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/s/store-a/main/cuenta?ver=listas", nil)
	req.AddCookie(&http.Cookie{Name: "apexx_shopper", Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	_, _ = bufCopy(buf, resp)
	if !strings.Contains(buf.String(), "Mensual") || !strings.Contains(buf.String(), `data-sku="S1" data-qty="6"`) {
		t.Fatal("the list is not on the account page")
	}
	// Another store and a stranger cannot touch it.
	if st, _ := e.do("DELETE", "/s/store-b/api/lists/"+listID, "", nil, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL); st != 401 && st != 404 {
		t.Fatalf("deleted from another store: %d", st)
	}
	if st, _ := as("DELETE", "/s/store-a/api/lists/"+listID, nil); st != 204 {
		t.Fatalf("delete own list = %d", st)
	}
	// The quick order page is there.
	if _, qp, _ := e.raw(http.MethodGet, "/s/store-a/main/pedido-rapido", "", nil, ""); !strings.Contains(string(qp), "data-quick-order") {
		t.Fatal("quick order page missing")
	}
}

func bufCopy(dst *strings.Builder, resp *http.Response) (int64, error) {
	defer resp.Body.Close()
	b := make([]byte, 32*1024)
	var n int64
	for {
		k, err := resp.Body.Read(b)
		dst.Write(b[:k])
		n += int64(k)
		if err != nil {
			return n, nil
		}
	}
}
