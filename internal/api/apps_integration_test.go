package api

import (
	"context"
	"testing"
)

// Apps: a token with permissions per module, never over the team, changed
// and revoked with effect on the very next request, all in the audit chain.
func TestAppsScopesAndRevocation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("POST", "/v1/apps", e.adminA, m{"name": "Sin permisos", "permissions": m{}}); st != 400 {
		t.Fatalf("app without permissions accepted: %d", st)
	}
	if st, _ := e.do("POST", "/v1/apps", e.adminA, m{"name": "Equipo", "permissions": m{"team": "admin"}}); st != 400 {
		t.Fatalf("app over the team accepted: %d", st)
	}
	if st, _ := e.do("POST", "/v1/apps", e.adminA, m{"name": "Mala", "url": "http://x.example", "permissions": m{"catalog": "read"}}); st != 400 {
		t.Fatalf("plain http url accepted: %d", st)
	}
	if st, _ := e.do("POST", "/v1/apps", e.sfA, m{"name": "Tienda", "permissions": m{"catalog": "read"}}); st != 401 && st != 403 {
		t.Fatalf("storefront key installed an app: %d", st)
	}
	r := e.must(201, "POST", "/v1/apps", e.adminA, m{"name": "Sincronizador ERP", "developer": "ERP SA", "url": "https://erp.example", "permissions": m{"catalog": "edit", "orders": "read"}})
	tok, id := r["token"].(string), r["app"].(map[string]any)["id"].(string)
	me := e.must(200, "GET", "/v1/apps/me", tok, nil)
	if me["kind"] != "app" || me["store"] != "store-a" {
		t.Fatalf("app me = %v", me)
	}
	// What it may and may not do.
	e.must(200, "GET", "/v1/products/P1", tok, nil)
	e.must(200, "GET", "/v1/orders", tok, nil)
	if st, _ := e.do("PUT", "/v1/inventory/S1/w1", tok, m{"onHand": 1}); st != 403 {
		t.Fatalf("app without inventory access changed stock: %d", st)
	}
	for _, path := range []string{"/v1/apps", "/v1/team/members"} {
		if st, _ := e.do("GET", path, tok, nil); st != 403 && st != 404 {
			t.Fatalf("app read %s: %d", path, st)
		}
	}
	if st, _ := e.do("POST", "/v1/apps", tok, m{"name": "Hija", "permissions": m{"catalog": "admin"}}); st != 403 {
		t.Fatalf("an app installed another app: %d", st)
	}
	// Another store sees nothing of it and cannot touch it.
	if l := e.must(200, "GET", "/v1/apps", e.adminB, nil); len(l["items"].([]any)) != 0 {
		t.Fatalf("store B lists store A's apps: %v", l)
	}
	if st, _ := e.do("DELETE", "/v1/apps/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("store B revoked store A's app: %d", st)
	}
	if st, _ := e.do("GET", "/v1/products/P1", tok, nil, "X-Tenant", "store-b"); st != 200 {
		t.Fatalf("app request = %d", st)
	}
	// Narrowing permissions applies to the next request.
	e.must(200, "PATCH", "/v1/apps/"+id, e.adminA, m{"permissions": m{"catalog": "read"}})
	if st, _ := e.do("GET", "/v1/orders", tok, nil); st != 403 {
		t.Fatalf("narrowed app still reads orders: %d", st)
	}
	// Revoking cuts it at once.
	e.must(204, "DELETE", "/v1/apps/"+id, e.adminA, nil)
	if st, _ := e.do("GET", "/v1/products/P1", tok, nil); st != 401 {
		t.Fatalf("revoked app still works: %d", st)
	}
	// A forged secret for a real id fails.
	if st, _ := e.do("GET", "/v1/products/P1", "apa_"+id+"_00", nil); st != 401 {
		t.Fatalf("forged app token: %d", st)
	}
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND target=$1 AND action IN ('app.installed','app.permissions','app.revoked')`, id).Scan(&n)
	if n != 3 {
		t.Fatalf("audit entries = %d, want 3", n)
	}
}
