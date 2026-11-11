package orders

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/subs"
)

// Renewal marks a checkout made by the subscription worker.
type Renewal struct {
	SubscriptionID  string
	PaymentCustomer string
}

// SubItem is one product of a subscription.
type SubItem struct {
	SKU   string `json:"sku"`
	Qty   int64  `json:"qty"`
	Title string `json:"title"`
}

// Subscription is a recurring purchase.
type Subscription struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	Plan         string     `json:"plan"`
	PlanLabel    string     `json:"planLabel"`
	DiscountBps  int64      `json:"discountBps"`
	Items        []SubItem  `json:"items"`
	NextAt       time.Time  `json:"nextAt"`
	Cycle        int        `json:"cycle"`
	Failures     int        `json:"failures"`
	LastError    string     `json:"lastError,omitempty"`
	CustomerID   string     `json:"customerId,omitempty"`
	Customer     *Customer  `json:"customer,omitempty"`
	FirstOrderID string     `json:"firstOrderId"`
	CreatedAt    time.Time  `json:"createdAt"`
	CancelledAt  *time.Time `json:"cancelledAt,omitempty"`
	Orders       []SubOrder `json:"orders,omitempty"`
}

// SubOrder is a delivery of a subscription.
type SubOrder struct {
	OrderID string    `json:"orderId"`
	Number  int64     `json:"number"`
	Total   int64     `json:"total"`
	Status  string    `json:"status"`
	Cycle   int       `json:"cycle"`
	At      time.Time `json:"at"`
}

const subCols = `id, status, plan, discount_bps, items, next_at, cycle, failures, last_error, COALESCE(customer_id,''), first_order_id, created_at, cancelled_at, customer`

func (s *Service) scanSub(tenant string, r pgx.Row, pii bool) (Subscription, error) {
	var v Subscription
	var items, enc []byte
	if err := r.Scan(&v.ID, &v.Status, &v.Plan, &v.DiscountBps, &items, &v.NextAt, &v.Cycle, &v.Failures, &v.LastError, &v.CustomerID, &v.FirstOrderID, &v.CreatedAt, &v.CancelledAt, &enc); err != nil {
		return v, err
	}
	_ = json.Unmarshal(items, &v.Items)
	v.PlanLabel = subs.Label(v.Plan)
	if pii {
		if plain, err := s.Keys.Decrypt(tenant, enc, v.ID); err == nil {
			var c Customer
			if json.Unmarshal(plain, &c) == nil {
				v.Customer = &c
			}
		}
	}
	return v, nil
}

func subToken() (string, []byte) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	t := base64.RawURLEncoding.EncodeToString(b)
	return t, tokenHash(t)
}

// SubscriptionURL is the page where the customer manages a subscription.
func (s *Service) SubscriptionURL(tenant, token string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/suscripcion/" + token
}

