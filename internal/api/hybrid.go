package api

// Hybrid search (plan IA-1). A text query is answered by two retrievers:
//
//   - lexical: the word index (exact words, prefixes, typos, synonyms);
//   - semantic: the closest product vectors (meaning, intent, other words).
//
// Both lists are fused by rank (Reciprocal Rank Fusion), so neither score
// has to be made comparable to the other, and then re-ranked with the
// store's signals (pins, learned clicks, stock). It runs only when the
// browse cache misses (once per catalog version and query), never in a
// shopper's request otherwise.

import (
	"context"
	"sync"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/projection"
)

// semanticK is how many products by meaning join a search.
const semanticK = 50

// semanticFloor drops products whose meaning is too far from the query
// (cosine similarity); below it a search for nonsense would still "find"
// something. semanticRel keeps only products within this share of the best
// match. Both are measured on the evaluation set (internal/ai/evalset).
var semanticFloor, semanticRel = 0.20, 0.3

// qcache keeps query vectors: the same searches repeat all day.
type qcache struct {
	mu sync.Mutex
	m  map[string][]int8
	// pending: queries being embedded in the background (remote providers).
	pending map[string]bool
}

const qcacheMax = 20000

func (c *qcache) get(k string) ([]int8, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *qcache) put(k string, v []int8) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= qcacheMax {
		c.m = map[string][]int8{} // simple and bounded: a full cache starts over
	}
	c.m[k] = v
	delete(c.pending, k)
}

// queryVector returns the vector of q. With the local embedder it is
// computed at once (microseconds). With a remote provider a cache miss
// starts the call in the background and returns nil: this search is
// lexical, the next ones with the same words are hybrid. A shopper never
// waits for the network.
func (s *Server) queryVector(q string) []int8 {
	e := s.embedder()
	k := e.Model() + "\x00" + q
	if v, ok := s.qvec.get(k); ok {
		return v
	}
	if _, local := e.(*ai.Local); local {
		vs, _, err := e.Embed(context.Background(), []string{q}, ai.Query)
		if err != nil {
			return nil
		}
		v := ai.Quantize(vs[0])
		s.qvec.put(k, v)
		return v
	}
	s.qvec.mu.Lock()
	if s.qvec.pending == nil {
		s.qvec.pending = map[string]bool{}
	}
	if s.qvec.pending[k] || len(s.qvec.pending) > 64 {
		s.qvec.mu.Unlock()
		return nil
	}
	s.qvec.pending[k] = true
	s.qvec.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*1e9)
		defer cancel()
		vs, _, err := e.Embed(ctx, []string{q}, ai.Query)
		if err != nil {
			s.qvec.mu.Lock()
			delete(s.qvec.pending, k)
			s.qvec.mu.Unlock()
			return
		}
		s.qvec.put(k, ai.Quantize(vs[0]))
	}()
	return nil
}

// semActive reports whether searches of tenant use vectors: the store did
// not turn them off and its index has vectors.
func (s *Server) semActive(tenant string) bool {
	if s.aiCfg(context.Background(), tenant).SemanticOff {
		return false
	}
	x := s.loadedIndex(tenant)
	if x == nil {
		// Every node loads its copy in the background on first use; until
		// then searches stay lexical.
		go func() { _, _ = s.semIndex(context.Background(), tenant) }()
		return false
	}
	return x.Len() > 0
}

// semKey goes into browse cache keys: results change when vectors are
// turned on or off.
func (s *Server) semKey(tenant string) string {
	if s.semActive(tenant) {
		return "\x00sem"
	}
	return ""
}

// narrow prepares a text query for Browse: the word index limits the scan
// to products that can match the words, their synonyms or the pins, and
// the semantic index adds the products closest in meaning.
func (s *Server) narrow(t *projection.Tenant, q *catalog.BrowseQuery) {
	if q.Q == "" {
		return
	}
	narrow(t, q)
	if !s.semActive(t.ID) {
		return
	}
	x := s.loadedIndex(t.ID)
	v := s.queryVector(q.Q)
	if x == nil || v == nil {
		return
	}
	hits, err := x.Search(v, semanticK, func(id string) bool { return !q.Exclude[id] })
	if err != nil || len(hits) == 0 {
		return
	}
	floor := max(semanticFloor, hits[0].Score*semanticRel)
	sem := map[string]int{}
	for i, h := range hits {
		if h.Score < floor {
			break
		}
		sem[h.ID] = i
	}
	if len(sem) == 0 {
		return
	}
	q.Semantic = sem
	if q.Only != nil {
		for id := range sem {
			q.Only[id] = true
		}
	}
}
