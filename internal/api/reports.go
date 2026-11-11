package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/reports"
	"github.com/helloghc/apexx/internal/resilience"
	"github.com/helloghc/apexx/internal/store"
)

// ---------- storefront beacon ----------

// eventsPerMinute beacons are allowed per store and address; the script
// batches, so a person browsing sends a handful.
const eventsPerMinute = 120

type catalogResolver struct{ t *projection.Tenant }

func (c catalogResolver) ProductByHandle(h string) (string, bool) {
	p, ok := c.t.Catalog.ByHandle(h)
	return p.ID, ok
}

func (c catalogResolver) ProductBySKU(sku string) (string, bool) {
	_, p, err := c.t.Catalog.VariantBySKU(sku)
	return p.ID, err == nil
}

// shopEvents receives the first-party analytics beacon. It answers 204 with
// no body in every accepted case, including requests it drops on purpose
// (bots, Do Not Track, Global Privacy Control), so it reveals nothing.
func (s *Server) shopEvents(w http.ResponseWriter, r *http.Request) {
	t, release, ok := s.publicTenant(w, r, s.eventsLimiter)
	if !ok {
		return
	}
	defer release()
	// Browsers send beacons cross-site too; only this storefront may write here.
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Los eventos solo se aceptan desde la tienda"})
		return
	}
	b, err := reports.ParseBeacon(http.MaxBytesReader(w, r.Body, reports.MaxBeaconBytes+1))
	var ve *reports.ValidationError
	if errors.As(err, &ve) {
		fe := make([]apierr.FieldError, 0, len(ve.Fields))
		for _, f := range ve.Fields {
			fe = append(fe, apierr.FieldError{Field: f.Field, Code: f.Code, Message: f.Message})
		}
		apierr.BadRequest(w, "Eventos no válidos", fe...)
		return
	}
	if err != nil {
		apierr.BadRequest(w, "No se pudo leer el cuerpo")
		return
	}
	if b.Channel != "" {
		if _, ok := t.Channel(b.Channel); !ok {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "c", Code: "unknown", Message: "La tienda no tiene ese canal"})
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	c := reports.Client{IP: s.clientIP(r), UserAgent: r.UserAgent(), DNT: r.Header.Get("DNT") == "1" || r.Header.Get("Sec-GPC") == "1"}
	if _, err := s.Reports.Ingest(ctx, r.PathValue("tenant"), b, c, catalogResolver{t}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// ---------- reports ----------

// reportFilter reads ?range=&from=&to=&channel=.
func (s *Server) reportFilter(w http.ResponseWriter, r *http.Request) (reports.Filter, bool) {
	q := r.URL.Query()
	rg, err := reports.ParseRange(q.Get("range"), q.Get("from"), q.Get("to"), time.Now())
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "range", Code: "invalid", Message: strings.TrimPrefix(err.Error(), reports.ErrRange.Error()+": ")})
		return reports.Filter{}, false
	}
	ch := q.Get("channel")
	if len(ch) > 32 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "channel", Code: "invalid", Message: "Canal no válido"})
		return reports.Filter{}, false
	}
	return reports.Filter{Range: rg, Channel: ch}, true
}

type tabler interface{ Table() reports.Table }

// writeReport answers JSON, or CSV with ?format=csv.
func (s *Server) writeReport(w http.ResponseWriter, r *http.Request, name string, f reports.Filter, v tabler, err error) {
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if r.URL.Query().Get("format") != "csv" {
		httpx.JSON(w, r, http.StatusOK, v)
		return
	}
	file := "apexx-" + name
	if !f.Range.From.IsZero() {
		file += "-" + f.Range.FromDay() + "_" + f.Range.ToDay()
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_ = reports.WriteCSV(w, v.Table())
}

func (s *Server) reportDashboard(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	d, err := s.Reports.Dashboard(r.Context(), principal(r).Tenant, f)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// The storefront reads from memory: warn when it trails the database.
	if st := s.Proj.Stats(); st.Errors > 0 || st.LastLagMs > 5000 {
		d.Alerts = append(d.Alerts, reports.Alert{ID: "projection_lag", Severity: "bad", Count: st.Errors,
			Title:  "La tienda tarda en reflejar cambios",
			Detail: fmt.Sprintf("Último cambio aplicado en %.0f ms; %d errores de actualización desde el arranque.", st.LastLagMs, st.Errors)})
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) reportSales(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	grain := r.URL.Query().Get("grain")
	switch grain {
	case "", reports.GrainHour, reports.GrainDay, reports.GrainWeek, reports.GrainMonth:
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "grain", Code: "invalid", Message: "grain: hour, day, week o month"})
		return
	}
	v, err := s.Reports.SalesReport(r.Context(), principal(r).Tenant, f, grain)
	s.writeReport(w, r, "ventas", f, v, err)
}

