package api

import (
	"context"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/recs"
)

func (e *env) catProduct(h, title, desc, cat string, cents int64) string {
	e.t.Helper()
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": h, "title": title, "description": desc, "status": "active", "categories": []string{cat},
		"variants": []map[string]any{{"sku": "SKU-" + h, "title": "Única"}}})
	_ = e.db.SetPrices(context.Background(), "store-a", "test", "mx", "SKU-"+h, []pricing.Tier{{MinQty: 1, Unit: money.New(cents, "MXN")}})
	_ = e.db.SetStock(context.Background(), "store-a", "test", "SKU-"+h, "w1", 5)
	return e.idOf(h)
}

func edgeIDs(m map[string]any, rel string) (ids []string, srcs []string) {
	for _, it := range m["items"].([]any) {
		x := it.(map[string]any)
		if rel == "" || x["rel"] == rel {
			ids = append(ids, x["id"].(string))
			srcs = append(srcs, x["source"].(string))
		}
	}
	return
}

// The graph proposes relations with their reason; only approved ones reach
// the storefront and the assistant; a rejected one is never proposed again.
func TestKnowledgeGraph(t *testing.T) {
	e := setup(t)
	cat := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Taladros", "handle": "taladros"})
	catID := cat["id"].(string)
	if catID == "" {
		catID, _ = cat["category"].(map[string]any)["id"].(string)
	}
	acc := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Accesorios", "handle": "accesorios"})
	accID, _ := acc["id"].(string)
	if accID == "" {
		accID, _ = acc["category"].(map[string]any)["id"].(string)
	}
	drill := e.catProduct("taladro", "Taladro percutor 650 W", "Taladro con percusión para concreto, madera y metal.", catID, 150000)
	alt := e.catProduct("taladro-pro", "Taladro percutor 700 W", "Taladro con percusión para concreto, madera y metal, más potente.", catID, 170000)
	bits := e.catProduct("brocas", "Juego de brocas para concreto", "20 piezas. Compatible con Taladro percutor 650 W y otros taladros.", accID, 25000)
	e.catProduct("lampara", "Lámpara de escritorio", "Luz LED regulable.", accID, 40000)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	e.embed()

	e.must(200, "POST", "/v1/ai/graph/run", e.adminA, nil)
	prop := e.must(200, "GET", "/v1/ai/graph?status=proposed", e.adminA, nil)
	compat, csrc := edgeIDs(prop, relCompatible)
	subs, ssrc := edgeIDs(prop, relSubstitute)
	if len(compat) != 1 || !strings.Contains(csrc[0], "Compatible con Taladro percutor 650 W") {
		t.Fatalf("compatibility proposals: %v %v", compat, csrc)
	}
	if len(subs) != 1 || !strings.Contains(ssrc[0], "Misma categoría") {
		t.Fatalf("substitute proposals: %v %v", subs, ssrc)
	}
	for _, it := range prop["items"].([]any) {
		x := it.(map[string]any)
		if x["srcId"] == bits && x["dstId"] != drill && x["rel"] == relCompatible {
			t.Fatalf("brocas proposed as compatible with the wrong product: %v", x)
		}
	}
	// Nothing reaches the storefront before approval.
	page := e.shop("/s/store-a/main/p/taladro")
	if strings.Contains(page, "data-alternatives") || strings.Contains(page, "Juego de brocas") {
		t.Fatal("a proposed edge is visible before approval")
	}
	// Approve compatibility, reject the substitute.
	e.must(200, "POST", "/v1/ai/graph/decide", e.adminA, map[string]any{"ids": compat, "status": "approved"})
	e.must(200, "POST", "/v1/ai/graph/decide", e.adminA, map[string]any{"ids": subs, "status": "rejected"})
	e.drain()
	page = e.shop("/s/store-a/main/p/taladro")
	if !strings.Contains(page, "Juego de brocas para concreto") {
		t.Fatal("the approved accessory is not on the drill's page")
	}
	if strings.Contains(page, "data-alternatives") {
		t.Fatal("a rejected substitute is visible")
	}
	if page := e.shop("/s/store-a/main/p/brocas"); !strings.Contains(snippet(page, "data-compatible"), "Taladro percutor 650 W") {
		t.Fatal("'Compatible con' missing on the bits' page")
	}
	// The assistant answers from the approved graph.
	if a := e.ask("brocas", "¿Con qué es compatible?"); a.Kind != "fact" || !strings.Contains(a.Text, "Taladro percutor 650 W") {
		t.Fatalf("assistant: %+v", a)
	}
	if a := e.ask("taladro", "¿Qué accesorios tiene?"); !strings.Contains(a.Text, "Juego de brocas") {
		t.Fatalf("assistant accessories: %+v", a)
	}
	// A rejected edge is not proposed again; the approval rate is reported.
	e.must(200, "POST", "/v1/ai/graph/run", e.adminA, nil)
	again := e.must(200, "GET", "/v1/ai/graph?status=proposed", e.adminA, nil)
	if ids, _ := edgeIDs(again, relSubstitute); len(ids) != 0 {
		t.Fatalf("rejected substitute proposed again: %v", ids)
	}
	if r := again["approvalRate"].(float64); r != 0.5 {
		t.Fatalf("approval rate = %v", r)
	}
	// Every decision is in the audit log, and the chain holds.
	var n int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE tenant_id='store-a' AND action LIKE 'ai.edge.%'`).Scan(&n)
	if n != 2 {
		t.Fatalf("audited decisions = %d", n)
	}
	if broken, err := e.db.VerifyAudit(context.Background(), "store-a"); err != nil || broken != 0 {
		t.Fatalf("audit chain broken at %d (%v)", broken, err)
	}
	// Undo: back to proposed, and the storefront forgets it.
	e.must(200, "POST", "/v1/ai/graph/decide", e.adminA, map[string]any{"ids": compat, "status": "proposed"})
	if strings.Contains(e.shop("/s/store-a/main/p/taladro"), "Juego de brocas para concreto") {
		t.Fatal("undone edge still visible")
	}
	// Deleting a product removes its edges.
	e.must(204, "DELETE", "/v1/products/"+bits, e.adminA, nil)
	var left int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM kg_edges WHERE tenant_id='store-a' AND (src_id=$1 OR dst_id=$1)`, bits).Scan(&left)
	if left != 0 {
		t.Fatalf("%d edges left after deleting the product", left)
	}
	// Another store sees none of it.
	if m := e.must(200, "GET", "/v1/ai/graph?status=", e.adminB, nil); len(m["items"].([]any)) != 0 {
		t.Fatal("edges leaked to another store")
	}
	_ = alt
}

