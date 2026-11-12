package api

// Panel copilot (plan IA-3): a question box for the store's team.
//
//   - Figures ("¿cuánto vendí esta semana?", "¿qué está agotado?", "¿qué
//     buscan y no encuentran?") come from the reports and the read model,
//     with the screen where they live. Nothing is estimated or generated.
//   - "¿Cómo hago…?" is answered with the panel's own help, with a link to
//     the screen that does it.
//   - Every answer respects the person's permissions: someone without
//     reports access gets no sales figures, and help only lists screens
//     they can open.
//   - Without a source the answer is "no sé", never a guess.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/ai"
	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/help"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/reports"
)

// CopilotAnswer is what the copilot says, and where to see or do it.
type CopilotAnswer struct {
	Text  string        `json:"text"`
	Kind  string        `json:"kind"` // data, help, unknown, forbidden
	Links []CopilotLink `json:"links"`
	Rows  []CopilotRow  `json:"rows,omitempty"`
}

// CopilotLink opens a panel screen.
type CopilotLink struct {
	Label string `json:"label"`
	Route string `json:"route"` // after "#"
}

// CopilotRow is one line of a list answer.
type CopilotRow struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Route string `json:"route,omitempty"`
}

var (
	reSales    = regexp.MustCompile(`\b(vend[ií]|vendimos|vendido|ventas|ingresos?|factur[eé]|ganamos|pedidos (de|del|esta|este|hoy|ayer))\b`)
	reTop      = regexp.MustCompile(`\b(mas vendid[oa]s?|top|mejores productos|lo que mas se vende)\b`)
	reOut      = regexp.MustCompile(`\b(agotad[oa]s?|sin existencias?|sin stock|por agotarse|poco inventario|pocas existencias)\b`)
	reNoResult = regexp.MustCompile(`\b(busquedas? sin resultados?|no encuentran|que buscan)\b`)
	reHowTo    = regexp.MustCompile(`\b(como|donde|puedo|quiero|necesito)\b`)
)

func periodOf(fq string) (preset, label string) {
	switch {
	case strings.Contains(fq, "ayer"):
		return "yesterday", "ayer"
	case strings.Contains(fq, "hoy"):
		return "today", "hoy"
	case strings.Contains(fq, "mes pasado"):
		return "lastmonth", "el mes pasado"
	case strings.Contains(fq, "este mes") || strings.Contains(fq, "mes"):
		return "mtd", "este mes"
	case strings.Contains(fq, "ano"):
		return "ytd", "este año"
	case strings.Contains(fq, "30 dias"):
		return "30d", "los últimos 30 días"
	}
	return "7d", "los últimos 7 días"
}

func money2(cents int64, cur string) string { return "$" + catalog.FormatAmount(cents) + " " + cur }

type helpIndex struct {
	once   sync.Once
	topics []help.Topic
	x      *ai.Index
}

var panelHelp helpIndex

func (h *helpIndex) load() {
	h.once.Do(func() {
		h.topics = help.Topics()
		h.x = ai.NewIndex(localEmb.Model(), localEmb.Dim())
		for i, t := range h.topics {
			v, _, _ := localEmb.Embed(context.Background(), []string{t.Title + ". " + t.Title + ". " + t.Body}, ai.Document)
			_ = h.x.Put(fmt.Sprint(i), ai.Quantize(v[0]), nil)
		}
	})
}

func can(p auth.Principal, module, level string) bool {
	return p.Owner || p.Permissions.Allows(module, level)
}

