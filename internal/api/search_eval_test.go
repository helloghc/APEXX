package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/ai/evalset"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/projection"
)

// evalStore loads the evaluation catalog into a read model, the same one
// the storefront searches.
func evalStore(t testing.TB) (*projection.Tenant, map[string]string) {
	tx := catalog.NewTaxonomy()
	cat := catalog.New(nil)
	ids := map[string]string{}
	catID := map[string]string{}
	for i, p := range evalset.Catalog {
		path := strings.Split(p.Category, " > ")
		parent := ""
		for d := range path {
			key := strings.Join(path[:d+1], " > ")
			id, ok := catID[key]
			if !ok {
				id = "cat" + string(rune('a'+len(catID)%26)) + strings.Repeat("x", len(catID)/26)
				catID[key] = id
				tx.SetCategory(catalog.Category{ID: id, ParentID: parent, Handle: id, Name: path[d], Active: true})
			}
			parent = id
		}
		id := "e" + strings.Repeat("0", 3-len(itoa(i))) + itoa(i)
		ids[id] = p.Handle
		cat.Upsert(catalog.Product{ID: id, Handle: p.Handle, Title: p.Title, Type: p.Type, Brand: p.Brand, Tags: p.Tags, Description: p.Desc,
			Categories: []string{parent}, Status: "active", Variants: []catalog.Variant{{SKU: "SKU-" + p.Handle, Title: "Única"}}})
	}
	return &projection.Tenant{ID: "eval", Catalog: cat, Taxo: tx}, ids
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func lexicalRank(t *projection.Tenant, ids map[string]string) func(q string) []string {
	offer := func(*catalog.Product) catalog.Offer {
		return catalog.Offer{Price: 10000, HasPrice: true, InStock: true}
	}
	return func(q string) []string {
		res := catalog.Browse(t.Catalog.Products(), t.Taxo, offer, catalog.BrowseQuery{Q: q, Limit: 50, Only: t.Catalog.Matching(q)})
		out := make([]string, len(res.Items))
		for i, p := range res.Items {
			out[i] = ids[p.ID]
		}
		return out
	}
}

func vectorRank(t *projection.Tenant, ids map[string]string, e ai.Embedder) func(q string) []string {
	x := ai.NewIndex(e.Model(), e.Dim())
	for _, p := range t.Catalog.Products() {
		v, _, _ := e.Embed(context.Background(), []string{productText(t, p)}, ai.Document)
		_ = x.Put(p.ID, ai.Quantize(v[0]), nil)
	}
	return func(q string) []string {
		v, _, _ := e.Embed(context.Background(), []string{q}, ai.Query)
		hits, _ := x.Search(ai.Quantize(v[0]), 50, nil)
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = ids[h.ID]
		}
		return out
	}
}

// hybridRank is the storefront's own path: the word index, the vectors
// (with the floors that drop weak matches) and the fused order of Browse.
func hybridRank(t *projection.Tenant, ids map[string]string, e ai.Embedder) (func(q string) []string, *Server) {
	s := &Server{Embedder: e}
	x := ai.NewIndex(e.Model(), e.Dim())
	for _, p := range t.Catalog.Products() {
		v, _, _ := e.Embed(context.Background(), []string{productText(t, p)}, ai.Document)
		_ = x.Put(p.ID, ai.Quantize(v[0]), nil)
	}
	st := &semTenant{x: x}
	st.once.Do(func() {})
	s.sem.idx.Store(t.ID, st)
	offer := func(*catalog.Product) catalog.Offer {
		return catalog.Offer{Price: 10000, HasPrice: true, InStock: true}
	}
	return func(q string) []string {
		bq := catalog.BrowseQuery{Q: q, Limit: 50}
		s.narrow(t, &bq)
		res := catalog.Browse(t.Catalog.Products(), t.Taxo, offer, bq)
		out := make([]string, len(res.Items))
		for i, p := range res.Items {
			out[i] = ids[p.ID]
		}
		return out
	}, s
}

// TestSearchEvaluation measures search quality on the Spanish evaluation
// set: the lexical index alone (what the store has today), vectors alone
// and both fused. It writes the scores to $APEXX_EVAL_OUT when set.
func TestSearchEvaluation(t *testing.T) {
	ten, ids := evalStore(t)
	lex := lexicalRank(ten, ids)
	vec := vectorRank(ten, ids, ai.NewLocal(384))
	hyb, _ := hybridRank(ten, ids, ai.NewLocal(384))
	scores := map[string]evalset.Score{
		"lexico":       evalset.Evaluate(lex, ai.NDCG, ai.Recall),
		"vector-local": evalset.Evaluate(vec, ai.NDCG, ai.Recall),
		"hibrida":      evalset.Evaluate(hyb, ai.NDCG, ai.Recall),
	}
	for name, s := range scores {
		t.Logf("%-13s nDCG@10 %.3f  recall@50 %.3f  sin resultado %.0f%%  por tipo %v", name, s.NDCG10, s.Recall50, s.Empty*100, round(s.ByKind))
	}
	if out := os.Getenv("APEXX_EVAL_OUT"); out != "" {
		b, _ := json.MarshalIndent(scores, "", " ")
		_ = os.WriteFile(out, b, 0o644)
	}
	if scores["lexico"].NDCG10 <= 0 {
		t.Fatal("lexical baseline found nothing: the harness is broken")
	}
	// The gate of IA-1: hybrid wins overall and loses no kind of search.
	h, l := scores["hibrida"], scores["lexico"]
	if h.NDCG10 <= l.NDCG10 || h.Empty >= l.Empty {
		t.Errorf("hybrid does not beat lexical: %.3f vs %.3f, empty %.2f vs %.2f", h.NDCG10, l.NDCG10, h.Empty, l.Empty)
	}
	for k, v := range l.ByKind {
		if h.ByKind[k] < v-0.02 {
			t.Errorf("hybrid loses on %s searches: %.3f vs %.3f", k, h.ByKind[k], v)
		}
	}
	// Nonsense finds nothing, as before: the floors keep weak matches out.
	for _, q := range []string{"zzqxv", "kkkjjj wwq", "xptlqz"} {
		if r := hyb(q); len(r) != 0 {
			t.Errorf("nonsense %q found %v", q, r)
		}
	}
}

func round(m map[string]float64) map[string]float64 {
	o := map[string]float64{}
	for k, v := range m {
		o[k] = float64(int(v*1000)) / 1000
	}
	return o
}