// createSubscriptions turns the subscribed lines of a paid order into
// subscriptions, one per frequency, and emails the manage link.
func (s *Service) createSubscriptions(ctx context.Context, tenant, orderID string, req CheckoutRequest, lines []cart.Line, discountBps int64, shippingOption string, ch payments.Charge) error {
	titles := map[string]string{}
	for _, l := range lines {
		titles[l.SKU] = l.Title
	}
	byPlan := map[string][]SubItem{}
	for _, it := range req.Items {
		if it.Plan != "" {
			byPlan[it.Plan] = append(byPlan[it.Plan], SubItem{SKU: it.SKU, Qty: it.Qty, Title: titles[it.SKU]})
		}
	}
	plans := make([]string, 0, len(byPlan))
	for p := range byPlan {
		plans = append(plans, p)
	}
	sort.Strings(plans)
	cust, _ := json.Marshal(req.Customer)
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var cid *string
		var created time.Time
		if err := tx.QueryRow(ctx, `SELECT customer_id, created_at FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, orderID).Scan(&cid, &created); err != nil {
			return err
		}
		for _, plan := range plans {
			id := newID("sub_")
			enc, err := s.Keys.Encrypt(tenant, cust, id)
			if err != nil {
				return err
			}
			token, hash := subToken()
			items, _ := json.Marshal(byPlan[plan])
			next := subs.Next(created, plan)
			if _, err := tx.Exec(ctx, `INSERT INTO subscriptions (tenant_id, id, customer_id, plan, discount_bps, items, channel, postal_code, shipping_option, customer, payment_method, payment_customer, token_hash, next_at, first_order_id)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
				tenant, id, cid, plan, discountBps, items, req.Channel, req.PostalCode, shippingOption, enc, ch.PaymentMethod, ch.Customer, hash, next, orderID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscription_orders (tenant_id, subscription_id, order_id, cycle) VALUES ($1,$2,$3,1)`, tenant, id, orderID); err != nil {
				return err
			}
			if err := store.Audit(ctx, tx, tenant, "storefront", "subscription.created", id, map[string]any{"plan": plan, "order": orderID}); err != nil {
				return err
			}
			if s.Notify != nil {
				var names []string
				for _, it := range byPlan[plan] {
					names = append(names, fmt.Sprintf("%d × %s", it.Qty, it.Title))
				}
				m := notify.Message{Template: notify.TplSubscriptionCreated, To: req.Customer.Email, Name: req.Customer.Name, OrderID: orderID, DedupeKey: "sub-created-" + id,
					Data: notify.Data{ActionURL: s.SubscriptionURL(tenant, token), Vars: map[string]string{"cliente.nombre": firstWord(req.Customer.Name),
						"suscripcion.frecuencia": subs.Label(plan), "suscripcion.proxima": day(next), "suscripcion.productos": strings.Join(names, ", ")}}}
				if cid != nil {
					m.CustomerID = *cid
				}
				if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, m); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func day(t time.Time) string {
	months := []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}
	t = t.In(time.FixedZone("CST", -6*3600))
	return fmt.Sprintf("%d de %s de %d", t.Day(), months[t.Month()-1], t.Year())
}

// Subscriptions lists subscriptions, newest first.
func (s *Service) Subscriptions(ctx context.Context, tenant, status string, before time.Time, limit int, pii bool) ([]Subscription, map[string]int, error) {
	var out []Subscription
	counts := map[string]int{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var b *time.Time
		if !before.IsZero() {
			b = &before
		}
		rows, err := tx.Query(ctx, `SELECT `+subCols+` FROM subscriptions WHERE tenant_id=$1 AND ($2='' OR status=$2) AND ($3::timestamptz IS NULL OR created_at < $3)
			ORDER BY created_at DESC LIMIT $4`, tenant, status, b, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			v, err := s.scanSub(tenant, rows, pii)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		crow, err := tx.Query(ctx, `SELECT status, count(*)::int FROM subscriptions WHERE tenant_id=$1 GROUP BY status`, tenant)
		if err != nil {
			return err
		}
		defer crow.Close()
		for crow.Next() {
			var st string
			var n int
			if err := crow.Scan(&st, &n); err != nil {
				return err
			}
			counts[st] = n
		}
		return crow.Err()
	})
	return out, counts, err
}

func (s *Service) subTx(ctx context.Context, tx pgx.Tx, tenant, where string, arg any, lock, pii bool) (Subscription, error) {
	q := `SELECT ` + subCols + ` FROM subscriptions WHERE tenant_id=$1 AND ` + where
	if lock {
		q += ` FOR UPDATE`
	}
	v, err := s.scanSub(tenant, tx.QueryRow(ctx, q, tenant, arg), pii)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id, o.number, o.grand_total, o.status, so.cycle, o.created_at FROM subscription_orders so
		JOIN orders o ON o.tenant_id=so.tenant_id AND o.id=so.order_id WHERE so.tenant_id=$1 AND so.subscription_id=$2 ORDER BY so.cycle DESC LIMIT 24`, tenant, v.ID)
	if err != nil {
		return v, err
	}
	v.Orders, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (SubOrder, error) {
		var o SubOrder
		return o, r.Scan(&o.OrderID, &o.Number, &o.Total, &o.Status, &o.Cycle, &o.At)
	})
	return v, err
}