func (s *Server) copilot(ctx context.Context, p auth.Principal, q string) CopilotAnswer {
	fq := foldQ(q)
	forbidden := func(area string) CopilotAnswer {
		return CopilotAnswer{Kind: "forbidden", Text: "Tu acceso no incluye " + area + ". Pídele a quien administra la tienda que te dé permiso.", Links: []CopilotLink{}}
	}
	switch {
	case reTop.MatchString(fq):
		if !can(p, "reports", auth.Read) {
			return forbidden("reportes")
		}
		preset, label := periodOf(fq)
		rg, _ := reports.ParseRange(preset, "", "", time.Now())
		rep, err := s.Reports.ProductsReport(ctx, p.Tenant, reports.Filter{Range: rg}, "product", 5)
		if err != nil {
			break
		}
		a := CopilotAnswer{Kind: "data", Links: []CopilotLink{{"Ver reporte de productos", "reportes/productos"}}}
		if len(rep.Rows) == 0 {
			a.Text = "No hubo ventas " + label + "."
			return a
		}
		a.Text = "Lo más vendido " + label + ", por ingresos:"
		for _, r := range rep.Rows {
			a.Rows = append(a.Rows, CopilotRow{Label: r.Name, Value: fmt.Sprintf("%d piezas · %s", r.Units, money2(r.Revenue, rep.Currency))})
		}
		return a
	case reSales.MatchString(fq):
		if !can(p, "reports", auth.Read) {
			return forbidden("reportes de ventas")
		}
		preset, label := periodOf(fq)
		rg, _ := reports.ParseRange(preset, "", "", time.Now())
		d, err := s.Reports.Dashboard(ctx, p.Tenant, reports.Filter{Range: rg})
		if err != nil {
			break
		}
		t := d.Totals
		return CopilotAnswer{Kind: "data", Text: fmt.Sprintf("%s: %d pedidos, %d piezas, %s de venta neta.", upperFirst(label), t.Orders, t.Units, money2(t.Net, d.Currency)),
			Rows:  []CopilotRow{{"Pedidos", fmt.Sprint(t.Orders), ""}, {"Venta neta", money2(t.Net, d.Currency), ""}, {"Periodo anterior", fmt.Sprintf("%d pedidos · %s", d.PrevTotals.Orders, money2(d.PrevTotals.Net, d.Currency)), ""}},
			Links: []CopilotLink{{"Ver reportes", "reportes"}}}
	case reOut.MatchString(fq):
		if !can(p, "reports", auth.Read) {
			return forbidden("inventario")
		}
		state := "out"
		if strings.Contains(fq, "poco") || strings.Contains(fq, "pocas") || strings.Contains(fq, "por agotarse") {
			state = "low"
		}
		inv, err := s.Reports.InventoryReport(ctx, p.Tenant, mainChannel, state, reports.LowStockThreshold, 8)
		if err != nil {
			break
		}
		a := CopilotAnswer{Kind: "data", Links: []CopilotLink{{"Ver inventario", "reportes/inventario"}}}
		a.Text = fmt.Sprintf("%d SKU agotados y %d con %d piezas o menos.", inv.OutOfStock, inv.LowStock, inv.Threshold)
		for _, r := range inv.Rows {
			a.Rows = append(a.Rows, CopilotRow{Label: r.Title + " (" + r.SKU + ")", Value: fmt.Sprintf("%d disponibles", r.Available), Route: "catalogo/productos/" + r.ProductID})
		}
		return a
	case reNoResult.MatchString(fq):
		if !can(p, "catalog", auth.Read) {
			return forbidden("búsqueda")
		}
		_ = s.FlushSearchTerms(ctx)
		zero, err := s.DB.SearchTerms(ctx, p.Tenant, 30, 8, true)
		if err != nil {
			break
		}
		a := CopilotAnswer{Kind: "data", Links: []CopilotLink{{"Ver qué buscan", "busqueda/terminos"}}}
		if len(zero) == 0 {
			a.Text = "En los últimos 30 días todas las búsquedas encontraron productos."
			return a
		}
		a.Text = "Lo que más buscaron sin encontrar en los últimos 30 días:"
		for _, z := range zero {
			a.Rows = append(a.Rows, CopilotRow{Label: z.Term, Value: fmt.Sprintf("%d búsquedas", z.Searches)})
		}
		return a
	}
	// "¿Cómo hago…?": the panel's help, limited to what this person can do.
	panelHelp.load()
	v, _, _ := localEmb.Embed(ctx, []string{q}, ai.Query)
	hits, _ := panelHelp.x.Search(ai.Quantize(v[0]), 3, nil)
	qw := contentStems(q)
	type cand struct {
		t     help.Topic
		score float64
	}
	var best *cand
	for _, h := range hits {
		var i int
		fmt.Sscan(h.ID, &i)
		t := panelHelp.topics[i]
		sw := contentStems(t.Title + " " + t.Body)
		tw := contentStems(t.Title)
		n, nt := 0, 0
		for w := range qw {
			if sw[w] {
				n++
			}
			if tw[w] {
				nt++
			}
		}
		score := 0.5 * h.Score
		if len(qw) > 0 {
			score += 0.3*float64(n)/float64(len(qw)) + 0.2*float64(nt)/float64(len(qw))
		}
		if best == nil || score > best.score {
			best = &cand{t, score}
		}
	}
	if best != nil && best.score >= copilotFloor && (reHowTo.MatchString(fq) || best.score >= copilotFloor+0.1) {
		t := best.t
		if !can(p, t.Module, t.Level) {
			return CopilotAnswer{Kind: "forbidden", Text: t.Title + ": tu acceso no lo permite. Pídele permiso a quien administra la tienda.", Links: []CopilotLink{}}
		}
		return CopilotAnswer{Kind: "help", Text: t.Body, Links: []CopilotLink{{"Ir a: " + t.Title, t.Route}}}
	}
	return CopilotAnswer{Kind: "unknown", Text: "No sé responder eso todavía. Puedo darte ventas, lo más vendido, lo agotado, lo que buscan sin encontrar y explicarte cómo hacer tareas del panel.", Links: []CopilotLink{}}
}

// copilotFloor is the score a help topic needs to be the answer.
var copilotFloor = 0.3

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

func (s *Server) postCopilot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Q string `json:"q"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Q = strings.TrimSpace(body.Q)
	if n := utf8.RuneCountInString(body.Q); n < 3 || n > 300 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "q", Code: "length", Message: "Escribe una pregunta de 3 a 300 caracteres"})
		return
	}
	httpx.JSON(w, r, http.StatusOK, s.copilot(r.Context(), principal(r), body.Q))
}
