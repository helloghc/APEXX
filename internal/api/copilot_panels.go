package api

// Panel copilots (plan IA-5) for customers, promotions and orders. Each one
// turns what the merchant writes into the system's own structure, validated
// by the system, and shows it before anything is saved: the merchant saves
// it through the usual screen (audited there) or discards it.
//
//   segment    "compraron más de 3 veces y gastaron más de $2,000" -> rules,
//              with how many customers match today
//   promotion  "3x2 en calcetas", "20% en playeras", "envío gratis desde
//              $999" -> a draft promotion, checked by the promotion engine
//              and summarized in its own words
//   order      a summary of what happened, a reply to the customer by state,
//              and the reason of a return read from the customer's words

import (
	"fmt"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/reports"
)

// ---------- segments ----------

var (
	reSegOrdersMore = regexp.MustCompile(`\b(?:mas de|mayor a|arriba de)\s+(\d+)\s+(?:veces|compras|pedidos|ordenes)`)
	reSegOrdersMin  = regexp.MustCompile(`\b(?:al menos|minimo|por lo menos|(\d+) o mas)\s*(\d+)?\s*(?:veces|compras|pedidos|ordenes)`)
	reSegOrdersLess = regexp.MustCompile(`\b(?:menos de|hasta)\s+(\d+)\s+(?:veces|compras|pedidos|ordenes)`)
	reSegOnce       = regexp.MustCompile(`\b(?:solo una vez|una sola vez|una sola compra|primera compra)\b`)
	reSegSpentMore  = regexp.MustCompile(`\b(?:(?:gastaron|gastado|gasto)\s+(?:mas de|mayor a|arriba de|al menos)\s+\$?\s*(\d+)|(?:compraron|han comprado)\s+(?:mas de|mayor a|arriba de|al menos)\s+\$\s*(\d+))`)
	reSegSpentLess  = regexp.MustCompile(`\b(?:gastaron|gastado|gasto)\s+(?:menos de|hasta)\s+\$?\s*(\d+)`)
	reThousands     = regexp.MustCompile(`(\d),(\d{3})\b`)
	reSegInactive   = regexp.MustCompile(`\b(?:no (?:han )?compra(?:n|do)? (?:desde hace|en)|sin comprar (?:desde hace|en)?|inactivos? (?:desde hace|por)?|hace mas de)\s+(\d+)\s+(dias?|semanas?|meses|mes|anos?)`)
	reSegRecent     = regexp.MustCompile(`\b(?:compraron|han comprado|activos?) (?:en|durante) (?:los |el )?ultimos?\s+(\d+)\s+(dias?|semanas?|meses|mes|anos?)`)
	reSegConsent    = regexp.MustCompile(`\b(aceptan|aceptaron|reciben|suscritos?|suscritas?|quieren) (correos|emails|marketing|el boletin|promociones|newsletter)`)
	reSegAccount    = regexp.MustCompile(`\b(sin cuenta|no tienen cuenta|tienen cuenta|con cuenta)\b`)
	reSegTag        = regexp.MustCompile(`\b(?:etiqueta|etiquetados? como|tag)\s+«?"?([a-z0-9][a-z0-9 _-]{0,30}?)"?»?(?:\s|$|,|\.)`)
)

func daysOf(n int64, unit string) int64 {
	switch {
	case strings.HasPrefix(unit, "semana"):
		return n * 7
	case strings.HasPrefix(unit, "mes"):
		return n * 30
	case strings.HasPrefix(unit, "ano"):
		return n * 365
	}
	return n
}

// normCmd prepares what the merchant wrote for the copilots' rules:
// lowercase, no accents, amounts without thousands separators, and "$" and
// "%" kept (they carry meaning: "$999", "20%").
func normCmd(s string) string {
	s = reThousands.ReplaceAllString(reThousands.ReplaceAllString(s, "$1$2"), "$1$2")
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '$' || r == '%':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	return strings.ReplaceAll(out, "$ ", "$")
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n
}

