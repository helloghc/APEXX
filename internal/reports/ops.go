package reports

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuditEntry is one record of the tamper-evident activity log.
type AuditEntry struct {
	Seq       int64           `json:"seq"`
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	ActorName string          `json:"actorName"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Data      json.RawMessage `json:"data"`
	Hash      string          `json:"hash"`
}

// AuditFilter narrows the activity log. Zero values mean "any".
type AuditFilter struct {
	Actor  string // exact, e.g. "member:m_1"
	Action string // exact or prefix before a dot: "order" matches "order.created"
	Target string // substring
	From   time.Time
	To     time.Time
	Before int64 // cursor: only seq < Before
	Limit  int
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// AuditList returns activity newest first and the cursor of the next page (0 = end).
func (s *Service) AuditList(ctx context.Context, tenant string, f AuditFilter) ([]AuditEntry, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.To.IsZero() {
		f.To = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	var out []AuditEntry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT a.seq, a.at, a.actor, COALESCE(NULLIF(m.name, ''), m.email, ''), a.action, a.target, a.data::text, left(encode(a.hash, 'hex'), 16)
			FROM audit_log a LEFT JOIN members m ON m.tenant_id = a.tenant_id AND a.actor = 'member:' || m.id
			WHERE a.tenant_id = $1 AND ($2 = 0 OR a.seq < $2) AND ($3 = '' OR a.actor = $3)
				AND ($4 = '' OR a.action = $4 OR a.action LIKE $5 ESCAPE '\')
				AND ($6 = '' OR a.target ILIKE $7 ESCAPE '\') AND a.at >= $8 AND a.at < $9
			ORDER BY a.seq DESC LIMIT $10`,
			tenant, f.Before, f.Actor, f.Action, likeEscape(f.Action)+".%", f.Target, "%"+likeEscape(f.Target)+"%", f.From, f.To, f.Limit+1)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditEntry, error) {
			var e AuditEntry
			var data string
			err := r.Scan(&e.Seq, &e.At, &e.Actor, &e.ActorName, &e.Action, &e.Target, &data, &e.Hash)
			e.Data = json.RawMessage(data)
			return e, err
		})
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].Seq
	}
	if out == nil {
		out = []AuditEntry{}
	}
	return out, next, nil
}

// Facet is a filter option with how often it appears.
type Facet struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

// AuditFacets lists the actors and actions seen in the last 90 days.
func (s *Service) AuditFacets(ctx context.Context, tenant string) (actors, actions []Facet, total int64, err error) {
	since := s.now().Add(-90 * 24 * time.Hour)
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		scan := func(r pgx.CollectableRow) (Facet, error) {
			var f Facet
			return f, r.Scan(&f.Value, &f.Label, &f.Count)
		}
		rows, err := tx.Query(ctx, `SELECT a.actor, COALESCE(NULLIF(max(m.name), ''), max(m.email), a.actor), count(*) FROM audit_log a
			LEFT JOIN members m ON m.tenant_id = a.tenant_id AND a.actor = 'member:' || m.id
			WHERE a.tenant_id = $1 AND a.at >= $2 GROUP BY a.actor ORDER BY 3 DESC LIMIT 100`, tenant, since)
		if err != nil {
			return err
		}
		if actors, err = pgx.CollectRows(rows, scan); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT action, action, count(*) FROM audit_log WHERE tenant_id = $1 AND at >= $2 GROUP BY action ORDER BY 3 DESC LIMIT 200`, tenant, since)
		if err != nil {
			return err
		}
		if actions, err = pgx.CollectRows(rows, scan); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM audit_log WHERE tenant_id = $1`, tenant).Scan(&total)
	})
	if actors == nil {
		actors = []Facet{}
	}
	if actions == nil {
		actions = []Facet{}
	}
	return actors, actions, total, err
}

