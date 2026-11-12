package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/customers"
)

// Email campaigns: only customers with consent get them, once; each has an
// unsubscribe link that works; orders from the link are attributed.
func TestEmailCampaigns(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	yes, no := true, false
	for _, c := range []struct {
		email string
		ok    *bool
	}{{"si1@cliente.example", &yes}, {"si2@cliente.example", &yes}, {"no@cliente.example", &no}} {
		email := c.email
		if _, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email, MarketingConsent: c.ok}); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := e.do("POST", "/v1/email-campaigns", e.adminA, m{"name": "x", "subject": "y", "link": "https://evil.example"}); st != 400 {
		t.Fatalf("external link accepted: %d", st)
	}
	c := e.must(200, "POST", "/v1/email-campaigns", e.adminA, m{"name": "Buen Fin", "subject": "Llegó el Buen Fin", "link": "/c",
		"blocks": m{"heading": "Hola {{cliente.nombre}}", "body": "Todo con 20%.", "cta": "Ver ofertas"}})
	id := c["id"].(string)
	if st, _ := e.do("POST", "/v1/email-campaigns/"+id+"/send", e.adminB, nil); st != 404 {
		t.Fatalf("store B sent store A's campaign: %d", st)
	}
	e.must(202, "POST", "/v1/email-campaigns/"+id+"/send", e.adminA, nil)
	for i := 0; i < 2; i++ { // running twice sends nothing twice
		if _, err := e.server.RunCampaigns(ctx); err != nil {
			t.Fatal(err)
		}
		_, _ = e.db.System.Exec(ctx, `UPDATE email_campaigns SET status='scheduled' WHERE id=$1`, id)
	}
	_, _ = e.db.System.Exec(ctx, `UPDATE email_campaigns SET status='sent' WHERE id=$1`, id)
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM notification_messages WHERE tenant_id='store-a' AND template='campaign'`).Scan(&n)
	if n != 2 {
		t.Fatalf("campaign messages = %d, want 2 (consent only, once)", n)
	}
	// The message carries the unsubscribe link; following it turns consent off.
	var mid string
	var payload []byte
	_ = e.db.System.QueryRow(ctx, `SELECT id, payload FROM notification_messages WHERE tenant_id='store-a' AND template='campaign' LIMIT 1`).Scan(&mid, &payload)
	plain, _ := e.keys.Decrypt("store-a", payload, mid)
	text := strings.ReplaceAll(string(plain), `\u0026`, "&")
	i := strings.Index(text, "/baja?c=")
	if i < 0 || !strings.Contains(text, "cmp="+id) {
		t.Fatalf("message without unsubscribe or tracked link: %.400s", text)
	}
	unsub := text[i : i+strings.IndexAny(text[i:], `"\`)]
	unsub = strings.ReplaceAll(unsub, `&`, "&")
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	res, err := http.Get(e.srv.URL + "/s/store-a/main" + unsub)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "ya no te enviaremos") {
		i := strings.Index(string(body), "<h1>")
		t.Fatalf("unsubscribe page %s: %d %.300s", unsub, res.StatusCode, string(body)[max(i, 0):])
	}
	var consents int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM customers WHERE tenant_id='store-a' AND marketing_consent`).Scan(&consents)
	if consents != 1 {
		t.Fatalf("consents after unsubscribe = %d, want 1", consents)
	}
	if r, _ := http.Get(e.srv.URL + "/s/store-a/main" + strings.Replace(unsub, "&t=", "&t=00", 1)); r != nil {
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !strings.Contains(string(b), "no es válido") {
			t.Fatal("a forged unsubscribe link worked")
		}
	}
	// An order from the campaign link is attributed; a made-up campaign is not.
	for i, cmp := range []string{id, "cmp_00000000"} {
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "campaign": cmp,
			"customer": m{"email": "comprador@cliente.example", "name": "Comprador"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "campaign-order-"+string(rune('a'+i)))
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
	}
	time.Sleep(10 * time.Millisecond)
	list := e.must(200, "GET", "/v1/email-campaigns", e.adminA, nil)["items"].([]any)
	got := list[0].(map[string]any)
	if got["orders"].(float64) != 1 || got["revenue"].(float64) <= 0 || got["sentCount"].(float64) != 2 {
		t.Fatalf("campaign stats = %v", got)
	}
	if l := e.must(200, "GET", "/v1/email-campaigns", e.adminB, nil)["items"].([]any); len(l) != 0 {
		t.Fatalf("store B sees store A's campaigns: %v", l)
	}
}
