// Package webhooks delivers platform events to connectors (Mercado Libre,
// Google Merchant, Meta, ERPs, Facturapi...) as signed CloudEvents 1.0.
//
// Contract (docs/audit/conectores.md):
//   - Body: CloudEvent JSON; data carries the current state of the resource.
//   - Headers: Apexx-Event-Id, Apexx-Delivery-Attempt,
//     Apexx-Signature: t=<unix>,v1=HMAC-SHA256(secret, "<t>.<body>").
//   - 2xx = delivered. 5xx, 429 or network error = retry with exponential
//     backoff and jitter for up to 24 h. Any other 4xx = dead letter.
//   - Replay: re-enqueue every event after a given sequence.
//   - Pull feed: GET /v1/events?after=&limit= with the same envelopes.
//
// Webhook URLs may not point to private, loopback or link-local addresses
// (SSRF protection), unless AllowPrivate is set for development.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/projection"
	"github.com/helloghc/apexx/internal/store"
)

// CloudEvent is the envelope sent to connectors.
type CloudEvent struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Source      string          `json:"source"`
	Type        string          `json:"type"`
	Subject     string          `json:"subject"`
	Time        time.Time       `json:"time"`
	Tenant      string          `json:"apexxtenant"`
	Sequence    int64           `json:"apexxsequence"`
	ContentType string          `json:"datacontenttype"`
	Data        json.RawMessage `json:"data"`
}

// TypeOf maps an outbox topic (and payload) to a public event type.
func TypeOf(topic string, payload []byte) string {
	switch topic {
	case store.TopicProduct:
		return "apexx.product.updated"
	case store.TopicStock:
		return "apexx.inventory.level.updated"
	case store.TopicPrice:
		return "apexx.price.updated"
	case store.TopicPromotion:
		return "apexx.promotion.updated"
	case store.TopicChannel:
		return "apexx.channel.updated"
	case store.TopicOrder:
		var p struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(payload, &p)
		switch p.Type {
		case "order.created":
			return "apexx.order.created"
		case "payment.captured":
			return "apexx.order.paid"
		case "payment.failed":
			return "apexx.payment.failed"
		case "order.fulfilled":
			return "apexx.order.fulfilled"
		case "order.refunded":
			return "apexx.order.refunded"
		case "order.cancelled":
			return "apexx.order.cancelled"
		case "invoice.issued":
			return "apexx.invoice.issued"
		}
		return "apexx.order.updated"
	case "site.released":
		return "apexx.site.published"
	}
	return "apexx." + topic
}

// Service manages subscriptions, the dispatcher and the sender.
type Service struct {
	DB           *store.DB
	Keys         *crypto.Keyring
	Proj         *projection.Projector
	HTTP         *http.Client
	AllowPrivate bool
	MaxAge       time.Duration // give up after this long (default 24h)
}

// Subscription is a webhook endpoint of a store.
type Subscription struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Types     []string  `json:"types"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrUnsafeURL means the URL points to a forbidden destination.
var ErrUnsafeURL = errors.New("webhooks: URL must be https and public")

func (s *Service) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ErrUnsafeURL
	}
	if s.AllowPrivate {
		return nil
	}
	if u.Scheme != "https" {
		return ErrUnsafeURL
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return ErrUnsafeURL
	}
	if host == "localhost" || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return ErrUnsafeURL
	}
	return nil
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.ParseIP("169.254.169.254")))
}

// Client returns an HTTP client whose dialer refuses non-public addresses
// at connect time (defends against DNS rebinding).
func Client(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return ErrUnsafeURL
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, MaxIdleConnsPerHost: 4, TLSHandshakeTimeout: 5 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Subscribe creates a subscription and returns its signing secret (shown once).
func (s *Service) Subscribe(ctx context.Context, tenant, actor, rawURL string, types []string) (Subscription, string, error) {
	if err := s.checkURL(rawURL); err != nil {
		return Subscription{}, "", err
	}
	if types == nil {
		types = []string{}
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	secret := "whsec_" + hex.EncodeToString(b)
	enc, err := s.Keys.Encrypt(tenant, []byte(secret), "webhook")
	if err != nil {
		return Subscription{}, "", err
	}
	idb := make([]byte, 8)
	_, _ = rand.Read(idb)
	sub := Subscription{ID: "wh_" + hex.EncodeToString(idb), URL: rawURL, Types: types, Active: true, CreatedAt: time.Now().UTC()}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO webhook_subscriptions (tenant_id, id, url, secret_enc, types) VALUES ($1,$2,$3,$4,$5)`,
			tenant, sub.ID, rawURL, enc, types); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "webhook.subscribe", sub.ID, map[string]any{"url": rawURL, "types": types})
	})
	return sub, secret, err
}

