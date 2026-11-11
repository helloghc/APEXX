package api

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Access audit over every registered route: nothing under /v1 answers without
// credentials unless it is on the short public list, and the publishable
// storefront key cannot reach any management route.
func TestEveryRouteRequiresAccess(t *testing.T) {
	e := setup(t)
	public := map[string]bool{
		"POST /v1/auth/login": true, "POST /v1/auth/stores": true, "GET /v1/invitations/{token}": true, "POST /v1/invitations/{token}/accept": true,
		"POST /v1/stripe/webhook/{tenant}": true,
	}
	// What a publishable key may do: read the catalogue, simulate and buy.
	storefront := regexp.MustCompile(`^(GET /v1/(me|products|search|catalog|categories|brands|collections|channels|inventory|prices|facets)|POST /v1/(simulate|carts/simulate|checkout|auth/logout|me/))`)
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked, managed := 0, 0
	for _, pat := range e.server.patterns {
		method, path, ok := strings.Cut(pat, " ")
		if !ok || !strings.HasPrefix(path, "/v1/") {
			continue
		}
		url := e.srv.URL + param.ReplaceAllString(path, "x")
		do := func(key string) int {
			req, _ := http.NewRequest(method, url, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			return res.StatusCode
		}
		checked++
		if st := do(""); st != 401 && !public[pat] {
			t.Errorf("%s answers %d without credentials", pat, st)
		}
		if public[pat] || storefront.MatchString(pat) {
			continue
		}
		managed++
		if st := do(e.sfA); st != 403 {
			t.Errorf("%s answers %d to a publishable storefront key (want 403)", pat, st)
		}
	}
	if checked < 250 || managed < 200 {
		t.Fatalf("audit covered only %d routes (%d management): the route list is not being recorded", checked, managed)
	}
	t.Logf("%d routes under /v1 checked, %d of them management routes", checked, managed)
}

// Every table that holds data of a store has row level security forced, and
// the application role cannot bypass it nor read credentials.
func TestEveryTenantTableIsIsolated(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rows, err := e.db.System.Query(ctx, `SELECT c.relname, c.relrowsecurity AND c.relforcerowsecurity AND EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid)
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind='r' AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='tenant_id' AND NOT a.attisdropped)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	// Looked up before the store is known, so they are closed to the app role instead.
	noRLS := map[string]bool{"api_keys": true, "sessions": true}
	n := 0
	for rows.Next() {
		var name string
		var isolated bool
		if err := rows.Scan(&name, &isolated); err != nil {
			t.Fatal(err)
		}
		n++
		if !isolated && !noRLS[name] {
			t.Errorf("table %s holds store data without forced row level security", name)
		}
	}
	if n < 100 {
		t.Fatalf("only %d tenant tables found", n)
	}
	for name := range noRLS {
		if _, err := e.db.App.Exec(ctx, "SELECT 1 FROM "+name+" LIMIT 1"); err == nil {
			t.Errorf("the application role can read %s", name)
		}
	}
	var bypass, super bool
	if err := e.db.System.QueryRow(ctx, `SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname='apexx_app'`).Scan(&bypass, &super); err != nil || bypass || super {
		t.Fatalf("application role: bypassrls=%v superuser=%v err=%v", bypass, super, err)
	}
	// Without a store in the session the app role sees nothing at all.
	for _, tbl := range []string{"orders", "customers", "gift_cards", "companies", "seller_entries", "entity_records", "subscriptions", "reviews"} {
		var c int
		if err := e.db.App.QueryRow(ctx, "SELECT count(*) FROM "+tbl).Scan(&c); err != nil || c != 0 {
			t.Errorf("app role without a store sees %d rows of %s (err %v)", c, tbl, err)
		}
	}
	t.Logf("%d tables with store data, all isolated", n)
}