// Subscription returns one subscription with its deliveries.
func (s *Service) Subscription(ctx context.Context, tenant, id string, pii bool) (Subscription, error) {
	var v Subscription
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		v, err = s.subTx(ctx, tx, tenant, "id=$2", id, false, pii)
		return err
	})
	return v, err
}

// SubscriptionByToken is what the manage page of the customer shows.
func (s *Service) SubscriptionByToken(ctx context.Context, tenant, token string) (Subscription, error) {
	var v Subscription
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		v, err = s.subTx(ctx, tx, tenant, "token_hash=$2", tokenHash(token), false, false)
		return err
	})
	return v, err
}

// SubscriptionAction changes a subscription: pause, resume, cancel, skip (the
// next delivery) or charge (deliver now). byToken is true when the customer acts.
func (s *Service) SubscriptionAction(ctx context.Context, tenant, actor, idOrToken, action string, byToken bool) (Subscription, error) {
	var v Subscription
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if byToken {
			v, err = s.subTx(ctx, tx, tenant, "token_hash=$2", tokenHash(idOrToken), true, false)
		} else {
			v, err = s.subTx(ctx, tx, tenant, "id=$2", idOrToken, true, false)
		}
		if err != nil {
			return err
		}
		if v.Status == "cancelled" {
			return fmt.Errorf("%w: la suscripción ya está cancelada", ErrInvalidState)
		}
		now := time.Now()
		switch action {
		case "pause":
			if v.Status != "active" && v.Status != "past_due" {
				return fmt.Errorf("%w: la suscripción no está activa", ErrInvalidState)
			}
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET status='paused', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID)
		case "resume":
			if v.Status == "active" {
				return fmt.Errorf("%w: la suscripción ya está activa", ErrInvalidState)
			}
			next := v.NextAt
			if next.Before(now) {
				next = now.Add(time.Hour) // a paused past date delivers soon, not instantly
			}
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET status='active', failures=0, last_error='', next_at=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID, next)
		case "cancel":
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET status='cancelled', cancelled_at=now(), updated_at=now(), payment_method='', payment_customer='' WHERE tenant_id=$1 AND id=$2`, tenant, v.ID)
		case "skip":
			if v.Status != "active" {
				return fmt.Errorf("%w: solo se puede saltar una entrega de una suscripción activa", ErrInvalidState)
			}
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET next_at=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID, subs.Next(v.NextAt, v.Plan))
		case "charge":
			if byToken || v.Status != "active" {
				return fmt.Errorf("%w: no se puede cobrar ahora", ErrInvalidState)
			}
			_, err = tx.Exec(ctx, `UPDATE subscriptions SET next_at=now(), updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID)
		default:
			return FieldError{"action", "Acción desconocida"}
		}
		if err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, actor, "subscription."+action, v.ID, map[string]any{"byCustomer": byToken}); err != nil {
			return err
		}
		v, err = s.subTx(ctx, tx, tenant, "id=$2", v.ID, false, false)
		return err
	})
	return v, err
}

// RenewSubscriptions delivers every subscription that is due. Each delivery is
// a normal checkout with the saved card; its idempotency key comes from the
// subscription, the cycle and the attempt, so a crash or a second node never
// charges the same delivery twice.
func (s *Service) RenewSubscriptions(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id FROM subscriptions WHERE status='active' AND next_at <= now() ORDER BY next_at LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type ti struct{ t, id string }
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ti, error) {
		var x ti
		return x, r.Scan(&x.t, &x.id)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range due {
		if err := s.renewOne(ctx, x.t, x.id); err != nil {
			if s.Log != nil {
				s.Log.Warn("subscription renewal", "tenant", x.t, "subscription", x.id, "err", err)
			}
			continue
		}
		n++
	}
	return n, nil
}