// parseSegment turns a description into segment rules and says, line by
// line, how each part was understood.
func parseSegment(text string) (customers.Rules, []string) {
	fq := normCmd(text)
	r := customers.Rules{Match: "all"}
	var why []string
	add := func(field, op string, v any, explain string) {
		r.Conditions = append(r.Conditions, customers.Condition{Field: field, Op: op, Value: v})
		why = append(why, explain)
	}
	if m := reSegOrdersMore.FindStringSubmatch(fq); m != nil {
		n := atoi(m[1]) + 1
		add("orders_count", "gte", n, fmt.Sprintf("Pedidos: %d o más", n))
	} else if m := reSegOrdersMin.FindStringSubmatch(fq); m != nil {
		n := atoi(m[1] + m[2])
		if n > 0 {
			add("orders_count", "gte", n, fmt.Sprintf("Pedidos: %d o más", n))
		}
	} else if m := reSegOrdersLess.FindStringSubmatch(fq); m != nil {
		n := atoi(m[1]) - 1
		add("orders_count", "lte", n, fmt.Sprintf("Pedidos: %d o menos", n))
	} else if reSegOnce.MatchString(fq) {
		add("orders_count", "eq", 1, "Pedidos: exactamente 1")
	}
	if m := reSegSpentMore.FindStringSubmatch(fq); m != nil {
		n := atoi(m[1] + m[2])
		add("total_spent", "gte", n*100, fmt.Sprintf("Total gastado: $%s o más", catalog.FormatAmount(n*100)))
	} else if m := reSegSpentLess.FindStringSubmatch(fq); m != nil {
		n := atoi(m[1])
		add("total_spent", "lte", n*100, fmt.Sprintf("Total gastado: hasta $%s", catalog.FormatAmount(n*100)))
	}
	if m := reSegInactive.FindStringSubmatch(fq); m != nil {
		d := daysOf(atoi(m[1]), m[2])
		add("days_since_last_order", "gte", d, fmt.Sprintf("Sin comprar desde hace %d días o más", d))
	} else if m := reSegRecent.FindStringSubmatch(fq); m != nil {
		d := daysOf(atoi(m[1]), m[2])
		add("days_since_last_order", "lte", d, fmt.Sprintf("Compraron en los últimos %d días", d))
	}
	if reSegConsent.MatchString(fq) {
		add("marketing_consent", "is", true, "Aceptan correos de marketing")
	}
	if m := reSegAccount.FindStringSubmatch(fq); m != nil {
		has := m[1] == "tienen cuenta" || m[1] == "con cuenta"
		add("has_account", "is", has, map[bool]string{true: "Tienen cuenta en la tienda", false: "No tienen cuenta"}[has])
	}
	if m := reSegTag.FindStringSubmatch(fq); m != nil {
		tag := strings.TrimSpace(m[1])
		add("tag", "has", tag, "Con la etiqueta «"+tag+"»")
	}
	if strings.Contains(fq, " o ") && strings.Contains(fq, "cualquier") || strings.HasPrefix(fq, "cualquiera") {
		r.Match = "any"
	}
	return r, why
}

func (s *Server) copilotSegment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Q string `json:"q"`
	}
	if !decode(w, r, &body) {
		return
	}
	rules, why := parseSegment(body.Q)
	if len(rules.Conditions) == 0 {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"understood": false, "explain": []string{}, "message": "No entendí condiciones. Prueba con: «compraron más de 3 veces y gastaron más de $2,000», «no compran desde hace 90 días», «aceptan correos»."})
		return
	}
	n, err := s.Customers.PreviewRules(r.Context(), principal(r).Tenant, rules)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"understood": true, "rules": rules, "explain": why, "count": n})
}

// ---------- promotions ----------

