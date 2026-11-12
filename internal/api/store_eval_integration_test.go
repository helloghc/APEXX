package api

import (
	"context"
	"fmt"
	"testing"
)

// The store's own clicks grade its search; the decision follows the
// documented thresholds; learned boosts never take part.
func TestStoreSearchEval(t *testing.T) {
	e := setup(t)
	e.product("chamarra", "Chamarra impermeable", "Para lluvia intensa")
	e.product("botas", "Botas de hule", "No dejan pasar el agua")
	e.product("taladro", "Taladro percutor", "Para concreto")
	e.embed()
	ev := e.must(200, "GET", "/v1/ai/eval", e.adminA, nil)
	if ev["decision"] != "collect" || ev["queries"].(float64) != 0 {
		t.Fatalf("no data: %v", ev)
	}
	// Labeled searches: what shoppers chose.
	ctx := context.Background()
	add := func(term, handle string, clicks, carts int) {
		_, err := e.db.System.Exec(ctx, `INSERT INTO search_clicks (tenant_id, term, product_id, clicks, carts) VALUES ('store-a',$1,$2,$3,$4)`, term, e.idOf(handle), clicks, carts)
		if err != nil {
			t.Fatal(err)
		}
	}
	add("algo para la lluvia", "chamarra", 3, 1)
	add("algo para la lluvia", "botas", 2, 0)
	add("chamarra", "chamarra", 5, 2)
	add("taladro", "taladro", 4, 1)
	tt, _ := e.server.Proj.Tenant("store-a")
	res, err := e.server.storeSearchEval(ctx, tt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Queries != 3 || res.Decision != "collect" {
		t.Fatalf("eval: %+v", res)
	}
	// "algo para la lluvia" finds nothing lexically and both products by meaning.
	if !(res.Hybrid.NDCG10 > res.Lexical.NDCG10) || !(res.Hybrid.Recall50 > res.Lexical.Recall50) {
		t.Fatalf("hybrid must beat lexical on the store's own searches: %+v", res)
	}
	t.Logf("con datos de la tienda: léxica %.3f, híbrida %.3f", res.Lexical.NDCG10, res.Hybrid.NDCG10)
	// With enough searches, the decision follows the thresholds.
	for i := 0; i < evalMinQueries; i++ {
		add(fmt.Sprintf("taladro percutor %d", i), "taladro", 2, 0)
	}
	res, _ = e.server.storeSearchEval(ctx, tt)
	if res.Queries < evalMinQueries || (res.Hybrid.NDCG10 >= evalGoodEnough) != (res.Decision == "not_needed") {
		t.Fatalf("decision: %+v", res)
	}
	if c, _ := e.do("GET", "/v1/ai/eval", e.adminB, nil); c != 200 {
		t.Fatal("other store")
	}
	if m := e.must(200, "GET", "/v1/ai/eval", e.adminB, nil); m["queries"].(float64) != 0 {
		t.Fatalf("labels leaked to another store: %v", m)
	}
}
