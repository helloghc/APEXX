package api

import (
	"context"
	"strings"
	"testing"
)

// Custom fields: defined once, filled per record, personal values encrypted,
// usable in segments; promotions can target product attributes.
func TestCustomFields(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("PUT", "/v1/fields/product", e.adminA, m{"fields": []m{}}); st != 404 {
		t.Fatalf("unknown resource = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/fields/customer", e.adminA, m{"fields": []m{{"key": "Talla", "label": "Talla", "type": "text"}}}); st != 400 {
		t.Fatalf("bad key accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/fields/customer", e.adminA, m{"fields": []m{
		{"key": "talla", "label": "Talla preferida", "type": "select", "options": []string{"CH", "M", "G"}},
		{"key": "puntos_tienda", "label": "Puntos", "type": "number"},
		{"key": "curp", "label": "CURP", "type": "text", "personal": true},
	}})
	// A customer from a purchase.
	if st, b := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "luz@example.com", "name": "Luz Campos", "address": "Calle 2"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "fields-key-1"); st != 201 {
		t.Fatalf("checkout = %d %v", st, b)
	}
	e.drain()
	cid := e.must(200, "GET", "/v1/customers?q=luz@example.com", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)

	if st, _ := e.do("PUT", "/v1/fields/customer/"+cid, e.adminA, m{"talla": "XL"}); st != 400 {
		t.Fatalf("value outside the options accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/fields/customer/"+cid, e.adminA, m{"otro": "x"}); st != 400 {
		t.Fatalf("unknown field accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/fields/customer/no-existe", e.adminA, m{"talla": "M"}); st != 404 {
		t.Fatalf("values for a record that does not exist: %d", st)
	}
	e.must(204, "PUT", "/v1/fields/customer/"+cid, e.adminA, m{"talla": "M", "puntos_tienda": "120", "curp": "CAML900101MDFMPZ09"})
	got := e.must(200, "GET", "/v1/fields/customer/"+cid, e.adminA, nil)["values"].(map[string]any)
	if got["talla"] != "M" || got["curp"] != "CAML900101MDFMPZ09" {
		t.Fatalf("values read back: %v", got)
	}
	// The personal value is not stored in clear.
	var clear int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM custom_values WHERE data::text ILIKE '%CAML900101%' OR encode(secret,'escape') ILIKE '%CAML900101%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("a personal custom value is stored in clear")
	}
	// Segments on non-personal fields, live.
	for _, c := range []struct {
		cond m
		want float64
	}{
		{m{"field": "cf:talla", "op": "eq", "value": "M"}, 1},
		{m{"field": "cf:talla", "op": "eq", "value": "G"}, 0},
		{m{"field": "cf:puntos_tienda", "op": "gte", "value": 100}, 1},
		{m{"field": "cf:puntos_tienda", "op": "lte", "value": 50}, 0},
		{m{"field": "cf:curp", "op": "set"}, 0}, // personal values never reach a query
	} {
		pv := e.must(200, "POST", "/v1/customer-groups/preview", e.adminA, m{"rules": m{"match": "all", "conditions": []m{c.cond}}})
		if pv["count"].(float64) != c.want {
			t.Fatalf("segment %v = %v, want %v", c.cond, pv["count"], c.want)
		}
	}
	if st, _ := e.do("POST", "/v1/customer-groups/preview", e.adminA, m{"rules": m{"match": "all", "conditions": []m{{"field": "cf:talla'; DROP TABLE x;--", "op": "eq", "value": "M"}}}}); st != 400 {
		t.Fatalf("a malformed field name was accepted: %d", st)
	}
	// Isolation and keys: another store and the storefront key see nothing.
	if st, _ := e.do("GET", "/v1/fields/customer/"+cid, e.adminB, nil); st != 404 {
		t.Fatalf("another store read the values: %d", st)
	}
	if st, _ := e.do("GET", "/v1/fields/customer/"+cid, e.sfA, nil); st != 403 && st != 401 {
		t.Fatalf("storefront key read the values: %d", st)
	}

	// Promotions by product attribute.
	e.must(201, "POST", "/v1/catalog/attributes", e.adminA, m{"name": "Material", "kind": "text"})
	p1 := e.must(200, "GET", "/v1/products/P1", e.adminA, nil)
	e.must(200, "PUT", "/v1/products/P1", e.adminA, m{"handle": p1["handle"], "title": p1["title"], "variants": []m{{"sku": "S1", "title": "Única"}}, "attributes": m{"material": "Algodón"}})
	e.drain()
	id := e.mustPromo(m{"name": "20% en algodón", "type": "item_percent", "bps": 2000, "target": m{"attributes": m{"Material": "algodón"}}})
	sim := e.simulate(m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 1}, {"sku": "S2", "qty": 1}}})
	applied := false
	for _, r := range sim["promotions"].([]any) {
		x := r.(map[string]any)
		if x["id"] == id && x["applied"] == true {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("attribute promotion did not apply: %v", sim["promotions"])
	}
	for _, l := range sim["lines"].([]any) {
		x := l.(map[string]any)
		if x["sku"] == "S2" && x["discount"] != nil {
			t.Fatalf("a product without the attribute got the discount: %v", x)
		}
	}
	got2 := e.must(200, "GET", "/v1/promotions/"+id, e.adminA, nil)
	if !strings.Contains(strings.ToLower(got2Summary(got2)), "algodón") {
		t.Fatalf("summary does not name the attribute: %v", got2)
	}
}

func got2Summary(v map[string]any) string {
	for _, k := range []string{"summary", "promotion"} {
		switch x := v[k].(type) {
		case string:
			return x
		case map[string]any:
			if s, ok := x["summary"].(string); ok {
				return s
			}
		}
	}
	return ""
}