var (
	reBxGy      = regexp.MustCompile(`\b(\d)\s*[x×]\s*(\d)\b`)
	rePercent   = regexp.MustCompile(`\b(\d{1,2})\s*(?:%|por ciento)`)
	reFixedOff  = regexp.MustCompile(`\$\s*([\d,]+)\s+(?:de descuento|menos|off)`)
	reFreeShip  = regexp.MustCompile(`\benvio gratis\b`)
	reFromAmt   = regexp.MustCompile(`\b(?:desde|a partir de|en compras (?:de|mayores a|desde))\s+\$?\s*([\d,]+)`)
	reOnTarget  = regexp.MustCompile(`\b(?:en|para|de)\s+(?:todas? (?:las?|los) )?(?:la |las |los |el )?([a-z][a-z ]{2,40}?)(?:\s+(?:desde|del|a partir|hasta|este|esta|en compras|por)\b|$)`)
	reUntilDate = regexp.MustCompile(`\b(?:hasta el|hasta)\s+(\d{1,2})\s+de\s+([a-z]+)`)
	reCoupon    = regexp.MustCompile(`\b(?:con (?:el )?cupon|codigo)\s+([a-z0-9-]{3,20})`)
)

var monthsEs = map[string]time.Month{"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7, "agosto": 8, "septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12}

// parsePromotion builds a draft promotion from a description. It never
// activates anything: the result is a draft for the promotion builder.
func parsePromotion(t *projection.Tenant, cur money.Currency, text string, now time.Time) (promo.Promotion, []string) {
	fq := normCmd(text)
	p := promo.Promotion{Name: strings.TrimSpace(text), Status: promo.StatusDraft, Currency: cur, Stacking: promo.Stacking{}}
	var why []string
	switch {
	case reBxGy.MatchString(fq):
		m := reBxGy.FindStringSubmatch(fq)
		buy, pay := atoi(m[1]), atoi(m[2])
		if buy > pay && pay > 0 {
			p.Type, p.BuyQty, p.GetQty, p.GetBps = promo.TypeBuyXGetY, pay, buy-pay, 10000
			why = append(why, fmt.Sprintf("%d×%d: al llevar %d, %d gratis", buy, pay, buy, buy-pay))
		}
	case reFreeShip.MatchString(fq):
		p.Type = promo.TypeFreeShipping
		why = append(why, "Envío gratis")
	case rePercent.MatchString(fq):
		n := atoi(rePercent.FindStringSubmatch(fq)[1])
		p.Type, p.Bps = promo.TypeOrderPercent, n*100
		why = append(why, fmt.Sprintf("%d %% de descuento", n))
	case reFixedOff.MatchString(fq):
		n := atoi(reFixedOff.FindStringSubmatch(fq)[1])
		p.Type, p.Amount = promo.TypeOrderFixed, n*100
		why = append(why, fmt.Sprintf("$%d de descuento", n))
	}
	if p.Type == "" {
		return p, nil
	}
	if m := reFromAmt.FindStringSubmatch(fq); m != nil {
		p.Conditions.MinSubtotal = atoi(m[1]) * 100
		why = append(why, "En compras desde $"+catalog.FormatAmount(p.Conditions.MinSubtotal))
	}
	// The products: a category, brand or word of the catalog named in the text.
	if m := reOnTarget.FindStringSubmatch(strings.Replace(fq, "envio gratis", "", 1)); m != nil && p.Type != promo.TypeFreeShipping {
		phrase := strings.TrimSpace(m[1])
		if c, ok := categoryNamed(t, phrase); ok {
			p.Target.Categories = []string{c.ID}
			why = append(why, "Solo en la categoría «"+c.Name+"»")
		} else if b, ok := t.Taxo.BrandByName(phrase); ok {
			p.Target.Brands = []string{b.ID}
			why = append(why, "Solo en la marca «"+b.Name+"»")
		} else if ids := t.Catalog.Matching(phrase); len(ids) > 0 && len(ids) <= 500 {
			for id := range ids {
				p.Target.ProductIDs = append(p.Target.ProductIDs, id)
			}
			why = append(why, fmt.Sprintf("En %d productos que se llaman «%s»", len(ids), phrase))
		}
		if len(p.Target.Categories)+len(p.Target.Brands)+len(p.Target.ProductIDs) > 0 && p.Type == promo.TypeOrderPercent {
			p.Type = promo.TypeItemPercent // a percentage on some products, not the whole order
		}
		if len(p.Target.Categories)+len(p.Target.Brands)+len(p.Target.ProductIDs) > 0 && p.Type == promo.TypeOrderFixed {
			p.Type = promo.TypeItemFixed
		}
	}
	if m := reUntilDate.FindStringSubmatch(fq); m != nil {
		if mo, ok := monthsEs[m[2]]; ok {
			day := int(atoi(m[1]))
			// The whole day, in the store's time zone (central Mexico).
			end := time.Date(now.Year(), mo, day, 23, 59, 59, 0, reports.Zone)
			if end.Before(now) {
				end = end.AddDate(1, 0, 0)
			}
			p.Conditions.EndsAt = &end
			why = append(why, "Hasta el "+end.Format("02/01/2006"))
		}
	}
	if m := reCoupon.FindStringSubmatch(fq); m != nil {
		p.Conditions.RequiresCoupon = true
		why = append(why, "Con el cupón "+strings.ToUpper(m[1])+" (créalo en Cupones)")
	}
	return p, why
}