func (s *Server) reportProducts(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if r.URL.Query().Get("format") == "csv" {
		limit = 1000
	}
	v, err := s.Reports.ProductsReport(r.Context(), principal(r).Tenant, f, r.URL.Query().Get("by"), limit)
	s.writeReport(w, r, "productos-"+v.By, f, v, err)
}

func (s *Server) reportCustomers(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	v, err := s.Reports.CustomersReport(r.Context(), principal(r).Tenant, f)
	s.writeReport(w, r, "clientes", f, v, err)
}

func (s *Server) reportFunnel(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	v, err := s.Reports.FunnelReport(r.Context(), principal(r).Tenant, f)
	s.writeReport(w, r, "embudo", f, v, err)
}

func (s *Server) reportPromotions(w http.ResponseWriter, r *http.Request) {
	f, ok := s.reportFilter(w, r)
	if !ok {
		return
	}
	v, err := s.Reports.PromotionsReport(r.Context(), principal(r).Tenant, f)
	s.writeReport(w, r, "promociones", f, v, err)
}

func (s *Server) reportInventory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	switch state {
	case "", "ok", "low", "out":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "state", Code: "invalid", Message: "state: ok, low u out"})
		return
	}
	threshold := int64(reports.LowStockThreshold)
	if t := q.Get("threshold"); t != "" {
		v, err := strconv.ParseInt(t, 10, 64)
		if err != nil || v < 0 || v > 100000 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "threshold", Code: "invalid", Message: "El umbral debe ser un número entre 0 y 100000"})
			return
		}
		threshold = v
	}
	limit := 500
	if q.Get("format") == "csv" {
		limit = 20000
	}
	v, err := s.Reports.InventoryReport(r.Context(), principal(r).Tenant, q.Get("channel"), state, threshold, limit)
	s.writeReport(w, r, "inventario", reports.Filter{}, v, err)
}

// reportRebuild recomputes every fact and rollup of the store from its orders
// and raw events. The periodic sync never needs it; it is the repair tool.
func (s *Server) reportRebuild(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	start := time.Now()
	n, err := s.Reports.Rebuild(ctx, p.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	err = s.DB.Tx(ctx, p.Tenant, func(tx pgx.Tx) error {
		return store.Audit(ctx, tx, p.Tenant, p.Actor(), "analytics.rebuilt", p.Tenant, map[string]any{"orders": n})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"orders": n, "tookMs": time.Since(start).Milliseconds()})
}

// ---------- operations ----------

func parseDay(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, true
	}
	t, err := time.ParseInLocation("2006-01-02", s, reports.Zone)
	return t, err == nil
}

func (s *Server) opsAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := reports.AuditFilter{Actor: q.Get("actor"), Action: q.Get("action"), Target: q.Get("target")}
	if len(f.Actor) > 200 || len(f.Action) > 200 || len(f.Target) > 200 {
		apierr.BadRequest(w, "Los filtros admiten hasta 200 caracteres")
		return
	}
	from, ok1 := parseDay(q.Get("from"))
	to, ok2 := parseDay(q.Get("to"))
	if !ok1 || !ok2 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "from", Code: "invalid", Message: "Usa fechas AAAA-MM-DD"})
		return
	}
	f.From = from
	if !to.IsZero() {
		f.To = to.AddDate(0, 0, 1)
	}
	csv := q.Get("format") == "csv"
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	if csv {
		f.Limit = 200
	}
	if c := q.Get("cursor"); c != "" {
		k, err := httpx.DecodeCursor(c)
		if err == nil {
			f.Before, err = strconv.ParseInt(k, 10, 64)
		}
		if err != nil || f.Before < 1 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "cursor", Code: "invalid", Message: "Cursor no válido"})
			return
		}
	}
	tenant := principal(r).Tenant
	if csv {
		// Export follows the cursor up to 5,000 records.
		var all []reports.AuditEntry
		for len(all) < 5000 {
			items, next, err := s.Reports.AuditList(r.Context(), tenant, f)
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
			all = append(all, items...)
			if next == 0 {
				break
			}
			f.Before = next
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="apexx-actividad.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		_ = reports.WriteCSV(w, reports.AuditTable(all))
		return
	}
	items, next, err := s.Reports.AuditList(r.Context(), tenant, f)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"items": items, "nextCursor": nil}
	if next > 0 {
		out["nextCursor"] = httpx.EncodeCursor(strconv.FormatInt(next, 10))
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) opsAuditFacets(w http.ResponseWriter, r *http.Request) {
	actors, actions, total, err := s.Reports.AuditFacets(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"actors": actors, "actions": actions, "total": total})
}

