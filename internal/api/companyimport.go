package api

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// Bulk import of companies from a CSV: a preview says what each line would do
// (create, update or why it is wrong) and the same file applied does it, line
// by line, so one bad line never blocks the rest. A company is matched by RFC,
// else by its exact name; a parent may be any company already in the store or
// earlier in the file.

type importRow struct {
	Line    int    `json:"line"`
	Name    string `json:"name"`
	Action  string `json:"action"` // create | update | error
	Message string `json:"message,omitempty"`
	ID      string `json:"id,omitempty"`
}

var importCols = map[string]string{
	"nombre": "name", "name": "name", "razon social": "name", "razón social": "name",
	"rfc": "rfc", "plazo": "terms", "terms": "terms", "limite": "limit", "límite": "limit", "credit_limit": "limit", "limite de credito": "limit", "límite de crédito": "limit",
	"descuento": "discount", "discount": "discount", "grupo": "group", "price_group": "group", "grupo de precios": "group",
	"compradores": "buyers", "buyers": "buyers", "matriz": "parent", "parent": "parent", "medios": "payments", "payments": "payments", "medios de pago": "payments",
	"nota": "note", "note": "note",
}

func (s *Server) routesCompanyImport(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/companies/import", s.authed("customers", auth.Admin, s.importCompanies))
}

func parseTerms(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "contado", "prepaid", "0":
		return "prepaid", true
	case "15", "net15", "15 dias", "15 días":
		return "net15", true
	case "30", "net30", "30 dias", "30 días":
		return "net30", true
	case "60", "net60", "60 dias", "60 días":
		return "net60", true
	}
	return "", false
}

func parsePayments(v string) ([]string, bool) {
	m := map[string]string{"tarjeta": "card", "card": "card", "oxxo": "oxxo", "spei": "spei", "credito": "credit", "crédito": "credit", "credit": "credit"}
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ',' || r == '|' }) {
		k, ok := m[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return nil, false
		}
		out = append(out, k)
	}
	return out, true
}

