package ai

import (
	"math"
	"sort"
)

// NDCG is the normalized discounted cumulative gain at k of a ranked list,
// given graded relevance (0 = not relevant, 1 = relevant, 2 = exact).
func NDCG(ranked []string, rel map[string]int, k int) float64 {
	dcg := 0.0
	for i, id := range ranked {
		if i >= k {
			break
		}
		if g := rel[id]; g > 0 {
			dcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i)+2)
		}
	}
	var gains []int
	for _, g := range rel {
		if g > 0 {
			gains = append(gains, g)
		}
	}
	// ideal order: highest grades first
	for i := 1; i < len(gains); i++ {
		for j := i; j > 0 && gains[j] > gains[j-1]; j-- {
			gains[j], gains[j-1] = gains[j-1], gains[j]
		}
	}
	idcg := 0.0
	for i, g := range gains {
		if i >= k {
			break
		}
		idcg += (math.Pow(2, float64(g)) - 1) / math.Log2(float64(i)+2)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// Recall is the share of relevant ids found in the first k.
func Recall(ranked []string, rel map[string]int, k int) float64 {
	total, found := 0, 0
	in := map[string]bool{}
	for i, id := range ranked {
		if i >= k {
			break
		}
		in[id] = true
	}
	for id, g := range rel {
		if g > 0 {
			total++
			if in[id] {
				found++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(found) / float64(total)
}

// RRF fuses ranked lists with Reciprocal Rank Fusion (k = 60 is the usual
// constant): an id high in either list ends high, without having to make
// lexical and vector scores comparable.
func RRF(k float64, lists ...[]string) []string {
	score := map[string]float64{}
	first := map[string]int{}
	n := 0
	for _, l := range lists {
		for r, id := range l {
			score[id] += 1 / (k + float64(r) + 1)
			if _, ok := first[id]; !ok {
				first[id] = n
				n++
			}
		}
	}
	out := make([]string, 0, len(score))
	for id := range score {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if score[a] != score[b] {
			return score[a] > score[b]
		}
		return first[a] < first[b]
	})
	return out
}
