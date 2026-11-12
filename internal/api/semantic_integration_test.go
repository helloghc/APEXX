package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/helloghc/apexx/internal/ai"
)

func (e *env) embed() int {
	e.t.Helper()
	e.drain()
	n, err := e.server.RunEmbeddings(context.Background())
	if err != nil {
		e.t.Fatalf("embeddings: %v", err)
	}
	e.drain()
	return n
}

func (e *env) chunks(tenant string) int {
	var n int
	if err := e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM ai_chunks WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) semTop(tenant, q string) string {
	hits, err := e.server.SemanticSearch(context.Background(), tenant, q, 3, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(hits) == 0 {
		return ""
	}
	return hits[0].ID
}

func (e *env) product(handle, title, desc string) string {
	_, m := e.do("POST", "/v1/products", e.adminA, map[string]any{"handle": handle, "title": title, "description": desc, "status": "active",
		"variants": []map[string]any{{"sku": "SKU-" + handle, "title": "Única"}}})
	id, _ := m["id"].(string)
	if id == "" {
		e.t.Fatalf("product %s: %v", handle, m)
	}
	return id
}

// Vectors follow the catalog through the outbox: a new product is embedded,
// a changed one again (and only it), a deleted one leaves the table and the
// index in the same step, and unchanged products cost nothing.
func TestSemanticIndexFollowsCatalog(t *testing.T) {
	e := setup(t)
	if n := e.embed(); n != 3 {
		t.Fatalf("first run embeds the 3 seed products, got %d", n)
	}
	rain := e.product("chamarra", "Chamarra impermeable", "Para lluvia intensa, capucha ajustable")
	shoe := e.product("tenis", "Zapatillas para correr", "Suela de goma y malla transpirable")
	if n := e.embed(); n != 2 {
		t.Fatalf("only the 2 new products, got %d", n)
	}
	if e.semTop("store-a", "chamarras impermeables") != rain || e.semTop("store-a", "zapatilla correr") != shoe {
		t.Fatal("semantic search does not find the new products")
	}
	if n := e.embed(); n != 0 {
		t.Fatalf("nothing changed, %d embedded", n)
	}
	// A change of meaning moves the vector.
	e.must(200, "PUT", "/v1/products/"+shoe, e.adminA, map[string]any{"handle": "tenis", "title": "Mochila de montaña", "description": "40 litros, impermeable al polvo", "status": "active",
		"variants": []map[string]any{{"sku": "SKU-tenis", "title": "Única"}}})
	if n := e.embed(); n != 1 {
		t.Fatalf("one product changed, %d embedded", n)
	}
	if e.semTop("store-a", "mochila montaña") != shoe {
		t.Fatal("changed product not found by its new meaning")
	}
	// Deleting a product deletes its vector, in the database and in memory.
	e.must(204, "DELETE", "/v1/products/"+rain, e.adminA, nil)
	e.drain()
	if e.semTop("store-a", "chamarras impermeables") == rain {
		t.Fatal("deleted product still found")
	}
	var left int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM ai_chunks WHERE tenant_id='store-a' AND source_id=$1`, rain).Scan(&left)
	if left != 0 {
		t.Fatalf("%d chunks of the deleted product left", left)
	}
	// Bulk delete takes the vectors too.
	e.must(200, "POST", "/v1/products/bulk", e.adminA, map[string]any{"action": "delete", "ids": []string{shoe, "P1", "P2", "P3"}})
	e.drain()
	if c := e.chunks("store-a"); c != 0 {
		t.Fatalf("%d chunks left after deleting every product", c)
	}
	if x := e.server.loadedIndex("store-a"); x == nil || x.Len() != 0 {
		t.Fatal("index not empty after deleting every product")
	}
}

// One store never sees another's vectors.
func TestSemanticIsolation(t *testing.T) {
	e := setup(t)
	e.product("taladro", "Taladro percutor", "Para concreto y madera")
	e.embed()
	if e.semTop("store-b", "taladro") != "" {
		t.Fatal("store-b found a product of store-a")
	}
	// The app role, inside store-b, cannot read store-a's chunks.
	var n int
	err := e.db.App.QueryRow(context.Background(), `SELECT count(*) FROM ai_chunks`).Scan(&n)
	if err != nil || n != 0 {
		t.Fatalf("app role without a store sees %d chunks (%v)", n, err)
	}
}

// The leader writes vectors; another node gets them from the outbox.
func TestSemanticVectorsReachEveryNode(t *testing.T) {
	e := setup(t)
	nodeA := e.server
	_ = e.newSrv()
	nodeB, projB := e.server, e.proj
	e.server = nodeA
	// Node B loads its index (empty) before the vectors exist.
	if _, err := nodeB.semIndex(context.Background(), "store-a"); err != nil {
		t.Fatal(err)
	}
	e.product("lampara", "Lámpara de escritorio LED", "Luz cálida regulable")
	e.server = nodeA
	e.proj = projB // drain both projectors below
	_ = nodeA.Proj.Drain(context.Background())
	if _, err := nodeA.RunEmbeddings(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = projB.Drain(context.Background())
	hits, _ := nodeB.SemanticSearch(context.Background(), "store-a", "lampara escritorio", 1, nil)
	if len(hits) == 0 || hits[0].Score < 0.3 {
		t.Fatalf("node B did not receive the vectors: %v", hits)
	}
}

// A paid provider stops at the store's monthly budget; when it is down,
// nothing is lost: products are retried and search stays lexical.
func TestSemanticBudgetAndOutage(t *testing.T) {
	e := setup(t)
	var calls, down atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if down.Load() == 1 {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		var in struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		l := ai.NewLocal(64)
		vecs, _, _ := l.Embed(r.Context(), in.Input, ai.Document)
		type d struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		}
		out := struct {
			Data  []d            `json:"data"`
			Usage map[string]int `json:"usage"`
		}{Usage: map[string]int{"total_tokens": 1_000_000 * len(in.Input)}}
		for i, v := range vecs {
			out.Data = append(out.Data, d{v, i})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer fake.Close()
	// 1 USD per million tokens, one million tokens per product: 1 USD each.
	e.server.Embedder = &ai.Voyage{ModelName: "fake", D: 64, URL: fake.URL, PricePerM: 1_000_000}
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, map[string]any{"budgetUsd": 2})

	down.Store(1)
	if _, err := e.server.RunEmbeddings(context.Background()); err == nil {
		t.Fatal("provider down must report an error")
	}
	if e.chunks("store-a") != 0 {
		t.Fatal("nothing should be written while the provider is down")
	}
	down.Store(0)
	// The first batch (3 products, 3 USD) is allowed because the month was at 0;
	// after it the budget is spent and nothing more goes out.
	_, _ = e.server.RunEmbeddings(context.Background())
	if e.chunks("store-a") != 3 {
		t.Fatalf("after recovery: %d chunks", e.chunks("store-a"))
	}
	e.product("nuevo", "Producto nuevo", "")
	e.drain()
	before := calls.Load()
	_, _ = e.server.RunEmbeddings(context.Background())
	if calls.Load() != before {
		t.Fatal("budget spent but the provider was called again")
	}
	_, st := e.do("GET", "/v1/ai/settings", e.adminA, nil)
	sem := st["semantic"].(map[string]any)
	if sem["monthCostUsd"].(float64) < 3 || sem["local"].(bool) {
		t.Fatalf("status: %v", sem)
	}
	// Turning vectors off for the store stops the work.
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, map[string]any{"budgetUsd": 100, "semanticOff": true})
	before = calls.Load()
	_, _ = e.server.RunEmbeddings(context.Background())
	if calls.Load() != before {
		t.Fatal("semantic off but the provider was called")
	}
}
