package reports

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Filter narrows a report.
type Filter struct {
	Range   Range
	Channel string // "" = every channel
}

// Sales are the money and volume figures of a period or a bucket.
//
//	Net = Gross − Discounts + Shipping − Refunds   (what the store kept)
//	AOV = (Gross − Discounts + Shipping) / Orders
//
// Tax is the VAT already included in those amounts.
type Sales struct {
	Orders             int64 `json:"orders"`
	Units              int64 `json:"units"`
	Gross              int64 `json:"gross"`
	Discounts          int64 `json:"discounts"`
	Shipping           int64 `json:"shipping"`
	Tax                int64 `json:"tax"`
	Refunds            int64 `json:"refunds"`
	RefundCount        int64 `json:"refundCount"`
	NewCustomers       int64 `json:"newCustomers"`
	ReturningCustomers int64 `json:"returningCustomers"`
	Net                int64 `json:"net"`
	AOV                int64 `json:"aov"`
}

func (s *Sales) add(o Sales) {
	s.Orders += o.Orders
	s.Units += o.Units
	s.Gross += o.Gross
	s.Discounts += o.Discounts
	s.Shipping += o.Shipping
	s.Tax += o.Tax
	s.Refunds += o.Refunds
	s.RefundCount += o.RefundCount
	s.NewCustomers += o.NewCustomers
	s.ReturningCustomers += o.ReturningCustomers
	s.finish()
}

func (s *Sales) finish() {
	s.Net = s.Gross - s.Discounts + s.Shipping - s.Refunds
	s.AOV = 0
	if s.Orders > 0 {
		s.AOV = (s.Gross - s.Discounts + s.Shipping) / s.Orders
	}
}

// Traffic are the funnel figures of a period or a bucket. Each step counts
// sessions that reached at least that step.
type Traffic struct {
	Sessions  int64 `json:"sessions"`
	Product   int64 `json:"productViews"`
	Cart      int64 `json:"addToCart"`
	Checkout  int64 `json:"checkout"`
	Purchase  int64 `json:"purchase"`
	PageViews int64 `json:"pageViews"`
}

func (t *Traffic) add(o Traffic) {
	t.Sessions += o.Sessions
	t.Product += o.Product
	t.Cart += o.Cart
	t.Checkout += o.Checkout
	t.Purchase += o.Purchase
	t.PageViews += o.PageViews
}

// ConversionBps is purchases per 10,000 sessions.
func (t Traffic) ConversionBps() int64 {
	if t.Sessions == 0 {
		return 0
	}
	return t.Purchase * 10000 / t.Sessions
}

// Point is one bucket of a time series: a day "2006-01-02", an hour
// "2006-01-02T15", a week (its Monday) or a month "2006-01".
type Point struct {
	Bucket string `json:"bucket"`
	Sales
	Traffic Traffic `json:"traffic"`
}

const salesSums = `COALESCE(sum(orders),0)::bigint, COALESCE(sum(units),0)::bigint, COALESCE(sum(gross),0)::bigint, COALESCE(sum(discounts),0)::bigint,
	COALESCE(sum(shipping),0)::bigint, COALESCE(sum(tax),0)::bigint, COALESCE(sum(refunds),0)::bigint, COALESCE(sum(refund_count),0)::bigint,
	COALESCE(sum(new_customers),0)::bigint, COALESCE(sum(returning_customers),0)::bigint`

func scanSales(row pgx.Row, lead ...any) (Sales, error) {
	var s Sales
	dst := append(lead, &s.Orders, &s.Units, &s.Gross, &s.Discounts, &s.Shipping, &s.Tax, &s.Refunds, &s.RefundCount, &s.NewCustomers, &s.ReturningCustomers)
	err := row.Scan(dst...)
	s.finish()
	return s, err
}

// currencies lists the currencies with sales in the range, biggest first.
func currencies(ctx context.Context, tx pgx.Tx, tenant string, f Filter) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT currency::text FROM analytics_sales_daily WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date
		AND ($4 = '' OR channel = $4) GROUP BY currency ORDER BY sum(gross) DESC, currency`, tenant, f.Range.FromDay(), f.Range.ToDay(), f.Channel)
	if err != nil {
		return nil, err
	}
	cs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		var c string
		err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT currency::text FROM analytics_sales_daily WHERE tenant_id=$1 ORDER BY day DESC LIMIT 1),
			(SELECT config->>'currency' FROM channels WHERE tenant_id=$1 ORDER BY id LIMIT 1), 'MXN')`, tenant).Scan(&c)
		if err != nil {
			return nil, err
		}
		if len(c) != 3 {
			c = "MXN"
		}
		cs = []string{c}
	}
	return cs, nil
}

