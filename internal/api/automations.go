package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
)

// Automations: "when this happens to an order, do this". Runs are queued in
// the order's own transaction, carried out here with retries, resume at the
// action that failed, and stay as history.

// AutomationTriggers are the events an automation can listen to.
var AutomationTriggers = map[string]string{
	"order.created":         "Se crea un pedido",
	"payment.captured":      "Se paga un pedido",
	"payment.failed":        "Se rechaza un pago",
	"order.cancelled":       "Se cancela un pedido",
	"order.refunded":        "Se reembolsa un pedido",
	"fulfillment.shipped":   "Se envía un pedido",
	"fulfillment.delivered": "Se entrega un pedido",
	"return.requested":      "Se pide una devolución",
}

type automationConditions struct {
	MinTotal int64    `json:"minTotal,omitempty"` // cents
	Channel  string   `json:"channel,omitempty"`
	SKUs     []string `json:"skus,omitempty"` // any of them in the order
}

type automationAction struct {
	Kind string `json:"kind"` // note | tag_customer | webhook
	Text string `json:"text,omitempty"`
	Tag  string `json:"tag,omitempty"`
	URL  string `json:"url,omitempty"`
}

type automation struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Trigger    string               `json:"trigger"`
	Conditions automationConditions `json:"conditions"`
	Actions    []automationAction   `json:"actions"`
	Active     bool                 `json:"active"`
	UpdatedAt  time.Time            `json:"updatedAt"`
	Stats      map[string]int       `json:"stats,omitempty"`
}

type automationRun struct {
	ID        string          `json:"id"`
	OrderID   string          `json:"orderId"`
	Event     string          `json:"event"`
	Status    string          `json:"status"`
	Attempts  int             `json:"attempts"`
	Step      int             `json:"step"`
	Log       json.RawMessage `json:"log"`
	LastError string          `json:"lastError,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	Finished  *time.Time      `json:"finishedAt,omitempty"`
}

var automationHTTPClient *http.Client // tests

func (s *Server) routesAutomations(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/automations", s.authed("settings", auth.Read, s.listAutomations))
	handle("POST /v1/automations", s.authed("settings", auth.Admin, s.saveAutomation))
	handle("PUT /v1/automations/{id}", s.authed("settings", auth.Admin, s.saveAutomation))
	handle("DELETE /v1/automations/{id}", s.authed("settings", auth.Admin, s.deleteAutomation))
	handle("GET /v1/automations/{id}/runs", s.authed("settings", auth.Read, s.automationRuns))
	handle("POST /v1/automations/runs/{run}/retry", s.authed("settings", auth.Admin, s.retryAutomationRun))
}

func (s *Server) cleanAutomation(a *automation) []apierr.FieldError {
	var errs []apierr.FieldError
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" || len([]rune(a.Name)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "name", Code: "invalid", Message: "Escribe un nombre de hasta 80 caracteres"})
	}
	if _, ok := AutomationTriggers[a.Trigger]; !ok {
		errs = append(errs, apierr.FieldError{Field: "trigger", Code: "invalid", Message: "Elige cuándo se ejecuta"})
	}
	if a.Conditions.MinTotal < 0 || len(a.Conditions.SKUs) > 50 {
		errs = append(errs, apierr.FieldError{Field: "conditions", Code: "invalid", Message: "Condición inválida"})
	}
	if len(a.Actions) == 0 || len(a.Actions) > 10 {
		errs = append(errs, apierr.FieldError{Field: "actions", Code: "invalid", Message: "De una a diez acciones"})
	}
	for i, x := range a.Actions {
		f := fmt.Sprintf("actions[%d]", i)
		x.Text, x.Tag, x.URL = strings.TrimSpace(x.Text), strings.TrimSpace(x.Tag), strings.TrimSpace(x.URL)
		switch x.Kind {
		case "note":
			if x.Text == "" || len([]rune(x.Text)) > 500 {
				errs = append(errs, apierr.FieldError{Field: f + ".text", Code: "invalid", Message: "Escribe la nota (hasta 500 caracteres)"})
			}
		case "tag_customer":
			if x.Tag == "" || len(x.Tag) > 40 || strings.ContainsAny(x.Tag, ",") {
				errs = append(errs, apierr.FieldError{Field: f + ".tag", Code: "invalid", Message: "Etiqueta de hasta 40 caracteres, sin comas"})
			}
		case "webhook":
			if s.Webhooks == nil || s.Webhooks.CheckURL(x.URL) != nil {
				errs = append(errs, apierr.FieldError{Field: f + ".url", Code: "invalid", Message: "La dirección debe ser https y pública"})
			}
		default:
			errs = append(errs, apierr.FieldError{Field: f + ".kind", Code: "invalid", Message: "Acción: nota, etiqueta o aviso a otro sistema"})
		}
		a.Actions[i] = x
	}
	return errs
}

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	var out []automation
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT a.id, a.name, a.trigger, a.conditions, a.actions, a.active, a.updated_at,
				COALESCE((SELECT jsonb_object_agg(status, n) FROM (SELECT status, count(*) n FROM automation_runs x WHERE x.tenant_id=a.tenant_id AND x.automation_id=a.id GROUP BY status) c), '{}')
			FROM automations a WHERE a.tenant_id=$1 ORDER BY a.created_at`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (automation, error) {
			var a automation
			var c, ac, st []byte
			err := row.Scan(&a.ID, &a.Name, &a.Trigger, &c, &ac, &a.Active, &a.UpdatedAt, &st)
			_ = json.Unmarshal(c, &a.Conditions)
			_ = json.Unmarshal(ac, &a.Actions)
			_ = json.Unmarshal(st, &a.Stats)
			return a, err
		})
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if out == nil {
		out = []automation{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "triggers": AutomationTriggers})
}

