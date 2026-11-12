package cart

import "testing"

func TestQtyRule(t *testing.T) {
	for _, c := range []struct {
		r        QtyRule
		in, want int64
		code     string
	}{
		{QtyRule{Min: 6}, 2, 6, "qty_min"},
		{QtyRule{Step: 12}, 5, 12, "qty_min"},
		{QtyRule{Step: 12}, 13, 24, "qty_step"},
		{QtyRule{Step: 12, Max: 30}, 25, 24, "qty_step"},
		{QtyRule{Max: 10}, 15, 10, "qty_max"},
		{QtyRule{Min: 5, Step: 4}, 1, 8, "qty_min"},
		{QtyRule{Min: 6, Step: 6, Max: 60}, 36, 36, ""},
	} {
		got, code, _ := c.r.Apply(c.in)
		if got != c.want || code != c.code {
			t.Errorf("%+v Apply(%d) = %d %q, want %d %q", c.r, c.in, got, code, c.want, c.code)
		}
	}
}