// Health is the database-side view of the system for one store.
type Health struct {
	DB struct {
		OK       bool    `json:"ok"`
		PingMs   float64 `json:"pingMs"`
		Version  string  `json:"version"`
		PoolUsed int32   `json:"poolUsed"`
		PoolMax  int32   `json:"poolMax"`
	} `json:"db"`
	Outbox struct {
		LastID  int64 `json:"lastId"`
		Last24h int64 `json:"last24h"`
	} `json:"outbox"`
	Analytics struct {
		OrdersSyncedAt  *time.Time `json:"ordersSyncedAt"`
		TrafficRolledAt *time.Time `json:"trafficRolledAt"`
		OrdersBehind    int64      `json:"ordersBehind"`
		Events24h       int64      `json:"events24h"`
	} `json:"analytics"`
	Webhooks struct {
		Subscriptions    int64 `json:"subscriptions"`
		Delivered24h     int64 `json:"delivered24h"`
		Retrying         int64 `json:"retrying"`
		Dead7d           int64 `json:"dead7d"`
		OldestPendingSec int64 `json:"oldestPendingSec"`
	} `json:"webhooks"`
	Invoices struct {
		Pending int64 `json:"pending"`
		Failed  int64 `json:"failed"`
	} `json:"invoices"`
	Payments struct {
		Pending          int64 `json:"pending"`
		OldestPendingSec int64 `json:"oldestPendingSec"`
	} `json:"payments"`
	Stock struct {
		Low int64 `json:"low"`
		Out int64 `json:"out"`
	} `json:"stock"`
}

// Health measures the database and the store's queues. A database failure is
// reported inside the result (DB.OK false), not as an error.
func (s *Service) Health(ctx context.Context, tenant string) Health {
	var h Health
	st := s.DB.App.Stat()
	h.DB.PoolUsed, h.DB.PoolMax = st.AcquiredConns(), st.MaxConns()
	start := time.Now()
	var version string
	if err := s.DB.App.QueryRow(ctx, `SELECT current_setting('server_version')`).Scan(&version); err != nil {
		return h
	}
	h.DB.PingMs = float64(time.Since(start).Microseconds()) / 1000
	h.DB.OK, h.DB.Version = true, version
	now := s.now()
	_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		c, err := opsCounts(ctx, tx, tenant, now)
		if err != nil {
			return err
		}
		h.Webhooks.Subscriptions, h.Webhooks.Delivered24h, h.Webhooks.Retrying = c.WebhookSubs, c.WebhookDelivered, c.WebhookPending
		h.Webhooks.Dead7d, h.Webhooks.OldestPendingSec = c.WebhookDead, c.WebhookOldestPendingSec
		h.Invoices.Pending, h.Invoices.Failed = c.InvoicesPending, c.InvoicesFailed
		h.Payments.Pending, h.Payments.OldestPendingSec = c.PaymentsPending, c.PaymentsOldestPendingSec
		h.Stock.Low, h.Stock.Out = c.LowStock, c.OutOfStock
		return tx.QueryRow(ctx, `SELECT
				COALESCE((SELECT max(id) FROM outbox WHERE tenant_id = $1), 0),
				(SELECT count(*) FROM outbox WHERE tenant_id = $1 AND created_at >= $2::timestamptz - interval '1 day'),
				(SELECT count(*) FROM analytics_events WHERE tenant_id = $1 AND at >= $2::timestamptz - interval '1 day'),
				(SELECT count(*) FROM orders o WHERE o.tenant_id = $1 AND o.updated_at >= $2::timestamptz - interval '1 day' AND NOT EXISTS (
					SELECT 1 FROM analytics_orders a WHERE a.tenant_id = o.tenant_id AND a.order_id = o.id AND a.src_version = o.version))`,
			tenant, now).Scan(&h.Outbox.LastID, &h.Outbox.Last24h, &h.Analytics.Events24h, &h.Analytics.OrdersBehind)
	})
	rows, err := s.DB.System.Query(ctx, `SELECT name, at FROM analytics_state`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var at time.Time
			if rows.Scan(&name, &at) != nil || at.Year() < 2000 {
				continue
			}
			t := at
			switch name {
			case "orders":
				h.Analytics.OrdersSyncedAt = &t
			case "traffic":
				h.Analytics.TrafficRolledAt = &t
			}
		}
	}
	return h
}
