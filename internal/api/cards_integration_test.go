package api

import (
	"context"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// A signed-in customer keeps a card at checkout and pays with it next time;
// nobody else can use it, and it can be removed.
func TestSavedCards(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	login := func(email string) string {
		c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, c.ID)
		if err := e.server.Customers.SetPassword(ctx, "store-a", c.ID, "Una-clave-larga-2026", ""); err != nil {
			t.Fatal(err)
		}
		tok, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	ana, beto := login("ana@tarjetas.example"), login("beto@tarjetas.example")
	buy := func(tok, key string, extra m) (int, m) {
		b := m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "customer": m{"email": "ana@tarjetas.example", "name": "Ana"}, "paymentMethod": "pm_card_visa"}
		for k, v := range extra {
			b[k] = v
		}
		h := []string{"Idempotency-Key", key, "Origin", e.srv.URL}
		if tok != "" {
			h = append(h, "Cookie", "apexx_shopper="+tok)
		}
		return e.do("POST", "/s/store-a/api/checkout", "", b, h...)
	}
	// A guest asking to save: the purchase goes through, nothing is kept.
	if st, _ := buy("", "card-key-0", m{"saveCard": true}); st != 201 {
		t.Fatalf("guest checkout = %d", st)
	}
	if st, b := buy(ana, "card-key-1", m{"saveCard": true}); st != 201 {
		t.Fatalf("checkout saving the card = %d %v", st, b)
	}
	cards := func(tok string) []any {
		st, b := e.do("GET", "/s/store-a/api/account/cards", "", nil, "Cookie", "apexx_shopper="+tok)
		if st != 200 {
			t.Fatalf("cards = %d", st)
		}
		return b["items"].([]any)
	}
	list := cards(ana)
	if len(list) != 1 || list[0].(map[string]any)["last4"] != "4242" {
		t.Fatalf("Ana's cards = %v", list)
	}
	card := list[0].(map[string]any)["id"].(string)
	var clear int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM customer_cards WHERE payment_method NOT LIKE 'pm_%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("something other than a provider token was stored")
	}
	// Ana pays with it; Beto cannot; a guest cannot.
	if st, b := buy(ana, "card-key-2", m{"savedCard": card, "paymentMethod": "pm_saved"}); st != 201 {
		t.Fatalf("pay with the kept card = %d %v", st, b)
	}
	if st, _ := buy(beto, "card-key-3", m{"savedCard": card, "paymentMethod": "pm_saved"}); st != 400 {
		t.Fatalf("Beto used Ana's card: %d", st)
	}
	if st, _ := buy("", "card-key-4", m{"savedCard": card, "paymentMethod": "pm_saved"}); st != 401 {
		t.Fatalf("a guest used a kept card: %d", st)
	}
	if len(cards(beto)) != 0 {
		t.Fatal("Beto sees cards")
	}
	if st, _ := e.do("GET", "/s/store-b/api/account/cards", "", nil, "Cookie", "apexx_shopper="+ana); st != 401 {
		t.Fatalf("store B lists store A's cards: %d", st)
	}
	// Forgetting it.
	if st, _ := e.do("DELETE", "/s/store-a/api/account/cards/"+card, "", nil, "Cookie", "apexx_shopper="+beto, "Origin", e.srv.URL); st != 404 {
		t.Fatalf("Beto removed Ana's card: %d", st)
	}
	if st, _ := e.do("DELETE", "/s/store-a/api/account/cards/"+card, "", nil, "Cookie", "apexx_shopper="+ana, "Origin", e.srv.URL); st != 204 {
		t.Fatalf("remove = %d", st)
	}
	if st, _ := buy(ana, "card-key-5", m{"savedCard": card, "paymentMethod": "pm_saved"}); st != 400 {
		t.Fatalf("paid with a removed card: %d", st)
	}
	// One-step purchase data needs a card and an address.
	if st, _ := e.do("GET", "/s/store-a/api/account/express", "", nil, "Cookie", "apexx_shopper="+ana); st != 204 {
		t.Fatalf("express without a card = %d", st)
	}
}
