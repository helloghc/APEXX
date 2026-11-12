package api

// Review summary (plan IA-3): what customers highlight and what they would
// improve, in their own words. Each point is a real sentence of a real
// review, with its author: a summary that can be checked, never a model's
// paraphrase that could put words in a customer's mouth.
//
// Sentences of the same opinion are close in meaning, so the most
// representative sentence of each side is the one closest to the others
// (a medoid); the next points must say something different (they are kept
// only if they are not near a point already chosen).

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/store"
)

// ReviewPoint is one cited sentence.
type ReviewPoint struct {
	Text   string `json:"text"`
	Author string `json:"author"`
	Rating int    `json:"rating"`
}

// ReviewSummary of a product.
type ReviewSummary struct {
	Pros  []ReviewPoint `json:"pros"`
	Cons  []ReviewPoint `json:"cons"`
	Count int           `json:"count"`
}

// reviewSumMin is how many approved reviews a summary needs.
const reviewSumMin = 3

type revSumCache struct {
	at  time.Time
	sum *ReviewSummary
}

type revSumState struct{ m sync.Map } // tenant|product -> revSumCache

func (s *Server) reviewSummary(ctx context.Context, tenant, productID string) *ReviewSummary {
	key := tenant + "|" + productID
	if v, ok := s.revSum.m.Load(key); ok {
		if c := v.(revSumCache); time.Since(c.at) < 5*time.Minute {
			return c.sum
		}
	}
	rv, err := s.DB.PublicReviews(ctx, tenant, productID, 200)
	var sum *ReviewSummary
	if err == nil {
		sum = summarizeReviews(rv.Items)
	}
	s.revSum.m.Store(key, revSumCache{at: time.Now(), sum: sum})
	return sum
}

func summarizeReviews(items []store.Review) *ReviewSummary {
	if len(items) < reviewSumMin {
		return nil
	}
	var pos, neg []ReviewPoint
	for _, r := range items {
		for _, sn := range ai.Sentences(r.Body) {
			n := utf8.RuneCountInString(sn)
			if n < 15 || n > 220 || reInjection.MatchString(foldQ(sn)) {
				continue
			}
			p := ReviewPoint{Text: sn, Author: r.Author, Rating: r.Rating}
			switch {
			case r.Rating >= 4:
				pos = append(pos, p)
			case r.Rating <= 2:
				neg = append(neg, p)
			}
		}
	}
	out := &ReviewSummary{Count: len(items), Pros: representative(pos, 3), Cons: representative(neg, 2)}
	if len(out.Pros) == 0 && len(out.Cons) == 0 {
		return nil
	}
	return out
}

// representative picks up to k sentences: the one closest to all others
// first, then the next most central that says something else.
func representative(ps []ReviewPoint, k int) []ReviewPoint {
	if len(ps) == 0 {
		return []ReviewPoint{}
	}
	if len(ps) > 300 {
		ps = ps[:300]
	}
	texts := make([]string, len(ps))
	for i, p := range ps {
		texts[i] = p.Text
	}
	vs, _, _ := localEmb.Embed(context.Background(), texts, ai.Document)
	q := make([][]int8, len(vs))
	for i, v := range vs {
		q[i] = ai.Quantize(v)
	}
	central := make([]float64, len(q))
	for i := range q {
		for j := range q {
			if i != j {
				central[i] += ai.Similarity(ai.Dot(q[i], q[j]))
			}
		}
	}
	stems := make([]map[string]bool, len(ps))
	for i, p := range ps {
		stems[i] = contentStems(p.Text)
	}
	same := func(a, b int) bool {
		if ai.Similarity(ai.Dot(q[a], q[b])) > 0.6 {
			return true
		}
		inter, union := 0, len(stems[b])
		for w := range stems[a] {
			if stems[b][w] {
				inter++
			} else {
				union++
			}
		}
		return union > 0 && float64(inter)/float64(union) >= 0.4
	}
	used := make([]bool, len(q))
	var out []ReviewPoint
	var chosen []int
	for len(out) < k {
		best := -1
		for i := range q {
			if used[i] {
				continue
			}
			near := false
			for _, c := range chosen {
				if same(i, c) {
					near = true
					break
				}
			}
			if near {
				continue
			}
			if best < 0 || central[i] > central[best] {
				best = i
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		chosen = append(chosen, best)
		out = append(out, ps[best])
	}
	return out
}
