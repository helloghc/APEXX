package api

import (
	"net/http"
	"strings"

	"github.com/helloghc/apexx/internal/catalog"
)

// Product comparison: up to four products side by side, with their price,
// availability, brand and every specification; rows where they all agree can
// be hidden to see only the differences.

const maxCompare = 4

type compareRow struct {
	Name   string
	Values []string
	Same   bool
}

type comparePage struct {
	pageData
	Cards []cardView
	Rows  []compareRow
}

func (s *Server) shopCompare(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pd.Title, pd.NoIndex = "Comparar productos", true
	pg := comparePage{pageData: pd}
	seen := map[string]bool{}
	var prods []catalog.Product
	for _, h := range strings.Split(r.URL.Query().Get("p"), ",") {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] || len(prods) == maxCompare {
			continue
		}
		seen[h] = true
		if p, found := t.Catalog.ByHandle(h); found && p.Status == "active" {
			prods = append(prods, p)
		}
	}
	of := offerOf(t, pd.Channel)
	row := func(name string, vals []string) {
		same := len(vals) > 1
		for _, v := range vals[1:] {
			same = same && v == vals[0]
		}
		for _, v := range vals {
			if v != "—" {
				pg.Rows = append(pg.Rows, compareRow{name, vals, same})
				return
			}
		}
	}
	price, avail, brand := make([]string, len(prods)), make([]string, len(prods)), make([]string, len(prods))
	for i := range prods {
		p := &prods[i]
		c := shopCard(t, pd, p, of(p), false)
		pg.Cards = append(pg.Cards, c)
		price[i], brand[i] = c.Price, dash(p.Brand)
		if c.Price == "" {
			price[i] = "—"
		}
		avail[i] = "Agotado"
		for _, v := range p.Variants {
			if _, pre := t.Inventory.PreorderOf(v.SKU); pre && t.Inventory.Available(v.SKU) > 0 {
				avail[i] = "Preventa"
				break
			}
			if t.Inventory.Available(v.SKU) > 0 {
				avail[i] = "Disponible"
				break
			}
		}
	}
	if len(prods) > 0 {
		row("Precio", price)
		row("Disponibilidad", avail)
		row("Marca", brand)
		for _, a := range t.Taxo.Attributes() {
			vals := make([]string, len(prods))
			for i := range prods {
				vals[i] = "—"
				if _, label, ok := attrDisplay(a, prods[i].Attributes[a.ID]); ok {
					vals[i] = label
				}
			}
			row(a.Name, vals)
		}
	}
	s.renderShop(w, "compare", pg, false)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
