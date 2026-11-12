// Package recs computes product recommendations from what shoppers buy and
// view together. Everything is counted pairs: no model to train, nothing to
// sync, and every recommendation carries the numbers that explain it.
package recs

import (
	"sort"
	"time"
)

// Pair is an unordered pair of products (A < B).
type Pair struct{ A, B string }

// Of returns the pair of two products in canonical order.
func Of(a, b string) Pair {
	if a > b {
		a, b = b, a
	}
	return Pair{a, b}
}

// Rec is one recommended product and why.
type Rec struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Buys  int     `json:"buys"`  // orders that had both products
	Views int     `json:"views"` // visits that saw both products
}

// Index holds the recommendations of one store.
type Index struct {
	Built  time.Time
	byProd map[string][]Rec
	Orders int // orders that fed the index
}

// Weight of a purchase together against a view together.
const buyWeight = 5

// Build ranks, for every product, the products most bought or viewed with it.
// keep says whether a product may be recommended (active, not excluded);
// per is how many to keep for each product.
func Build(buys, views map[Pair]int, keep func(id string) bool, per int) *Index {
	type acc struct{ buys, views int }
	all := map[Pair]*acc{}
	for p, n := range buys {
		if all[p] == nil {
			all[p] = &acc{}
		}
		all[p].buys += n
	}
	for p, n := range views {
		if all[p] == nil {
			all[p] = &acc{}
		}
		all[p].views += n
	}
	byProd := map[string][]Rec{}
	for p, a := range all {
		if p.A == p.B {
			continue
		}
		score := float64(a.buys*buyWeight + a.views)
		if keep(p.B) {
			byProd[p.A] = append(byProd[p.A], Rec{ID: p.B, Score: score, Buys: a.buys, Views: a.views})
		}
		if keep(p.A) {
			byProd[p.B] = append(byProd[p.B], Rec{ID: p.A, Score: score, Buys: a.buys, Views: a.views})
		}
	}
	for id, l := range byProd {
		sort.Slice(l, func(i, j int) bool {
			if l[i].Score != l[j].Score {
				return l[i].Score > l[j].Score
			}
			return l[i].ID < l[j].ID
		})
		if len(l) > per {
			l = l[:per]
		}
		byProd[id] = l
	}
	return &Index{Built: time.Now(), byProd: byProd}
}

// For returns the recommendations for a product, best first.
func (x *Index) For(id string) []Rec {
	if x == nil {
		return nil
	}
	return x.byProd[id]
}

// ForMany merges the recommendations for several products (a cart), leaving
// out the products themselves.
func (x *Index) ForMany(ids []string, limit int) []Rec {
	if x == nil {
		return nil
	}
	in := map[string]bool{}
	for _, id := range ids {
		in[id] = true
	}
	sum := map[string]*Rec{}
	for _, id := range ids {
		for _, r := range x.byProd[id] {
			if in[r.ID] {
				continue
			}
			if c := sum[r.ID]; c != nil {
				c.Score += r.Score
				c.Buys += r.Buys
				c.Views += r.Views
			} else {
				rr := r
				sum[r.ID] = &rr
			}
		}
	}
	out := make([]Rec, 0, len(sum))
	for _, r := range sum {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Products is how many products have recommendations.
func (x *Index) Products() int {
	if x == nil {
		return 0
	}
	return len(x.byProd)
}

// Basket turns the products of one order (or one visit) into its pairs.
// Large baskets are cut so one bulk order cannot dominate.
func Basket(ids []string, into map[Pair]int) {
	seen := map[string]bool{}
	var u []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			u = append(u, id)
		}
	}
	if len(u) > 20 {
		u = u[:20]
	}
	for i := 0; i < len(u); i++ {
		for j := i + 1; j < len(u); j++ {
			into[Of(u[i], u[j])]++
		}
	}
}
