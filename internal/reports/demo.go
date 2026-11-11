package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// DemoOptions sizes a synthetic history.
type DemoOptions struct {
	Orders   int   // orders to create
	Days     int   // spread over the last N days
	Sessions int   // storefront sessions per order (0 = no traffic)
	Seed     int64 // deterministic output for tests
}

// SeedDemo writes a synthetic sales and traffic history for demos, QA and
// load tests: real rows in orders, order_lines, order_events and payments
// (customers encrypted like real ones) plus raw storefront events. It needs a
// catalog with prices. Never run it on a store with real sales.
func (s *Service) SeedDemo(ctx context.Context, tenant, actor string, o DemoOptions) error {
	if o.Orders <= 0 || o.Days <= 0 {
		return errors.New("reports: demo needs orders and days")
	}
	rng := rand.New(rand.NewSource(o.Seed))
	now := s.now()
	type item struct {
		sku, title, product string
		price               int64
	}
	var items []item
	var channel, currency string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT channel, currency::text FROM prices WHERE tenant_id=$1 GROUP BY 1, 2 ORDER BY count(*) DESC LIMIT 1`, tenant).Scan(&channel, &currency); err != nil {
			return fmt.Errorf("reports: the store has no prices to build a demo from: %w", err)
		}
		rows, err := tx.Query(ctx, `SELECT v.sku, p.title || CASE WHEN v.title <> '' THEN ' · ' || v.title ELSE '' END, p.id, pr.amount
			FROM variants v JOIN products p ON p.tenant_id=v.tenant_id AND p.id=v.product_id
			JOIN prices pr ON pr.tenant_id=v.tenant_id AND pr.sku=v.sku AND pr.channel=$2 AND pr.min_qty=1
			WHERE v.tenant_id=$1 ORDER BY v.sku LIMIT 400`, tenant, channel)
		if err != nil {
			return err
		}
		items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
			var i item
			return i, r.Scan(&i.sku, &i.title, &i.product, &i.price)
		})
		return err
	})
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New("reports: the store has no priced products to build a demo from")
	}
	// A few products sell much more than the rest.
	pick := func() item { return items[int(math.Pow(rng.Float64(), 2.2)*float64(len(items)))] }
	customers := max(o.Orders*45/100, 1)
	// Hour-of-day and weekday weights (store time).
	hourW := []float64{1, .6, .4, .3, .3, .5, 1, 2, 3, 4, 5, 6, 6.5, 6, 5, 5, 5.5, 6, 7, 8, 8.5, 7, 4, 2}
	dayW := []float64{1.25, .9, .95, 1, 1.05, 1.2, 1.4} // Sunday first
	var hourSum float64
	for _, w := range hourW {
		hourSum += w
	}
	randomTime := func() time.Time {
		for {
			back := int(math.Pow(rng.Float64(), 1.35) * float64(o.Days)) // more orders recently: a growing store
			d := dayStart(now).AddDate(0, 0, -back)
			if rng.Float64() > dayW[int(d.Weekday())]/1.4 {
				continue
			}
			x, h := rng.Float64()*hourSum, 0
			for ; h < 23 && x > hourW[h]; h++ {
				x -= hourW[h]
			}
			t := d.Add(time.Duration(h)*time.Hour + time.Duration(rng.Intn(3600))*time.Second)
			if t.Before(now.Add(-2 * time.Minute)) {
				return t
			}
		}
	}

	const chunk = 5000
	for done := 0; done < o.Orders; done += chunk {
		n := min(chunk, o.Orders-done)
		var orders, lines, events, pays, raw [][]any
		err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT nextval('order_numbers') FROM generate_series(1, $1)`, n)
			if err != nil {
				return err
			}
			numbers, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return err
			}
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("ord_demo%x%06d", uint32(o.Seed), numbers[i])
				at := randomTime()
				cust, _ := json.Marshal(map[string]string{
					"email": fmt.Sprintf("cliente%d@demo.apexx.test", int(math.Pow(rng.Float64(), 1.6)*float64(customers))), "name": "Cliente de demostración"})
				enc, err := s.Keys.Encrypt(tenant, cust, id)
				if err != nil {
					return err
				}
				var gross, units int64
				nl := 1 + rng.Intn(3)
				seen := map[string]bool{}
				for l := 1; l <= nl; l++ {
					it := pick()
					if seen[it.sku] {
						continue
					}
					seen[it.sku] = true
					q := int64(1 + rng.Intn(3)/2)
					lines = append(lines, []any{tenant, id, l, it.sku, it.title, q, it.price, q * it.price})
					gross += q * it.price
					units += q
				}
				var disc int64
				promos := []any{}
				if gross >= 300000 {
					disc = gross / 10
					promos = append(promos, map[string]any{"id": "promo-10-3000", "name": "10% en compras desde $3,000", "applied": true,
						"discount": map[string]any{"amount": disc, "currency": currency}})
				} else if rng.Float64() < 0.08 {
					disc = gross * 15 / 100
					promos = append(promos, map[string]any{"id": "bienvenida-15", "name": "15% de bienvenida", "applied": true,
						"discount": map[string]any{"amount": disc, "currency": currency}})
				}
				ship := int64(9900)
				if gross-disc >= 150000 {
					ship = 0
				}
				grand := gross - disc + ship
				tax := grand * 16 / 116
				status, version := "paid", int64(2)
				created, _ := json.Marshal(map[string]any{"promotions": promos})
				events = append(events, []any{tenant, id, 1, at, actor, "order.created", nil, "pending_payment", string(created)})
				paidAt := at.Add(time.Duration(2+rng.Intn(20)) * time.Second)
				updated := paidAt
				switch r := rng.Float64(); {
				case r < 0.03: // card declined
					status = "cancelled"
					events = append(events, []any{tenant, id, 2, paidAt, actor, "payment.failed", "pending_payment", "cancelled", `{"reason":"demo"}`})
					pays = append(pays, []any{tenant, "pay_" + id[4:], id, "sandbox", "demo", "failed", grand, int64(0), currency, paidAt, paidAt})
				default:
					events = append(events, []any{tenant, id, 2, paidAt, actor, "payment.captured", "pending_payment", "paid", `{"ref":"demo"}`})
					refundAt := paidAt.Add(time.Duration(6+rng.Intn(120)) * time.Hour)
					shipAt := paidAt.Add(time.Duration(4+rng.Intn(40)) * time.Hour)
					switch {
					case r < 0.09 && refundAt.Before(now):
						status, version, updated = "refunded", 3, refundAt
						events = append(events, []any{tenant, id, 3, refundAt, actor, "order.refunded", "paid", "refunded", fmt.Sprintf(`{"amount":%d,"reason":"demo"}`, grand)})
						pays = append(pays, []any{tenant, "pay_" + id[4:], id, "sandbox", "demo", "refunded", grand, grand, currency, paidAt, refundAt})
					case shipAt.Before(now):
						status, version, updated = "fulfilled", 3, shipAt
						events = append(events, []any{tenant, id, 3, shipAt, actor, "order.fulfilled", "paid", "fulfilled", `{}`})
						fallthrough
					default:
						pays = append(pays, []any{tenant, "pay_" + id[4:], id, "sandbox", "demo", "captured", grand, int64(0), currency, paidAt, paidAt})
					}
				}
				orders = append(orders, []any{tenant, id, numbers[i], channel, status, currency, gross, disc, ship, tax, grand, enc, "06600", version, at, updated})

				// Storefront sessions around this order: one buys, the rest drop along the funnel.
				if o.Sessions > 0 && at.After(now.Add(-Retention+time.Hour)) {
					for sn := 0; sn < o.Sessions; sn++ {
						v := make([]byte, 16)
						rng.Read(v)
						t0 := at.Add(-time.Duration(rng.Intn(1500)) * time.Second)
						if sn > 0 {
							t0 = at.Add(time.Duration(rng.Intn(7200)-3600) * time.Second)
							if !t0.Before(now) {
								t0 = now.Add(-time.Minute)
							}
						}
						dev := []string{"mobile", "mobile", "mobile", "desktop", "desktop", "tablet"}[rng.Intn(6)]
						ref := []string{"", "", "", "www.google.com", "www.google.com", "instagram.com", "facebook.com", "tiktok.com", "correo.demo.test"}[rng.Intn(9)]
						it := pick()
						ev := func(dt int, typ, path, product, sku string, qty int, value int64, order string) {
							raw = append(raw, []any{tenant, t0.Add(time.Duration(dt) * time.Second), typ, v, channel, "main", path, product, sku, qty, value, order, ref, dev})
						}
						depth := rng.Float64()
						if sn == 0 && status != "cancelled" {
							depth = 0 // the buyer goes all the way
						}
						ev(0, EvPageView, "/s/"+tenant+"/main/index", "", "", 0, 0, "")
						if depth < 0.62 {
							ev(20, EvProductView, "/s/"+tenant+"/main/p/x", it.product, "", 0, 0, "")
						}
						if depth < 0.2 {
							ev(45, EvAddToCart, "", it.product, it.sku, 1, 0, "")
							ev(50, EvPageView, "/s/"+tenant+"/main/cart", "", "", 0, 0, "")
						}
						if depth < 0.1 {
							ev(70, EvCheckoutStart, "/s/"+tenant+"/main/checkout", "", "", 0, 0, "")
						}
						if depth == 0 {
							ev(120, EvPurchase, "", "", "", 0, grand, id)
						}
					}
				}
			}
			for _, c := range []struct {
				table string
				cols  []string
				rows  [][]any
			}{
				{"orders", []string{"tenant_id", "id", "number", "channel", "status", "currency", "items_total", "discounts", "shipping", "tax_included", "grand_total", "customer", "postal_code", "version", "created_at", "updated_at"}, orders},
				{"order_lines", []string{"tenant_id", "order_id", "line", "sku", "title", "qty", "unit", "total"}, lines},
				{"order_events", []string{"tenant_id", "order_id", "seq", "at", "actor", "type", "from_status", "to_status", "data"}, events},
				{"payments", []string{"tenant_id", "id", "order_id", "provider", "provider_ref", "status", "amount", "refunded", "currency", "created_at", "updated_at"}, pays},
				{"analytics_events", []string{"tenant_id", "at", "type", "visitor", "channel", "site", "path", "product_id", "sku", "qty", "value", "order_id", "referrer", "device"}, raw},
			} {
				if len(c.rows) == 0 {
					continue
				}
				if err := insertRows(ctx, tx, c.table, c.cols, c.rows); err != nil {
					return fmt.Errorf("%s: %w", c.table, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return store.Audit(ctx, tx, tenant, actor, "analytics.demo_seeded", tenant, map[string]any{"orders": o.Orders, "days": o.Days})
	})
}
