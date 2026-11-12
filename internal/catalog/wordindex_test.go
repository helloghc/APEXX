package catalog

import (
	"fmt"
	"math/rand"
	"testing"
)

// The word index gives exactly the products matchesAll accepts, and stays
// right as products are created, renamed and deleted.
func TestMatchingEqualsFullScan(t *testing.T) {
	s := New(nil)
	kinds := []string{"Playera", "Sudadera", "Chamarra", "Tenis", "Pantalón", "Gorra"}
	brands := []string{"Sierra", "Pacífico", "Maya", "Norte"}
	mats := []string{"algodón", "lino", "piel", "mezclilla"}
	r := rand.New(rand.NewSource(7))
	put := func(i int) {
		s.Upsert(Product{ID: fmt.Sprintf("P%04d", i), Handle: fmt.Sprintf("p-%d", i), Status: "active",
			Title:    fmt.Sprintf("%s %s de %s %d", kinds[r.Intn(len(kinds))], brands[r.Intn(len(brands))], mats[r.Intn(len(mats))], i),
			Variants: []Variant{{SKU: fmt.Sprintf("SKU-%d-A", i)}, {SKU: fmt.Sprintf("SKU-%d-B", i), Barcode: fmt.Sprintf("750%07d", i)}}})
	}
	for i := 0; i < 600; i++ {
		put(i)
	}
	queries := []string{"sudadera", "sudadra", "chamara maya", "lino", "lnio sierra", "tenis 12", "p-1", "sku 33", "750000", "pacifco", "gor", "zzz", "piel norte playera", "pantalon"}
	check := func(label string) {
		for _, q := range queries {
			got := s.Matching(q)
			want := map[string]bool{}
			for _, p := range s.Products() {
				if matchesAll(words(q), p.hay) {
					want[p.ID] = true
				}
			}
			if len(got) != len(want) {
				t.Fatalf("%s %q: index %d products, full scan %d", label, q, len(got), len(want))
			}
			for id := range want {
				if !got[id] {
					t.Fatalf("%s %q: index missed %s", label, q, id)
				}
			}
		}
	}
	check("created")
	for i := 0; i < 600; i += 3 { // rename a third
		put(i)
	}
	check("renamed")
	for i := 1; i < 600; i += 4 {
		s.Delete(fmt.Sprintf("P%04d", i))
	}
	check("deleted")
	if s.Matching("  ") != nil {
		t.Fatal("no tokens must mean every product (nil)")
	}
}