func categoryNamed(t *projection.Tenant, phrase string) (catalog.Category, bool) {
	f := engageFold(phrase)
	for _, c := range t.Taxo.Categories() {
		cf := engageFold(c.Name)
		if cf == f || ai2plural(cf) == f || ai2plural(f) == cf {
			return c, true
		}
	}
	return catalog.Category{}, false
}

func engageFold(s string) string { return strings.Join(strings.Fields(foldQ(s)), " ") }

func ai2plural(w string) string {
	if w == "" {
		return w
	}
	if strings.ContainsAny(w[len(w)-1:], "aeiou") {
		return w + "s"
	}
	return w + "es"
}

func (s *Server) copilotPromotion(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var body struct {
		Q string `json:"q"`
	}
	if !decode(w, r, &body) {
		return
	}
	cur := money.Currency("MXN")
	if c, ok := t.Channel(mainChannel); ok {
		cur = c.Currency
	}
	p, why := parsePromotion(t, cur, body.Q, time.Now())
	if p.Type == "" {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"understood": false, "explain": []string{}, "message": "No entendí el beneficio. Prueba con: «3x2 en calcetas», «20% en playeras hasta el 30 de noviembre», «envío gratis desde $999»."})
		return
	}
	p.ID = "vista-previa"
	p.Normalize()
	errs := p.Validate()
	problems := make([]string, 0, len(errs))
	for _, e := range errs {
		problems = append(problems, e.Message)
	}
	out := map[string]any{"understood": true, "promotion": p, "explain": why, "problems": problems}
	if len(errs) == 0 {
		out["summary"] = promo.Summary(p, namer(t))
	}
	p.ID = ""
	out["promotion"] = p
	httpx.JSON(w, r, http.StatusOK, out)
}

// ---------- orders ----------

