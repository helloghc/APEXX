package api

// Knowledge graph (plan IA-4): relations a search does not see.
//
//   compatible_with  X fits Y        the text of X says "compatible con Y",
//                                    "repuesto para Y", "sirve para Y"…
//   accessory_of     X goes with Y   bought together in several orders, X
//                                    from another category and much cheaper
//   substitute_of    X instead of Y  same category, very close in meaning,
//                                    similar price (symmetric)
//
// Every edge is a proposal with its reason in words the merchant can check
// and a confidence. Only approved edges reach the storefront, the assistant
// and MCP; a rejected edge is never proposed again. Decisions are audited.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

const (
	relCompatible = "compatible_with"
	relAccessory  = "accessory_of"
	relSubstitute = "substitute_of"
)

// productGraph is the approved graph of one store, in memory.
type productGraph struct {
	compat      map[string][]string // product -> products it fits
	fits        map[string][]string // product -> products that fit it
	accessories map[string][]string // product -> its accessories
	substitutes map[string][]string // product -> alternatives (both ways)
}

type graphState struct {
	idx   sync.Map // tenant -> *productGraph
	mu    sync.Mutex
	dirty map[string]map[string]bool
	swept map[string]time.Time
}

func (s *Server) graphOf(ctx context.Context, tenant string) *productGraph {
	if v, ok := s.graph.idx.Load(tenant); ok {
		return v.(*productGraph)
	}
	g := s.loadGraph(ctx, tenant)
	s.graph.idx.Store(tenant, g)
	return g
}

func (s *Server) loadGraph(ctx context.Context, tenant string) *productGraph {
	g := &productGraph{compat: map[string][]string{}, fits: map[string][]string{}, accessories: map[string][]string{}, substitutes: map[string][]string{}}
	es, err := s.DB.ApprovedEdges(ctx, tenant)
	if err != nil {
		return g
	}
	for _, e := range es {
		if e.SrcKind != "product" || e.DstKind != "product" {
			continue
		}
		switch e.Rel {
		case relCompatible:
			g.compat[e.SrcID] = append(g.compat[e.SrcID], e.DstID)
			g.fits[e.DstID] = append(g.fits[e.DstID], e.SrcID)
		case relAccessory:
			g.accessories[e.DstID] = append(g.accessories[e.DstID], e.SrcID)
		case relSubstitute:
			g.substitutes[e.SrcID] = append(g.substitutes[e.SrcID], e.DstID)
			g.substitutes[e.DstID] = append(g.substitutes[e.DstID], e.SrcID)
		}
	}
	return g
}

func (s *Server) graphHooks() {
	s.Proj.On(store.TopicAIGraph, func(tenant, _ string) {
		if _, ok := s.graph.idx.Load(tenant); ok {
			s.graph.idx.Store(tenant, s.loadGraph(context.Background(), tenant))
		}
	})
	s.Proj.On(store.TopicProduct, func(tenant, id string) {
		s.graph.mu.Lock()
		if s.graph.dirty == nil {
			s.graph.dirty = map[string]map[string]bool{}
		}
		if s.graph.dirty[tenant] == nil {
			s.graph.dirty[tenant] = map[string]bool{}
		}
		s.graph.dirty[tenant][id] = true
		s.graph.mu.Unlock()
	})
}

// ---------- proposals ----------

var reCompat = regexp.MustCompile(`(?i)\b(compatible con|compatibles con|funciona con|para usarse con|se usa con|sirve para|repuesto para|refaccion para|refacción para|accesorio para|diseñad[oa] para|disenad[oa] para|ideal para usar con)\s+(?:el |la |los |las |tu |su |modelo )?([^.;,\n()]{3,80})`)

func catSet(t *projection.Tenant, p *catalog.Product) map[string]bool {
	return t.Taxo.ProductCategories(p)
}

