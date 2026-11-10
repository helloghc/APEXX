package money

import (
	"errors"
	"testing"
)

func TestAddMismatch(t *testing.T) {
	if _, err := New(1, "MXN").Add(New(1, "USD")); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
}

func TestPercentRounding(t *testing.T) {
	cases := []struct{ amt, bps, want int64 }{{1000, 1000, 100}, {5, 1000, 1}, {4, 1000, 0}, {168438, 1600, 26950}}
	for _, c := range cases {
		if got := New(c.amt, "MXN").Percent(c.bps).Amount; got != c.want {
			t.Errorf("%d*%dbps = %d, want %d", c.amt, c.bps, got, c.want)
		}
	}
}
