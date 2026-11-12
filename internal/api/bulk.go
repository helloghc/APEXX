package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Bulk operations: up to 20,000 stock or price lines in one job. Bad lines
// are reported and skipped; good ones are applied in chunks of 500 with
// visible progress. Exports stream the whole catalog as JSON lines.

const (
	maxBulkItems = 20000
	bulkChunk    = 500
)

type bulkError struct {
	Line    int    `json:"line"`
	SKU     string `json:"sku,omitempty"`
	Message string `json:"message"`
}

type bulkJob struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Channel    string      `json:"channel,omitempty"`
	Status     string      `json:"status"`
	Total      int         `json:"total"`
	Processed  int         `json:"processed"`
	Failed     int         `json:"failed"`
	Errors     []bulkError `json:"errors"`
	CreatedAt  time.Time   `json:"createdAt"`
	FinishedAt *time.Time  `json:"finishedAt,omitempty"`
}

func (s *Server) routesBulk(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/bulk-operations", s.authed("inventory", auth.Edit, s.createBulk))
	handle("GET /v1/bulk-operations", s.authed("inventory", auth.Read, noStorefront(s.listBulk)))
	handle("GET /v1/bulk-operations/{id}", s.authed("inventory", auth.Read, noStorefront(s.getBulk)))
	handle("GET /v1/bulk/products.jsonl", s.authed("catalog", auth.Read, noStorefront(s.exportProductsJSONL)))
}

// noStorefront keeps a publishable storefront key out of management reads it
// would otherwise pass (it may read catalog and stock to show the store).
func noStorefront(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Storefront {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Una llave pública de tienda no tiene acceso de gestión"})
			return
		}
		h(w, r)
	}
}