func sharesCategory(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// proposeFor builds the proposals that involve product p.
func (s *Server) proposeFor(ctx context.Context, t *projection.Tenant, p *catalog.Product, x *ai.Index) []store.Edge {
	var out []store.Edge
	if p.Status != "active" {
		return nil
	}
	of := offerOf(t, mainChannel)
	pc := catSet(t, p)
	po := of(p)

	// 1. Compatibility written in the product's own text.
	text := stripTags(p.Description)
	for k, v := range p.Attributes {
		text += ". " + k + ": " + v
	}
	for _, m := range reCompat.FindAllStringSubmatch(text, 5) {
		phrase := strings.TrimSpace(m[2])
		pw := contentStems(phrase)
		if len(pw) == 0 {
			continue
		}
		// Candidates: products that share a word with the phrase; the one
		// whose title the phrase mentions most completely wins.
		only := map[string]bool{}
		for _, w := range ai.Words(phrase) {
			if ai.IsStopword(w) || len(w) < 3 {
				continue
			}
			for id := range t.Catalog.Matching(w) {
				only[id] = true
			}
			if len(only) > 500 {
				break
			}
		}
		bestID, best, bestShared := "", 0.0, 0
		for id := range only {
			if id == p.ID {
				continue
			}
			q, err := t.Catalog.Get(id)
			if err != nil || q.Status != "active" {
				continue
			}
			tw := contentStems(q.Title)
			n := 0
			for w := range tw {
				if pw[w] {
					n++
				}
			}
			if len(tw) == 0 {
				continue
			}
			cov := float64(n) / float64(len(tw))
			if cov > best || (cov == best && n > bestShared) {
				bestID, best, bestShared = id, cov, n
			}
		}
		if bestShared < 2 {
			continue
		}
		if bestID != "" && best >= 0.6 {
			out = append(out, store.Edge{SrcKind: "product", SrcID: p.ID, Rel: relCompatible, DstKind: "product", DstID: bestID,
				Confidence: math.Round(best*100) / 100, Source: "El texto del producto dice: «" + strings.TrimSpace(m[1]+" "+phrase) + "»"})
		}
	}

	// 2. Accessories: what shoppers buy with p, from another category and
	//    at most half its price.
	if l := s.learned(t.ID); l != nil && l.recs != nil && po.HasPrice {
		for _, r := range l.recs.For(p.ID) {
			if r.Buys < 2 {
				continue
			}
			q, err := t.Catalog.Get(r.ID)
			if err != nil || q.Status != "active" {
				continue
			}
			qo := of(&q)
			if !qo.HasPrice || qo.Price*2 > po.Price || sharesCategory(pc, catSet(t, &q)) {
				continue
			}
			out = append(out, store.Edge{SrcKind: "product", SrcID: q.ID, Rel: relAccessory, DstKind: "product", DstID: p.ID,
				Confidence: math.Min(0.95, 0.5+0.1*float64(r.Buys)),
				Source:     fmt.Sprintf("Se compraron juntos en %d pedidos; es de otra categoría y cuesta menos de la mitad", r.Buys)})
		}
	}

	// 3. Substitutes: same category, very close in meaning, similar price.
	if x != nil && len(pc) > 0 && po.HasPrice {
		if v, ok := x.Vector(p.ID); ok {
			hits, _ := x.Search(v, 8, nil)
			for _, h := range hits {
				if h.ID == p.ID || h.Score < substituteFloor {
					continue
				}
				q, err := t.Catalog.Get(h.ID)
				if err != nil || q.Status != "active" || !sharesCategory(pc, catSet(t, &q)) {
					continue
				}
				qo := of(&q)
				if !qo.HasPrice {
					continue
				}
				diff := math.Abs(float64(qo.Price-po.Price)) / float64(po.Price)
				if diff > 0.4 {
					continue
				}
				a, b := p.ID, q.ID
				if b < a {
					a, b = b, a // one edge per pair: substitutes go both ways
				}
				out = append(out, store.Edge{SrcKind: "product", SrcID: a, Rel: relSubstitute, DstKind: "product", DstID: b,
					Confidence: math.Round(h.Score*100) / 100,
					Source:     fmt.Sprintf("Misma categoría, muy parecidos (%.0f %%) y precio similar (%.0f %% de diferencia)", h.Score*100, diff*100)})
			}
		}
	}
	return out
}

// substituteFloor is how close in meaning two products must be to propose
// one as an alternative to the other.
var substituteFloor = 0.75

// RunGraph proposes edges for changed products of every store (the leader
// runs it); every 6 hours it looks at the whole catalog again.
func (s *Server) RunGraph(ctx context.Context) (int, error) {
	total := 0
	for _, tenant := range s.Proj.TenantIDs() {
		n, err := s.graphTenant(ctx, tenant, false)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *Server) graphTenant(ctx context.Context, tenant string, all bool) (int, error) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok || s.aiCfg(ctx, tenant).SemanticOff {
		return 0, nil
	}
	s.graph.mu.Lock()
	ids := s.graph.dirty[tenant]
	delete(s.graph.dirty, tenant)
	if s.graph.swept == nil {
		s.graph.swept = map[string]time.Time{}
	}
	if time.Since(s.graph.swept[tenant]) > 6*time.Hour {
		all = true
		s.graph.swept[tenant] = time.Now()
	}
	s.graph.mu.Unlock()
	x := s.loadedIndex(tenant)
	var ps []*catalog.Product
	if all {
		ps = t.Catalog.Products()
	} else {
		for id := range ids {
			if p, err := t.Catalog.Get(id); err == nil {
				pp := p
				ps = append(ps, &pp)
			}
		}
	}
	var es []store.Edge
	for _, p := range ps {
		es = append(es, s.proposeFor(ctx, t, p, x)...)
		if len(es) >= 2000 {
			if _, err := s.DB.ProposeEdges(ctx, tenant, es); err != nil {
				return 0, err
			}
			es = es[:0]
		}
	}
	return s.DB.ProposeEdges(ctx, tenant, es)
}

// ---------- panel ----------

type edgeView struct {
	store.Edge
	SrcTitle string `json:"srcTitle"`
	DstTitle string `json:"dstTitle"`
}

func (s *Server) getGraph(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "proposed"
	}
	off, _ := strconv.Atoi(q.Get("offset"))
	es, counts, err := s.DB.Edges(r.Context(), p.Tenant, status, q.Get("rel"), 50, max(off, 0))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	title := func(id string) string {
		if pr, err := t.Catalog.Get(id); err == nil {
			return pr.Title
		}
		return "(producto eliminado)"
	}
	items := make([]edgeView, 0, len(es))
	for _, e := range es {
		items = append(items, edgeView{Edge: e, SrcTitle: title(e.SrcID), DstTitle: title(e.DstID)})
	}
	decided := counts["approved"] + counts["rejected"]
	rate := 0.0
	if decided > 0 {
		rate = float64(counts["approved"]) / float64(decided)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "counts": counts, "approvalRate": rate})
}

