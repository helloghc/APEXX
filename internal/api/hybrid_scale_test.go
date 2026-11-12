package api

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/projection"
)

// TestHybridSearchCost50k measures what one search costs when the cache
// misses (a query never seen in this catalog version), lexical alone and
// hybrid, over 50 000 products like the scale simulation's. APEXX_SCALE=1.
func TestHybridSearchCost50k(t *testing.T) {
	if os.Getenv("APEXX_SCALE") == "" {
		t.Skip("APEXX_SCALE not set")
	}
	kinds := []string{"Playera", "Sudadera", "Pantalón", "Chamarra", "Vestido", "Falda", "Camisa", "Short", "Tenis", "Bota", "Gorra", "Mochila", "Calcetas", "Bufanda", "Cinturón", "Lentes", "Reloj", "Bolsa", "Sandalia", "Suéter"}
	mats := []string{"algodón", "lino", "mezclilla", "piel", "lana", "seda", "poliéster", "bambú", "gamuza", "nylon"}
	colors := []string{"negro", "blanco", "azul marino", "rojo", "verde olivo", "gris", "beige", "café", "rosa", "mostaza"}
	brands := []string{"Sierra", "Pacífico", "Norte", "Bajío", "Maya", "Volcán", "Laguna", "Cenote", "Huasteca", "Barranca", "Desierto", "Selva"}
	cat := catalog.New(nil)
	for i := 0; i < 50000; i++ {
		k, m, c, b := kinds[i%len(kinds)], mats[(i>>2)%len(mats)], colors[(i>>3)%len(colors)], brands[(i>>1)%len(brands)]
		cat.Upsert(catalog.Product{ID: fmt.Sprintf("p%05d", i), Handle: fmt.Sprintf("esc-%d", i), Title: fmt.Sprintf("%s %s de %s %s %d", k, b, m, c, i),
			Description: fmt.Sprintf("%s de %s en color %s. Hecho en México, colección %s.", k, m, c, b), Brand: b, Status: "active",
			Variants: []catalog.Variant{{SKU: fmt.Sprintf("ESC-%d-1", i), Title: "CH"}}})
	}
	ten := &projection.Tenant{ID: "scale", Catalog: cat, Taxo: catalog.NewTaxonomy()}
	e := ai.NewLocal(384)
	s := &Server{Embedder: e}
	x := ai.NewIndex(e.Model(), e.Dim())
	t0 := time.Now()
	for _, p := range cat.Products() {
		v, _, _ := e.Embed(context.Background(), []string{productText(ten, p)}, ai.Document)
		_ = x.Put(p.ID, ai.Quantize(v[0]), nil)
	}
	t.Logf("embedded 50 000 products in %v, %0.1f MB", time.Since(t0).Round(time.Millisecond), float64(x.Bytes())/(1<<20))
	st := &semTenant{x: x}
	st.once.Do(func() {})
	s.sem.idx.Store(ten.ID, st)
	offer := func(*catalog.Product) catalog.Offer {
		return catalog.Offer{Price: 10000, HasPrice: true, InStock: true}
	}
	var queries []string
	for i := 0; i < 200; i++ {
		queries = append(queries, []string{
			fmt.Sprintf("%s %s", kinds[i%20], colors[i%10]),
			fmt.Sprintf("%s de %s para regalo %d", kinds[(i+3)%20], mats[i%10], i),
			fmt.Sprintf("algo abrigador para el frío %d", i),
			fmt.Sprintf("%s %s talla %d", kinds[(i+7)%20], brands[i%12], i%40),
		}[i%4])
	}
	run := func(name string, hybrid bool) {
		var ms []float64
		for _, q := range queries {
			s.qvec = qcache{} // a query never seen: nothing cached
			start := time.Now()
			bq := catalog.BrowseQuery{Q: q, Limit: 24}
			if hybrid {
				s.narrow(ten, &bq)
			} else {
				narrow(ten, &bq)
			}
			_ = catalog.Browse(ten.Catalog.ProductsIn(bq.Only), ten.Taxo, offer, bq)
			ms = append(ms, float64(time.Since(start).Microseconds())/1000)
		}
		sort.Float64s(ms)
		t.Logf("%-8s cache miss: p50 %.2f ms  p95 %.2f ms  max %.2f ms", name, ms[len(ms)/2], ms[len(ms)*95/100], ms[len(ms)-1])
		if hybrid && ms[len(ms)*95/100] > 15 {
			t.Errorf("hybrid p95 %.2f ms over the 15 ms gate", ms[len(ms)*95/100])
		}
	}
	run("lexico", false)
	run("hibrida", true)
}