func (s *Server) saveAutomation(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var a automation
	if !decode(w, r, &a) {
		return
	}
	if errs := s.cleanAutomation(&a); len(errs) > 0 {
		apierr.BadRequest(w, "Revisa la automatización", errs...)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		id = newRandomID("aut_")
	}
	c, _ := json.Marshal(a.Conditions)
	ac, _ := json.Marshal(a.Actions)
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if r.PathValue("id") != "" {
			ct, err := tx.Exec(r.Context(), `UPDATE automations SET name=$3, trigger=$4, conditions=$5, actions=$6, active=$7, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				pr.Tenant, id, a.Name, a.Trigger, c, ac, a.Active)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return store.ErrNotFound
			}
		} else {
			var n int
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM automations WHERE tenant_id=$1`, pr.Tenant).Scan(&n); err != nil {
				return err
			}
			if n >= 100 {
				return errors.New("límite")
			}
			if _, err := tx.Exec(r.Context(), `INSERT INTO automations (tenant_id, id, name, trigger, conditions, actions, active) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				pr.Tenant, id, a.Name, a.Trigger, c, ac, a.Active); err != nil {
				return err
			}
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "automation.saved", id, map[string]any{"name": a.Name, "trigger": a.Trigger, "active": a.Active})
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Automatización no encontrada")
		return
	case err != nil && err.Error() == "límite":
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "too_many", Detail: "Hasta 100 automatizaciones por tienda"})
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	a.ID = id
	httpx.JSON(w, r, http.StatusOK, a)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM automations WHERE tenant_id=$1 AND id=$2`, pr.Tenant, r.PathValue("id"))
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		// Runs still waiting are dropped; the history stays.
		if _, err := tx.Exec(r.Context(), `UPDATE automation_runs SET status='skipped', last_error='La automatización se borró', finished_at=now() WHERE tenant_id=$1 AND automation_id=$2 AND status='pending'`, pr.Tenant, r.PathValue("id")); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "automation.deleted", r.PathValue("id"), nil)
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Automatización no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) automationRuns(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var out []automationRun
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, order_id, event, status, attempts, step, log, last_error, created_at, finished_at
			FROM automation_runs WHERE tenant_id=$1 AND automation_id=$2 ORDER BY created_at DESC LIMIT 100`, pr.Tenant, r.PathValue("id"))
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (automationRun, error) {
			var x automationRun
			return x, row.Scan(&x.ID, &x.OrderID, &x.Event, &x.Status, &x.Attempts, &x.Step, &x.Log, &x.LastError, &x.CreatedAt, &x.Finished)
		})
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if out == nil {
		out = []automationRun{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) retryAutomationRun(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var n int64
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `UPDATE automation_runs SET status='pending', attempts=0, next_attempt=now(), finished_at=NULL WHERE tenant_id=$1 AND id=$2 AND status='failed'`, pr.Tenant, r.PathValue("run"))
		n = ct.RowsAffected()
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Solo se reintenta una ejecución fallida")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// RunAutomations carries out the due runs. Each action is done once: a retry
// resumes at the action that failed. Six failed attempts end the run.
func (s *Server) RunAutomations(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, id FROM automation_runs WHERE status='pending' AND next_attempt <= now() ORDER BY next_attempt LIMIT 100 FOR UPDATE SKIP LOCKED)
		UPDATE automation_runs x SET next_attempt = now() + interval '2 minutes' FROM due
		WHERE x.tenant_id=due.tenant_id AND x.id=due.id
		RETURNING x.tenant_id, x.id, x.automation_id, x.order_id, x.event, x.step, x.attempts`)
	if err != nil {
		return 0, err
	}
	type due struct {
		tenant, id, automation, order, event string
		step, attempts                       int
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.tenant, &d.id, &d.automation, &d.order, &d.event, &d.step, &d.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, d)
	}
	rows.Close()
	for _, d := range list {
		s.runAutomation(ctx, d.tenant, d.id, d.automation, d.order, d.step, d.attempts)
	}
	return len(list), nil
}

type runLog struct {
	Action string `json:"action"`
	Result string `json:"result"`
	At     string `json:"at"`
}

