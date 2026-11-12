package api

// Semantic index (plan IA-0): one vector per product, kept in step with the
// catalog by the outbox like every other read model.
//
//	product saved ──► outbox ──► every node marks it dirty
//	leader (lease "ai-embed") ──► embeds what changed ──► ai_chunks + event ai.vectors
//	every node ──► loads those vectors into its in-memory index
//	product deleted ──► its chunks go in the same transaction; every node drops it
//
// Shoppers never wait for any of it: search reads the in-memory index and,
// while vectors are missing or the provider is down, keeps answering with
// the lexical index alone.

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// embedBatch is how many products go to the provider per call.
const embedBatch = 256

// sweepEvery is how often the leader compares the whole catalog with the
// vectors (self-healing: anything missed, changed or orphaned is fixed).
const sweepEvery = 10 * time.Minute

type semState struct {
	mu        sync.Mutex
	dirty     map[string]map[string]bool // tenant -> product ids to (re)embed
	swept     map[string]time.Time       // tenant -> last full comparison
	idx       sync.Map                   // tenant -> *semTenant
	lastError string
	// own: events this node wrote (tenant + key); its index already has
	// those vectors, so the event does not read them back.
	own map[string]bool
}

type semTenant struct {
	once sync.Once
	x    *ai.Index
	err  error
}

// embedder returns the provider of this node (local when none is set).
func (s *Server) embedder() ai.Embedder {
	if s.Embedder == nil {
		s.Embedder = ai.NewLocal(384)
	}
	return s.Embedder
}

// semIndex returns the vector index of tenant, loading it from Postgres the
// first time (on every node, so any node can answer a search).
func (s *Server) semIndex(ctx context.Context, tenant string) (*ai.Index, error) {
	v, _ := s.sem.idx.LoadOrStore(tenant, &semTenant{})
	st := v.(*semTenant)
	st.once.Do(func() {
		e := s.embedder()
		x := ai.NewIndex(e.Model(), e.Dim())
		st.err = s.DB.ProductChunks(ctx, tenant, e.Model(), nil, func(id string, vec, hash []byte) {
			_ = x.Put(id, ai.Decode(vec), hash)
		})
		st.x = x
	})
	if st.err != nil {
		// Try again on the next call instead of keeping a broken index.
		s.sem.idx.Delete(tenant)
		return nil, st.err
	}
	return st.x, nil
}

// loadedIndex returns the index only if it is already in memory.
func (s *Server) loadedIndex(tenant string) *ai.Index {
	v, ok := s.sem.idx.Load(tenant)
	if !ok {
		return nil
	}
	st := v.(*semTenant)
	if st.x == nil {
		return nil
	}
	return st.x
}

// semHooks follows the catalog: changed products are marked for the
// leader, deleted ones leave the index, written vectors enter it.
func (s *Server) semHooks() {
	s.Proj.On(store.TopicProduct, func(tenant, id string) {
		t, ok := s.Proj.Tenant(tenant)
		if ok {
			if _, err := t.Catalog.Get(id); err != nil {
				if x := s.loadedIndex(tenant); x != nil {
					x.Delete(id)
				}
				return
			}
		}
		s.sem.mu.Lock()
		if s.sem.dirty == nil {
			s.sem.dirty = map[string]map[string]bool{}
		}
		if s.sem.dirty[tenant] == nil {
			s.sem.dirty[tenant] = map[string]bool{}
		}
		s.sem.dirty[tenant][id] = true
		s.sem.mu.Unlock()
	})
	s.Proj.On(store.TopicAIVectors, func(tenant, key string) {
		s.sem.mu.Lock()
		mine := s.sem.own[tenant+"\x00"+key]
		delete(s.sem.own, tenant+"\x00"+key)
		s.sem.mu.Unlock()
		x := s.loadedIndex(tenant)
		if x == nil || mine {
			return // loaded in full on first use, or written by this node
		}
		ids := strings.Split(key, ",")
		_ = s.DB.ProductChunks(context.Background(), tenant, x.Model(), ids, func(id string, vec, hash []byte) {
			_ = x.Put(id, ai.Decode(vec), hash)
		})
	})
}

