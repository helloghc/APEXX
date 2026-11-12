package reports

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// The bounded heap picks the same rows, in the same order, as sorting all.
func TestTopLinesEqualsFullSort(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, n := range []int{0, 1, 3, 500} {
		lines := make([]StockLine, 2000)
		for i := range lines {
			lines[i] = StockLine{SKU: fmt.Sprintf("S%04d", r.IntN(3000)), OnHand: r.Int64N(20), Reserved: r.Int64N(5)}
		}
		less := func(a, b StockLine) bool {
			if x, y := a.OnHand-a.Reserved, b.OnHand-b.Reserved; x != y {
				return x < y
			}
			return a.SKU < b.SKU
		}
		top := &topLines{less: less}
		for _, l := range lines {
			top.offer(l, n)
		}
		all := slices.Clone(lines)
		slices.SortStableFunc(all, func(a, b StockLine) int {
			if less(a, b) {
				return -1
			}
			if less(b, a) {
				return 1
			}
			return 0
		})
		want := all[:min(n, len(all))]
		got := top.sorted()
		if len(got) != len(want) {
			t.Fatalf("n=%d: %d rows, want %d", n, len(got), len(want))
		}
		for i := range got {
			if !(!less(got[i], want[i]) && !less(want[i], got[i])) {
				t.Fatalf("n=%d row %d: %+v want %+v", n, i, got[i], want[i])
			}
		}
	}
}
