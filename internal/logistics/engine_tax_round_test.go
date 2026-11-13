package logistics

import "testing"

func TestIncludedTaxRoundsToNearestCent(t *testing.T) {
	for _, c := range []struct{ gross, bps, want int64 }{
		{20900, 1600, 2883},   // 28.8276 → 28.83 (truncation gave 28.82)
		{41800, 1600, 5766},   // 57.655 → 57.66
		{11600, 1600, 1600},   // exact
		{10000, 800, 741},     // 7.407 → 7.41
		{-20900, 1600, -2883}, // a credit mirrors the charge
		{0, 1600, 0},
		{5000, 0, 0},
	} {
		if got := IncludedTax(c.gross, c.bps); got != c.want {
			t.Errorf("IncludedTax(%d, %d) = %d, want %d", c.gross, c.bps, got, c.want)
		}
	}
	// Net plus tax always gives back the gross amount.
	r := (&Config{}).TaxFor("MX", "", "", 1600, []TaxPart{{Class: TaxGeneral, Gross: 20900}}, false)
	if r.Total != 2883 || r.Lines[0].Net+r.Lines[0].Tax != 20900 {
		t.Fatalf("TaxFor inclusive = %+v", r)
	}
}
