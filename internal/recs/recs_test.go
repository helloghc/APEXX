package recs

import "testing"

func TestBuildRanksBuysOverViews(t *testing.T) {
	buys, views := map[Pair]int{}, map[Pair]int{}
	Basket([]string{"a", "b", "c"}, buys)
	Basket([]string{"a", "b"}, buys)
	for i := 0; i < 6; i++ {
		Basket([]string{"a", "d"}, views)
	}
	x := Build(buys, views, func(id string) bool { return id != "c" }, 8)
	got := x.For("a")
	if len(got) != 2 || got[0].ID != "b" || got[0].Buys != 2 || got[1].ID != "d" || got[1].Views != 6 {
		t.Fatalf("a = %+v", got)
	}
	if len(x.For("b")) != 1 { // c is excluded
		t.Fatalf("b = %+v", x.For("b"))
	}
	if m := x.ForMany([]string{"a", "b"}, 5); len(m) != 1 || m[0].ID != "d" {
		t.Fatalf("cart = %+v", m)
	}
	var nilIdx *Index
	if nilIdx.For("a") != nil || nilIdx.Products() != 0 {
		t.Fatal("nil index")
	}
}

func TestBasketDedupesAndCaps(t *testing.T) {
	m := map[Pair]int{}
	Basket([]string{"a", "a", "b", ""}, m)
	if len(m) != 1 || m[Of("b", "a")] != 1 {
		t.Fatalf("%v", m)
	}
	big := make([]string, 50)
	for i := range big {
		big[i] = string(rune('A' + i))
	}
	m = map[Pair]int{}
	Basket(big, m)
	if len(m) != 190 {
		t.Fatalf("pairs = %d", len(m))
	}
}
