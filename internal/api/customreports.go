package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/reports"
	"github.com/helloghc/apexx/internal/store"
)

// Custom reports: the store picks what to count (orders or products sold),
// how to group it (up to two groupings), which figures to show and which
// orders count. Every piece is chosen from a fixed list, so the query is
// built only from known SQL fragments and the store's values travel as
// parameters. Reports are computed at the moment from the orders themselves
// (no extra tables to keep in sync) and can be saved, charted and exported.

type customDef struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Dataset  string   `json:"dataset"`  // orders | lines
	Dims     []string `json:"dims"`     // up to 2
	Metrics  []string `json:"metrics"`  // 1 to 6
	Statuses []string `json:"statuses"` // empty: paid, fulfilled and refunded
	Channel  string   `json:"channel,omitempty"`
	Source   string   `json:"source,omitempty"`
	SortBy   string   `json:"sortBy,omitempty"` // a metric, or "" for the first grouping
	Asc      bool     `json:"asc,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	Chart    string   `json:"chart,omitempty"` // table | bar | line
	Range    string   `json:"range,omitempty"` // preset saved with the report
}

type dimDef struct {
	Label  string `json:"label"`
	expr   string
	Lines  bool   `json:"lines,omitempty"` // only for products sold
	Time   bool   `json:"time,omitempty"`  // ordered by time, good for a line chart
	mapped string // brand | category: resolved from the catalog
}

type metricDef struct {
	Label string `json:"label"`
	Money bool   `json:"money,omitempty"`
	For   string `json:"for"` // orders | lines | both
	expr  string
}

const tzExpr = `(o.created_at AT TIME ZONE $5)`

var customDims = map[string]dimDef{
	"day":      {Label: "Día", Time: true, expr: `to_char(` + tzExpr + `,'YYYY-MM-DD')`},
	"week":     {Label: "Semana", Time: true, expr: `to_char(date_trunc('week',` + tzExpr + `),'YYYY-MM-DD')`},
	"month":    {Label: "Mes", Time: true, expr: `to_char(` + tzExpr + `,'YYYY-MM')`},
	"weekday":  {Label: "Día de la semana", expr: `extract(isodow from ` + tzExpr + `)::int::text`},
	"hour":     {Label: "Hora del día", expr: `lpad(extract(hour from ` + tzExpr + `)::int::text,2,'0')`},
	"channel":  {Label: "Canal", expr: `o.channel`},
	"source":   {Label: "Origen", expr: `COALESCE(o.source,'checkout')`},
	"status":   {Label: "Estado", expr: `o.status`},
	"shipping": {Label: "Forma de entrega", expr: `COALESCE(NULLIF(o.shipping_method->>'name',''),'(sin entrega)')`},
	"postal":   {Label: "Código postal (zona)", expr: `COALESCE(NULLIF(left(o.postal_code,2),''),'—')`},
	"currency": {Label: "Moneda", expr: `o.currency::text`},
	"customer": {Label: "Tipo de cliente", expr: `CASE WHEN o.customer_id IS NULL THEN 'Invitado' WHEN EXISTS (SELECT 1 FROM orders p WHERE p.tenant_id=o.tenant_id AND p.customer_id=o.customer_id
		AND p.created_at < o.created_at AND p.status IN ('paid','fulfilled','refunded')) THEN 'Recurrente' ELSE 'Nuevo' END`},
	"company": {Label: "Empresa (B2B)", expr: `COALESCE((SELECT c.name FROM company_buyers b JOIN companies c ON c.tenant_id=b.tenant_id AND c.id=b.company_id
		WHERE b.tenant_id=o.tenant_id AND b.email_hash=o.email_hash LIMIT 1),'(sin empresa)')`},
	"campaign": {Label: "Campaña de correo", expr: `COALESCE((SELECT e.name FROM email_campaigns e WHERE e.tenant_id=o.tenant_id AND e.id=o.email_campaign_id),'(sin campaña)')`},
	"payment":  {Label: "Pago", expr: `CASE WHEN o.installments > 1 THEN o.installments::text || ' meses sin intereses' WHEN o.voucher IS NOT NULL THEN 'Efectivo o transferencia' ELSE 'Un pago' END`},
	"gift":     {Label: "Regalo", expr: `CASE WHEN o.registry_id IS NOT NULL THEN 'Mesa de regalos' WHEN o.gift IS NOT NULL THEN 'Regalo' ELSE 'No es regalo' END`},
	"sku":      {Label: "SKU", Lines: true, expr: `l.sku`},
	"product":  {Label: "Producto", Lines: true, expr: `l.title`},
	"brand":    {Label: "Marca", Lines: true, mapped: "brand"},
	"category": {Label: "Categoría", Lines: true, mapped: "category"},
}

var customMetrics = map[string]metricDef{
	"orders":    {Label: "Pedidos", For: "both", expr: `count(DISTINCT o.id)`},
	"revenue":   {Label: "Ventas (total cobrado)", Money: true, For: "orders", expr: `COALESCE(sum(o.grand_total),0)`},
	"net":       {Label: "Ventas netas (sin reembolsos)", Money: true, For: "orders", expr: `COALESCE(sum(o.grand_total - COALESCE(o.refunded_total,0)),0)`},
	"items":     {Label: "Productos (antes de descuentos)", Money: true, For: "orders", expr: `COALESCE(sum(o.items_total),0)`},
	"discounts": {Label: "Descuentos", Money: true, For: "orders", expr: `COALESCE(sum(o.discounts),0)`},
	"shipping":  {Label: "Envíos cobrados", Money: true, For: "orders", expr: `COALESCE(sum(o.shipping),0)`},
	"fees":      {Label: "Cargos extra", Money: true, For: "orders", expr: `COALESCE(sum(COALESCE(o.fees,0)),0)`},
	"tax":       {Label: "IVA incluido", Money: true, For: "orders", expr: `COALESCE(sum(o.tax_included),0)`},
	"refunds":   {Label: "Reembolsos", Money: true, For: "orders", expr: `COALESCE(sum(COALESCE(o.refunded_total,0)),0)`},
	"aov":       {Label: "Ticket promedio", Money: true, For: "orders", expr: `COALESCE(sum(o.grand_total) / NULLIF(count(*),0),0)`},
	"customers": {Label: "Clientes", For: "both", expr: `count(DISTINCT COALESCE(o.customer_id, encode(o.email_hash,'hex')))`},
	"units":     {Label: "Piezas vendidas", For: "lines", expr: `COALESCE(sum(l.qty - l.cancelled_qty),0)`},
	"sales":     {Label: "Venta de productos", Money: true, For: "lines", expr: `COALESCE(sum(l.total * (l.qty - l.cancelled_qty) / l.qty),0)`},
	"returned":  {Label: "Piezas devueltas", For: "lines", expr: `COALESCE(sum(l.returned_qty),0)`},
	"avgprice":  {Label: "Precio promedio", Money: true, For: "lines", expr: `COALESCE(sum(l.total) / NULLIF(sum(l.qty),0),0)`},
}

var customStatuses = map[string]string{"paid": "Pagado", "fulfilled": "Enviado", "refunded": "Reembolsado", "cancelled": "Cancelado", "pending_payment": "Pendiente de pago"}
var weekdays = []string{"", "Lunes", "Martes", "Miércoles", "Jueves", "Viernes", "Sábado", "Domingo"}
var sourceNames = map[string]string{"checkout": "Tienda en línea", "draft": "Pedido manual", "pos": "Caja (tienda física)"}

func (d *customDef) clean() error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Dataset != "orders" && d.Dataset != "lines" {
		return errors.New("Elige qué contar: pedidos o productos vendidos")
	}
	if len(d.Dims) > 2 {
		return errors.New("Agrupa por hasta dos datos")
	}
	seen := map[string]bool{}
	for _, k := range d.Dims {
		dd, ok := customDims[k]
		if !ok || seen[k] {
			return fmt.Errorf("Agrupación no válida: %s", k)
		}
		if dd.Lines && d.Dataset != "lines" {
			return fmt.Errorf("«%s» solo aplica a productos vendidos", dd.Label)
		}
		seen[k] = true
	}
	if len(d.Metrics) == 0 || len(d.Metrics) > 6 {
		return errors.New("Elige de 1 a 6 cifras")
	}
	seen = map[string]bool{}
	for _, k := range d.Metrics {
		md, ok := customMetrics[k]
		if !ok || seen[k] {
			return fmt.Errorf("Cifra no válida: %s", k)
		}
		if md.For != "both" && md.For != d.Dataset {
			return fmt.Errorf("«%s» no aplica a %s", md.Label, map[string]string{"orders": "pedidos", "lines": "productos vendidos"}[d.Dataset])
		}
		seen[k] = true
	}
	if len(d.Statuses) == 0 {
		d.Statuses = []string{"paid", "fulfilled", "refunded"}
	}
	for _, st := range d.Statuses {
		if _, ok := customStatuses[st]; !ok {
			return fmt.Errorf("Estado no válido: %s", st)
		}
	}
	if d.SortBy != "" && !seen[d.SortBy] {
		return errors.New("Ordena por una de las cifras elegidas")
	}
	if d.Source != "" && sourceNames[d.Source] == "" {
		return errors.New("Origen no válido")
	}
	if len(d.Channel) > 32 {
		return errors.New("Canal no válido")
	}
	if d.Limit <= 0 {
		d.Limit = 200
	}
	if d.Limit > 2000 {
		d.Limit = 2000
	}
	switch d.Chart {
	case "", "table", "bar", "line":
	default:
		return errors.New("Gráfica no válida")
	}
	if d.Range != "" {
		if _, err := reports.ParseRange(d.Range, "", "", time.Now()); err != nil && d.Range != "custom" {
			return errors.New("Periodo no válido")
		}
	}
	return nil
}

type customResult struct {
	Columns   []customColumn `json:"columns"`
	Rows      [][]any        `json:"rows"`
	Totals    []any          `json:"totals"`
	Truncated bool           `json:"truncated"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	TookMs    float64        `json:"tookMs"`
}

type customColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // dim | number | money
}

// skuMap lists every SKU of the catalog with its brand or category name.
func skuMap(t *projection.Tenant, kind string) (skus, vals []string) {
	for _, p := range t.Catalog.Products() {
		val := ""
		switch kind {
		case "brand":
			_, val = t.Taxo.BrandKey(p)
		case "category":
			if len(p.Categories) > 0 {
				val = p.Categories[0]
				if c, ok := t.Taxo.Category(val); ok {
					val = c.Name
				}
			}
		}
		if val == "" {
			continue
		}
		for _, v := range p.Variants {
			skus, vals = append(skus, v.SKU), append(vals, val)
		}
	}
	return skus, vals
}

func (s *Server) runCustom(ctx context.Context, tenant string, d customDef, rg reports.Range) (customResult, error) {
	start := time.Now()
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return customResult{}, store.ErrNotFound
	}
	args := []any{tenant, rg.From, rg.To, d.Statuses, reports.Zone.String(), d.Channel, d.Source}
	from := `orders o`
	if d.Dataset == "lines" {
		from += ` JOIN order_lines l ON l.tenant_id=o.tenant_id AND l.order_id=o.id`
	}
	var sel, group []string
	res := customResult{From: rg.FromDay(), To: rg.ToDay()}
	for i, k := range d.Dims {
		dd := customDims[k]
		expr := dd.expr
		if dd.mapped != "" {
			skus, vals := skuMap(t, dd.mapped)
			args = append(args, skus, vals)
			alias := "m" + strconv.Itoa(i)
			from += fmt.Sprintf(` LEFT JOIN unnest($%d::text[], $%d::text[]) AS %s(sku, val) ON %s.sku=l.sku`, len(args)-1, len(args), alias, alias)
			expr = fmt.Sprintf(`COALESCE(%s.val,'(sin %s)')`, alias, map[string]string{"brand": "marca", "category": "categoría"}[dd.mapped])
		}
		sel = append(sel, expr)
		group = append(group, strconv.Itoa(i+1))
		res.Columns = append(res.Columns, customColumn{Key: k, Label: dd.Label, Kind: "dim"})
	}
	for _, k := range d.Metrics {
		md := customMetrics[k]
		sel = append(sel, md.expr+"::bigint")
		kind := "number"
		if md.Money {
			kind = "money"
		}
		res.Columns = append(res.Columns, customColumn{Key: k, Label: md.Label, Kind: kind})
	}
	where := `o.tenant_id=$1 AND o.created_at >= $2 AND o.created_at < $3 AND o.status = ANY($4) AND ($6 = '' OR o.channel=$6) AND ($7 = '' OR COALESCE(o.source,'checkout')=$7) AND $5::text <> ''`
	q := `SELECT ` + strings.Join(sel, ", ") + ` FROM ` + from + ` WHERE ` + where
	if len(group) > 0 {
		q += ` GROUP BY ` + strings.Join(group, ",")
	}
	order := "1"
	if d.SortBy != "" {
		for i, k := range d.Metrics {
			if k == d.SortBy {
				order = strconv.Itoa(len(d.Dims) + i + 1)
			}
		}
		if d.Asc {
			order += " ASC"
		} else {
			order += " DESC"
		}
	} else if len(d.Dims) > 0 && !customDims[d.Dims[0]].Time {
		order = strconv.Itoa(len(d.Dims)+1) + " DESC" // biggest first
	}
	if len(group) > 0 {
		q += ` ORDER BY ` + order + `, 1 LIMIT ` + strconv.Itoa(d.Limit+1)
	}
	// Totals over the same orders, without groupings.
	var tsel []string
	for _, k := range d.Metrics {
		tsel = append(tsel, customMetrics[k].expr+"::bigint")
	}
	tq := `SELECT ` + strings.Join(tsel, ", ") + ` FROM ` + from + ` WHERE ` + where
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '8s'`); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals := make([]any, len(sel))
			ptr := make([]any, len(sel))
			for i := range d.Dims {
				var sv string
				vals[i], ptr[i] = &sv, &sv
			}
			for i := range d.Metrics {
				var n int64
				vals[len(d.Dims)+i], ptr[len(d.Dims)+i] = &n, &n
			}
			if err := rows.Scan(ptr...); err != nil {
				return err
			}
			row := make([]any, len(sel))
			for i, k := range d.Dims {
				row[i] = dimLabel(k, *(vals[i].(*string)))
			}
			for i := range d.Metrics {
				row[len(d.Dims)+i] = *(vals[len(d.Dims)+i].(*int64))
			}
			res.Rows = append(res.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		tv := make([]int64, len(tsel))
		tp := make([]any, len(tsel))
		for i := range tv {
			tp[i] = &tv[i]
		}
		if err := tx.QueryRow(ctx, tq, args...).Scan(tp...); err != nil {
			return err
		}
		for _, v := range tv {
			res.Totals = append(res.Totals, v)
		}
		return nil
	})
	if len(res.Rows) > d.Limit {
		res.Rows, res.Truncated = res.Rows[:d.Limit], true
	}
	if res.Rows == nil {
		res.Rows = [][]any{}
	}
	res.TookMs = float64(time.Since(start).Microseconds()) / 1000
	return res, err
}

func dimLabel(k, v string) string {
	switch k {
	case "weekday":
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 7 {
			return weekdays[n]
		}
	case "source":
		if s, ok := sourceNames[v]; ok {
			return s
		}
	case "status":
		if s, ok := customStatuses[v]; ok {
			return s
		}
	case "hour":
		return v + ":00"
	}
	return v
}

// Table renders the result for CSV.
func (c customResult) Table() reports.Table {
	t := reports.Table{}
	for _, col := range c.Columns {
		t.Header = append(t.Header, col.Label)
	}
	cell := func(i int, v any) string {
		switch x := v.(type) {
		case int64:
			if c.Columns[i].Kind == "money" {
				neg := ""
				if x < 0 {
					neg, x = "-", -x
				}
				return fmt.Sprintf("%s%d.%02d", neg, x/100, x%100)
			}
			return strconv.FormatInt(x, 10)
		case string:
			return x
		}
		return fmt.Sprint(v)
	}
	for _, r := range c.Rows {
		var out []string
		for i, v := range r {
			out = append(out, cell(i, v))
		}
		t.Rows = append(t.Rows, out)
	}
	if len(c.Totals) > 0 {
		dims := len(c.Columns) - len(c.Totals)
		out := make([]string, 0, len(c.Columns))
		for i := 0; i < dims; i++ {
			if i == 0 {
				out = append(out, "Total")
			} else {
				out = append(out, "")
			}
		}
		for i, v := range c.Totals {
			out = append(out, cell(dims+i, v))
		}
		t.Rows = append(t.Rows, out)
	}
	return t
}

// --- routes ---

func (s *Server) routesCustomReports(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/reports/custom/catalog", s.authed("reports", auth.Read, noStorefront(s.customCatalog)))
	handle("GET /v1/reports/custom", s.authed("reports", auth.Read, noStorefront(s.listCustomReports)))
	handle("POST /v1/reports/custom", s.authed("reports", auth.Edit, s.saveCustomReport))
	handle("PUT /v1/reports/custom/{id}", s.authed("reports", auth.Edit, s.saveCustomReport))
	handle("DELETE /v1/reports/custom/{id}", s.authed("reports", auth.Edit, s.deleteCustomReport))
	handle("POST /v1/reports/custom/run", s.authed("reports", auth.Read, noStorefront(s.runCustomReport)))
	handle("GET /v1/reports/custom/{id}/run", s.authed("reports", auth.Read, noStorefront(s.runSavedReport)))
}

func (s *Server) customCatalog(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, map[string]any{"dims": customDims, "metrics": customMetrics, "statuses": customStatuses, "sources": sourceNames})
}

func (s *Server) listCustomReports(w http.ResponseWriter, r *http.Request) {
	docs, err := s.DB.ListDocs(r.Context(), principal(r).Tenant, "custom_reports")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := []customDef{}
	for _, d := range docs {
		var def customDef
		if json.Unmarshal(d.Data, &def) == nil {
			def.ID = d.ID
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) saveCustomReport(w http.ResponseWriter, r *http.Request) {
	var d customDef
	if !decode(w, r, &d) {
		return
	}
	if err := d.clean(); err != nil {
		apierr.BadRequest(w, err.Error())
		return
	}
	if d.Name == "" || len([]rune(d.Name)) > 80 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "name", Code: "invalid", Message: "Ponle un nombre de hasta 80 caracteres"})
		return
	}
	pr := principal(r)
	id := r.PathValue("id")
	status := http.StatusOK
	if id == "" {
		docs, err := s.DB.ListDocs(r.Context(), pr.Tenant, "custom_reports")
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		if len(docs) >= 100 {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "limit", Detail: "Puedes guardar hasta 100 reportes"})
			return
		}
		id, status = newRandomID("rpt_"), http.StatusCreated
	} else {
		var old customDef
		if found, err := s.DB.GetDoc(r.Context(), pr.Tenant, "custom_reports", id, &old); err != nil || !found {
			apierr.NotFound(w, "Reporte no encontrado")
			return
		}
	}
	d.ID = ""
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "custom_reports", id, "", d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	d.ID = id
	httpx.JSON(w, r, status, d)
}

func (s *Server) deleteCustomReport(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.RemoveDoc(r.Context(), pr.Tenant, pr.Actor(), "custom_reports", r.PathValue("id"), ""); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierr.NotFound(w, "Reporte no encontrado")
			return
		}
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) customRange(w http.ResponseWriter, r *http.Request, saved string) (reports.Range, bool) {
	q := r.URL.Query()
	preset := q.Get("range")
	if preset == "" {
		preset = saved
	}
	rg, err := reports.ParseRange(preset, q.Get("from"), q.Get("to"), time.Now())
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "range", Code: "invalid", Message: strings.TrimPrefix(err.Error(), reports.ErrRange.Error()+": ")})
		return rg, false
	}
	return rg, true
}

func (s *Server) answerCustom(w http.ResponseWriter, r *http.Request, d customDef) {
	rg, ok := s.customRange(w, r, d.Range)
	if !ok {
		return
	}
	res, err := s.runCustom(r.Context(), principal(r).Tenant, d, rg)
	name := "reporte"
	if d.Name != "" {
		name = slugify(d.Name)
	}
	s.writeReport(w, r, name, reports.Filter{Range: rg}, res, err)
}

func (s *Server) runCustomReport(w http.ResponseWriter, r *http.Request) {
	var d customDef
	if !decode(w, r, &d) {
		return
	}
	if err := d.clean(); err != nil {
		apierr.BadRequest(w, err.Error())
		return
	}
	s.answerCustom(w, r, d)
}

func (s *Server) runSavedReport(w http.ResponseWriter, r *http.Request) {
	var d customDef
	found, err := s.DB.GetDoc(r.Context(), principal(r).Tenant, "custom_reports", r.PathValue("id"), &d)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !found {
		apierr.NotFound(w, "Reporte no encontrado")
		return
	}
	if err := d.clean(); err != nil {
		apierr.BadRequest(w, err.Error())
		return
	}
	s.answerCustom(w, r, d)
}