func (s *Server) importCompanies(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	apply := r.URL.Query().Get("apply") == "1"
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20+1))
	if err != nil || len(body) > 5<<20 {
		apierr.Write(w, apierr.Problem{Status: http.StatusRequestEntityTooLarge, Code: "file_too_large", Detail: "El archivo pasa de 5 MB"})
		return
	}
	cr := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\ufeff")))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	recs, err := cr.ReadAll()
	if err != nil || len(recs) < 2 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "invalid", Message: "Sube un CSV con encabezados y al menos una empresa"})
		return
	}
	if len(recs) > 2001 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "too_many", Message: "Hasta 2,000 empresas por archivo"})
		return
	}
	col := map[string]int{}
	for i, h := range recs[0] {
		if k, ok := importCols[strings.ToLower(strings.TrimSpace(h))]; ok {
			col[k] = i
		}
	}
	if _, ok := col["name"]; !ok {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "file", Code: "no_name", Message: "Falta la columna «nombre»"})
		return
	}
	existing, err := s.Orders.Companies(r.Context(), p.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	byRFC, byName := map[string]orders.Company{}, map[string]orders.Company{}
	for _, c := range existing {
		if c.RFC != "" {
			byRFC[strings.ToUpper(c.RFC)] = c
		}
		byName[strings.ToLower(c.Name)] = c
	}
	cell := func(rec []string, k string) string {
		if i, ok := col[k]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	out := []importRow{}
	counts := map[string]int{"create": 0, "update": 0, "error": 0}
	for n, rec := range recs[1:] {
		row := importRow{Line: n + 2, Name: cell(rec, "name")}
		fail := func(msg string) {
			row.Action, row.Message = "error", msg
			out = append(out, row)
			counts["error"]++
		}
		if row.Name == "" {
			fail("Falta el nombre")
			continue
		}
		terms, ok := parseTerms(cell(rec, "terms"))
		if !ok {
			fail("Plazo desconocido (contado, 15, 30 o 60)")
			continue
		}
		var limit int64
		if v := cell(rec, "limit"); v != "" {
			c, ok := pesosToCents(v)
			if !ok {
				fail("Límite de crédito no válido")
				continue
			}
			limit = c
		}
		var discount int64
		if v := strings.TrimSuffix(cell(rec, "discount"), "%"); v != "" {
			f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
			if err != nil {
				fail("Descuento no válido")
				continue
			}
			discount = int64(f*100 + 0.5)
		}
		pay, ok := parsePayments(cell(rec, "payments"))
		if !ok {
			fail("Medio de pago desconocido (tarjeta, oxxo, spei, crédito)")
			continue
		}
		in := orders.Company{Name: row.Name, RFC: strings.ToUpper(cell(rec, "rfc")), Terms: terms, CreditLimit: limit, DiscountBps: discount,
			PriceGroup: cell(rec, "group"), Note: cell(rec, "note"), AllowedPayments: pay}
		var cur *orders.Company
		if in.RFC != "" {
			if c, ok := byRFC[in.RFC]; ok {
				cur = &c
			}
		}
		if cur == nil {
			if c, ok := byName[strings.ToLower(in.Name)]; ok {
				cur = &c
			}
		}
		if pv := cell(rec, "parent"); pv != "" {
			par, ok := byRFC[strings.ToUpper(pv)]
			if !ok {
				par, ok = byName[strings.ToLower(pv)]
			}
			if !ok {
				fail("La matriz «" + pv + "» no existe (debe estar antes en el archivo o ya en la tienda)")
				continue
			}
			in.ParentID = par.ID
		}
		var buyers []string
		if v := cell(rec, "buyers"); v != "" {
			buyers = strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == ',' || r == ' ' })
		}
		row.Action = "create"
		if cur != nil {
			row.Action, row.ID = "update", cur.ID
			// Fields the file does not bring keep their value.
			in.ID, in.Status, in.ApprovalThreshold, in.ApprovalRules, in.ApprovalDays = cur.ID, cur.Status, cur.ApprovalThreshold, cur.ApprovalRules, cur.ApprovalDays
			in.TaxExempt, in.TaxExemptUntil, in.RequirePO, in.CreditOnly, in.CatalogRestricted = cur.TaxExempt, cur.TaxExemptUntil, cur.RequirePO, cur.CreditOnly, cur.CatalogRestricted
			in.SharedCredit, in.InheritTerms, in.SalesRep, in.RepAccess = cur.SharedCredit, cur.InheritTerms, cur.SalesRep, cur.RepAccess
			if in.ParentID == "" {
				in.ParentID = cur.ParentID
			}
			if _, ok := col["group"]; !ok {
				in.PriceGroup = cur.PriceGroup
			}
			if _, ok := col["payments"]; !ok {
				in.AllowedPayments = cur.AllowedPayments
			}
		}
		if !apply {
			// The preview runs the same checks the save runs, without saving.
			c := in
			if err := orders.ValidateCompany(&c); err != nil {
				var fe orders.FieldError
				if errors.As(err, &fe) {
					fail(fe.Message)
					continue
				}
			}
			out = append(out, row)
			counts[row.Action]++
			byName[strings.ToLower(in.Name)] = orders.Company{ID: "nuevo", Name: in.Name}
			if in.RFC != "" {
				byRFC[in.RFC] = orders.Company{ID: "nuevo", Name: in.Name}
			}
			continue
		}
		saved, err := s.Orders.SaveCompany(r.Context(), p.Tenant, p.Actor(), in, buyers)
		if err != nil {
			var fe orders.FieldError
			if errors.As(err, &fe) {
				fail(fe.Message)
				continue
			}
			internalError(w, s.Log, err)
			return
		}
		row.ID = saved.ID
		out = append(out, row)
		counts[row.Action]++
		byName[strings.ToLower(saved.Name)] = saved
		if saved.RFC != "" {
			byRFC[saved.RFC] = saved
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rows": out, "counts": counts, "applied": apply})
}

// pesosToCents reads "12,500.50", "$12500" or "12500,5" as minor units.
func pesosToCents(v string) (int64, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "$"))
	v = strings.ReplaceAll(v, " ", "")
	if i := strings.LastIndex(v, ","); i >= 0 && len(v)-i <= 3 && !strings.Contains(v, ".") {
		v = v[:i] + "." + v[i+1:] // decimal comma
	}
	v = strings.ReplaceAll(v, ",", "")
	whole, frac, _ := strings.Cut(v, ".")
	if whole == "" || len(frac) > 2 {
		return 0, false
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, false
	}
	f := int64(0)
	if frac != "" {
		frac = (frac + "0")[:2]
		if f, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return 0, false
		}
	}
	return w*100 + f, true
}
