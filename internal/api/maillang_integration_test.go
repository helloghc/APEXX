package api

import (
	"context"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/notify"
)

// Buyer emails in the buyer's language: an order placed in the English store
// gets its confirmation in English (subject, text, totals and the page
// language), a Spanish order stays in Spanish, a template the store rewrote
// keeps the store's words, and the access code follows the language of the
// page that asked for it.
func TestBuyerEmailsInTheirLanguage(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.must(200, "PUT", "/v1/settings/languages", e.adminA, m{"enabled": []string{"en", "ja"}})
	e.drain()
	buy := func(lang, key, email string) string {
		h := []string{"Idempotency-Key", key, "Origin", e.srv.URL}
		if lang != "" {
			h = append(h, "X-Apexx-Lang", lang)
		}
		st, o := e.do("POST", "/s/store-a/api/checkout", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
			"customer": m{"email": email, "name": "Ana"}, "paymentMethod": "pm_card_visa"}, h...)
		if st != 201 {
			t.Fatalf("checkout %s = %d %v", lang, st, o)
		}
		return o["id"].(string)
	}
	mail := func(order, tpl string) notify.Entry {
		if _, err := e.server.Notify.Send(ctx); err != nil {
			t.Fatal(err)
		}
		var id string
		_ = e.db.System.QueryRow(ctx, `SELECT id FROM notification_messages WHERE tenant_id='store-a' AND order_id=$1 AND template=$2`, order, tpl).Scan(&id)
		en, err := e.server.Notify.Get(ctx, "store-a", id, true)
		if err != nil || en.Status != "sent" {
			t.Fatalf("mail %s %s: %v %+v", order, tpl, err, en.Status)
		}
		return en
	}
	enOrder := buy("en", "mail-lang-en-1", "ana.en@example.com")
	m1 := mail(enOrder, notify.TplOrderConfirmed)
	if !strings.HasPrefix(m1.Subject, "We've confirmed your order #") || !strings.Contains(m1.HTML, `<html lang="en">`) ||
		!strings.Contains(m1.HTML, "Thank you for your purchase, Ana!") || !strings.Contains(m1.Text, "Shipping: ") || strings.Contains(m1.Text, "Recibimos tu pago") {
		t.Fatalf("english confirmation: %s\n%s", m1.Subject, m1.Text)
	}
	var lang string
	_ = e.db.System.QueryRow(ctx, `SELECT c.lang FROM customers c JOIN orders o ON o.customer_id=c.id AND o.tenant_id=c.tenant_id WHERE o.id=$1`, enOrder).Scan(&lang)
	if lang != "en" {
		t.Fatalf("customer language = %q", lang)
	}
	esOrder := buy("", "mail-lang-es-1", "ana.es@example.com")
	if m2 := mail(esOrder, notify.TplOrderConfirmed); !strings.HasPrefix(m2.Subject, "Confirmamos tu pedido #") || !strings.Contains(m2.HTML, `<html lang="es">`) {
		t.Fatalf("spanish confirmation: %s", m2.Subject)
	}
	// A language the store did not turn on stays in Spanish.
	frOrder := buy("fr", "mail-lang-fr-1", "ana.fr@example.com")
	if m3 := mail(frOrder, notify.TplOrderConfirmed); !strings.HasPrefix(m3.Subject, "Confirmamos tu pedido #") {
		t.Fatalf("language not enabled: %s", m3.Subject)
	}
	// The store rewrote the template: its words are kept; its own translation of
	// a phrase is used.
	e.must(200, "PUT", "/v1/settings/languages", e.adminA, m{"enabled": []string{"en", "ja"}, "own": m{"en": m{"¡Gracias, de corazón!": "Thank you, truly!"}}})
	e.drain()
	e.must(200, "PUT", "/v1/notifications/templates/"+notify.TplOrderConfirmed, e.adminA, m{"subject": "Pedido #{{pedido.numero}} en camino a tu casa",
		"blocks": m{"heading": "¡Gracias, de corazón!", "body": "Texto propio de la tienda.", "cta": "Ver mi pedido", "footer": ""}, "enabled": true})
	jaOrder := buy("en", "mail-lang-en-2", "ana.en2@example.com")
	m4 := mail(jaOrder, notify.TplOrderConfirmed)
	if m4.Subject == "" || !strings.Contains(m4.Subject, "Pedido #") || !strings.Contains(m4.Text, "Thank you, truly!") || !strings.Contains(m4.Text, "Texto propio de la tienda.") {
		t.Fatalf("custom template: %s\n%s", m4.Subject, m4.Text)
	}
	// The access code follows the page that asked for it.
	if st, _ := e.do("POST", "/s/store-a/api/account/code", "", m{"email": "codigo.ja@example.com"}, "Origin", e.srv.URL, "X-Apexx-Lang", "ja"); st >= 300 {
		t.Fatalf("code request = %d", st)
	}
	if _, err := e.server.Notify.Send(ctx); err != nil {
		t.Fatal(err)
	}
	var subj string
	_ = e.db.System.QueryRow(ctx, `SELECT id FROM notification_messages WHERE tenant_id='store-a' AND template=$1 ORDER BY created_at DESC LIMIT 1`, notify.TplAccessCode).Scan(&subj)
	code, _ := e.server.Notify.Get(ctx, "store-a", subj, true)
	if !strings.Contains(code.Subject, "ログインコード") {
		t.Fatalf("access code in japanese: %q", code.Subject)
	}
}
