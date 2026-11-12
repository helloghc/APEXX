package api

// Search quality with the store's own data (plan IA-6). The searches where
// shoppers clicked or added to cart are labels: what they chose is what the
// search should show first. The same searches run with the lexical index
// alone and with hybrid search, and the result decides whether training a
// model of our own (embeddings or re-ranker) would be worth it.
//
// Learned boosts are turned off while measuring: they come from these very
// clicks, and measuring with them would grade the answer with its key.

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
)

// Decision thresholds (documented in docs/fases/ia-6-decision.md).
const (
	evalMinQueries   = 200  // fewer labeled searches: not enough to decide
	evalGoodEnough   = 0.75 // hybrid nDCG@10 from which training would not pay off
	evalMaxQueries   = 1000 // the most searched, to keep the measurement short
	evalMinClicksPer = 2    // a search needs this many choices to count
)

type evalScore struct {
	NDCG10   float64 `json:"ndcg10"`
	Recall50 float64 `json:"recall50"`
}

// StoreEval is the measurement and what it means.
type StoreEval struct {
	Queries  int       `json:"queries"`
	Lexical  evalScore `json:"lexical"`
	Hybrid   evalScore `json:"hybrid"`
	Semantic bool      `json:"semantic"` // vectors on for this store
	Model    string    `json:"model"`
	Decision string    `json:"decision"` // collect, not_needed, try_semantic_provider, consider_training
	Advice   string    `json:"advice"`
}

func (s *Server) storeSearchEval(ctx context.Context, t *projection.Tenant) (StoreEval, error) {
	type lab struct {
		term string
		rel  map[string]int
	}
	var labs []lab
	err := s.DB.Tx(ctx, t.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT term, product_id, clicks, carts FROM search_clicks
			WHERE tenant_id=$1 AND term IN (
				SELECT term FROM search_clicks WHERE tenant_id=$1 GROUP BY term HAVING sum(clicks)+sum(carts) >= $2
				ORDER BY sum(clicks)+sum(carts) DESC LIMIT $3)
			ORDER BY term`, t.ID, evalMinClicksPer, evalMaxQueries)
		if err != nil {
			return err
		}
		defer rows.Close()
		byTerm := map[string]map[string]int{}
		var order []string
		for rows.Next() {
			var term, pid string
			var clicks, carts int
			if err := rows.Scan(&term, &pid, &clicks, &carts); err != nil {
				return err
			}
			if byTerm[term] == nil {
				byTerm[term] = map[string]int{}
				order = append(order, term)
			}
			g := 1
			if carts > 0 {
				g = 2
			}
			if g > byTerm[term][pid] {
				byTerm[term][pid] = g
			}
		}
		for _, term := range order {
			labs = append(labs, lab{term, byTerm[term]})
		}
		return rows.Err()
	})
	if err != nil {
		return StoreEval{}, err
	}
	out := StoreEval{Queries: len(labs), Semantic: s.semActive(t.ID), Model: s.embedder().Model()}
	of := offerOf(t, mainChannel)
	run := func(q string, hybrid bool) []string {
		bq := catalog.BrowseQuery{Q: q, Limit: 50}
		s.searchQuery(t, &bq)
		bq.Boost = nil // never grade with the clicks being graded
		if hybrid {
			s.narrow(t, &bq)
		} else {
			narrow(t, &bq)
		}
		res := catalog.Browse(t.Catalog.ProductsIn(bq.Only), t.Taxo, of, bq)
		ids := make([]string, len(res.Items))
		for i, p := range res.Items {
			ids[i] = p.ID
		}
		return ids
	}
	for _, l := range labs {
		lr, hr := run(l.term, false), run(l.term, true)
		out.Lexical.NDCG10 += ai.NDCG(lr, l.rel, 10)
		out.Lexical.Recall50 += ai.Recall(lr, l.rel, 50)
		out.Hybrid.NDCG10 += ai.NDCG(hr, l.rel, 10)
		out.Hybrid.Recall50 += ai.Recall(hr, l.rel, 50)
	}
	if n := float64(len(labs)); n > 0 {
		out.Lexical.NDCG10 /= n
		out.Lexical.Recall50 /= n
		out.Hybrid.NDCG10 /= n
		out.Hybrid.Recall50 /= n
	}
	_, local := s.embedder().(*ai.Local)
	switch {
	case out.Queries < evalMinQueries:
		out.Decision = "collect"
		out.Advice = "Todavía hay pocas búsquedas con clics para decidir. Se mide sola conforme tus clientes buscan y eligen."
	case out.Hybrid.NDCG10 >= evalGoodEnough:
		out.Decision = "not_needed"
		out.Advice = "La búsqueda ya ordena bien lo que tus clientes eligen. Entrenar un modelo propio no se justifica."
	case local:
		out.Decision = "try_semantic_provider"
		out.Advice = "Antes de entrenar, prueba un proveedor de vectores semánticos y vuelve a medir con estas mismas búsquedas."
	default:
		out.Decision = "consider_training"
		out.Advice = "Con un proveedor semántico la búsqueda sigue abajo de la meta. Estas búsquedas sirven para entrenar el reordenador o los vectores; compara el resultado con esta misma medición antes de usarlo."
	}
	return out, nil
}

func (s *Server) getStoreEval(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		httpx.JSON(w, r, http.StatusOK, StoreEval{Decision: "collect"})
		return
	}
	ev, err := s.storeSearchEval(r.Context(), t)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, ev)
}
