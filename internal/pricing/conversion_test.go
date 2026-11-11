package pricing

import (
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/money"
)

func TestConversion(t *testing.T) {
	c := Conversion{From: "mx", Currency: "USD", RateMicros: 58_000} // 1 MXN = 0.058 USD
	for _, x := range []struct {
		rounding string
		in, want int64
	}{
		{"", 100000, 5800},     // $1,000.00 MXN -> $58.00 USD
		{"", 1999, 116},        // 115.94 -> 116 cents
		{"unit", 129900, 7500}, // 75.34 -> 75.00
		{"99", 129900, 7599},   // 75.34 -> 75.99
		{"ten", 129900, 8000},  // 75.34 -> 80.00
		{"", 0, 0},
	} {
		c.Rounding = x.rounding
		if got := c.Convert(x.in); got != x.want {
			t.Errorf("%q %d = %d, want %d", x.rounding, x.in, got, x.want)
		}
	}
	// No overflow with very large amounts.
	if v := (Conversion{RateMicros: 1_000_000_000}).Convert(1 << 60); v <= 0 {
		t.Fatalf("overflow: %d", v)
	}
}

func TestMarketPrices(t *testing.T) {
	s := New()
	cmp := money.New(150000, "MXN")
	s.Set("mx", "A", Tier{MinQty: 1, Unit: money.New(100000, "MXN"), CompareAt: &cmp})
	s.Set("mx", "B", Tier{MinQty: 1, Unit: money.New(50000, "MXN")})
	s.Set("us", "B", Tier{MinQty: 1, Unit: money.New(2500, "USD")}) // fixed price in the market wins
	s.SetConversions(map[string]Conversion{"us": {From: "mx", Currency: "USD", RateMicros: 58_000, Rounding: "99"}})

	q, ok := s.Resolve("us", "A", 1)
	if !ok || q.Unit != money.New(5899, "USD") || q.CompareAt == nil || *q.CompareAt != money.New(8799, "USD") {
		t.Fatalf("converted price: %+v %v", q, q.CompareAt)
	}
	if q, _ := s.Resolve("us", "B", 1); q.Unit != money.New(2500, "USD") {
		t.Fatalf("fixed market price lost: %+v", q)
	}
	if _, ok := s.Resolve("eu", "A", 1); ok {
		t.Fatal("a channel without prices or conversion sold the product")
	}

	// A list with MXN amounts converts in the market; a list in another
	// currency does not apply.
	now := time.Now()
	s.SetList(List{ID: "l1", Active: true, Kind: "override", Items: map[string]int64{"A": 80000}, Currency: "MXN"})
	if q, _ := s.ResolveAt("us", "A", 1, "", now); q.Unit != money.New(4699, "USD") || q.List != "l1" {
		t.Fatalf("MXN list in the US market: %+v", q)
	}
	s.SetList(List{ID: "l2", Active: true, Kind: "override", Priority: 5, Items: map[string]int64{"B": 1000}, Currency: "EUR"})
	if q, _ := s.ResolveAt("us", "B", 1, "", now); q.List == "l2" {
		t.Fatalf("a EUR list applied to USD prices: %+v", q)
	}
	if q, _ := s.ResolveAt("mx", "B", 1, "", now); q.List == "l2" {
		t.Fatalf("a EUR list applied to MXN prices: %+v", q)
	}
}