// returnReasons are the reasons a return can have, with the words customers
// use for each.
var returnReasons = []struct {
	Code, Label string
	re          *regexp.Regexp
}{
	{"size", "Talla o medida", regexp.MustCompile(`\b(talla|chico|chica|grande|queda|quedo|mediano|medida|aprieta|holgad)`)},
	{"damaged", "Llegó dañado o defectuoso", regexp.MustCompile(`\b(roto|rota|danad|defect|no funciona|no sirve|no prende|despostill|rayad|manchad|falla)`)},
	{"wrong_item", "Llegó otro producto", regexp.MustCompile(`\b(equivocad|otro producto|no es el que|no corresponde|diferente al|color equivocado|modelo equivocado)`)},
	{"not_as_described", "No es como se describía", regexp.MustCompile(`\b(no es como|diferente a la foto|no se parece|calidad|material|esperaba)`)},
	{"late", "Llegó tarde", regexp.MustCompile(`\b(tarde|retras|demor|nunca llego|no llego a tiempo)`)},
	{"changed_mind", "Cambio de opinión", regexp.MustCompile(`\b(ya no (lo |la )?(quiero|necesito)|cambie de opinion|me arrepenti|no me gusto)`)},
}

func classifyReturn(text string) (code, label string, ok bool) {
	fq := foldQ(text)
	for _, r := range returnReasons {
		if r.re.MatchString(fq) {
			return r.Code, r.Label, true
		}
	}
	return "other", "Otro", false
}

func (s *Server) copilotReturnReason(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	code, label, ok := classifyReturn(body.Text)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"code": code, "label": label, "confident": ok})
}

// copilotOrder summarizes an order and drafts a reply to its customer
// according to its state. Everything comes from the order itself.
func (s *Server) copilotOrder(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	o, err := s.Orders.Get(r.Context(), p.Tenant, r.PathValue("id"), true)
	if err != nil {
		apierr.NotFound(w, "Pedido no encontrado")
		return
	}
	num := strconv.FormatInt(o.Number, 10)
	name := o.CustomerName
	if name == "" && o.Customer != nil {
		name = o.Customer.Name
	}
	hello := "Hola"
	if f := strings.Fields(name); len(f) > 0 {
		hello = "Hola, " + f[0]
	}
	var lines []string
	for _, l := range o.Lines {
		lines = append(lines, fmt.Sprintf("%d × %s", l.Qty, l.Title))
	}
	summary := fmt.Sprintf("Pedido #%s: %s. Estado: %s.", num, strings.Join(lines, ", "), statusLabel(o.Status))
	reply := hello + ". "
	switch o.Status {
	case "cancelled":
		reply += "Tu pedido #" + num + " está cancelado. Si se hizo algún cargo, el reembolso se refleja en tu medio de pago en los próximos días hábiles."
	case "refunded":
		reply += "Ya hicimos el reembolso de tu pedido #" + num + ". Se refleja en tu medio de pago en los próximos días hábiles, según tu banco."
	case "fulfilled":
		reply += "Tu pedido #" + num + " ya fue enviado. Puedes seguirlo con la guía que te mandamos por correo. Cualquier duda, aquí estamos."
	case "paid":
		reply += "Recibimos el pago de tu pedido #" + num + " y lo estamos preparando. Te avisaremos por correo en cuanto salga, con su guía de rastreo."
	default:
		reply += "Tu pedido #" + num + " está registrado y en espera de pago. En cuanto se confirme, lo preparamos y te avisamos."
	}
	reply += " Gracias por tu compra."
	httpx.JSON(w, r, http.StatusOK, map[string]any{"summary": summary, "reply": reply})
}

func statusLabel(status string) string {
	labels := map[string]string{"pending_payment": "en espera de pago", "paid": "pagado, por enviar", "fulfilled": "enviado", "cancelled": "cancelado", "refunded": "reembolsado"}
	if l := labels[status]; l != "" {
		return l
	}
	return status
}

func (s *Server) routesPanelCopilots(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/ai/segment", s.staff("customers", auth.Read, s.copilotSegment))
	handle("POST /v1/ai/promotion", s.staff("promotions", auth.Read, s.copilotPromotion))
	handle("POST /v1/ai/return-reason", s.staff("orders", auth.Read, s.copilotReturnReason))
	handle("GET /v1/ai/orders/{id}", s.staff("orders", auth.Read, s.copilotOrder))
}
