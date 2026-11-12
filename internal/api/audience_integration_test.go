package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/customers"
)

// Content per segment: a section for a customer group is published hidden
// and the page reveals it to members only (their groups come from the API).
func TestContentPerSegment(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	g := e.must(201, "POST", "/v1/customer-groups", e.adminA, m{"name": "Mayoristas", "kind": "manual"})
	gid := g["id"].(string)
	if c, b := e.do("PATCH", "/v1/sites/main/templates/index/draft", e.adminA, m{"ops": []m{{"op": "add", "path": "/regions/main/0/visibility", "value": m{"audience": m{"mode": "only", "groups": []string{gid}}}}}}); c != 200 {
		t.Fatalf("set audience = %d %v", c, b)
	}
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	res, err := http.Get(e.srv.URL + "/s/store-a/main/index")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(b), `data-audience="`+gid+`" data-audience-mode="only" hidden`) {
		t.Fatal("the segment section is not published hidden with its audience")
	}
	// A guest has no groups; a member gets theirs.
	if r := e.must(200, "GET", "/s/store-a/api/account/groups", "", nil); len(r["groups"].([]any)) != 0 {
		t.Fatalf("guest groups = %v", r)
	}
	email := "mayorista@cliente.example"
	c, err := e.server.Customers.Create(ctx, "store-a", "test", customers.Input{Email: &email})
	if err != nil {
		t.Fatal(err)
	}
	e.must(200, "POST", "/v1/customer-groups/"+gid+"/members", e.adminA, m{"ids": []string{c.ID}})
	_, _ = e.db.System.Exec(ctx, `UPDATE customers SET verified_at=now() WHERE id=$1`, c.ID)
	_ = e.server.Customers.SetPassword(ctx, "store-a", c.ID, "Una-clave-larga-2026", "")
	token, _, err := e.server.Customers.LoginPassword(ctx, "store-a", email, "Una-clave-larga-2026")
	if err != nil {
		t.Fatal(err)
	}
	_, r := e.do("GET", "/s/store-a/api/account/groups", "", nil, "Cookie", "apexx_shopper="+token)
	if gs := r["groups"].([]any); len(gs) != 1 || gs[0] != gid {
		t.Fatalf("member groups = %v", r)
	}
	// Another store's shopper session does not read store A's groups.
	_, r = e.do("GET", "/s/store-b/api/account/groups", "", nil, "Cookie", "apexx_shopper="+token)
	if len(r["groups"].([]any)) != 0 {
		t.Fatalf("store B read store A's groups: %v", r)
	}
}
