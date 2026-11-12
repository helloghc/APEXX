package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Gifts: wrapping is a charge with its reason, the card message is kept on
// the order and validated; a registry is shared by link without its owner's
// address, guests send gifts there, what was bought is derived from paid
// orders (a cancellation puts the gift back) and other stores see nothing.
func TestGiftsAndRegistries(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	if st, _ := e.do("PUT", "/v1/gift-options", e.adminA, m{"enabled": true, "wrapPrice": -1}); st != 400 {
		t.Fatalf("negative wrap price accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/gift-options", e.adminA, m{"enabled": true, "wrapPrice": 3500, "messageMax": 40, "receipt": true, "registries": true})
	e.drain()
	if st, _ := e.do("PUT", "/v1/gift-options", e.sfA, m{"enabled": false}); st != 403 {
		t.Fatalf("storefront key changed gift options: %d", st)
	}
	item := []m{{"sku": "S1", "qty": 1}}
	_, plain := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": item})
	st, wrapped := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": item, "gift": m{"wrap": true, "message": "¡Feliz cumpleaños!", "hidePrices": true}})
	grand := func(r m) float64 { return r["totals"].(map[string]any)["grand"].(map[string]any)["amount"].(float64) }
	if st != 200 || grand(wrapped)-grand(plain) != 3500 || wrapped["gift"].(map[string]any)["hidePrices"] != true {
		t.Fatalf("wrapping simulate = %d %v (plain %v)", st, wrapped["totals"], plain["totals"])
	}
	if st, r := e.do("POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": item, "gift": m{"message": strings.Repeat("x", 41)}}); st != 400 || !strings.Contains(r["errors"].([]any)[0].(map[string]any)["field"].(string), "gift") {
		t.Fatalf("long message = %d %v", st, r)
	}

	// The shopper opens a registry with an address and wishes for two S1.
	email := "novios@cliente.example"
	c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, c.ID)
	_ = e.server.Customers.SetPassword(ctx, "store-a", c.ID, "Una-clave-larga-2026", "")
	token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	as := func(method, path string, body any) (int, m) {
		return e.do(method, path, "", body, "Cookie", "apexx_shopper="+token, "Origin", e.srv.URL)
	}
	if st, _ := as("POST", "/s/store-a/api/account/registries", m{"title": "AB"}); st != 400 {
		t.Fatalf("short title accepted: %d", st)
	}
	const secret = "Calle Secreta 123, Col. Centro"
	st, reg := as("POST", "/s/store-a/api/account/registries", m{"title": "Boda Ana y Luis", "kind": "boda", "eventDate": "2027-03-20", "listed": true,
		"shipTo": m{"name": "Ana", "address": secret, "postalCode": "53129"}})
	if st != 201 || reg["handle"] == "" || reg["shipTo"].(map[string]any)["address"] != secret {
		t.Fatalf("create registry = %d %v", st, reg)
	}
	id, handle := reg["id"].(string), reg["handle"].(string)
	if st, _ := as("PUT", "/s/store-a/api/account/registries/"+id+"/items", m{"sku": "NOPE", "wanted": 1}); st != 400 {
		t.Fatalf("unknown sku accepted: %d", st)
	}
	if st, r := as("PUT", "/s/store-a/api/account/registries/"+id+"/items", m{"sku": "S1", "wanted": 2}); st != 200 || len(r["items"].([]any)) != 1 {
		t.Fatalf("add item = %d %v", st, r)
	}
	// Without a session or from another site nothing changes.
	if st, _ := e.do("PUT", "/s/store-a/api/account/registries/"+id+"/items", "", m{"sku": "S1", "wanted": 5}); st != 401 {
		t.Fatalf("anonymous item edit: %d", st)
	}
	if st, _ := e.do("PUT", "/s/store-a/api/account/registries/"+id+"/items", "", m{"sku": "S1", "wanted": 5}, "Cookie", "apexx_shopper="+token, "Origin", "https://evil.example"); st != 403 {
		t.Fatalf("cross-site item edit: %d", st)
	}
	// Guests find it by name and see the list, never the address or the owner.
	st, found := e.do("GET", "/s/store-a/api/registries?q=ana%20y%20luis", "", nil)
	if st != 200 || len(found["items"].([]any)) != 1 {
		t.Fatalf("search = %d %v", st, found)
	}
	if f := found["items"].([]any)[0].(map[string]any); f["customerId"] != nil || f["shipTo"] != nil {
		t.Fatalf("search leaks owner data: %v", f)
	}
	resp, err := http.Get(e.srv.URL + "/s/store-a/main/mesa/" + handle)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Boda Ana y Luis") || strings.Contains(string(page), "Secreta") || !strings.Contains(string(page), `data-registry-add="S1"`) {
		t.Fatalf("registry page = %d", resp.StatusCode)
	}
	if !strings.Contains(string(page), "noindex") {
		t.Fatal("registry page is indexable")
	}

	// A guest sends one S1 to the couple, wrapped: no address typed.
	buy := m{"channel": "mx", "items": item, "registry": handle, "shipToRegistry": true, "gift": m{"wrap": true, "message": "Con cariño"},
		"customer": m{"email": "invitado@cliente.example", "name": "Invitado"}, "paymentMethod": "pm_card_visa"}
	st, o := e.do("POST", "/s/store-a/api/checkout", "", buy, "Idempotency-Key", "regalo-0001")
	if st != 201 {
		t.Fatalf("registry checkout = %d %v", st, o)
	}
	if strings.Contains(toJSON(o), "Secreta") {
		t.Fatal("the guest's response carries the registry address")
	}
	full := e.must(200, "GET", "/v1/orders/"+o["id"].(string), e.adminA, nil)
	if full["registryId"] != id || full["gift"].(map[string]any)["message"] != "Con cariño" || !strings.Contains(full["customer"].(map[string]any)["address"].(string), secret) {
		t.Fatalf("order = gift %v registry %v customer %v", full["gift"], full["registryId"], full["customer"])
	}
	if fees := toJSON(full["feeLines"]); !strings.Contains(fees, "gift-wrap") {
		t.Fatalf("order fees = %s", fees)
	}
	e.drain()
	bought := func() float64 {
		r := e.must(200, "GET", "/v1/gift-registries/"+id, e.adminA, nil)
		return r["registry"].(map[string]any)["items"].([]any)[0].(map[string]any)["bought"].(float64)
	}
	if b := bought(); b != 1 {
		t.Fatalf("bought after gift = %v", b)
	}
	// Cancelling the gift puts it back on the list.
	e.must(200, "POST", "/v1/orders/"+o["id"].(string)+"/cancel", e.adminA, m{"reason": "prueba"})
	e.drain()
	if b := bought(); b != 0 {
		t.Fatalf("bought after cancel = %v", b)
	}
	// Another store sees nothing; a closed registry takes no more gifts.
	if st, _ := e.do("GET", "/v1/gift-registries/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("store B read the registry: %d", st)
	}
	if st, r := e.do("GET", "/s/store-b/api/registries?q=ana%20y%20luis", "", nil); st == 200 && len(r["items"].([]any)) > 0 {
		t.Fatalf("store B found store A's registry: %v", r)
	}
	e.must(200, "PUT", "/v1/gift-registries/"+id+"/status", e.adminA, m{"status": "closed"})
	if st, r := e.do("POST", "/s/store-a/api/checkout", "", buy, "Idempotency-Key", "regalo-0002"); st != 400 || !strings.Contains(toJSON(r), "registry") {
		t.Fatalf("closed registry checkout = %d %v", st, r)
	}
	if resp, _ := http.Get(e.srv.URL + "/s/store-a/main/mesa/" + handle); resp.StatusCode != 404 {
		t.Fatalf("closed registry page = %d", resp.StatusCode)
	}
}