func channelIDs(ctx context.Context, tx pgx.Tx, tenant string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM channels WHERE tenant_id=$1 UNION SELECT DISTINCT channel FROM analytics_sales_daily WHERE tenant_id=$1 ORDER BY 1`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// salesByDay returns the sales of every day with activity in the range.
func salesByDay(ctx context.Context, tx pgx.Tx, tenant string, f Filter, cur string) (map[string]Sales, error) {
	rows, err := tx.Query(ctx, `SELECT day::text, `+salesSums+` FROM analytics_sales_daily
		WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY day`,
		tenant, f.Range.FromDay(), f.Range.ToDay(), cur, f.Channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Sales{}
	for rows.Next() {
		var d string
		s, err := scanSales(rows, &d)
		if err != nil {
			return nil, err
		}
		out[d] = s
	}
	return out, rows.Err()
}

const hourKey = "2006-01-02T15"

// salesByHour returns the sales of every hour with activity, keyed by store-local hour.
func salesByHour(ctx context.Context, tx pgx.Tx, tenant string, f Filter, cur string) (map[string]Sales, error) {
	rows, err := tx.Query(ctx, `SELECT hour, `+salesSums+` FROM analytics_sales_hourly
		WHERE tenant_id=$1 AND hour >= $2 AND hour < $3 AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY hour`,
		tenant, f.Range.From, f.Range.To, cur, f.Channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Sales{}
	for rows.Next() {
		var h time.Time
		s, err := scanSales(rows, &h)
		if err != nil {
			return nil, err
		}
		k := h.In(Zone).Format(hourKey)
		prev := out[k]
		prev.add(s)
		out[k] = prev
	}
	return out, rows.Err()
}

func trafficByDay(ctx context.Context, tx pgx.Tx, tenant string, f Filter) (map[string]Traffic, error) {
	rows, err := tx.Query(ctx, `SELECT day::text, sum(sessions)::bigint, sum(s_product)::bigint, sum(s_cart)::bigint, sum(s_checkout)::bigint,
			sum(s_purchase)::bigint, sum(page_views)::bigint FROM analytics_funnel_daily
		WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND ($4 = '' OR channel = $4) GROUP BY day`,
		tenant, f.Range.FromDay(), f.Range.ToDay(), f.Channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Traffic{}
	for rows.Next() {
		var d string
		var t Traffic
		if err := rows.Scan(&d, &t.Sessions, &t.Product, &t.Cart, &t.Checkout, &t.Purchase, &t.PageViews); err != nil {
			return nil, err
		}
		out[d] = t
	}
	return out, rows.Err()
}

// trafficByHour gives hourly activity (not unique sessions per step: an hour
// only knows distinct visitors and event counts).
func trafficByHour(ctx context.Context, tx pgx.Tx, tenant string, f Filter) (map[string]Traffic, error) {
	rows, err := tx.Query(ctx, `SELECT hour, sum(sessions)::bigint, sum(product_views)::bigint, sum(add_to_cart)::bigint, sum(checkout_starts)::bigint,
			sum(purchases)::bigint, sum(page_views)::bigint FROM analytics_traffic_hourly
		WHERE tenant_id=$1 AND hour >= $2 AND hour < $3 AND ($4 = '' OR channel = $4) GROUP BY hour`, tenant, f.Range.From, f.Range.To, f.Channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Traffic{}
	for rows.Next() {
		var h time.Time
		var t Traffic
		if err := rows.Scan(&h, &t.Sessions, &t.Product, &t.Cart, &t.Checkout, &t.Purchase, &t.PageViews); err != nil {
			return nil, err
		}
		k := h.In(Zone).Format(hourKey)
		prev := out[k]
		prev.add(t)
		out[k] = prev
	}
	return out, rows.Err()
}

// Grains of a series.
const (
	GrainHour  = "hour"
	GrainDay   = "day"
	GrainWeek  = "week"
	GrainMonth = "month"
)

// DefaultGrain picks a readable grain for a range.
func DefaultGrain(r Range) string {
	switch n := r.Days(); {
	case n <= 2:
		return GrainHour
	case n <= 92:
		return GrainDay
	case n <= 370:
		return GrainWeek
	}
	return GrainMonth
}

func bucketOfDay(day, grain string) string {
	switch grain {
	case GrainWeek:
		t, _ := time.ParseInLocation("2006-01-02", day, Zone)
		wd := (int(t.Weekday()) + 6) % 7 // Monday = 0
		return t.AddDate(0, 0, -wd).Format("2006-01-02")
	case GrainMonth:
		return day[:7]
	}
	return day
}

// series builds a gap-free series for the range at the given grain, plus totals.
func series(ctx context.Context, tx pgx.Tx, tenant string, f Filter, cur, grain string) ([]Point, Sales, Traffic, error) {
	var pts []Point
	var tot Sales
	var tr Traffic
	days, err := salesByDay(ctx, tx, tenant, f, cur)
	if err != nil {
		return nil, tot, tr, err
	}
	tdays, err := trafficByDay(ctx, tx, tenant, f)
	if err != nil {
		return nil, tot, tr, err
	}
	for _, s := range days {
		tot.add(s)
	}
	for _, t := range tdays {
		tr.add(t)
	}
	if grain == GrainHour {
		hs, err := salesByHour(ctx, tx, tenant, f, cur)
		if err != nil {
			return nil, tot, tr, err
		}
		ths, err := trafficByHour(ctx, tx, tenant, f)
		if err != nil {
			return nil, tot, tr, err
		}
		seen := map[string]bool{}
		for h := f.Range.From; h.Before(f.Range.To); h = h.Add(time.Hour) {
			k := h.In(Zone).Format(hourKey)
			if seen[k] {
				continue // the repeated hour of a clock change
			}
			seen[k] = true
			pts = append(pts, Point{Bucket: k, Sales: hs[k], Traffic: ths[k]})
		}
		return pts, tot, tr, nil
	}
	idx := map[string]int{}
	for _, d := range f.Range.DayList() {
		b := bucketOfDay(d, grain)
		i, ok := idx[b]
		if !ok {
			i = len(pts)
			idx[b] = i
			pts = append(pts, Point{Bucket: b})
		}
		pts[i].Sales.add(days[d])
		pts[i].Traffic.add(tdays[d])
	}
	return pts, tot, tr, nil
}

// ---------- dashboard ----------

// ProductRow is one line of a product ranking.
type ProductRow struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	ProductID    string `json:"productId,omitempty"`
	Brand        string `json:"brand,omitempty"`
	Orders       int64  `json:"orders"`
	Units        int64  `json:"units"`
	Revenue      int64  `json:"revenue"`
	Views        int64  `json:"views"`
	Visitors     int64  `json:"visitors"`
	CartVisitors int64  `json:"cartVisitors"`
}

// RecentOrder is an order summary without personal data.
type RecentOrder struct {
	ID        string    `json:"id"`
	Number    int64     `json:"number"`
	Status    string    `json:"status"`
	Channel   string    `json:"channel"`
	Currency  string    `json:"currency"`
	Total     int64     `json:"total"`
	Units     int64     `json:"units"`
	CreatedAt time.Time `json:"createdAt"`
}

// Alert is something the merchant should look at.
type Alert struct {
	ID       string `json:"id"`
	Severity string `json:"severity"` // warn | bad
	Count    int64  `json:"count"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

// Dashboard feeds the admin home.
type Dashboard struct {
	Range        RangeInfo     `json:"range"`
	Previous     RangeInfo     `json:"previous"`
	Grain        string        `json:"grain"`
	Currency     string        `json:"currency"`
	Currencies   []string      `json:"currencies"`
	Channels     []string      `json:"channels"`
	Totals       Sales         `json:"totals"`
	PrevTotals   Sales         `json:"previousTotals"`
	Traffic      Traffic       `json:"traffic"`
	PrevTraffic  Traffic       `json:"previousTraffic"`
	Series       []Point       `json:"series"`
	PrevSeries   []Point       `json:"previousSeries"`
	TopProducts  []ProductRow  `json:"topProducts"`
	RecentOrders []RecentOrder `json:"recentOrders"`
	Alerts       []Alert       `json:"alerts"`
	SyncedAt     *time.Time    `json:"syncedAt"`
}

// LowStockThreshold is the default "low stock" level (available units).
const LowStockThreshold = 5

// Dashboard computes the admin home for a period. Everything money-related
// comes from rollups, so the cost does not grow with the number of orders.
func (s *Service) Dashboard(ctx context.Context, tenant string, f Filter) (Dashboard, error) {
	d := Dashboard{Range: f.Range.Info(), Previous: f.Range.Previous().Info(), Grain: GrainDay, Alerts: []Alert{}}
	if f.Range.Days() <= 2 {
		d.Grain = GrainHour
	}
	prev := Filter{Range: f.Range.Previous(), Channel: f.Channel}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if d.Currencies, err = currencies(ctx, tx, tenant, f); err != nil {
			return err
		}
		d.Currency = d.Currencies[0]
		if d.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if d.Series, d.Totals, d.Traffic, err = series(ctx, tx, tenant, f, d.Currency, d.Grain); err != nil {
			return err
		}
		if d.PrevSeries, d.PrevTotals, d.PrevTraffic, err = series(ctx, tx, tenant, prev, d.Currency, d.Grain); err != nil {
			return err
		}
		if d.TopProducts, err = productRows(ctx, tx, tenant, f, d.Currency, "product", 6, false); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT o.id, o.number, o.status, o.channel, o.currency::text, o.grand_total,
				COALESCE((SELECT sum(qty) FROM order_lines l WHERE l.tenant_id=o.tenant_id AND l.order_id=o.id), 0)::bigint, o.created_at
			FROM orders o WHERE o.tenant_id=$1 AND ($2 = '' OR o.channel = $2) ORDER BY o.number DESC LIMIT 8`, tenant, f.Channel)
		if err != nil {
			return err
		}
		d.RecentOrders, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RecentOrder, error) {
			var o RecentOrder
			return o, r.Scan(&o.ID, &o.Number, &o.Status, &o.Channel, &o.Currency, &o.Total, &o.Units, &o.CreatedAt)
		})
		if err != nil {
			return err
		}
		c, err := opsCounts(ctx, tx, tenant, s.now())
		if err != nil {
			return err
		}
		d.Alerts = c.alerts()
		return nil
	})
	if err != nil {
		return d, err
	}
	var at time.Time
	if err := s.DB.System.QueryRow(ctx, `SELECT at FROM analytics_state WHERE name='orders'`).Scan(&at); err == nil && at.Year() > 2000 {
		d.SyncedAt = &at
	}
	if d.TopProducts == nil {
		d.TopProducts = []ProductRow{}
	}
	return d, nil
}

// counts are the operational numbers behind alerts and the status page.
type counts struct {
	LowStock, OutOfStock                      int64
	WebhookDead, WebhookPending               int64
	WebhookDelivered, WebhookSubs             int64
	WebhookOldestPendingSec                   int64
	InvoicesPending, InvoicesFailed           int64
	PaymentsPending, PaymentsOldestPendingSec int64
}

func opsCounts(ctx context.Context, tx pgx.Tx, tenant string, now time.Time) (counts, error) {
	var c counts
	err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE avail > 0 AND avail <= $2), count(*) FILTER (WHERE avail <= 0) FROM (
			SELECT sum(on_hand - reserved) AS avail FROM stock_levels WHERE tenant_id=$1 GROUP BY sku) x`, tenant, LowStockThreshold).
		Scan(&c.LowStock, &c.OutOfStock)
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM webhook_deliveries WHERE tenant_id=$1 AND status='dead' AND created_at >= $2::timestamptz - interval '7 days'),
			(SELECT count(*) FROM webhook_deliveries WHERE tenant_id=$1 AND status='pending' AND attempts > 0),
			(SELECT count(*) FROM webhook_deliveries WHERE tenant_id=$1 AND status='delivered' AND created_at >= $2::timestamptz - interval '1 day'),
			(SELECT count(*) FROM webhook_subscriptions WHERE tenant_id=$1 AND active),
			COALESCE((SELECT extract(epoch FROM $2::timestamptz - min(created_at))::bigint FROM webhook_deliveries WHERE tenant_id=$1 AND status='pending'), 0),
			(SELECT count(*) FROM invoices WHERE tenant_id=$1 AND status='pending' AND updated_at < $2::timestamptz - interval '10 minutes'),
			(SELECT count(*) FROM invoices WHERE tenant_id=$1 AND status='failed'),
			(SELECT count(*) FROM orders WHERE tenant_id=$1 AND status='pending_payment' AND created_at < $2::timestamptz - interval '5 minutes'),
			COALESCE((SELECT extract(epoch FROM $2::timestamptz - min(created_at))::bigint FROM orders WHERE tenant_id=$1 AND status='pending_payment'), 0)`,
		tenant, now).Scan(&c.WebhookDead, &c.WebhookPending, &c.WebhookDelivered, &c.WebhookSubs, &c.WebhookOldestPendingSec,
		&c.InvoicesPending, &c.InvoicesFailed, &c.PaymentsPending, &c.PaymentsOldestPendingSec)
	return c, err
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatInt(n, 10) + " " + many
}

func (c counts) alerts() []Alert {
	out := []Alert{}
	if c.PaymentsPending > 0 {
		out = append(out, Alert{"payments_pending", "bad", c.PaymentsPending, plural(c.PaymentsPending, "pago sin conciliar", "pagos sin conciliar"),
			"Pedidos con pago pendiente desde hace más de 5 minutos. Se resuelven solos al confirmar con el procesador; revisa si siguen aquí."})
	}
	if c.WebhookDead > 0 {
		out = append(out, Alert{"webhooks_failed", "bad", c.WebhookDead, plural(c.WebhookDead, "aviso a conectores falló", "avisos a conectores fallaron"),
			"Entregas de webhooks que agotaron sus reintentos en los últimos 7 días. Puedes reenviarlas desde Conectores."})
	}
	if c.InvoicesFailed+c.InvoicesPending > 0 {
		n := c.InvoicesFailed + c.InvoicesPending
		out = append(out, Alert{"invoices_pending", "warn", n, plural(n, "factura pendiente", "facturas pendientes"),
			fmt.Sprintf("%d con error y %d en espera desde hace más de 10 minutos.", c.InvoicesFailed, c.InvoicesPending)})
	}
	if c.OutOfStock > 0 {
		out = append(out, Alert{"out_of_stock", "warn", c.OutOfStock, plural(c.OutOfStock, "SKU agotado", "SKU agotados"),
			"No se pueden vender hasta que registres existencias."})
	}
	if c.LowStock > 0 {
		out = append(out, Alert{"low_stock", "warn", c.LowStock, plural(c.LowStock, "SKU con pocas existencias", "SKU con pocas existencias"),
			fmt.Sprintf("Quedan %d unidades o menos disponibles.", LowStockThreshold)})
	}
	return out
}

// ---------- sales report ----------

// ChannelSales is the sales of one channel.
type ChannelSales struct {
	Channel string `json:"channel"`
	Sales
}

// Slot is the sales of an hour of the day (0–23) or a weekday (0 = Monday).
type Slot struct {
	Slot int `json:"slot"`
	Sales
}

// SalesReport is the "Ventas" report.
type SalesReport struct {
	Range      RangeInfo      `json:"range"`
	Previous   RangeInfo      `json:"previous"`
	Grain      string         `json:"grain"`
	Currency   string         `json:"currency"`
	Currencies []string       `json:"currencies"`
	Channels   []string       `json:"channels"`
	Totals     Sales          `json:"totals"`
	PrevTotals Sales          `json:"previousTotals"`
	Series     []Point        `json:"series"`
	PrevSeries []Point        `json:"previousSeries"`
	ByChannel  []ChannelSales `json:"byChannel"`
	ByHour     []Slot         `json:"byHour"`
	ByWeekday  []Slot         `json:"byWeekday"`
}

// SalesReport computes sales over time, by channel, hour of day and weekday.
func (s *Service) SalesReport(ctx context.Context, tenant string, f Filter, grain string) (SalesReport, error) {
	if grain == "" {
		grain = DefaultGrain(f.Range)
	}
	if grain == GrainHour && f.Range.Days() > 14 {
		grain = GrainDay
	}
	r := SalesReport{Range: f.Range.Info(), Previous: f.Range.Previous().Info(), Grain: grain}
	prev := Filter{Range: f.Range.Previous(), Channel: f.Channel}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if r.Currencies, err = currencies(ctx, tx, tenant, f); err != nil {
			return err
		}
		r.Currency = r.Currencies[0]
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if r.Series, r.Totals, _, err = series(ctx, tx, tenant, f, r.Currency, grain); err != nil {
			return err
		}
		if r.PrevSeries, r.PrevTotals, _, err = series(ctx, tx, tenant, prev, r.Currency, grain); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT channel, `+salesSums+` FROM analytics_sales_daily
			WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY channel ORDER BY sum(gross) DESC`,
			tenant, f.Range.FromDay(), f.Range.ToDay(), r.Currency, f.Channel)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c ChannelSales
			if c.Sales, err = scanSales(rows, &c.Channel); err != nil {
				rows.Close()
				return err
			}
			r.ByChannel = append(r.ByChannel, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		r.ByHour = make([]Slot, 24)
		for i := range r.ByHour {
			r.ByHour[i].Slot = i
		}
		rows, err = tx.Query(ctx, `SELECT extract(hour FROM hour AT TIME ZONE '`+ZoneName+`')::int, `+salesSums+` FROM analytics_sales_hourly
			WHERE tenant_id=$1 AND hour >= $2 AND hour < $3 AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY 1`,
			tenant, f.Range.From, f.Range.To, r.Currency, f.Channel)
		if err != nil {
			return err
		}
		for rows.Next() {
			var h int
			sl, err := scanSales(rows, &h)
			if err != nil {
				rows.Close()
				return err
			}
			if h >= 0 && h < 24 {
				r.ByHour[h].Sales = sl
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		r.ByWeekday = make([]Slot, 7)
		for i := range r.ByWeekday {
			r.ByWeekday[i].Slot = i
		}
		days, err := salesByDay(ctx, tx, tenant, f, r.Currency)
		if err != nil {
			return err
		}
		for d, v := range days {
			t, _ := time.ParseInLocation("2006-01-02", d, Zone)
			r.ByWeekday[(int(t.Weekday())+6)%7].Sales.add(v)
		}
		return nil
	})
	if r.ByChannel == nil {
		r.ByChannel = []ChannelSales{}
	}
	return r, err
}

