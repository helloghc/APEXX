package api

import "testing"

// The first-steps guide reads each store's own state: store A has products
// and no sale yet, a storefront key cannot read it, and store B's answer does
// not depend on A's data.
func TestSetupStatus(t *testing.T) {
	e := setup(t)
	a := e.must(200, "GET", "/v1/setup", e.adminA, nil)
	if a["products"] != true || a["firstSale"] != false || a["payments"] != false {
		t.Fatalf("store A setup = %v", a)
	}
	for _, k := range []string{"products", "payments", "shipping", "published", "team", "domain", "firstSale"} {
		if _, ok := a[k].(bool); !ok {
			t.Fatalf("missing %s in %v", k, a)
		}
	}
	if st, _ := e.do("GET", "/v1/setup", e.sfA, nil); st != 401 && st != 403 {
		t.Fatalf("storefront key read setup: %d", st)
	}
	b := e.must(200, "GET", "/v1/setup", e.adminB, nil)
	if b["firstSale"] != false {
		t.Fatalf("store B setup = %v", b)
	}
}