func (s *Server) createBulk(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	var in struct {
		Kind    string            `json:"kind"`
		Channel string            `json:"channel"`
		Items   []json.RawMessage `json:"items"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	if err := dec.Decode(&in); err != nil {
		apierr.BadRequest(w, "JSON inválido o de más de 8 MB: "+err.Error())
		return
	}
	// Prices need the price module; stock needs inventory (checked by the route).
	if in.Kind == "prices" && !pr.Permissions.Allows("pricing", auth.Edit) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Tu acceso a precios no permite esta acción"})
		return
	}
	if in.Kind != "stock" && in.Kind != "prices" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "kind", Code: "invalid", Message: "Tipo: stock o prices"})
		return
	}
	if len(in.Items) == 0 || len(in.Items) > maxBulkItems {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "items", Code: "invalid", Message: fmt.Sprintf("De 1 a %d líneas", maxBulkItems)})
		return
	}
	var errs []bulkError
	var good any
	switch in.Kind {
	case "stock":
		cfg, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		last := map[string]int{}
		var items []store.StockItem
		for i, raw := range in.Items {
			var it store.StockItem
			if err := json.Unmarshal(raw, &it); err != nil {
				errs = append(errs, bulkError{Line: i + 1, Message: "Línea ilegible"})
				continue
			}
			_, _, skuErr := t.Catalog.VariantBySKU(it.SKU)
			_, locOK := cfg.LocationByID(it.Location)
			switch {
			case skuErr != nil:
				errs = append(errs, bulkError{i + 1, it.SKU, "No existe el SKU"})
			case len(t.Inventory.Bundle(it.SKU)) > 0:
				errs = append(errs, bulkError{i + 1, it.SKU, "Es un kit: su stock sale de los componentes"})
			case !locOK && len(cfg.Locations) > 0:
				errs = append(errs, bulkError{i + 1, it.SKU, "No existe la ubicación " + it.Location})
			case it.Location == "":
				errs = append(errs, bulkError{i + 1, it.SKU, "Falta la ubicación"})
			case it.OnHand < 0 || it.OnHand > 1e9:
				errs = append(errs, bulkError{i + 1, it.SKU, "Existencias de 0 a 1,000,000,000"})
			default:
				k := it.SKU + "|" + it.Location
				if j, dup := last[k]; dup {
					items[j] = it // the last line for the same SKU and location wins
					continue
				}
				last[k] = len(items)
				items = append(items, it)
			}
		}
		good = items
	case "prices":
		ch, found := t.Channel(in.Channel)
		if !found || ch.Currency == "" || ch.PriceFrom != "" {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "channel", Code: "invalid", Message: "Elige un canal con precios propios"})
			return
		}
		last := map[string]int{}
		var items []store.PriceItem
		for i, raw := range in.Items {
			var it store.PriceItem
			if err := json.Unmarshal(raw, &it); err != nil {
				errs = append(errs, bulkError{Line: i + 1, Message: "Línea ilegible"})
				continue
			}
			_, _, skuErr := t.Catalog.VariantBySKU(it.SKU)
			switch {
			case skuErr != nil:
				errs = append(errs, bulkError{i + 1, it.SKU, "No existe el SKU"})
			case it.Amount < 0 || it.Amount > 1e13:
				errs = append(errs, bulkError{i + 1, it.SKU, "Precio inválido"})
			case it.CompareAt != nil && *it.CompareAt <= it.Amount:
				errs = append(errs, bulkError{i + 1, it.SKU, "El precio anterior debe ser mayor al precio"})
			default:
				if j, dup := last[it.SKU]; dup {
					items[j] = it
					continue
				}
				last[it.SKU] = len(items)
				items = append(items, it)
			}
		}
		good = items
		in.Channel = ch.ID
	}
	itemsJSON, _ := json.Marshal(good)
	errJSON, _ := json.Marshal(capErrors(errs))
	total := len(in.Items)
	id := newRandomID("blk_")
	status := "queued"
	if string(itemsJSON) == "null" || string(itemsJSON) == "[]" {
		status, itemsJSON = "done", []byte("[]")
	}
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO bulk_jobs (tenant_id, id, kind, channel, status, total, processed, failed, errors, items, created_by, finished_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$10, CASE WHEN $5='done' THEN now() END)`,
			pr.Tenant, id, in.Kind, in.Channel, status, total, len(errs), errJSON, itemsJSON, pr.Actor()); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "bulk."+in.Kind, id, map[string]any{"lines": total, "rejected": len(errs)})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.wakeBulk()
	j, _ := s.bulkJob(r.Context(), pr.Tenant, id)
	httpx.JSON(w, r, http.StatusAccepted, j)
}

func capErrors(e []bulkError) []bulkError {
	if e == nil {
		return []bulkError{}
	}
	if len(e) > 200 {
		return e[:200]
	}
	return e
}

