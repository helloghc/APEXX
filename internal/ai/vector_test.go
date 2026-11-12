package ai

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

func TestIndexPutDeleteSearch(t *testing.T) {
	l := NewLocal(128)
	x := NewIndex(l.Model(), l.Dim())
	docs := map[string]string{
		"p1": "Chamarra impermeable para lluvia, negra",
		"p2": "Pantalones de mezclilla azul",
		"p3": "Zapatillas para correr, suela de goma",
		"p4": "Camisa de lino blanca",
	}
	for id, d := range docs {
		v, _, _ := l.Embed(context.Background(), []string{d}, Document)
		if err := x.Put(id, Quantize(v[0]), []byte(id)); err != nil {
			t.Fatal(err)
		}
	}
	q := func(s string) string {
		v, _, _ := l.Embed(context.Background(), []string{s}, Query)
		h, err := x.Search(Quantize(v[0]), 1, nil)
		if err != nil || len(h) == 0 {
			t.Fatal(err)
		}
		return h[0].ID
	}
	for query, want := range map[string]string{"chamarras impermeables": "p1", "pantalon mezclila": "p2", "zapatilla correr": "p3", "camisas lino": "p4"} {
		if got := q(query); got != want {
			t.Errorf("%q -> %s, want %s", query, got, want)
		}
	}
	x.Delete("p1")
	if x.Len() != 3 || q("chamarras impermeables") == "p1" {
		t.Fatal("deleted vector still found")
	}
	// The vector moved into the deleted slot keeps its own data.
	if q("camisas lino") != "p4" || q("zapatilla correr") != "p3" {
		t.Fatal("swap-delete corrupted another vector")
	}
	if err := x.Put("bad", make([]int8, 7), nil); err != ErrDim {
		t.Fatal("wrong dimension accepted")
	}
}

func TestMetrics(t *testing.T) {
	rel := map[string]int{"a": 2, "b": 1}
	if NDCG([]string{"a", "b", "c"}, rel, 10) != 1 {
		t.Fatal("ideal order must score 1")
	}
	if NDCG([]string{"c", "b", "a"}, rel, 10) >= 1 || NDCG([]string{"c"}, rel, 10) != 0 {
		t.Fatal("ndcg")
	}
	if Recall([]string{"a", "x"}, rel, 50) != 0.5 {
		t.Fatal("recall")
	}
	f := RRF(60, []string{"a", "b", "c"}, []string{"c", "a"})
	if f[0] != "a" || f[1] != "c" || f[2] != "b" {
		t.Fatalf("rrf %v", f)
	}
}

// 50 000 vectors of 384 int8: one exact search.
func BenchmarkSearch50k(b *testing.B) {
	const n, dim = 50000, 384
	x := NewIndex("bench", dim)
	r := rand.New(rand.NewSource(1))
	v := make([]float32, dim)
	for i := 0; i < n; i++ {
		for j := range v {
			v[j] = float32(r.NormFloat64())
		}
		_ = x.Put(fmt.Sprint(i), Quantize(v), nil)
	}
	q := Quantize(v)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = x.Search(q, 50, nil)
	}
	b.ReportMetric(float64(x.Bytes())/(1<<20), "MB")
}

// The two-pass search finds what the exact scan finds. The data is
// clustered like a catalog (products of one kind sit close together):
// 500 groups of 40 vectors.
func TestSearchMatchesExact(t *testing.T) {
	const groups, per, dim = 500, 40, 384
	x := NewIndex("t", dim)
	r := rand.New(rand.NewSource(7))
	centers := make([][]float32, groups)
	for g := range centers {
		centers[g] = make([]float32, dim)
		for j := range centers[g] {
			centers[g][j] = float32(r.NormFloat64())
		}
	}
	near := func(c []float32, noise float64) []int8 {
		v := make([]float32, dim)
		for j := range v {
			v[j] = c[j] + float32(r.NormFloat64()*noise)
		}
		return Quantize(v)
	}
	for g := 0; g < groups; g++ {
		for i := 0; i < per; i++ {
			_ = x.Put(fmt.Sprintf("%d-%d", g, i), near(centers[g], 0.8), nil)
		}
	}
	found, total := 0, 0
	for trial := 0; trial < 50; trial++ {
		q := near(centers[r.Intn(groups)], 0.8)
		ex, _ := x.Exact(q, 10, nil)
		fast, _ := x.Search(q, 10, nil)
		in := map[string]bool{}
		for _, h := range fast {
			in[h.ID] = true
		}
		for _, h := range ex {
			total++
			if in[h.ID] {
				found++
			}
		}
	}
	rec := float64(found) / float64(total)
	t.Logf("recall@10 vs exact = %.3f", rec)
	if rec < 0.98 {
		t.Fatalf("recall@10 vs exact = %.3f", rec)
	}
}

// One product text of a typical catalog, embedded locally.
func BenchmarkLocalEmbed(b *testing.B) {
	l := NewLocal(384)
	txt := []string{"Sudadera Maya de lana gris 1234. Sudadera Maya de lana gris 1234. Sudadera. Ropa > Sudaderas. Maya. lana, gris. Sudadera de lana en color gris. Hecho en México, colección Maya."}
	for i := 0; i < b.N; i++ {
		_, _, _ = l.Embed(context.Background(), txt, Document)
	}
}