func (s *Server) decideGraph(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 500 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "ids", Code: "length", Message: "Elige de 1 a 500 relaciones"})
		return
	}
	if body.Status != "approved" && body.Status != "rejected" && body.Status != "proposed" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "El estado es approved, rejected o proposed"})
		return
	}
	p := principal(r)
	n, err := s.DB.DecideEdges(r.Context(), p.Tenant, p.Actor(), body.Status, body.IDs)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// This node does not wait for the event to show the change.
	s.graph.idx.Store(p.Tenant, s.loadGraph(r.Context(), p.Tenant))
	httpx.JSON(w, r, http.StatusOK, map[string]any{"changed": n})
}

func (s *Server) runGraphNow(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	n, err := s.graphTenant(r.Context(), p.Tenant, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"proposed": n})
}

func (s *Server) routesGraph(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/ai/graph", s.staff("catalog", auth.Read, s.getGraph))
	handle("POST /v1/ai/graph/decide", s.staff("catalog", auth.Edit, s.decideGraph))
	handle("POST /v1/ai/graph/run", s.staff("catalog", auth.Edit, s.runGraphNow))
}

// liveIDs keeps the active products of ids, at most n, without repeats.
func liveIDs(t *projection.Tenant, ids []string, skip map[string]bool, n int) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || skip[id] {
			continue
		}
		seen[id] = true
		if p, err := t.Catalog.Get(id); err == nil && p.Status == "active" {
			out = append(out, id)
		}
		if len(out) >= n {
			break
		}
	}
	return out
}
