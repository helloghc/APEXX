package reports

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// span is a half-open interval of instants.
type span struct{ from, to time.Time }

// dayStart returns the instant a store day begins.
func dayStart(t time.Time) time.Time {
	l := t.In(Zone)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, Zone)
}

// spansOf turns the instants that changed into the hour and day intervals to
// recompute. Many scattered instants collapse into one interval.
func spansOf(ts []time.Time) (hours, days []span) {
	if len(ts) == 0 {
		return nil, nil
	}
	hs, ds := map[time.Time]bool{}, map[time.Time]bool{}
	lo, hi := ts[0], ts[0]
	for _, t := range ts {
		hs[t.UTC().Truncate(time.Hour)] = true
		ds[dayStart(t)] = true
		if t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
	}
	if len(hs) > 24 {
		return []span{{lo.UTC().Truncate(time.Hour), hi.UTC().Truncate(time.Hour).Add(time.Hour)}},
			[]span{{dayStart(lo), dayStart(hi).AddDate(0, 0, 1)}}
	}
	for h := range hs {
		hours = append(hours, span{h, h.Add(time.Hour)})
	}
	for d := range ds {
		days = append(days, span{d, d.AddDate(0, 0, 1)})
	}
	return hours, days
}

const syncBatch = 2000

// SyncOrders brings the order facts and the sales rollups up to date with
// every order whose version changed since the last run. It is idempotent and
// safe on several nodes (one runs, the others skip).
func (s *Service) SyncOrders(ctx context.Context) (int, error) {
	if _, err := s.DB.System.Exec(ctx, `INSERT INTO analytics_state (name, at) VALUES ('orders', 'epoch') ON CONFLICT DO NOTHING`); err != nil {
		return 0, err
	}
	tx, err := s.DB.System.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var mark time.Time
	err = tx.QueryRow(ctx, `SELECT at FROM analytics_state WHERE name='orders' FOR UPDATE SKIP LOCKED`).Scan(&mark)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // another node is syncing
	}
	if err != nil {
		return 0, err
	}
	total := 0
	var firstErr error
	for round := 0; round < 50; round++ {
		start := time.Now()
		// Overlap: a transaction that started before the mark may commit after it.
		rows, err := tx.Query(ctx, `SELECT o.tenant_id, o.id, o.updated_at FROM orders o
			LEFT JOIN analytics_orders a ON a.tenant_id = o.tenant_id AND a.order_id = o.id
			WHERE o.updated_at >= $1 AND (a.order_id IS NULL OR a.src_version <> o.version)
			ORDER BY o.updated_at LIMIT $2`, mark.Add(-10*time.Minute), syncBatch)
		if err != nil {
			return total, err
		}
		byTenant := map[string][]string{}
		n := 0
		var last time.Time
		for rows.Next() {
			var t, id string
			if err := rows.Scan(&t, &id, &last); err != nil {
				rows.Close()
				return total, err
			}
			byTenant[t] = append(byTenant[t], id)
			n++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return total, err
		}
		failed := false
		for t, ids := range byTenant {
			if err := s.syncTenantOrders(ctx, t, ids, true); err != nil {
				failed = true
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			total += len(ids)
		}
		if failed {
			break // keep the mark: the next run retries
		}
		if n < syncBatch {
			mark = start
		} else {
			mark = last
		}
		if _, err := tx.Exec(ctx, `UPDATE analytics_state SET at=$1 WHERE name='orders'`, mark); err != nil {
			return total, err
		}
		if n < syncBatch {
			break
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return total, err
	}
	return total, firstErr
}

// customerHash returns the keyed hash of the customer e-mail of an order.
func (s *Service) customerHash(tenant, orderID string, enc []byte) []byte {
	plain, err := s.Keys.Decrypt(tenant, enc, orderID)
	if err != nil {
		return nil
	}
	var c struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(plain, &c) != nil {
		return nil
	}
	email := strings.ToLower(strings.TrimSpace(c.Email))
	if email == "" {
		return nil
	}
	h, err := s.Keys.Hash(tenant, "customer-email", email)
	if err != nil {
		return nil
	}
	return h[:16]
}

// syncTenantOrders rebuilds the facts of the given orders and, when rollup is
// set, every sales bucket those orders touch (before and after the change).
func (s *Service) syncTenantOrders(ctx context.Context, tenant string, ids []string, rollup bool) error {
	if _, err := s.Keys.Hash(tenant, "probe", ""); err != nil {
		return ErrNoTenantKey
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var dirty []time.Time
		note := func(ts ...*time.Time) {
			for _, t := range ts {
				if t != nil {
					dirty = append(dirty, *t)
				}
			}
		}
		rows, err := tx.Query(ctx, `SELECT o.id, o.customer, a.customer_hash, a.paid_at, a.refunded_at FROM orders o
			LEFT JOIN analytics_orders a ON a.tenant_id = o.tenant_id AND a.order_id = o.id
			WHERE o.tenant_id = $1 AND o.id = ANY($2)`, tenant, ids)
		if err != nil {
			return err
		}
		var oids []string
		var hashes [][]byte
		for rows.Next() {
			var id string
			var enc, hash []byte
			var paid, refunded *time.Time
			if err := rows.Scan(&id, &enc, &hash, &paid, &refunded); err != nil {
				rows.Close()
				return err
			}
			if hash == nil {
				hash = s.customerHash(tenant, id, enc)
			}
			note(paid, refunded)
			oids = append(oids, id)
			hashes = append(hashes, hash)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(oids) == 0 {
			return nil
		}
		// An order counts as sold from its first transition to "paid"; states
		// this package does not know are treated as sold when the order was
		// created, so new order flows never silently drop revenue.
		rows, err = tx.Query(ctx, `INSERT INTO analytics_orders (tenant_id, order_id, number, channel, currency, status, src_version,
				created_at, paid_at, refunded_at, gross, discounts, shipping, tax, total, refunded, units, customer_hash)
			SELECT o.tenant_id, o.id, o.number, o.channel, o.currency, o.status, o.version, o.created_at,
				COALESCE(ev.paid_at, CASE WHEN o.status NOT IN ('pending_payment','cancelled','refunded') THEN o.created_at END),
				CASE WHEN COALESCE(p.refunded, 0) > 0 THEN COALESCE(ev.refunded_at, o.updated_at) END,
				o.items_total, o.discounts, o.shipping, o.tax_included, o.grand_total, COALESCE(p.refunded, 0), COALESCE(l.units, 0), i.hash
			FROM unnest($2::text[], $3::bytea[]) AS i(id, hash)
			JOIN orders o ON o.tenant_id = $1 AND o.id = i.id
			LEFT JOIN LATERAL (SELECT min(at) FILTER (WHERE to_status = 'paid') AS paid_at,
					max(at) FILTER (WHERE to_status = 'refunded' OR type LIKE '%refund%') AS refunded_at
				FROM order_events e WHERE e.tenant_id = o.tenant_id AND e.order_id = o.id) ev ON true
			LEFT JOIN LATERAL (SELECT sum(refunded)::bigint AS refunded FROM payments p
				WHERE p.tenant_id = o.tenant_id AND p.order_id = o.id) p ON true
			LEFT JOIN LATERAL (SELECT sum(qty)::bigint AS units FROM order_lines l
				WHERE l.tenant_id = o.tenant_id AND l.order_id = o.id) l ON true
			ON CONFLICT (tenant_id, order_id) DO UPDATE SET status = EXCLUDED.status, src_version = EXCLUDED.src_version,
				channel = EXCLUDED.channel, currency = EXCLUDED.currency, paid_at = EXCLUDED.paid_at, refunded_at = EXCLUDED.refunded_at,
				gross = EXCLUDED.gross, discounts = EXCLUDED.discounts, shipping = EXCLUDED.shipping, tax = EXCLUDED.tax,
				total = EXCLUDED.total, refunded = EXCLUDED.refunded, units = EXCLUDED.units,
				customer_hash = COALESCE(analytics_orders.customer_hash, EXCLUDED.customer_hash)
			RETURNING paid_at, refunded_at`, tenant, oids, hashes)
		if err != nil {
			return err
		}
		for rows.Next() {
			var paid, refunded *time.Time
			if err := rows.Scan(&paid, &refunded); err != nil {
				rows.Close()
				return err
			}
			note(paid, refunded)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM analytics_order_promos WHERE tenant_id = $1 AND order_id = ANY($2)`, tenant, oids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO analytics_order_promos (tenant_id, order_id, promotion_id, name, amount)
			SELECT e.tenant_id, e.order_id, left(pr->>'id', 200), left(COALESCE(max(pr->>'name'), ''), 200), sum((pr->'discount'->>'amount')::bigint)
			FROM order_events e, jsonb_array_elements(CASE WHEN jsonb_typeof(e.data->'promotions') = 'array' THEN e.data->'promotions' ELSE '[]'::jsonb END) pr
			WHERE e.tenant_id = $1 AND e.order_id = ANY($2) AND e.type = 'order.created'
				AND pr->>'applied' = 'true' AND COALESCE(pr->>'id', '') <> ''
				AND COALESCE(pr->'discount'->>'amount', '') ~ '^[1-9][0-9]{0,15}$'
			GROUP BY 1, 2, 3`, tenant, oids); err != nil {
			return err
		}
		// First paid order of a customer = new; later ones = returning.
		var hs [][]byte
		for _, h := range hashes {
			if h != nil {
				hs = append(hs, h)
			}
		}
		if len(hs) > 0 {
			rows, err = tx.Query(ctx, `UPDATE analytics_orders a SET customer_seq = s.rn FROM (
					SELECT order_id, row_number() OVER (PARTITION BY customer_hash ORDER BY paid_at, number) AS rn
					FROM analytics_orders WHERE tenant_id = $1 AND paid_at IS NOT NULL AND customer_hash = ANY($2)) s
				WHERE a.tenant_id = $1 AND a.order_id = s.order_id AND a.customer_seq <> s.rn RETURNING a.paid_at`, tenant, hs)
			if err != nil {
				return err
			}
			for rows.Next() {
				var paid *time.Time
				if err := rows.Scan(&paid); err != nil {
					rows.Close()
					return err
				}
				note(paid)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		if !rollup {
			return nil
		}
		hours, days := spansOf(dirty)
		return rollupSales(ctx, tx, tenant, hours, days)
	})
}

const salesCols = `orders, units, gross, discounts, shipping, tax, refunds, refund_count, new_customers, returning_customers`

// salesSource yields the sales and the refunds inside [$2, $3), bucketed by
// the expression in %BUCKET% over column "at".
const salesSource = `SELECT %BUCKET% AS bucket, channel, currency,
		sum(orders)::bigint, sum(units)::bigint, sum(gross)::bigint, sum(discounts)::bigint, sum(shipping)::bigint, sum(tax)::bigint,
		sum(refunds)::bigint, sum(refund_count)::bigint, sum(newc)::bigint, sum(retc)::bigint
	FROM (
		SELECT paid_at AS at, channel, currency, 1 AS orders, units, gross, discounts, shipping, tax, 0::bigint AS refunds, 0 AS refund_count,
			(customer_seq = 1)::int AS newc, (customer_seq > 1)::int AS retc
		FROM analytics_orders WHERE tenant_id = $1 AND paid_at >= $2 AND paid_at < $3
		UNION ALL
		SELECT refunded_at, channel, currency, 0, 0, 0, 0, 0, 0, refunded, 1, 0, 0
		FROM analytics_orders WHERE tenant_id = $1 AND refunded_at >= $2 AND refunded_at < $3 AND refunded > 0
	) x GROUP BY 1, 2, 3`

// rollupSales recomputes the sales buckets of the given intervals from facts.
func rollupSales(ctx context.Context, tx pgx.Tx, tenant string, hours, days []span) error {
	for _, h := range hours {
		if _, err := tx.Exec(ctx, `DELETE FROM analytics_sales_hourly WHERE tenant_id = $1 AND hour >= $2 AND hour < $3`, tenant, h.from, h.to); err != nil {
			return err
		}
		q := `INSERT INTO analytics_sales_hourly (tenant_id, hour, channel, currency, ` + salesCols + `) SELECT $1, y.* FROM (` +
			strings.Replace(salesSource, "%BUCKET%", `date_trunc('hour', at, 'UTC')`, 1) + `) y`
		if _, err := tx.Exec(ctx, q, tenant, h.from, h.to); err != nil {
			return err
		}
	}
	for _, d := range days {
		fromDay, toDay := Day(d.from), Day(d.to) // toDay exclusive
		for _, t := range []string{"analytics_sales_daily", "analytics_sku_daily", "analytics_promo_daily"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE tenant_id = $1 AND day >= $2::date AND day < $3::date`, tenant, fromDay, toDay); err != nil {
				return err
			}
		}
		q := `INSERT INTO analytics_sales_daily (tenant_id, day, channel, currency, ` + salesCols + `) SELECT $1, y.* FROM (` +
			strings.Replace(salesSource, "%BUCKET%", `(at AT TIME ZONE '`+ZoneName+`')::date`, 1) + `) y`
		if _, err := tx.Exec(ctx, q, tenant, d.from, d.to); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO analytics_sku_daily (tenant_id, day, channel, currency, sku, title, orders, units, revenue)
			SELECT $1, (a.paid_at AT TIME ZONE '`+ZoneName+`')::date, a.channel, a.currency, l.sku, max(l.title),
				count(DISTINCT a.order_id), sum(l.qty)::bigint, sum(l.total)::bigint
			FROM analytics_orders a JOIN order_lines l ON l.tenant_id = a.tenant_id AND l.order_id = a.order_id
			WHERE a.tenant_id = $1 AND a.paid_at >= $2 AND a.paid_at < $3 GROUP BY 2, 3, 4, 5`, tenant, d.from, d.to); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO analytics_promo_daily (tenant_id, day, channel, currency, promotion_id, name, orders, discount, revenue)
			SELECT $1, (a.paid_at AT TIME ZONE '`+ZoneName+`')::date, a.channel, a.currency, p.promotion_id, max(p.name),
				count(*), sum(p.amount)::bigint, sum(a.total)::bigint
			FROM analytics_orders a JOIN analytics_order_promos p ON p.tenant_id = a.tenant_id AND p.order_id = a.order_id
			WHERE a.tenant_id = $1 AND a.paid_at >= $2 AND a.paid_at < $3 GROUP BY 2, 3, 4, 5`, tenant, d.from, d.to); err != nil {
			return err
		}
	}
	return nil
}

// Rebuild recomputes every fact and rollup of a tenant from the source
// tables. It is the repair tool and the backfill for a store that predates
// analytics; the periodic sync never needs it.
func (s *Service) Rebuild(ctx context.Context, tenant string) (int, error) {
	total := 0
	var after int64 = -1
	for {
		var ids []string
		err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id, number FROM orders WHERE tenant_id = $1 AND number > $2 ORDER BY number LIMIT $3`, tenant, after, syncBatch)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id, &after); err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return rows.Err()
		})
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			break
		}
		if err := s.syncTenantOrders(ctx, tenant, ids, false); err != nil {
			return total, err
		}
		total += len(ids)
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var lo, hi *time.Time
		if err := tx.QueryRow(ctx, `SELECT min(LEAST(paid_at, refunded_at)), max(GREATEST(paid_at, refunded_at)) FROM analytics_orders WHERE tenant_id = $1`, tenant).Scan(&lo, &hi); err != nil {
			return err
		}
		for _, t := range []string{"analytics_sales_hourly", "analytics_sales_daily", "analytics_sku_daily", "analytics_promo_daily"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE tenant_id = $1`, tenant); err != nil {
				return err
			}
		}
		if lo == nil {
			return nil
		}
		return rollupSales(ctx, tx, tenant,
			[]span{{lo.UTC().Truncate(time.Hour), hi.UTC().Truncate(time.Hour).Add(time.Hour)}},
			[]span{{dayStart(*lo), dayStart(*hi).AddDate(0, 0, 1)}})
	})
	if err != nil {
		return total, err
	}
	return total, s.rollupTrafficTenant(ctx, tenant, s.now().Add(-Retention-48*time.Hour), true)
}

// RollupTraffic recomputes the traffic buckets that received events since the
// last run (with a two-minute overlap) for every store.
func (s *Service) RollupTraffic(ctx context.Context) (int, error) {
	if _, err := s.DB.System.Exec(ctx, `INSERT INTO analytics_state (name, at) VALUES ('traffic', 'epoch') ON CONFLICT DO NOTHING`); err != nil {
		return 0, err
	}
	tx, err := s.DB.System.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var mark time.Time
	err = tx.QueryRow(ctx, `SELECT at FROM analytics_state WHERE name='traffic' FOR UPDATE SKIP LOCKED`).Scan(&mark)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	start := s.now()
	since := mark.Add(-2 * time.Minute)
	if floor := start.Add(-Retention); since.Before(floor) {
		since = floor
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT tenant_id FROM analytics_events WHERE at >= $1`, since)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	sort.Strings(tenants)
	for _, t := range tenants {
		if err := s.rollupTrafficTenant(ctx, t, since, false); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE analytics_state SET at=$1 WHERE name='traffic'`, start); err != nil {
		return 0, err
	}
	return len(tenants), tx.Commit(ctx)
}

const pageTypes = `('page_view','product_view','checkout_start')`

const localDay = `(at AT TIME ZONE '` + ZoneName + `')::date`

// sessionsCTE is one row per session (visitor and store day) in [$2, $3).
const sessionsCTE = `WITH s AS (
	SELECT ` + localDay + ` AS day, visitor, min(channel) AS channel, min(device) AS device,
		bool_or(type = 'product_view') AS pv, bool_or(type = 'add_to_cart') AS ac,
		bool_or(type = 'checkout_start') AS co, bool_or(type = 'purchase') AS pu,
		count(*) FILTER (WHERE type IN ` + pageTypes + `) AS views,
		COALESCE((array_agg(referrer ORDER BY id) FILTER (WHERE referrer <> ''))[1], '') AS referrer
	FROM analytics_events WHERE tenant_id = $1 AND at >= $2 AND at < $3 GROUP BY 1, 2)`

// rollupTrafficTenant recomputes the hours and store days from since on.
func (s *Service) rollupTrafficTenant(ctx context.Context, tenant string, since time.Time, wipe bool) error {
	hourFrom := since.UTC().Truncate(time.Hour)
	dayFrom := dayStart(since)
	end := s.now().Add(48 * time.Hour)
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if wipe {
			for _, t := range []string{"analytics_traffic_hourly", "analytics_funnel_daily", "analytics_product_traffic_daily", "analytics_referrer_daily"} {
				if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE tenant_id = $1`, tenant); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM analytics_traffic_hourly WHERE tenant_id = $1 AND hour >= $2`, tenant, hourFrom); err != nil {
			return err
		}
		for _, t := range []string{"analytics_funnel_daily", "analytics_referrer_daily", "analytics_product_traffic_daily"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+t+` WHERE tenant_id = $1 AND day >= $2::date`, tenant, Day(dayFrom)); err != nil {
				return err
			}
		}
		stmts := []struct {
			q    string
			from time.Time
		}{
			{`INSERT INTO analytics_traffic_hourly (tenant_id, hour, channel, sessions, page_views, product_views, add_to_cart, checkout_starts, purchases)
				SELECT $1, date_trunc('hour', at, 'UTC'), channel, count(DISTINCT visitor),
					count(*) FILTER (WHERE type IN ` + pageTypes + `), count(*) FILTER (WHERE type = 'product_view'),
					count(*) FILTER (WHERE type = 'add_to_cart'), count(*) FILTER (WHERE type = 'checkout_start'),
					count(DISTINCT order_id) FILTER (WHERE type = 'purchase')
				FROM analytics_events WHERE tenant_id = $1 AND at >= $2 AND at < $3 GROUP BY 2, 3`, hourFrom},
			{sessionsCTE + ` INSERT INTO analytics_funnel_daily (tenant_id, day, channel, device, sessions, s_product, s_cart, s_checkout, s_purchase, page_views)
				SELECT $1, day, channel, device, count(*), count(*) FILTER (WHERE pv OR ac OR co OR pu), count(*) FILTER (WHERE ac OR co OR pu),
					count(*) FILTER (WHERE co OR pu), count(*) FILTER (WHERE pu), COALESCE(sum(views), 0)::bigint
				FROM s GROUP BY 2, 3, 4`, dayFrom},
			{sessionsCTE + ` INSERT INTO analytics_referrer_daily (tenant_id, day, referrer, sessions, purchases)
				SELECT $1, day, referrer, count(*), count(*) FILTER (WHERE pu) FROM s GROUP BY 2, 3`, dayFrom},
			{`INSERT INTO analytics_product_traffic_daily (tenant_id, day, product_id, views, visitors, add_to_cart, cart_visitors)
				SELECT $1, ` + localDay + `, product_id,
					count(*) FILTER (WHERE type = 'product_view'), count(DISTINCT visitor) FILTER (WHERE type = 'product_view'),
					count(*) FILTER (WHERE type = 'add_to_cart'), count(DISTINCT visitor) FILTER (WHERE type = 'add_to_cart')
				FROM analytics_events WHERE tenant_id = $1 AND at >= $2 AND at < $3 AND product_id <> '' GROUP BY 2, 3`, dayFrom},
		}
		for _, st := range stmts {
			if _, err := tx.Exec(ctx, st.q, tenant, st.from, end); err != nil {
				return err
			}
		}
		return nil
	})
}
