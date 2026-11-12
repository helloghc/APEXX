package notify

import (
	"strings"
	"testing"
)

// What a message carries that works as a key never stays readable in the log,
// and the caller's data is not changed behind its back.
func TestRedactForLog(t *testing.T) {
	gift := Data{Vars: map[string]string{"tarjeta.codigo": "ABCD-EFGH-JKLM-NPQR", "tarjeta.saldo": "$500.00 MXN"}}
	orig := gift.Vars
	if !redactForLog(TplGiftCard, &gift) || gift.Vars["tarjeta.codigo"] != "••••••" || gift.Vars["tarjeta.saldo"] != "$500.00 MXN" {
		t.Fatalf("gift = %v", gift.Vars)
	}
	if orig["tarjeta.codigo"] != "ABCD-EFGH-JKLM-NPQR" {
		t.Fatal("redaction changed the sender's map")
	}
	code := Data{Vars: map[string]string{"codigo": "482913"}}
	if !redactForLog(TplAccessCode, &code) || code.Vars["codigo"] != "••••••" {
		t.Fatalf("code = %v", code.Vars)
	}
	for _, tpl := range []string{TplTeamInvite, TplPurchaseApproval, TplQuoteOffered, TplSubscriptionCreated} {
		d := Data{ActionURL: "https://tienda.example/invitacion/secreto"}
		if !redactForLog(tpl, &d) || strings.Contains(d.ActionURL, "secreto") {
			t.Fatalf("%s link kept: %s", tpl, d.ActionURL)
		}
	}
	order := Data{ActionURL: "https://tienda.example/pedido/1042", Vars: map[string]string{"pedido.numero": "1042"}}
	if redactForLog(TplOrderConfirmed, &order) || order.ActionURL == "" {
		t.Fatalf("order message changed: %v", order)
	}
	// Rendered with the redacted data, the code is gone from subject and body.
	r := RenderLang("Tu código: {{codigo}}", Blocks{Heading: "Código", Body: "Usa {{codigo}}"}, code, Brand{}, false, "es")
	if strings.Contains(r.Subject+r.HTML+r.Text, "482913") {
		t.Fatalf("code in render: %s", r.Subject)
	}
}