// Products bought together, from another category and much cheaper, are
// proposed as accessories, with the number of orders as the reason.
func TestGraphAccessoriesFromOrders(t *testing.T) {
	e := setup(t)
	c1 := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Cafeteras", "handle": "cafeteras"})
	c2 := e.must(201, "POST", "/v1/catalog/categories", e.adminA, map[string]any{"name": "Café", "handle": "cafe"})
	id := func(m map[string]any) string {
		if s, _ := m["id"].(string); s != "" {
			return s
		}
		return m["category"].(map[string]any)["id"].(string)
	}
	maker := e.catProduct("cafetera", "Cafetera de goteo", "12 tazas.", id(c1), 89900)
	beans := e.catProduct("cafe", "Café molido 500 g", "Tueste medio.", id(c2), 18900)
	twin := e.catProduct("cafetera-2", "Cafetera de émbolo", "1 litro.", id(c1), 59900)
	e.drain()
	buys := map[recs.Pair]int{recs.Of(maker, beans): 3, recs.Of(maker, twin): 4}
	e.server.learn.idx.Store("store-a", &tenantLearn{recs: recs.Build(buys, nil, func(string) bool { return true }, 8)})
	e.must(200, "POST", "/v1/ai/graph/run", e.adminA, nil)
	prop := e.must(200, "GET", "/v1/ai/graph?status=proposed&rel="+relAccessory, e.adminA, nil)
	items := prop["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("accessory proposals: %v", items)
	}
	x := items[0].(map[string]any)
	if x["srcId"] != beans || x["dstId"] != maker || !strings.Contains(x["source"].(string), "3 pedidos") {
		t.Fatalf("accessory edge: %v", x)
	}
	// Same category (another coffee maker) is never an accessory.
	_ = twin
}