// httpSummary condenses the request metrics exposed on /metrics.
type httpSummary struct {
	Requests int64       `json:"requests"`
	Errors5x int64       `json:"errors5xx"`
	P95Ms    float64     `json:"p95Ms"`
	Slowest  []routeLine `json:"slowest"`
}

type routeLine struct {
	Route    string  `json:"route"`
	Requests int64   `json:"requests"`
	Errors5x int64   `json:"errors5xx"`
	AvgMs    float64 `json:"avgMs"`
	P95Ms    float64 `json:"p95Ms"`
}

// quantile estimates q from cumulative histogram counts (upper bucket bound).
func quantile(hist []int64, total int64, q float64) float64 {
	if total == 0 {
		return 0
	}
	need := int64(float64(total)*q + 0.5)
	for i, c := range hist {
		if c >= need {
			return buckets[i] * 1000
		}
	}
	return buckets[len(buckets)-1] * 1000
}

func (m *metrics) summary() httpSummary {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out httpSummary
	all := make([]int64, len(buckets))
	for name, rs := range m.routes {
		l := routeLine{Route: name, Requests: rs.total, P95Ms: quantile(rs.hist, rs.total, 0.95)}
		if rs.total > 0 {
			l.AvgMs = rs.sum / float64(rs.total) * 1000
		}
		for code, c := range rs.count {
			if code >= 500 {
				l.Errors5x += c
			}
		}
		for i, c := range rs.hist {
			all[i] += c
		}
		out.Requests += rs.total
		out.Errors5x += l.Errors5x
		out.Slowest = append(out.Slowest, l)
	}
	out.P95Ms = quantile(all, out.Requests, 0.95)
	sort.Slice(out.Slowest, func(i, j int) bool {
		if out.Slowest[i].P95Ms != out.Slowest[j].P95Ms {
			return out.Slowest[i].P95Ms > out.Slowest[j].P95Ms
		}
		return out.Slowest[i].Requests > out.Slowest[j].Requests
	})
	if len(out.Slowest) > 8 {
		out.Slowest = out.Slowest[:8]
	}
	return out
}

// workerMetrics appends per-worker gauges to the Prometheus output.
func workerMetrics(b *strings.Builder) {
	ws := resilience.Workers()
	b.WriteString("# TYPE apexx_worker_runs_total counter\n# TYPE apexx_worker_failures_total counter\n# TYPE apexx_worker_last_success_timestamp_seconds gauge\n")
	for _, w := range ws {
		ok := int64(0)
		if !w.LastOK.IsZero() {
			ok = w.LastOK.Unix()
		}
		fmt.Fprintf(b, "apexx_worker_runs_total{worker=%q} %d\napexx_worker_failures_total{worker=%q} %d\napexx_worker_last_success_timestamp_seconds{worker=%q} %d\n",
			w.Name, w.Runs, w.Name, w.Failures, w.Name, ok)
	}
}

type workerView struct {
	resilience.WorkerStatus
	State string `json:"state"` // ok | late | failing
}

// opsStatus is the system status page: the same numbers /metrics exposes to
// monitoring, plus the store's own queues.
func (s *Server) opsStatus(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	h := s.Reports.Health(r.Context(), tenant)
	st := s.Proj.Stats()
	now := time.Now()
	workers := []workerView{}
	for _, ws := range resilience.Workers() {
		v := workerView{WorkerStatus: ws, State: "ok"}
		late := time.Duration(ws.IntervalSec*3*float64(time.Second)) + 30*time.Second
		switch {
		case ws.LastError != "":
			v.State = "failing"
			v.LastError = "Falló la última ejecución (el detalle está en los registros del servidor)"
		case ws.LastOK.IsZero() || now.Sub(ws.LastOK) > late:
			v.State = "late"
		}
		workers = append(workers, v)
	}
	behind := h.Outbox.LastID - st.LastID
	if behind < 0 {
		behind = 0
	}
	overall := "ok"
	switch {
	case !h.DB.OK:
		overall = "down"
	case st.Errors > 0 || st.LastLagMs > 5000 || h.Webhooks.Dead7d > 0 || h.Payments.Pending > 0 || h.Invoices.Failed > 0:
		overall = "degraded"
	}
	for _, v := range workers {
		if v.State != "ok" && overall == "ok" {
			overall = "degraded"
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"status":     overall,
		"checkedAt":  now,
		"projection": map[string]any{"stats": st, "eventsBehind": behind},
		"workers":    workers,
		"health":     h,
		"http":       s.metrics.summary(),
		"counters": map[string]int64{
			"panics": resilience.Panics.Load(), "bulkheadRejects": resilience.BulkheadRejects.Load(),
			"workerRestarts": resilience.WorkerRestarts.Load(), "breakerOpen": resilience.BreakerOpenTotal.Load(),
		},
	})
}
