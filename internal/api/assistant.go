package api

// Shopper assistant (plan IA-3): one box for anything a shopper types.
//
//   - "busco unos tenis rojos para correr": products from the hybrid
//     search, with prices and stock from the read model;
//   - "¿la licuadora tritura hielo?": a question about a product it names,
//     answered from that product's own words;
//   - "¿cuánto tarda el envío?": a question about the store, answered from
//     its policies.
//
// Every number shown comes from the system or is quoted from a source.

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
)

var (
	reShopping = regexp.MustCompile(`^(busco|buscando|quiero|necesito|recomiendame|recomienda|muestrame|ensename|tienen|venden|hay)\b|\b(para regalar|regalo para|algo para)\b`)
	reQuestion = regexp.MustCompile(`^(como|cuanto|cuantos|cuantas|cual|cuales|cuando|donde|que|por que|puedo|se puede|aceptan|hacen|tiene|tienen garantia|es|son|incluye|trae)\b`)
)

// AssistantReply is the answer and, when the shopper is looking for
// something, the products that match.
type AssistantReply struct {
	Answer   Answer     `json:"answer"`
	Products []cardJSON `json:"products"`
	About    string     `json:"about,omitempty"` // handle of the product the question was about
}

func (s *Server) assistant(ctx context.Context, t *projection.Tenant, pd pageData, q string) AssistantReply {
	fq := foldQ(q)
	out := AssistantReply{Products: []cardJSON{}}
	of := offerOf(t, pd.Channel)
	find := func(text string, limit int) catalog.BrowseResult {
		bq := catalog.BrowseQuery{Q: text, Limit: limit}
		s.searchQuery(t, &bq)
		if it := catalog.ParseIntent(text, t.Taxo); it.Any() {
			iq := bq
			iq.Q, iq.Brands, iq.Attrs, iq.PriceMin, iq.PriceMax = it.Q, it.Brands, it.Attrs, it.PriceMin, it.PriceMax
			if it.Sort != "" {
				iq.Sort = it.Sort
			}
			if iq.Q != "" {
				s.narrow(t, &iq)
			}
			if r := catalog.Browse(t.Catalog.ProductsIn(iq.Only), t.Taxo, of, iq); r.Total > 0 {
				r.Understood = it.Labels
				return r
			}
		}
		s.narrow(t, &bq)
		return catalog.Browse(t.Catalog.ProductsIn(bq.Only), t.Taxo, of, bq)
	}
	// "¿Hay…disponible?" and "¿cuánto cuesta…?" are questions about a
	// product, not a search, even when they start like one.
	if reShopping.MatchString(fq) && !reQuestion.MatchString(fq) && !reStock.MatchString(strings.TrimPrefix(fq, "hay ")) && !rePrice.MatchString(fq) {
		text := reShopping.ReplaceAllString(fq, "")
		res := find(strings.TrimSpace(text), 4)
		res = liveOnly(t.Catalog, res)
		for i, p := range res.Items {
			out.Products = append(out.Products, cardJSONOf(t, pd, p, res.Offers[i]))
		}
		if len(out.Products) == 0 {
			out.Answer = Answer{Kind: "unknown", Text: "No encontré productos para eso. Prueba con otras palabras o escríbele a la tienda.", Sources: []Citation{}}
			return out
		}
		txt := "Esto es lo que encontré:"
		if len(res.Understood) > 0 {
			txt = "Esto es lo que encontré (" + strings.Join(res.Understood, " · ") + "):"
		}
		out.Answer = Answer{Kind: "products", Text: txt, Sources: []Citation{}}
		return out
	}
	// A question about a product the shopper names: the product whose title
	// shares most of its words with the question.
	var about *catalog.Product
	if res := find(q, 3); res.Total > 0 && res.Total <= 20 {
		qw := contentStems(q)
		bestN := 0
		for _, p := range res.Items {
			tw := contentStems(p.Title)
			n := 0
			for w := range tw {
				if qw[w] {
					n++
				}
			}
			if n > bestN && n*2 >= len(tw) {
				pp := *p
				about, bestN = &pp, n
			}
		}
	}
	out.Answer = s.answerQuestion(ctx, t, pd.Base, pd.Channel, q, about)
	if about != nil {
		out.About = about.Handle
	}
	return out
}

func (s *Server) shopAssistant(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, suggestLimiter) {
		return
	}
	var body struct {
		Q string `json:"q"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Q = strings.TrimSpace(body.Q)
	if n := utf8.RuneCountInString(body.Q); n < 3 || n > 300 {
		httpx.JSON(w, r, http.StatusBadRequest, map[string]any{"detail": "Escribe de 3 a 300 caracteres"})
		return
	}
	t, _ := s.Proj.Tenant(r.PathValue("tenant"))
	pd := pageData{Tenant: t.ID, Base: "/s/" + t.ID + "/" + siteParam(r), Channel: mainChannel}
	httpx.JSON(w, r, http.StatusOK, s.assistant(r.Context(), t, pd, body.Q))
}