// List returns the subscriptions of a store.
func (s *Service) List(ctx context.Context, tenant string) ([]Subscription, error) {
	var out []Subscription
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, url, types, active, created_at FROM webhook_subscriptions WHERE tenant_id=$1 ORDER BY created_at`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Subscription, error) {
			var x Subscription
			return x, r.Scan(&x.ID, &x.URL, &x.Types, &x.Active, &x.CreatedAt)
		})
		return err
	})
	return out, err
}

// Disable deactivates a subscription.
func (s *Service) Disable(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE webhook_subscriptions SET active=false WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "webhook.disable", id, nil)
	})
}

// Delivery is the status of one event for one subscription.
type Delivery struct {
	EventID    int64      `json:"eventId"`
	Type       string     `json:"type"`
	Subject    string     `json:"subject"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	LastStatus int        `json:"lastStatus"`
	LastError  string     `json:"lastError,omitempty"`
	Next       time.Time  `json:"nextAttempt"`
	Delivered  *time.Time `json:"deliveredAt,omitempty"`
}

// Deliveries lists recent deliveries of a subscription.
func (s *Service) Deliveries(ctx context.Context, tenant, sub string, limit int) ([]Delivery, error) {
	var out []Delivery
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT event_id, type, subject, status, attempts, last_status, last_error, next_attempt, delivered_at
			FROM webhook_deliveries WHERE tenant_id=$1 AND subscription_id=$2 ORDER BY event_id DESC LIMIT $3`, tenant, sub, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Delivery, error) {
			var d Delivery
			return d, r.Scan(&d.EventID, &d.Type, &d.Subject, &d.Status, &d.Attempts, &d.LastStatus, &d.LastError, &d.Next, &d.Delivered)
		})
		return err
	})
	return out, err
}

// Replay re-enqueues every event of the tenant after `after` for a subscription.
func (s *Service) Replay(ctx context.Context, tenant, actor, sub string, after int64) (int64, error) {
	var n int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var types []string
		if err := tx.QueryRow(ctx, `SELECT types FROM webhook_subscriptions WHERE tenant_id=$1 AND id=$2 AND active`, tenant, sub).Scan(&types); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, topic, key, payload FROM outbox WHERE tenant_id=$1 AND seq > $2 ORDER BY seq`, tenant, after)
		if err != nil {
			return err
		}
		type ev struct {
			id         int64
			topic, key string
			payload    []byte
		}
		var evs []ev
		for rows.Next() {
			var e ev
			if err := rows.Scan(&e.id, &e.topic, &e.key, &e.payload); err != nil {
				rows.Close()
				return err
			}
			evs = append(evs, e)
		}
		rows.Close()
		for _, e := range evs {
			t := TypeOf(e.topic, e.payload)
			if !matches(types, t) {
				continue
			}
			ct, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (tenant_id, subscription_id, event_id, type, subject) VALUES ($1,$2,$3,$4,$5)
				ON CONFLICT (tenant_id, subscription_id, event_id) DO UPDATE SET status='pending', attempts=0, next_attempt=now(), last_error=''`,
				tenant, sub, e.id, t, e.key)
			if err != nil {
				return err
			}
			n += ct.RowsAffected()
		}
		return store.Audit(ctx, tx, tenant, actor, "webhook.replay", sub, map[string]any{"after": after, "events": n})
	})
	return n, err
}

func matches(types []string, t string) bool {
	if len(types) == 0 {
		return true
	}
	for _, x := range types {
		if x == t || (strings.HasSuffix(x, ".*") && strings.HasPrefix(t, strings.TrimSuffix(x, "*"))) {
			return true
		}
	}
	return false
}

// Dispatch turns new outbox events into deliveries for matching subscriptions.
// Only one node dispatches at a time (transaction-scoped advisory lock), so
// several instances behind a load balancer never race on the cursor.
func (s *Service) Dispatch(ctx context.Context) (int, error) {
	n := 0
	err := pgx.BeginFunc(ctx, s.DB.System, func(tx pgx.Tx) error {
		var leader bool
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(hashtext('apexx:webhook-dispatcher'))`).Scan(&leader); err != nil || !leader {
			return err
		}
		var cursor int64
		if err := tx.QueryRow(ctx, `SELECT cursor FROM dispatcher_cursor WHERE name='webhooks' FOR UPDATE`).Scan(&cursor); err != nil {
			return err
		}
		// Follow the commit-ordered position (seq): an id can commit after a
		// larger one and would be skipped.
		rows, err := tx.Query(ctx, `SELECT id, seq, tenant_id, topic, key, payload FROM outbox WHERE seq > $1 ORDER BY seq LIMIT 1000`, cursor)
		if err != nil {
			return err
		}
		type ev struct {
			id, seq            int64
			tenant, topic, key string
			payload            []byte
		}
		var evs []ev
		for rows.Next() {
			var e ev
			if err := rows.Scan(&e.id, &e.seq, &e.tenant, &e.topic, &e.key, &e.payload); err != nil {
				rows.Close()
				return err
			}
			evs = append(evs, e)
		}
		rows.Close()
		if len(evs) == 0 {
			return nil
		}
		type sub struct {
			id    string
			types []string
		}
		subs := map[string][]sub{}
		for _, e := range evs {
			list, ok := subs[e.tenant]
			if !ok {
				r, err := tx.Query(ctx, `SELECT id, types FROM webhook_subscriptions WHERE tenant_id=$1 AND active
					AND EXISTS (SELECT 1 FROM tenants WHERE id=$1 AND kind='production')`, e.tenant)
				if err != nil {
					return err
				}
				for r.Next() {
					var x sub
					if err := r.Scan(&x.id, &x.types); err != nil {
						r.Close()
						return err
					}
					list = append(list, x)
				}
				r.Close()
				subs[e.tenant] = list
			}
			t := TypeOf(e.topic, e.payload)
			for _, sb := range list {
				if !matches(sb.types, t) {
					continue
				}
				if _, err := tx.Exec(ctx, `INSERT INTO webhook_deliveries (tenant_id, subscription_id, event_id, type, subject) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
					e.tenant, sb.id, e.id, t, e.key); err != nil {
					return err
				}
				n++
			}
		}
		_, err = tx.Exec(ctx, `UPDATE dispatcher_cursor SET cursor=$1 WHERE name='webhooks'`, evs[len(evs)-1].seq)
		return err
	})
	return n, err
}

// Envelope builds the CloudEvent for an outbox event with the current state.
// id identifies the event; seq is its commit-ordered position (Sequence).
func (s *Service) Envelope(ctx context.Context, tenant string, id, seq int64, topic, key string, payload []byte, at time.Time) CloudEvent {
	ev := CloudEvent{SpecVersion: "1.0", ID: fmt.Sprintf("%s-%d", tenant, id), Source: "apexx/" + tenant, Type: TypeOf(topic, payload),
		Subject: key, Time: at.UTC(), Tenant: tenant, Sequence: seq, ContentType: "application/json", Data: payload}
	if s.Proj != nil {
		if t, ok := s.Proj.Tenant(tenant); ok {
			var state any
			switch topic {
			case store.TopicProduct:
				if p, err := t.Catalog.Get(key); err == nil {
					state = p
				}
			case store.TopicStock:
				if snap, ok := t.Inventory.Get(key); ok {
					state = snap
				}
			case store.TopicPrice:
				ch, sku, _ := strings.Cut(key, "|")
				if q, ok := t.Pricing.Resolve(ch, sku, 1); ok {
					state = map[string]any{"channel": ch, "sku": sku, "unit": q.Unit, "compareAt": q.CompareAt}
				}
			}
			if state != nil {
				if b, err := json.Marshal(state); err == nil {
					ev.Data = b
				}
			}
		}
	}
	return ev
}

// Sign returns the Apexx-Signature header value.
func Sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// Send delivers due deliveries once. It returns how many were delivered.
func (s *Service) Send(ctx context.Context) (int, error) {
	// Claim due deliveries with a 2-minute lease. SKIP LOCKED lets several
	// nodes send in parallel without ever sending the same delivery twice;
	// if a node dies, the lease expires and another node retries.
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, subscription_id, event_id FROM webhook_deliveries
			WHERE status='pending' AND next_attempt <= now() ORDER BY event_id LIMIT 100 FOR UPDATE SKIP LOCKED),
		claimed AS (
			UPDATE webhook_deliveries d SET next_attempt = now() + interval '2 minutes'
			FROM due WHERE d.tenant_id=due.tenant_id AND d.subscription_id=due.subscription_id AND d.event_id=due.event_id
			RETURNING d.tenant_id, d.subscription_id, d.event_id, d.attempts, d.created_at, d.subject)
		SELECT c.tenant_id, c.subscription_id, c.event_id, c.attempts, c.created_at, w.url, w.secret_enc,
			COALESCE(o.topic,''), COALESCE(o.key,c.subject), COALESCE(o.payload,'{}'::jsonb), COALESCE(o.created_at, c.created_at), COALESCE(o.seq, c.event_id)
		FROM claimed c
		JOIN webhook_subscriptions w ON w.tenant_id=c.tenant_id AND w.id=c.subscription_id AND w.active
		LEFT JOIN outbox o ON o.id=c.event_id
		ORDER BY c.event_id`)
	if err != nil {
		return 0, err
	}
	type job struct {
		tenant, sub string
		event       int64
		attempts    int
		created     time.Time
		url         string
		secretEnc   []byte
		topic, key  string
		payload     []byte
		at          time.Time
		seq         int64
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.tenant, &j.sub, &j.event, &j.attempts, &j.created, &j.url, &j.secretEnc, &j.topic, &j.key, &j.payload, &j.at, &j.seq); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	client := s.HTTP
	if client == nil {
		client = Client(s.AllowPrivate)
	}
	maxAge := s.MaxAge
	if maxAge == 0 {
		maxAge = 24 * time.Hour
	}
	ok := 0
	for _, j := range jobs {
		secret, err := s.Keys.Decrypt(j.tenant, j.secretEnc, "webhook")
		if err != nil {
			continue
		}
		ev := s.Envelope(ctx, j.tenant, j.event, j.seq, j.topic, j.key, j.payload, j.at)
		body, _ := json.Marshal(ev)
		attempt := j.attempts + 1
		status, sendErr := post(ctx, client, j.url, body, ev.ID, attempt, Sign(string(secret), time.Now().Unix(), body))
		var q string
		var args []any
		switch {
		case sendErr == nil && status >= 200 && status < 300:
			q = `UPDATE webhook_deliveries SET status='delivered', attempts=$4, last_status=$5, last_error='', delivered_at=now() WHERE tenant_id=$1 AND subscription_id=$2 AND event_id=$3`
			args = []any{j.tenant, j.sub, j.event, attempt, status}
			ok++
		case sendErr == nil && status >= 400 && status < 500 && status != 429, time.Since(j.created) > maxAge:
			q = `UPDATE webhook_deliveries SET status='dead', attempts=$4, last_status=$5, last_error=$6 WHERE tenant_id=$1 AND subscription_id=$2 AND event_id=$3`
			args = []any{j.tenant, j.sub, j.event, attempt, status, errText(sendErr, status)}
		default:
			q = `UPDATE webhook_deliveries SET attempts=$4, last_status=$5, last_error=$6, next_attempt=now() + $7::interval WHERE tenant_id=$1 AND subscription_id=$2 AND event_id=$3`
			args = []any{j.tenant, j.sub, j.event, attempt, status, errText(sendErr, status), backoff(attempt)}
		}
		if _, err := s.DB.System.Exec(ctx, q, args...); err != nil {
			return ok, err
		}
	}
	return ok, nil
}

func errText(err error, status int) string {
	if err != nil {
		return err.Error()
	}
	return "HTTP " + strconv.Itoa(status)
}

// backoff: 10s, 20s, 40s ... capped at 1h, plus up to 20% jitter.
func backoff(attempt int) string {
	d := 10 * time.Second << min(attempt-1, 9)
	if d > time.Hour {
		d = time.Hour
	}
	j, _ := rand.Int(rand.Reader, big.NewInt(int64(d/5)+1))
	d += time.Duration(j.Int64())
	return fmt.Sprintf("%d milliseconds", d.Milliseconds())
}

func post(ctx context.Context, c *http.Client, url string, body []byte, id string, attempt int, sig string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/cloudevents+json")
	req.Header.Set("Apexx-Event-Id", id)
	req.Header.Set("Apexx-Delivery-Attempt", strconv.Itoa(attempt))
	req.Header.Set("Apexx-Signature", sig)
	req.Header.Set("User-Agent", "Apexx-Webhooks/1.0")
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return resp.StatusCode, nil
}

// Feed returns envelopes of a tenant's events after a sequence (pull API).
func (s *Service) Feed(ctx context.Context, tenant string, after int64, limit int, types []string) ([]CloudEvent, error) {
	var out []CloudEvent
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, seq, topic, key, payload, created_at FROM outbox WHERE tenant_id=$1 AND seq > $2 ORDER BY seq LIMIT $3`, tenant, after, limit*4)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() && len(out) < limit {
			var id, seq int64
			var topic, key string
			var payload []byte
			var at time.Time
			if err := rows.Scan(&id, &seq, &topic, &key, &payload, &at); err != nil {
				return err
			}
			if !matches(types, TypeOf(topic, payload)) {
				continue
			}
			out = append(out, s.Envelope(ctx, tenant, id, seq, topic, key, payload, at))
		}
		return rows.Err()
	})
	return out, err
}

// CheckURL tells whether raw is a destination this service may call.
func (s *Service) CheckURL(raw string) error { return s.checkURL(raw) }
