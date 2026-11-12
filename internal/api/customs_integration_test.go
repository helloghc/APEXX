package api

import (
	"fmt"
	"strings"
	"testing"
)

// Personalization: fields with prices are validated, add to the unit price,
// make separate lines for different choices, and are kept on the order.
func TestProductCustomizations(t *testing.T) {
	e := setup(t)
	if st, _ := e.do("PUT", "/v1/products/P1/customizations", e.adminA, m{"fields": []m{{"label": "Color", "kind": "select", "choices": []m{{"label": "Rojo"}}}}}); st != 400 {
		t.Fatalf("a list with one option accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/products/P1/customizations", e.adminB, m{"fields": []m{}}); st != 404 {
		t.Fatalf("store B edited store A's product: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/products/P1/customizations", e.sfA, m{"fields": []m{}}); st != 401 && st != 403 {
		t.Fatalf("storefront key edited personalization: %d", st)
	}
	f := e.must(200, "PUT", "/v1/products/P1/customizations", e.adminA, m{"fields": []m{
		{"label": "Grabado", "kind": "text", "maxLength": 12, "price": 5000, "required": true},
		{"label": "Acabado", "kind": "select", "required": true, "choices": []m{{"label": "Mate", "price": 0}, {"label": "Brillante", "price": 2000}}},
		{"label": "Envoltura de regalo", "kind": "checkbox", "price": 3000},
	}})["fields"].([]any)
	if f[0].(map[string]any)["id"] != "grabado" || f[1].(map[string]any)["choices"].([]any)[1].(map[string]any)["id"] != "brillante" {
		t.Fatalf("ids = %v", f)
	}
	e.drain()
	sim := func(items []m) m {
		return e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": items})
	}
	// Missing required, too long, unknown field and unknown choice are refused with a reason.
	for _, c := range []m{
		{"acabado": "mate"},
		{"grabado": "Un texto demasiado largo", "acabado": "mate"},
		{"grabado": "Ana", "acabado": "mate", "inventado": "x"},
		{"grabado": "Ana", "acabado": "dorado"},
	} {
		r := sim([]m{{"sku": "S1", "qty": 1, "custom": c}})
		if len(r["lines"].([]any)) != 0 || !strings.Contains(fmt.Sprint(r["messages"]), "invalid_customization") {
			t.Fatalf("invalid %v accepted: %v", c, r)
		}
	}
	// 100.00 + 50 engraving + 20 glossy + 30 wrap = 200.00 per unit; another choice is another line.
	r := sim([]m{
		{"sku": "S1", "qty": 2, "ref": "a", "custom": m{"grabado": "Ana", "acabado": "brillante", "envoltura-de-regalo": "true"}},
		{"sku": "S1", "qty": 1, "ref": "b", "custom": m{"grabado": "Beto", "acabado": "mate"}},
	})
	ls := r["lines"].([]any)
	l0, l1 := ls[0].(map[string]any), ls[1].(map[string]any)
	if l0["unit"].(map[string]any)["amount"].(float64) != 20000 || l0["total"].(map[string]any)["amount"].(float64) != 40000 || l0["ref"] != "a" || len(l0["custom"].([]any)) != 3 {
		t.Fatalf("line a = %v", l0)
	}
	if l1["unit"].(map[string]any)["amount"].(float64) != 15000 || l1["ref"] != "b" {
		t.Fatalf("line b = %v", l1)
	}
	// The order keeps each personalization, in the line and its title.
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129",
		"items":    []m{{"sku": "S1", "qty": 1, "custom": m{"grabado": "Ana", "acabado": "brillante"}}},
		"customer": m{"email": "grabado@cliente.example", "name": "Grabado"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "custom-order-1")
	if st != 201 {
		t.Fatalf("checkout = %d %v", st, o)
	}
	line := o["lines"].([]any)[0].(map[string]any)
	if line["unit"].(float64) != 17000 || !strings.Contains(line["title"].(string), "Grabado: Ana") || len(line["custom"].([]any)) != 2 {
		t.Fatalf("order line = %v", line)
	}
	// A product without personalization refuses made-up fields.
	if r := sim([]m{{"sku": "S2", "qty": 1, "custom": m{"grabado": "x"}}}); len(r["lines"].([]any)) != 0 {
		t.Fatalf("personalization on a product without fields: %v", r)
	}
	// Removing the fields: plain lines again.
	e.must(200, "PUT", "/v1/products/P1/customizations", e.adminA, m{"fields": []m{}})
	e.drain()
	if r := sim([]m{{"sku": "S1", "qty": 1}}); r["lines"].([]any)[0].(map[string]any)["unit"].(map[string]any)["amount"].(float64) != 10000 {
		t.Fatalf("after removing: %v", r["lines"])
	}
}
