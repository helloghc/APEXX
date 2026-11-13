package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Metrics per sandbox (Operación › Estado): requests and errors counted on
// this node since it started, and what the database knows (size, copies,
// publishes, captured emails, AI use).

type sbxCounter struct {
	Requests int64     `json:"requests"`
	Errors   int64     `json:"errors"` // answers 5xx
	Denied   int64     `json:"denied"` // private: 403 to visitors without access
	LastAt   time.Time `json:"lastAt"`
}

type sbxMetrics struct {
	mu sync.Mutex
	m  map[string]*sbxCounter
}

func (m *sbxMetrics) add(tenant string, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.m == nil {
		m.m = map[string]*sbxCounter{}
	}
	c := m.m[tenant]
	if c == nil {
		c = &sbxCounter{}
		m.m[tenant] = c
	}
	c.Requests++
	switch {
	case code >= 500:
		c.Errors++
	case code == http.StatusForbidden:
		c.Denied++
	}
	c.LastAt = time.Now()
}

func (m *sbxMetrics) get(tenant string) sbxCounter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c := m.m[tenant]; c != nil {
		return *c
	}
	return sbxCounter{}
}

func (m *sbxMetrics) forget(tenant string) {
	m.mu.Lock()
	delete(m.m, tenant)
	m.mu.Unlock()
}

// countSandbox wraps a request to a sandbox (by id prefix: production pays
// one string comparison) and counts its answer.
func (s *Server) countSandbox(tenant string, w http.ResponseWriter, serve func(http.ResponseWriter)) {
	if !strings.HasPrefix(tenant, store.SandboxPrefix) {
		serve(w)
		return
	}
	sw := &statusWriter{ResponseWriter: w}
	defer func() {
		code := sw.code
		if code == 0 {
			code = http.StatusOK
		}
		s.sbxStats.add(tenant, code)
	}()
	serve(sw)
}

// writeSandboxMetrics adds the per-sandbox counters to /metrics.
func (s *Server) writeSandboxMetrics(b *strings.Builder) {
	s.sbxStats.mu.Lock()
	defer s.sbxStats.mu.Unlock()
	ids := make([]string, 0, len(s.sbxStats.m))
	for id := range s.sbxStats.m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b.WriteString("# TYPE apexx_sandbox_requests_total counter\n")
	for _, id := range ids {
		c := s.sbxStats.m[id]
		fmt.Fprintf(b, "apexx_sandbox_requests_total{sandbox=%q} %d\napexx_sandbox_errors_total{sandbox=%q} %d\napexx_sandbox_denied_total{sandbox=%q} %d\n",
			id, c.Requests, id, c.Errors, id, c.Denied)
	}
}

type sandboxOps struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	HasPassword bool       `json:"hasPassword"`
	Products    int64      `json:"products"`
	Prices      int64      `json:"prices"`
	Orders      int64      `json:"orders"`
	CopiedAt    *time.Time `json:"copiedAt,omitempty"`
	CopyMs      int        `json:"copyMs"`
	LastUsedAt  time.Time  `json:"lastUsedAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	Publishes   int64      `json:"publishes"`
	LastPublish *time.Time `json:"lastPublishAt,omitempty"`
	Scheduled   int64      `json:"scheduled"` // pending scheduled publishes
	MailCapture int64      `json:"mailCaptured"`
	AIRequests  int64      `json:"aiRequests"` // last 30 days
	AICostUSD   float64    `json:"aiCostUsd"`
	Node        sbxCounter `json:"node"` // this node, since it started
}

// opsSandboxes: the metrics of every sandbox of the store.
func (s *Server) opsSandboxes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	parent := s.parentOf(principal(r).Tenant)
	list, err := s.DB.SandboxesOf(ctx, parent)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	plan, err := s.DB.TenantPlan(ctx, parent)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]sandboxOps, 0, len(list))
	for _, sb := range list {
		o := sandboxOps{ID: sb.ID, Name: sb.Name, Status: sb.Status, HasPassword: sb.HasPassword, CopiedAt: sb.CopiedAt, CopyMs: sb.CopyMs, LastUsedAt: sb.LastUsedAt, Node: s.sbxStats.get(sb.ID)}
		var cost int64
		err := s.DB.System.QueryRow(ctx, `SELECT
				(SELECT count(*) FROM products WHERE tenant_id=$1),
				(SELECT count(*) FROM prices WHERE tenant_id=$1),
				(SELECT count(*) FROM orders WHERE tenant_id=$1),
				(SELECT count(*) FROM sandbox_publishes WHERE sandbox_id=$1),
				(SELECT max(published_at) FROM sandbox_publishes WHERE sandbox_id=$1),
				(SELECT count(*) FROM sandbox_schedules WHERE sandbox_id=$1 AND status='pending'),
				(SELECT count(*) FROM notification_messages WHERE tenant_id=$1),
				(SELECT COALESCE(sum(requests),0) FROM ai_usage WHERE tenant_id=$1 AND day > current_date - 30),
				(SELECT COALESCE(sum(cost_micros),0) FROM ai_usage WHERE tenant_id=$1 AND day > current_date - 30)`, sb.ID).
			Scan(&o.Products, &o.Prices, &o.Orders, &o.Publishes, &o.LastPublish, &o.Scheduled, &o.MailCapture, &o.AIRequests, &cost)
		if err != nil && err != pgx.ErrNoRows {
			internalError(w, s.Log, err)
			return
		}
		o.AICostUSD = float64(cost) / 1e6
		hosts, _ := s.sandboxDomains(ctx, sb.ID)
		if !s.hasOwnDomain(hosts) {
			exp := sb.LastUsedAt.Add(sandboxIdleDays * 24 * time.Hour)
			o.ExpiresAt = &exp
		}
		out = append(out, o)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "plan": plan, "since": s.started})
}

// sandboxWork tracks the sandbox copies and scheduled publishes running on
// this node, so a node being replaced finishes them before it exits.
type sandboxWork struct {
	mu      sync.Mutex
	running map[string]string // id -> copy | schedule
	closed  bool
}

// start registers work; false once the node is shutting down.
func (w *sandboxWork) start(id, kind string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	if w.running == nil {
		w.running = map[string]string{}
	}
	w.running[id] = kind
	return true
}

func (w *sandboxWork) done(id string) {
	w.mu.Lock()
	delete(w.running, id)
	w.mu.Unlock()
}

func (w *sandboxWork) left() map[string]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[string]string, len(w.running))
	for k, v := range w.running {
		out[k] = v
	}
	return out
}

// FinishSandboxWork is called by a node that is shutting down: no new copy
// or scheduled publish starts here, the running ones get up to timeout to
// end, and whatever is still running is marked failed with a clear message
// (the other nodes never leave it "copying" or "running").
func (s *Server) FinishSandboxWork(timeout time.Duration) {
	s.sbxWork.mu.Lock()
	s.sbxWork.closed = true
	s.sbxWork.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for len(s.sbxWork.left()) > 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for id, kind := range s.sbxWork.left() {
		if kind == "copy" {
			_ = s.DB.SetSandboxStatus(ctx, id, "failed", "El servidor se reinició durante la copia. Refresca el sandbox para copiarlo de nuevo.", 0)
		} else {
			_ = s.DB.FinishSchedule(ctx, id, "failed", "", "El servidor se reinició mientras publicaba. Revisa Publicaciones y prográmala de nuevo si no aparece.")
		}
		if s.Log != nil {
			s.Log.Warn("sandbox work interrupted by shutdown", "id", id, "kind", kind)
		}
	}
}