// ---------- products report ----------

// ProductsReport is the "Productos" report.
type ProductsReport struct {
	Range    RangeInfo    `json:"range"`
	By       string       `json:"by"`
	Currency string       `json:"currency"`
	Channels []string     `json:"channels"`
	Rows     []ProductRow `json:"rows"`
	Totals   ProductRow   `json:"totals"`
}

// productRows ranks what was sold by sku, product, brand or category.
// withTraffic adds product page views (only meaningful by product).
func productRows(ctx context.Context, tx pgx.Tx, tenant string, f Filter, cur, by string, limit int, withTraffic bool) ([]ProductRow, error) {
	key, name, extra := `s.sku`, `s.title`, ``
	switch by {
	case "product":
		key, name = `COALESCE(p.id, 'sku:' || s.sku)`, `COALESCE(p.title, s.title)`
	case "brand":
		key, name = `COALESCE(NULLIF(p.brand, ''), 'Sin marca')`, `COALESCE(NULLIF(p.brand, ''), 'Sin marca')`
	case "category":
		key, name = `c.cat`, `c.cat`
		extra = `CROSS JOIN LATERAL unnest(CASE WHEN p.categories IS NULL OR cardinality(p.categories) = 0 THEN ARRAY['Sin categoría'] ELSE p.categories END) AS c(cat)`
	}
	rows, err := tx.Query(ctx, `WITH s AS (SELECT sku, max(title) AS title, sum(orders)::bigint AS orders, sum(units)::bigint AS units, sum(revenue)::bigint AS revenue
			FROM analytics_sku_daily WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY sku)
		SELECT `+key+`, max(`+name+`), COALESCE(max(p.id), ''), COALESCE(max(p.brand), ''), sum(s.orders)::bigint, sum(s.units)::bigint, sum(s.revenue)::bigint
		FROM s LEFT JOIN variants v ON v.tenant_id=$1 AND v.sku = s.sku
		LEFT JOIN products p ON p.tenant_id=$1 AND p.id = v.product_id `+extra+`
		GROUP BY 1 ORDER BY 7 DESC, 1 LIMIT $6`, tenant, f.Range.FromDay(), f.Range.ToDay(), cur, f.Channel, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProductRow, error) {
		var p ProductRow
		err := r.Scan(&p.Key, &p.Name, &p.ProductID, &p.Brand, &p.Orders, &p.Units, &p.Revenue)
		if by != "product" && by != "sku" {
			p.ProductID, p.Brand = "", ""
		}
		return p, err
	})
	if err != nil || !withTraffic || by != "product" {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT t.product_id, COALESCE(max(p.title), t.product_id), COALESCE(max(p.brand), ''), sum(t.views)::bigint, sum(t.visitors)::bigint, sum(t.cart_visitors)::bigint
		FROM analytics_product_traffic_daily t LEFT JOIN products p ON p.tenant_id=$1 AND p.id = t.product_id
		WHERE t.tenant_id=$1 AND t.day >= $2::date AND t.day <= $3::date GROUP BY t.product_id ORDER BY 4 DESC LIMIT $4`,
		tenant, f.Range.FromDay(), f.Range.ToDay(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for i, p := range out {
		idx[p.Key] = i
	}
	for rows.Next() {
		var t ProductRow
		if err := rows.Scan(&t.Key, &t.Name, &t.Brand, &t.Views, &t.Visitors, &t.CartVisitors); err != nil {
			return nil, err
		}
		if i, ok := idx[t.Key]; ok {
			out[i].Views, out[i].Visitors, out[i].CartVisitors = t.Views, t.Visitors, t.CartVisitors
		} else if len(out) < limit {
			t.ProductID = t.Key
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// ProductsReport ranks products, SKUs, brands or categories by revenue.
func (s *Service) ProductsReport(ctx context.Context, tenant string, f Filter, by string, limit int) (ProductsReport, error) {
	switch by {
	case "sku", "brand", "category":
	default:
		by = "product"
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	r := ProductsReport{Range: f.Range.Info(), By: by, Rows: []ProductRow{}}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		cs, err := currencies(ctx, tx, tenant, f)
		if err != nil {
			return err
		}
		r.Currency = cs[0]
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		rows, err := productRows(ctx, tx, tenant, f, r.Currency, by, limit, true)
		if err != nil {
			return err
		}
		if rows != nil {
			r.Rows = rows
		}
		// Totals come from the SKU level so a product in two categories is not counted twice.
		return tx.QueryRow(ctx, `SELECT COALESCE(sum(orders),0)::bigint, COALESCE(sum(units),0)::bigint, COALESCE(sum(revenue),0)::bigint FROM analytics_sku_daily
			WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5)`,
			tenant, f.Range.FromDay(), f.Range.ToDay(), r.Currency, f.Channel).Scan(&r.Totals.Orders, &r.Totals.Units, &r.Totals.Revenue)
	})
	return r, err
}

// ---------- customers report ----------

// CustomerRow is one customer, identified only by a short keyed hash.
type CustomerRow struct {
	Customer   string    `json:"customer"`
	Orders     int64     `json:"orders"`
	Spent      int64     `json:"spent"`
	First      time.Time `json:"firstOrderAt"`
	Last       time.Time `json:"lastOrderAt"`
	LastNumber int64     `json:"lastOrderNumber"`
	LastID     string    `json:"lastOrderId"`
	New        bool      `json:"new"`
}

// CustomersReport is the "Clientes" report.
type CustomersReport struct {
	Range            RangeInfo     `json:"range"`
	Currency         string        `json:"currency"`
	Channels         []string      `json:"channels"`
	Customers        int64         `json:"customers"`
	NewCustomers     int64         `json:"newCustomers"`
	Returning        int64         `json:"returningCustomers"`
	NewOrders        int64         `json:"newOrders"`
	ReturningOrders  int64         `json:"returningOrders"`
	NewRevenue       int64         `json:"newRevenue"`
	ReturningRevenue int64         `json:"returningRevenue"`
	PrevCustomers    int64         `json:"previousCustomers"`
	PrevNew          int64         `json:"previousNewCustomers"`
	PrevReturning    int64         `json:"previousReturningCustomers"`
	Series           []Point       `json:"series"`
	Grain            string        `json:"grain"`
	Frequency        []int64       `json:"frequency"` // lifetime customers with 1, 2, 3, 4, 5+ orders
	Top              []CustomerRow `json:"top"`
}

// CustomersReport splits sales between new and returning customers.
func (s *Service) CustomersReport(ctx context.Context, tenant string, f Filter) (CustomersReport, error) {
	r := CustomersReport{Range: f.Range.Info(), Frequency: make([]int64, 5), Top: []CustomerRow{}}
	r.Grain = DefaultGrain(f.Range)
	if r.Grain == GrainHour {
		r.Grain = GrainDay
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		cs, err := currencies(ctx, tx, tenant, f)
		if err != nil {
			return err
		}
		r.Currency = cs[0]
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if r.Series, _, _, err = series(ctx, tx, tenant, f, r.Currency, r.Grain); err != nil {
			return err
		}
		const q = `SELECT count(DISTINCT customer_hash), count(DISTINCT customer_hash) FILTER (WHERE customer_seq = 1),
				count(DISTINCT customer_hash) FILTER (WHERE customer_seq > 1),
				count(*) FILTER (WHERE customer_seq = 1), count(*) FILTER (WHERE customer_seq > 1),
				COALESCE(sum(total) FILTER (WHERE customer_seq = 1), 0)::bigint, COALESCE(sum(total) FILTER (WHERE customer_seq > 1), 0)::bigint
			FROM analytics_orders WHERE tenant_id=$1 AND paid_at >= $2 AND paid_at < $3 AND currency=$4 AND ($5 = '' OR channel = $5)`
		if err := tx.QueryRow(ctx, q, tenant, f.Range.From, f.Range.To, r.Currency, f.Channel).
			Scan(&r.Customers, &r.NewCustomers, &r.Returning, &r.NewOrders, &r.ReturningOrders, &r.NewRevenue, &r.ReturningRevenue); err != nil {
			return err
		}
		p := f.Range.Previous()
		var x1, x2 int64
		if err := tx.QueryRow(ctx, q, tenant, p.From, p.To, r.Currency, f.Channel).Scan(&r.PrevCustomers, &r.PrevNew, &r.PrevReturning, &x1, &x2, &x1, &x2); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT LEAST(n, 5)::int, count(*) FROM (SELECT count(*) AS n FROM analytics_orders
			WHERE tenant_id=$1 AND paid_at IS NOT NULL AND customer_hash IS NOT NULL GROUP BY customer_hash) x GROUP BY 1`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n int
			var c int64
			if err := rows.Scan(&n, &c); err != nil {
				rows.Close()
				return err
			}
			if n >= 1 && n <= 5 {
				r.Frequency[n-1] = c
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT left(encode(customer_hash, 'hex'), 10), count(*), sum(total - refunded)::bigint, min(paid_at), max(paid_at),
				max(number), (array_agg(order_id ORDER BY number DESC))[1], bool_or(customer_seq = 1)
			FROM analytics_orders WHERE tenant_id=$1 AND paid_at >= $2 AND paid_at < $3 AND currency=$4 AND ($5 = '' OR channel = $5) AND customer_hash IS NOT NULL
			GROUP BY customer_hash ORDER BY 3 DESC, 1 LIMIT 50`, tenant, f.Range.From, f.Range.To, r.Currency, f.Channel)
		if err != nil {
			return err
		}
		top, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (CustomerRow, error) {
			var c CustomerRow
			return c, row.Scan(&c.Customer, &c.Orders, &c.Spent, &c.First, &c.Last, &c.LastNumber, &c.LastID, &c.New)
		})
		if top != nil {
			r.Top = top
		}
		return err
	})
	return r, err
}

// ---------- funnel report ----------

// DeviceTraffic is the funnel of one device class.
type DeviceTraffic struct {
	Device string `json:"device"`
	Traffic
}

// ReferrerRow is one traffic source (host only; "" = direct).
type ReferrerRow struct {
	Referrer  string `json:"referrer"`
	Sessions  int64  `json:"sessions"`
	Purchases int64  `json:"purchases"`
}

// FunnelReport is the "Embudo" report.
type FunnelReport struct {
	Range       RangeInfo       `json:"range"`
	Previous    RangeInfo       `json:"previous"`
	Channels    []string        `json:"channels"`
	Grain       string          `json:"grain"`
	Totals      Traffic         `json:"totals"`
	PrevTotals  Traffic         `json:"previousTotals"`
	Series      []Point         `json:"series"`
	ByDevice    []DeviceTraffic `json:"byDevice"`
	Referrers   []ReferrerRow   `json:"referrers"`
	TopProducts []ProductRow    `json:"topProducts"`
}

// FunnelReport computes sessions → product → cart → checkout → purchase.
func (s *Service) FunnelReport(ctx context.Context, tenant string, f Filter) (FunnelReport, error) {
	r := FunnelReport{Range: f.Range.Info(), Previous: f.Range.Previous().Info(), ByDevice: []DeviceTraffic{}, Referrers: []ReferrerRow{}, TopProducts: []ProductRow{}}
	r.Grain = DefaultGrain(f.Range)
	if r.Grain == GrainHour {
		r.Grain = GrainDay
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if r.Series, _, r.Totals, err = series(ctx, tx, tenant, f, "MXN", r.Grain); err != nil {
			return err
		}
		pd, err := trafficByDay(ctx, tx, tenant, Filter{Range: f.Range.Previous(), Channel: f.Channel})
		if err != nil {
			return err
		}
		for _, t := range pd {
			r.PrevTotals.add(t)
		}
		rows, err := tx.Query(ctx, `SELECT device, sum(sessions)::bigint, sum(s_product)::bigint, sum(s_cart)::bigint, sum(s_checkout)::bigint, sum(s_purchase)::bigint, sum(page_views)::bigint
			FROM analytics_funnel_daily WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND ($4 = '' OR channel = $4) GROUP BY device ORDER BY 2 DESC`,
			tenant, f.Range.FromDay(), f.Range.ToDay(), f.Channel)
		if err != nil {
			return err
		}
		dev, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DeviceTraffic, error) {
			var d DeviceTraffic
			return d, row.Scan(&d.Device, &d.Sessions, &d.Product, &d.Cart, &d.Checkout, &d.Purchase, &d.PageViews)
		})
		if err != nil {
			return err
		}
		if dev != nil {
			r.ByDevice = dev
		}
		rows, err = tx.Query(ctx, `SELECT referrer, sum(sessions)::bigint, sum(purchases)::bigint FROM analytics_referrer_daily
			WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date GROUP BY referrer ORDER BY 2 DESC, 1 LIMIT 15`, tenant, f.Range.FromDay(), f.Range.ToDay())
		if err != nil {
			return err
		}
		refs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ReferrerRow, error) {
			var x ReferrerRow
			return x, row.Scan(&x.Referrer, &x.Sessions, &x.Purchases)
		})
		if err != nil {
			return err
		}
		if refs != nil {
			r.Referrers = refs
		}
		rows, err = tx.Query(ctx, `SELECT t.product_id, COALESCE(max(p.title), t.product_id), sum(t.views)::bigint, sum(t.visitors)::bigint, sum(t.cart_visitors)::bigint
			FROM analytics_product_traffic_daily t LEFT JOIN products p ON p.tenant_id=$1 AND p.id = t.product_id
			WHERE t.tenant_id=$1 AND t.day >= $2::date AND t.day <= $3::date GROUP BY t.product_id ORDER BY 3 DESC, 1 LIMIT 10`,
			tenant, f.Range.FromDay(), f.Range.ToDay())
		if err != nil {
			return err
		}
		tp, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (ProductRow, error) {
			var x ProductRow
			err := row.Scan(&x.Key, &x.Name, &x.Views, &x.Visitors, &x.CartVisitors)
			x.ProductID = x.Key
			return x, err
		})
		if tp != nil {
			r.TopProducts = tp
		}
		return err
	})
	return r, err
}

// ---------- promotions report ----------

// PromoRow is the impact of one promotion.
type PromoRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Active   bool   `json:"active"`
	Exists   bool   `json:"exists"`
	Orders   int64  `json:"orders"`
	Discount int64  `json:"discount"`
	Revenue  int64  `json:"revenue"`
}

// PromoPoint is the discount given on one bucket.
type PromoPoint struct {
	Bucket   string `json:"bucket"`
	Orders   int64  `json:"orders"`
	Discount int64  `json:"discount"`
}

// PromotionsReport is the "Promociones" report.
type PromotionsReport struct {
	Range    RangeInfo    `json:"range"`
	Currency string       `json:"currency"`
	Channels []string     `json:"channels"`
	Grain    string       `json:"grain"`
	Sales    Sales        `json:"sales"` // all orders of the period, for context
	Totals   PromoRow     `json:"totals"`
	Rows     []PromoRow   `json:"rows"`
	Series   []PromoPoint `json:"series"`
}

// PromotionsReport measures the discount each promotion gave and the sales it rode on.
func (s *Service) PromotionsReport(ctx context.Context, tenant string, f Filter) (PromotionsReport, error) {
	r := PromotionsReport{Range: f.Range.Info(), Rows: []PromoRow{}}
	r.Grain = DefaultGrain(f.Range)
	if r.Grain == GrainHour {
		r.Grain = GrainDay
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		cs, err := currencies(ctx, tx, tenant, f)
		if err != nil {
			return err
		}
		r.Currency = cs[0]
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if _, r.Sales, _, err = series(ctx, tx, tenant, f, r.Currency, GrainDay); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `WITH u AS (SELECT promotion_id, max(name) AS name, sum(orders)::bigint AS orders, sum(discount)::bigint AS discount, sum(revenue)::bigint AS revenue
				FROM analytics_promo_daily WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY promotion_id)
			SELECT COALESCE(u.promotion_id, p.id), COALESCE(NULLIF(p.rule->>'name', ''), NULLIF(u.name, ''), COALESCE(u.promotion_id, p.id)),
				COALESCE(p.kind, ''), COALESCE(p.active, false), p.id IS NOT NULL, COALESCE(u.orders, 0), COALESCE(u.discount, 0), COALESCE(u.revenue, 0)
			FROM u FULL JOIN (SELECT * FROM promotions WHERE tenant_id=$1) p ON p.id = u.promotion_id ORDER BY 7 DESC, 1`,
			tenant, f.Range.FromDay(), f.Range.ToDay(), r.Currency, f.Channel)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PromoRow, error) {
			var p PromoRow
			return p, row.Scan(&p.ID, &p.Name, &p.Kind, &p.Active, &p.Exists, &p.Orders, &p.Discount, &p.Revenue)
		})
		if err != nil {
			return err
		}
		for _, p := range list {
			r.Rows = append(r.Rows, p)
			r.Totals.Orders += p.Orders
			r.Totals.Discount += p.Discount
			r.Totals.Revenue += p.Revenue
		}
		rows, err = tx.Query(ctx, `SELECT day::text, sum(orders)::bigint, sum(discount)::bigint FROM analytics_promo_daily
			WHERE tenant_id=$1 AND day >= $2::date AND day <= $3::date AND currency=$4 AND ($5 = '' OR channel = $5) GROUP BY day`,
			tenant, f.Range.FromDay(), f.Range.ToDay(), r.Currency, f.Channel)
		if err != nil {
			return err
		}
		byDay := map[string]PromoPoint{}
		for rows.Next() {
			var p PromoPoint
			if err := rows.Scan(&p.Bucket, &p.Orders, &p.Discount); err != nil {
				rows.Close()
				return err
			}
			byDay[p.Bucket] = p
		}
		rows.Close()
		idx := map[string]int{}
		for _, d := range f.Range.DayList() {
			b := bucketOfDay(d, r.Grain)
			i, ok := idx[b]
			if !ok {
				i = len(r.Series)
				idx[b] = i
				r.Series = append(r.Series, PromoPoint{Bucket: b})
			}
			r.Series[i].Orders += byDay[d].Orders
			r.Series[i].Discount += byDay[d].Discount
		}
		return rows.Err()
	})
	return r, err
}

// ---------- inventory report ----------

// StockRow is the stock position of one SKU.
type StockRow struct {
	SKU       string `json:"sku"`
	Title     string `json:"title"`
	ProductID string `json:"productId"`
	OnHand    int64  `json:"onHand"`
	Reserved  int64  `json:"reserved"`
	Available int64  `json:"available"`
	Price     int64  `json:"price"`
	Value     int64  `json:"value"`
	Sold30    int64  `json:"sold30"`
	CoverDays int64  `json:"coverDays"` // -1 = no sales in 30 days
	State     string `json:"state"`     // ok | low | out
}

// InventoryReport is the "Inventario" report.
type InventoryReport struct {
	Currency   string     `json:"currency"`
	Channel    string     `json:"channel"`
	Channels   []string   `json:"channels"`
	Threshold  int64      `json:"threshold"`
	SKUs       int64      `json:"skus"`
	Units      int64      `json:"units"`
	Reserved   int64      `json:"reserved"`
	Value      int64      `json:"value"`
	LowStock   int64      `json:"lowStock"`
	OutOfStock int64      `json:"outOfStock"`
	Unpriced   int64      `json:"unpriced"`
	Rows       []StockRow `json:"rows"`
	Truncated  bool       `json:"truncated"`
}

// InventoryReport values the stock at the sale price of a channel and flags
// SKUs that are low or out. state filters rows: "", "low", "out" or "ok".
func (s *Service) InventoryReport(ctx context.Context, tenant, channel, state string, threshold int64, limit int) (InventoryReport, error) {
	if threshold < 0 || threshold > 100000 {
		threshold = LowStockThreshold
	}
	if limit <= 0 || limit > 20000 {
		limit = 500
	}
	r := InventoryReport{Threshold: threshold, Rows: []StockRow{}, Currency: "MXN"}
	today := Day(s.now())
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if r.Channels, err = channelIDs(ctx, tx, tenant); err != nil {
			return err
		}
		if channel == "" && len(r.Channels) > 0 {
			channel = r.Channels[0]
		}
		r.Channel = channel
		rows, err := tx.Query(ctx, `SELECT sl.sku, COALESCE(p.title, ''), COALESCE(v.title, ''), COALESCE(p.id, ''), sl.on_hand, sl.reserved,
				COALESCE(pr.amount, -1), COALESCE(pr.currency::text, ''), COALESCE(sd.units, 0)
			FROM (SELECT sku, sum(on_hand)::bigint AS on_hand, sum(reserved)::bigint AS reserved FROM stock_levels WHERE tenant_id=$1 GROUP BY sku) sl
			LEFT JOIN variants v ON v.tenant_id=$1 AND v.sku = sl.sku
			LEFT JOIN products p ON p.tenant_id=$1 AND p.id = v.product_id
			LEFT JOIN LATERAL (SELECT amount, currency FROM prices WHERE tenant_id=$1 AND channel=$2 AND sku = sl.sku ORDER BY min_qty LIMIT 1) pr ON true
			LEFT JOIN (SELECT sku, sum(units)::bigint AS units FROM analytics_sku_daily WHERE tenant_id=$1 AND day > $3::date - 30 GROUP BY sku) sd ON sd.sku = sl.sku
			ORDER BY (sl.on_hand - sl.reserved), sl.sku`, tenant, channel, today)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x StockRow
			var pt, vt, cur string
			if err := rows.Scan(&x.SKU, &pt, &vt, &x.ProductID, &x.OnHand, &x.Reserved, &x.Price, &cur, &x.Sold30); err != nil {
				return err
			}
			x.Title = strings.TrimSpace(pt)
			if vt != "" && x.Title != "" {
				x.Title += " · " + vt
			} else if x.Title == "" {
				x.Title = vt
			}
			x.Available = x.OnHand - x.Reserved
			if x.Price < 0 {
				x.Price = 0
				r.Unpriced++
			} else {
				r.Currency = cur
			}
			x.Value = x.OnHand * x.Price
			x.CoverDays = -1
			if x.Sold30 > 0 {
				x.CoverDays = x.Available * 30 / x.Sold30
			}
			switch {
			case x.Available <= 0:
				x.State = "out"
				r.OutOfStock++
			case x.Available <= threshold:
				x.State = "low"
				r.LowStock++
			default:
				x.State = "ok"
			}
			r.SKUs++
			r.Units += x.OnHand
			r.Reserved += x.Reserved
			r.Value += x.Value
			if state != "" && state != x.State {
				continue
			}
			if len(r.Rows) >= limit {
				r.Truncated = true
				continue
			}
			r.Rows = append(r.Rows, x)
		}
		return rows.Err()
	})
	return r, err
}