func (s *Server) bulkJob(ctx context.Context, tenant, id string) (bulkJob, error) {
	var j bulkJob
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e []byte
		err := tx.QueryRow(ctx, `SELECT id, kind, channel, status, total, processed, failed, errors, created_at, finished_at FROM bulk_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).
			Scan(&j.ID, &j.Kind, &j.Channel, &j.Status, &j.Total, &j.Processed, &j.Failed, &e, &j.CreatedAt, &j.FinishedAt)
		_ = json.Unmarshal(e, &j.Errors)
		return err
	})
	return j, err
}

func (s *Server) getBulk(w http.ResponseWriter, r *http.Request) {
	j, err := s.bulkJob(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.NotFound(w, "Operación no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, j)
}

func (s *Server) listBulk(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var out []bulkJob
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, kind, channel, status, total, processed, failed, created_at, finished_at FROM bulk_jobs WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 50`, pr.Tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (bulkJob, error) {
			var j bulkJob
			j.Errors = []bulkError{}
			return j, row.Scan(&j.ID, &j.Kind, &j.Channel, &j.Status, &j.Total, &j.Processed, &j.Failed, &j.CreatedAt, &j.FinishedAt)
		})
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if out == nil {
		out = []bulkJob{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// RunBulkJobs applies queued jobs in chunks; progress is saved after each
// chunk, so a restarted node resumes where it stopped.
func (s *Server) RunBulkJobs(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, id FROM bulk_jobs WHERE status IN ('queued','running') AND (started_at IS NULL OR started_at < now() - interval '2 minutes')
			ORDER BY created_at LIMIT 5 FOR UPDATE SKIP LOCKED)
		UPDATE bulk_jobs b SET status='running', started_at=now() FROM due WHERE b.tenant_id=due.tenant_id AND b.id=due.id
		RETURNING b.tenant_id, b.id, b.kind, b.channel, b.created_by`)
	if err != nil {
		return 0, err
	}
	type job struct{ tenant, id, kind, channel, actor string }
	var list []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.tenant, &j.id, &j.kind, &j.channel, &j.actor); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, j)
	}
	rows.Close()
	for _, j := range list {
		s.runBulkJob(ctx, j.tenant, j.id, j.kind, j.channel, j.actor)
	}
	return len(list), nil
}

func (s *Server) runBulkJob(ctx context.Context, tenant, id, kind, channel, actor string) {
	var raw []byte
	if err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT items FROM bulk_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&raw)
	}); err != nil {
		return
	}
	currency := "MXN"
	if t, ok := s.Proj.Tenant(tenant); ok {
		if ch, found := t.Channel(channel); found && ch.Currency != "" {
			currency = string(ch.Currency)
		}
	}
	var stock []store.StockItem
	var prices []store.PriceItem
	n := 0
	if kind == "stock" {
		_ = json.Unmarshal(raw, &stock)
		n = len(stock)
	} else {
		_ = json.Unmarshal(raw, &prices)
		n = len(prices)
	}
	var applied int
	_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT applied FROM bulk_jobs WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&applied)
	})
	for from := applied; from < n; from += bulkChunk {
		to := min(from+bulkChunk, n)
		err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			var err error
			if kind == "stock" {
				err = store.BulkSetStockTx(ctx, tx, tenant, stock[from:to])
			} else {
				err = store.BulkSetPricesTx(ctx, tx, tenant, actor, channel, currency, prices[from:to])
			}
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE bulk_jobs SET processed=processed+$3, applied=applied+$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, to-from)
			return err
		})
		if err != nil {
			_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
				msg, _ := json.Marshal([]bulkError{{Line: 0, Message: "Se detuvo en la línea " + fmt.Sprint(from+1) + ": " + err.Error()}})
				_, e := tx.Exec(ctx, `UPDATE bulk_jobs SET status='failed', errors = errors || $3::jsonb, finished_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, msg)
				return e
			})
			return
		}
	}
	_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// Repeated lines were merged into one: every line counts as processed.
		_, err := tx.Exec(ctx, `UPDATE bulk_jobs SET status='done', processed=total, finished_at=now(), items='[]' WHERE tenant_id=$1 AND id=$2`, tenant, id)
		return err
	})
}

// exportProductsJSONL streams every product with prices of the default
// channel and stock, one JSON object per line, without loading it all.
func (s *Server) exportProductsJSONL(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tenantView(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="productos.jsonl"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	fl, _ := w.(http.Flusher)
	after := ""
	for {
		page, next := t.Catalog.List(after, 500)
		for _, p := range page {
			type vline struct {
				SKU       string `json:"sku"`
				Title     string `json:"title"`
				Price     *int64 `json:"price,omitempty"`
				Available int64  `json:"available"`
			}
			vs := make([]vline, 0, len(p.Variants))
			for _, v := range p.Variants {
				vl := vline{SKU: v.SKU, Title: v.Title, Available: t.Inventory.Available(v.SKU)}
				if q, ok := t.Pricing.Resolve(defaultChannel, v.SKU, 1); ok {
					a := q.Unit.Amount
					vl.Price = &a
				}
				vs = append(vs, vl)
			}
			_ = enc.Encode(map[string]any{"id": p.ID, "handle": p.Handle, "title": p.Title, "status": p.Status, "brand": p.Brand, "categories": p.Categories, "tags": p.Tags, "variants": vs})
		}
		if fl != nil {
			fl.Flush()
		}
		if next == "" || len(page) == 0 {
			return
		}
		after = next
	}
}