// productText is what a product's vector is made from: what it is (title,
// type, category path, brand), what describes it (tags, attributes) and the
// start of its description. The title goes twice: it is the strongest clue.
func productText(t *projection.Tenant, p *catalog.Product) string {
	var b strings.Builder
	w := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			if b.Len() > 0 {
				b.WriteString(". ")
			}
			b.WriteString(s)
		}
	}
	w(p.Title)
	w(p.Title)
	w(p.Type)
	for _, c := range p.Categories {
		var names []string
		for _, n := range t.Taxo.Path(c) {
			names = append(names, n.Name)
		}
		if len(names) == 0 {
			names = []string{c}
		}
		w(strings.Join(names, " > "))
	}
	w(p.Brand)
	w(strings.Join(p.Tags, ", "))
	keys := make([]string, 0, len(p.Attributes))
	for k := range p.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		w(k + ": " + p.Attributes[k])
	}
	d := stripTags(p.Description)
	if r := []rune(d); len(r) > 1200 {
		d = string(r[:1200])
	}
	w(d)
	return b.String()
}

func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteByte(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func textHash(model, text string) []byte {
	h := sha256.Sum256([]byte(model + "\x00" + text))
	return h[:]
}

// aiCfg reads the store's AI settings (from the learning index when it is
// built, which keeps them in memory).
func (s *Server) aiCfg(ctx context.Context, tenant string) aiConfig {
	if l := s.learned(tenant); l != nil {
		return l.cfg
	}
	var cfg aiConfig
	if s.DB != nil {
		_, _ = s.DB.GetDoc(ctx, tenant, aiDocKind, aiDocID, &cfg)
	}
	return cfg
}

// errBudget means the store spent this month's AI budget.
var errBudget = errors.New("ai: monthly budget reached")

// RunEmbeddings embeds what changed in every store (the leader runs it).
// Returns how many products were embedded.
func (s *Server) RunEmbeddings(ctx context.Context) (int, error) {
	total := 0
	var first error
	for _, tenant := range s.Proj.TenantIDs() {
		n, err := s.embedTenant(ctx, tenant)
		total += n
		if err != nil && first == nil && !errors.Is(err, errBudget) {
			first = err
		}
	}
	s.sem.mu.Lock()
	if first != nil {
		s.sem.lastError = first.Error()
	} else {
		s.sem.lastError = ""
	}
	s.sem.mu.Unlock()
	return total, first
}

func (s *Server) embedTenant(ctx context.Context, tenant string) (int, error) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return 0, nil
	}
	cfg := s.aiCfg(ctx, tenant)
	if cfg.SemanticOff {
		return 0, nil
	}
	x, err := s.semIndex(ctx, tenant)
	if err != nil {
		return 0, err
	}
	e := s.embedder()

	// What to look at: the products marked dirty, or all of them on the
	// first run and every sweepEvery (self-healing).
	s.sem.mu.Lock()
	ids := s.sem.dirty[tenant]
	delete(s.sem.dirty, tenant)
	if s.sem.swept == nil {
		s.sem.swept = map[string]time.Time{}
	}
	sweep := time.Since(s.sem.swept[tenant]) > sweepEvery
	if sweep {
		s.sem.swept[tenant] = time.Now()
	}
	s.sem.mu.Unlock()
	requeue := func() {
		s.sem.mu.Lock()
		defer s.sem.mu.Unlock()
		if sweep {
			delete(s.sem.swept, tenant) // the comparison is redone on the next tick
		}
		if len(ids) == 0 {
			return
		}
		if s.sem.dirty == nil {
			s.sem.dirty = map[string]map[string]bool{}
		}
		if s.sem.dirty[tenant] == nil {
			s.sem.dirty[tenant] = map[string]bool{}
		}
		for id := range ids {
			s.sem.dirty[tenant][id] = true
		}
	}

	var todo []*catalog.Product
	var texts []string
	var hashes [][]byte
	consider := func(p *catalog.Product) {
		txt := productText(t, p)
		h := textHash(e.Model(), txt)
		if old, ok := x.Hash(p.ID); ok && string(old) == string(h) {
			return
		}
		todo = append(todo, p)
		texts = append(texts, txt)
		hashes = append(hashes, h)
	}
	if sweep {
		live := map[string]bool{}
		for _, p := range t.Catalog.Products() {
			live[p.ID] = true
			consider(p)
		}
		for _, id := range x.IDs() {
			if !live[id] {
				x.Delete(id)
			}
		}
		if _, err := s.DB.DropStaleChunks(ctx, tenant, e.Model()); err != nil {
			requeue()
			return 0, err
		}
	} else {
		for id := range ids {
			if p, err := t.Catalog.Get(id); err == nil {
				consider(&p)
			}
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	done := 0
	for start := 0; start < len(todo); start += embedBatch {
		end := min(start+embedBatch, len(todo))
		if _, paid := e.(*ai.Local); !paid {
			spent, err := s.DB.AIMonthCost(ctx, tenant)
			if err != nil {
				requeue()
				return done, err
			}
			if spent >= cfg.budgetMicros() {
				requeue()
				return done, errBudget
			}
		}
		vecs, use, err := e.Embed(ctx, texts[start:end], ai.Document)
		if err != nil {
			requeue() // provider down: lexical search keeps working, retried next tick
			return done, err
		}
		cs := make([]store.Chunk, 0, end-start)
		for i, v := range vecs {
			p := todo[start+i]
			cs = append(cs, store.Chunk{SourceKind: "product", SourceID: p.ID, Model: e.Model(), // the product itself is the text
				Vec: ai.Encode(ai.Quantize(v)), Hash: hashes[start+i]})
		}
		written, err := s.DB.PutChunks(ctx, tenant, cs)
		if err != nil {
			requeue()
			return done, err
		}
		// This node does not wait for its own event to search with them.
		s.sem.mu.Lock()
		if s.sem.own == nil {
			s.sem.own = map[string]bool{}
		}
		if len(written) > 0 && len(s.sem.own) < 10000 {
			s.sem.own[tenant+"\x00"+strings.Join(written, ",")] = true
		}
		s.sem.mu.Unlock()
		ok := map[string]bool{}
		for _, id := range written {
			ok[id] = true
		}
		for _, c := range cs {
			if ok[c.SourceID] {
				_ = x.Put(c.SourceID, ai.Decode(c.Vec), c.Hash)
			}
		}
		done += len(written)
		if err := s.DB.AddAIUsage(ctx, tenant, "embeddings", e.Model(), 1, int64(use.Tokens), 0, use.CostMicros); err != nil {
			return done, err
		}
	}
	return done, nil
}

// SemanticSearch returns the products closest in meaning to q (ids, best
// first). It answers from memory; an empty result means "no vectors yet".
func (s *Server) SemanticSearch(ctx context.Context, tenant, q string, k int, keep func(id string) bool) ([]ai.Hit, error) {
	x, err := s.semIndex(ctx, tenant)
	if err != nil || x.Len() == 0 {
		return nil, err
	}
	vs, _, err := s.embedder().Embed(ctx, []string{q}, ai.Query)
	if err != nil {
		return nil, err
	}
	return x.Search(ai.Quantize(vs[0]), k, keep)
}

// semanticStatus is what the panel shows about the store's vectors.
func (s *Server) semanticStatus(ctx context.Context, tenant string, t *projection.Tenant, cfg aiConfig) map[string]any {
	e := s.embedder()
	_, local := e.(*ai.Local)
	out := map[string]any{"on": !cfg.SemanticOff, "model": e.Model(), "local": local, "products": 0, "vectors": 0,
		"budgetUsd": float64(cfg.budgetMicros()) / 1e6}
	if t != nil {
		out["products"] = t.Catalog.Count()
	}
	if x := s.loadedIndex(tenant); x != nil {
		out["vectors"] = x.Len()
		out["memoryBytes"] = x.Bytes()
	}
	if c, err := s.DB.AIMonthCost(ctx, tenant); err == nil {
		out["monthCostUsd"] = float64(c) / 1e6
	}
	s.sem.mu.Lock()
	if s.sem.lastError != "" {
		out["lastError"] = s.sem.lastError
	}
	s.sem.mu.Unlock()
	return out
}