func (s *Server) runAutomation(ctx context.Context, tenant, runID, autID, orderID string, step, attempts int) {
	finish := func(status, msg string, step int, logs []runLog) {
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			b, _ := json.Marshal(logs)
			attempts++
			next := time.Now().Add(time.Duration(1<<min(attempts, 8)) * time.Minute)
			if status == "pending" && attempts >= 6 {
				status = "failed"
			}
			_, err := tx.Exec(ctx, `UPDATE automation_runs SET status=$3, step=$4, attempts=$5, next_attempt=$6, log = log || $7::jsonb, last_error=$8,
				finished_at = CASE WHEN $3 = 'pending' THEN NULL ELSE now() END WHERE tenant_id=$1 AND id=$2`,
				tenant, runID, status, step, attempts, next, b, msg)
			return err
		})
	}
	var a automation
	var c, ac []byte
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name, conditions, actions, active FROM automations WHERE tenant_id=$1 AND id=$2`, tenant, autID).Scan(&a.Name, &c, &ac, &a.Active)
	})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !a.Active) {
		finish("skipped", "La automatización está pausada o se borró", step, nil)
		return
	}
	if err != nil {
		finish("pending", err.Error(), step, nil)
		return
	}
	_ = json.Unmarshal(c, &a.Conditions)
	_ = json.Unmarshal(ac, &a.Actions)
	o, err := s.Orders.Get(ctx, tenant, orderID, false)
	if err != nil {
		finish("pending", "No se pudo leer el pedido: "+err.Error(), step, nil)
		return
	}
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }
	if step == 0 {
		if why := automationMismatch(a.Conditions, o); why != "" {
			finish("skipped", why, 0, []runLog{{Action: "condiciones", Result: why, At: now()}})
			return
		}
	}
	actor := "automation:" + autID
	var logs []runLog
	for i := step; i < len(a.Actions); i++ {
		x := a.Actions[i]
		res, err := s.automationAction(ctx, tenant, actor, autID, a.Name, x, o)
		if err != nil {
			logs = append(logs, runLog{Action: x.Kind, Result: "Error: " + err.Error(), At: now()})
			finish("pending", err.Error(), i, logs)
			return
		}
		logs = append(logs, runLog{Action: x.Kind, Result: res, At: now()})
		step = i + 1
	}
	finish("done", "", step, logs)
}

func automationMismatch(c automationConditions, o orders.Order) string {
	if c.MinTotal > 0 && o.GrandTotal < c.MinTotal {
		return "El total es menor a " + notify.Money(c.MinTotal, o.Currency)
	}
	if c.Channel != "" && o.Channel != c.Channel {
		return "El pedido es de otro canal"
	}
	if len(c.SKUs) > 0 && !slices.ContainsFunc(o.Lines, func(l orders.Line) bool { return slices.Contains(c.SKUs, l.SKU) }) {
		return "El pedido no tiene ninguno de los productos elegidos"
	}
	return ""
}

func (s *Server) automationAction(ctx context.Context, tenant, actor, autID, name string, x automationAction, o orders.Order) (string, error) {
	fill := strings.NewReplacer("{{pedido.numero}}", o.DisplayNumber, "{{pedido.total}}", notify.Money(o.GrandTotal, o.Currency))
	switch x.Kind {
	case "note":
		if _, err := s.Orders.AddNote(ctx, tenant, actor, "Automatización: "+name, o.ID, "internal", fill.Replace(x.Text)); err != nil {
			return "", err
		}
		return "Nota agregada", nil
	case "tag_customer":
		if o.CustomerID == "" {
			return "El pedido no tiene cliente: sin etiqueta", nil
		}
		if _, err := s.Customers.BulkTag(ctx, tenant, actor, []string{o.CustomerID}, x.Tag, false); err != nil {
			return "", err
		}
		return "Cliente etiquetado: " + x.Tag, nil
	case "webhook":
		lines := make([]map[string]any, len(o.Lines))
		for i, l := range o.Lines {
			lines[i] = map[string]any{"sku": l.SKU, "title": l.Title, "qty": l.Qty, "total": l.Total}
		}
		// No personal data of the buyer: the receiver asks the API with its own app permissions.
		body, _ := json.Marshal(map[string]any{"automation": autID, "store": tenant, "order": map[string]any{"id": o.ID, "number": o.DisplayNumber,
			"status": o.Status, "paymentStatus": o.PaymentStatus, "fulfillmentStatus": o.FulfillmentStatus, "currency": o.Currency, "total": o.GrandTotal, "channel": o.Channel, "lines": lines}})
		key, err := s.Keys.Blind(tenant, "automation-signing", autID)
		if err != nil {
			return "", err
		}
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, x.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Apexx-Timestamp", ts)
		req.Header.Set("X-Apexx-Signature", carriers.Sign([]byte(hex.EncodeToString(key)), ts, body))
		client := automationHTTPClient
		if client == nil {
			client = webhooks.Client(s.Webhooks != nil && s.Webhooks.AllowPrivate)
		}
		res, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("sin respuesta de %s", req.URL.Host)
		}
		res.Body.Close()
		if res.StatusCode >= 300 {
			return "", fmt.Errorf("%s respondió %d", req.URL.Host, res.StatusCode)
		}
		return fmt.Sprintf("Aviso entregado (%d)", res.StatusCode), nil
	}
	return "", fmt.Errorf("acción desconocida %q", x.Kind)
}

func newRandomID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}
