package reports

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Activity rollups (migration 159): the operations screens count changes
// and audit entries over long windows. RollupActivity adds up what is new
// since its mark; the screens add the rollup and the rows after the mark.

const rollupBatch = 200_000

// RollupActivity adds new outbox events (per store and hour) and new audit
// entries (per store, day, actor and action) to activity_rollup. It works
// in batches for at most budget and says how many rows it added.
func (s *Service) RollupActivity(ctx context.Context, budget time.Duration) (int, error) {
	start := time.Now()
	total := 0
	for time.Since(start) < budget {
		n, err := s.rollupOutbox(ctx)
		total += n
		if err != nil || n < rollupBatch {
			if err != nil {
				return total, err
			}
			break
		}
	}
	tenants, err := s.DB.Tenants(ctx)
	if err != nil {
		return total, err
	}
	for _, t := range tenants {
		for time.Since(start) < budget {
			n, err := s.rollupAudit(ctx, t)
			total += n
			if err != nil {
				return total, err
			}
			if n < rollupBatch {
				break
			}
		}
	}
	// Old buckets nobody asks for: hours older than 3 days, days older than 120.
	_, err = s.DB.System.Exec(ctx, `DELETE FROM activity_rollup WHERE (kind = 'outbox' AND bucket < now() - interval '3 days') OR bucket < now() - interval '120 days'`)
	return total, err
}

// mark locks and returns the mark of source (0 when it has none yet).
func mark(ctx context.Context, tx pgx.Tx, source string) (int64, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO activity_marks (source, seq) VALUES ($1, 0) ON CONFLICT DO NOTHING`, source); err != nil {
		return 0, err
	}
	var m int64
	err := tx.QueryRow(ctx, `SELECT seq FROM activity_marks WHERE source = $1 FOR UPDATE`, source).Scan(&m)
	return m, err
}

func (s *Service) rollupOutbox(ctx context.Context) (int, error) {
	tx, err := s.DB.System.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, err := mark(ctx, tx, "outbox")
	if err != nil {
		return 0, err
	}
	var last int64
	var n int
	if err := tx.QueryRow(ctx, `WITH r AS (SELECT seq, tenant_id, created_at FROM outbox WHERE seq > $1 ORDER BY seq LIMIT $2),
		a AS (INSERT INTO activity_rollup (tenant_id, kind, bucket, key, n)
			SELECT r.tenant_id, 'outbox', date_trunc('hour', r.created_at, 'UTC'), '', count(*) FROM r JOIN tenants t ON t.id = r.tenant_id GROUP BY 1, 3
			ON CONFLICT (tenant_id, kind, bucket, key) DO UPDATE SET n = activity_rollup.n + EXCLUDED.n)
		SELECT COALESCE(max(seq), $1), count(*) FROM r`, m, rollupBatch).Scan(&last, &n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE activity_marks SET seq = $2 WHERE source = $1`, "outbox", last); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

func (s *Service) rollupAudit(ctx context.Context, tenant string) (int, error) {
	tx, err := s.DB.System.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	src := "audit:" + tenant
	m, err := mark(ctx, tx, src)
	if err != nil {
		return 0, err
	}
	var last int64
	var n int
	if err := tx.QueryRow(ctx, `WITH r AS (SELECT seq, actor, action, at FROM audit_log WHERE tenant_id = $1 AND seq > $2 ORDER BY seq LIMIT $3),
		a AS (INSERT INTO activity_rollup (tenant_id, kind, bucket, key, n)
			SELECT $1, k, d, key, count(*) FROM (
				SELECT 'actor' AS k, date_trunc('day', at, 'UTC') AS d, actor AS key FROM r
				UNION ALL SELECT 'action', date_trunc('day', at, 'UTC'), action FROM r) x GROUP BY k, d, key
			ON CONFLICT (tenant_id, kind, bucket, key) DO UPDATE SET n = activity_rollup.n + EXCLUDED.n)
		SELECT COALESCE(max(seq), $2), count(*) FROM r`, tenant, m, rollupBatch).Scan(&last, &n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE activity_marks SET seq = $2 WHERE source = $1`, src, last); err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

// activityMarks reads the marks a store's counts start from.
func (s *Service) activityMarks(ctx context.Context, tenant string) (outbox, audit int64) {
	rows, err := s.DB.System.Query(ctx, `SELECT source, seq FROM activity_marks WHERE source IN ('outbox', $1)`, "audit:"+tenant)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var seq int64
		if rows.Scan(&src, &seq) != nil {
			continue
		}
		if src == "outbox" {
			outbox = seq
		} else {
			audit = seq
		}
	}
	return outbox, audit
}