const maxSubFailures = 3

func (s *Service) renewOne(ctx context.Context, tenant, id string) error {
	var (
		v                    Subscription
		channel, postal, opt string
		pm, pcust            string
		token                string
	)
	// Claim the delivery: only one worker sees the row as due.
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if v, err = s.subTx(ctx, tx, tenant, "id=$2", id, true, true); err != nil {
			return err
		}
		if v.Status != "active" || v.NextAt.After(time.Now()) {
			return ErrInvalidState
		}
		return tx.QueryRow(ctx, `SELECT channel, postal_code, shipping_option, payment_method, payment_customer FROM subscriptions WHERE tenant_id=$1 AND id=$2`, tenant, id).
			Scan(&channel, &postal, &opt, &pm, &pcust)
	})
	if errors.Is(err, ErrInvalidState) {
		return nil
	}
	if err != nil {
		return err
	}
	if v.Customer == nil {
		return fmt.Errorf("orders: subscription %s has no readable customer", id)
	}
	items := make([]cart.Item, 0, len(v.Items))
	for _, it := range v.Items {
		items = append(items, cart.Item{SKU: it.SKU, Qty: it.Qty, Plan: v.Plan})
	}
	req := CheckoutRequest{Channel: channel, PostalCode: postal, Items: items, Customer: *v.Customer, PaymentMethod: pm, ShippingOption: opt,
		AcceptedTerms: true, Renewal: &Renewal{SubscriptionID: id, PaymentCustomer: pcust}}
	key := fmt.Sprintf("sub-%s-%d-%d", id, v.Cycle+1, v.Failures)
	res, cerr := s.Checkout(ctx, tenant, "subscription:"+id, key, req)
	if cerr != nil {
		return cerr // infrastructure problem: nothing changed, the next round retries
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := s.subTx(ctx, tx, tenant, "id=$2", id, true, false); err != nil {
			return err
		}
		if res.Status == 201 {
			var o struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(res.Body, &o)
			if _, err := tx.Exec(ctx, `UPDATE subscriptions SET cycle=cycle+1, failures=0, last_error='', next_at=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, id, subs.Next(v.NextAt, v.Plan)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO subscription_orders (tenant_id, subscription_id, order_id, cycle) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, id, o.ID, v.Cycle+1)
			return err
		}
		if res.Status == 502 { // payment outcome unknown: the reconciler settles the order; try this delivery again later
			_, err := tx.Exec(ctx, `UPDATE subscriptions SET next_at=now() + interval '30 minutes', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id)
			return err
		}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(res.Body, &p)
		failures := v.Failures + 1
		status := "active"
		if failures >= maxSubFailures {
			status = "past_due"
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriptions SET failures=$3, last_error=$4, status=$5, next_at=now() + interval '1 day', updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, id, failures, truncate(p.Code+": "+p.Detail, 300), status); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, "subscription:"+id, "subscription.renewal_failed", id, map[string]any{"code": p.Code, "attempt": failures}); err != nil {
			return err
		}
		if status == "past_due" && s.Notify != nil {
			// A fresh manage link: the stored one is only a hash.
			var hash []byte
			token, hash = subToken()
			if _, err := tx.Exec(ctx, `UPDATE subscriptions SET token_hash=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, hash); err != nil {
				return err
			}
			_, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplSubscriptionFailed, To: v.Customer.Email, Name: v.Customer.Name, CustomerID: v.CustomerID,
				DedupeKey: fmt.Sprintf("sub-failed-%s-%d", id, v.Cycle+1), Data: notify.Data{ActionURL: s.SubscriptionURL(tenant, token),
					Vars: map[string]string{"cliente.nombre": firstWord(v.Customer.Name), "suscripcion.frecuencia": subs.Label(v.Plan)}}})
			return err
		}
		return nil
	})
}
