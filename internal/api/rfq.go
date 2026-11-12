package api

// Quote requests (plan IA-4, B2B): a buyer sends a list, as text or PDF,
// with lines like "50 pzas Taladro percutor 650W" or "BROCA-10MM x 200".
// Each line becomes a quantity and the catalog products that match it
// (an exact SKU first, then the hybrid search), with the store's price and
// stock. The sales rep reviews every line and turns the result into a draft
// order with the company's price list. Nothing is ordered automatically.

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/projection"
)

// RFQLine is one requested line with its candidates.
type RFQLine struct {
	Text    string     `json:"text"`
	Qty     int64      `json:"qty"`
	Matches []RFQMatch `json:"matches"`
}

// RFQMatch is a catalog variant that may be what the line asks for.
type RFQMatch struct {
	SKU       string `json:"sku"`
	Title     string `json:"title"`
	ProductID string `json:"productId"`
	Price     string `json:"price,omitempty"`
	Available int64  `json:"available"`
	Exact     bool   `json:"exact"` // the line names this SKU
}

var (
	reQtyLead  = regexp.MustCompile(`^\s*(\d{1,6})\s*(?:pzas?\.?|piezas?|pz\.?|unidades|unids?\.?|u\.?|cajas?|paquetes?|juegos?|x|×|-|\.)?\s+(.{3,})$`)
	reQtyTrail = regexp.MustCompile(`^(.{3,}?)\s*(?:x|×|cant\.?|cantidad:?|qty:?|-)\s*(\d{1,6})\s*(?:pzas?\.?|piezas?|pz\.?|unidades|u\.?)?\s*$`)
	reQtyTail  = regexp.MustCompile(`^(.{3,}?)\s+(\d{1,6})\s*(?:pzas?\.?|piezas?|pz\.?|unidades)\s*$`)
	reItemNo   = regexp.MustCompile(`^\s*\d{1,3}[.)]\s+`)
)

// parseRFQ reads the requested lines of a document: lines with a quantity,
// or lines that name an existing SKU (quantity 1).
func parseRFQ(t *projection.Tenant, lines []string) []RFQLine {
	var out []RFQLine
	for _, raw := range lines {
		l := strings.TrimSpace(reItemNo.ReplaceAllString(raw, ""))
		if l == "" || len(l) > 300 {
			continue
		}
		qty, text := int64(0), l
		if m := reQtyLead.FindStringSubmatch(l); m != nil {
			qty, _ = strconv.ParseInt(m[1], 10, 64)
			text = m[2]
		} else if m := reQtyTrail.FindStringSubmatch(l); m != nil {
			qty, _ = strconv.ParseInt(m[2], 10, 64)
			text = m[1]
		} else if m := reQtyTail.FindStringSubmatch(l); m != nil {
			qty, _ = strconv.ParseInt(m[2], 10, 64)
			text = m[1]
		}
		if qty == 0 {
			if skuIn(t, l) == "" {
				continue // a heading, a greeting, an address: not a line to quote
			}
			qty = 1
		}
		out = append(out, RFQLine{Text: strings.TrimSpace(text), Qty: qty})
		if len(out) >= 300 {
			break
		}
	}
	return out
}

// skuIn returns a SKU of the catalog named in the text, if any.
func skuIn(t *projection.Tenant, text string) string {
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '|' || r == '\t' || r == '(' || r == ')'
	}) {
		tok = strings.Trim(tok, ".:")
		if len(tok) < 3 {
			continue
		}
		for _, c := range []string{tok, strings.ToUpper(tok)} {
			if _, p, err := t.Catalog.VariantBySKU(c); err == nil && p.Status == "active" {
				return c
			}
		}
	}
	return ""
}

func (s *Server) matchRFQ(t *projection.Tenant, lines []RFQLine) []RFQLine {
	of := offerOf(t, mainChannel)
	cur := money.Currency("MXN")
	if c, ok := t.Channel(mainChannel); ok {
		cur = c.Currency
	}
	variant := func(p *catalog.Product, sku string, exact bool) RFQMatch {
		m := RFQMatch{SKU: sku, Title: p.Title, ProductID: p.ID, Exact: exact}
		for _, v := range p.Variants {
			if v.SKU == sku && v.Title != "" && len(p.Variants) > 1 {
				m.Title += " · " + v.Title
			}
		}
		if q, ok := t.Pricing.Resolve(mainChannel, sku, 1); ok {
			m.Price = pesos(q.Unit)
		} else if o := of(p); o.HasPrice {
			m.Price = pesos(money.New(o.Price, cur))
		}
		m.Available = t.Inventory.Available(sku)
		return m
	}
	for i := range lines {
		lines[i].Matches = []RFQMatch{}
		if sku := skuIn(t, lines[i].Text); sku != "" {
			_, p, _ := t.Catalog.VariantBySKU(sku)
			lines[i].Matches = append(lines[i].Matches, variant(&p, sku, true))
			continue
		}
		bq := catalog.BrowseQuery{Q: lines[i].Text, Limit: 3}
		s.searchQuery(t, &bq)
		s.narrow(t, &bq)
		res := catalog.Browse(t.Catalog.ProductsIn(bq.Only), t.Taxo, of, bq)
		for _, p := range res.Items {
			if len(p.Variants) == 0 {
				continue
			}
			lines[i].Matches = append(lines[i].Matches, variant(p, p.Variants[0].SKU, false))
		}
	}
	return lines
}

func (s *Server) postRFQ(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	t, _ := s.Proj.Tenant(p.Tenant)
	var lines []string
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/") && !strings.HasPrefix(ct, "application/pdf") {
		var body struct {
			Text string `json:"text"`
		}
		if !decode(w, r, &body) {
			return
		}
		if len(body.Text) > 200_000 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "text", Code: "too_long", Message: "El texto puede tener hasta 200 000 caracteres"})
			return
		}
		lines = strings.Split(body.Text, "\n")
	} else {
		_, ls, ok := readPDF(w, r)
		if !ok {
			return
		}
		lines = ls
	}
	parsed := parseRFQ(t, lines)
	if len(parsed) == 0 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "text", Code: "no_lines", Message: "No encontramos renglones con cantidad o SKU. Escribe un renglón por producto, por ejemplo «20 pzas Taladro percutor»."})
		return
	}
	out := s.matchRFQ(t, parsed)
	matched := 0
	for _, l := range out {
		if len(l.Matches) > 0 {
			matched++
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"lines": out, "matched": matched})
}
