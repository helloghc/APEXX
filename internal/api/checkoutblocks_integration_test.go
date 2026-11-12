package api

import (
	"fmt"
	"strings"
	"testing"
)

// Checkout blocks: drawn by Apexx, required ones enforced by the server, the
// answers kept with the order, and an app's blocks marked with its name.
func TestCheckoutBlocks(t *testing.T) {
	e := setup(t)
	if st, _ := e.do("PUT", "/v1/settings/checkout-blocks", e.adminA, m{"blocks": []m{{"kind": "script", "label": "x"}}}); st != 400 {
		t.Fatalf("unknown block kind accepted: %d", st)
	}
	app := e.must(201, "POST", "/v1/apps", e.adminA, m{"name": "Rentas", "permissions": m{"settings": "admin"}})["token"].(string)
	e.must(200, "PUT", "/v1/settings/checkout-blocks", app, m{"blocks": []m{
		{"kind": "notice", "label": "Entrega en 48 horas"},
		{"id": "renta", "kind": "checkbox", "label": "Acepto las condiciones de renta", "required": true},
		{"id": "factura", "kind": "text", "label": "Referencia para tu factura", "maxLength": 20},
	}})
	e.drain()
	pub := e.must(200, "GET", "/s/store-a/api/checkout-blocks", "", nil)["blocks"].([]any)
	if len(pub) != 3 || pub[1].(map[string]any)["source"] != "Rentas" {
		t.Fatalf("public blocks = %v", pub)
	}
	if b := e.must(200, "GET", "/s/store-b/api/checkout-blocks", "", nil)["blocks"].([]any); len(b) != 0 {
		t.Fatalf("store B has store A's blocks: %v", b)
	}
	n := 0
	buy := func(blocks m) (int, m) {
		n++
		return e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "blocks": blocks,
			"customer": m{"email": "bloques@cliente.example", "name": "Bloques"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("blocks-order-%d", n))
	}
	for _, bad := range []m{nil, {"factura": "A1"}, {"renta": "true", "inventado": "x"}, {"renta": "true", "factura": strings.Repeat("x", 21)}} {
		if st, b := buy(bad); st != 400 {
			t.Fatalf("checkout with %v = %d %v", bad, st, b)
		}
	}
	st, o := buy(m{"renta": "true", "factura": "OC-77"})
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	f := o["checkoutFields"].([]any)
	if len(f) != 2 || f[1].(map[string]any)["value"] != "OC-77" || f[0].(map[string]any)["value"] != "Sí" {
		t.Fatalf("checkout fields = %v", f)
	}
	// Store B's checkout ignores store A's blocks.
	e.must(200, "PUT", "/v1/settings/checkout-blocks", e.adminA, m{"blocks": []m{}})
	e.drain()
	if st, b := buy(nil); st != 201 {
		t.Fatalf("checkout after removing blocks = %d %v", st, b)
	}
}
