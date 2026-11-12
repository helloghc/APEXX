// Package reports is Apexx's first-party analytics: a privacy-first storefront
// beacon, order facts, hourly/daily rollups, the queries behind the admin home
// and the reports, and the read side of operations (activity log, status).
//
// Privacy rules enforced here:
//   - no cookies and no client identifier: a visitor is
//     HMAC(daily salt, tenant | ip | user agent), truncated to 16 bytes;
//   - the salt is random, rotates every store day and is deleted after two
//     days, so a hash cannot be linked across days or reversed later;
//   - neither the IP address nor the user agent is ever stored or logged;
//   - Do Not Track and Global Privacy Control are honoured (events dropped);
//   - customers are a keyed hash of the e-mail under the tenant key.
package reports

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the store day must resolve even on images without zoneinfo

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/store"
)

// ZoneName is the timezone of the store day.
const ZoneName = "America/Mexico_City"

// Zone is the location of ZoneName.
var Zone = func() *time.Location {
	l, err := time.LoadLocation(ZoneName)
	if err != nil {
		panic(err)
	}
	return l
}()

// Retention of raw storefront events.
const Retention = 90 * 24 * time.Hour

// Event types stored in analytics_events.
const (
	EvPageView      = "page_view"
	EvProductView   = "product_view"
	EvAddToCart     = "add_to_cart"
	EvCheckoutStart = "checkout_start"
	EvPurchase      = "purchase"
)

// Limits of one beacon request.
const (
	MaxBeaconBytes  = 8 << 10
	MaxBeaconEvents = 20
)

// Service owns analytics writes and reads.
type Service struct {
	DB   *store.DB
	Keys *crypto.Keyring
	Log  *slog.Logger
	Now  func() time.Time // tests replace the clock
	// StockCounts answers the low and out of stock counts from memory (the
	// projection) instead of summing every stock row; ok=false falls back.
	StockCounts func(tenant string, threshold int64) (low, out int64, ok bool)

	saltMu sync.Mutex
	salts  map[string][]byte
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Day returns the store day of t as YYYY-MM-DD.
func Day(t time.Time) string { return t.In(Zone).Format("2006-01-02") }

// Beacon is the body the storefront script sends.
type Beacon struct {
	Channel  string        `json:"c"`
	Referrer string        `json:"r"`
	Events   []BeaconEvent `json:"e"`
}

// BeaconEvent is one storefront event: "pv" page view (the server classifies
// product and checkout pages from the path), "ac" add to cart, "pu" purchase.
type BeaconEvent struct {
	Type  string `json:"t"`
	Path  string `json:"p,omitempty"`
	SKU   string `json:"s,omitempty"`
	Qty   int    `json:"q,omitempty"`
	Order string `json:"o,omitempty"`
	Value int64  `json:"v,omitempty"`
}

// FieldError is one invalid field of a beacon.
type FieldError struct {
	Field, Code, Message string
}

// ValidationError lists every invalid field.
type ValidationError struct{ Fields []FieldError }

func (e *ValidationError) Error() string { return "reports: invalid beacon" }

var (
	reChannel = regexp.MustCompile(`^[a-z0-9_-]{0,32}$`)
	reHost    = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,98}[a-z0-9])?$`)
	reSKU     = regexp.MustCompile(`^[^\x00-\x1f]{1,64}$`)
	reOrder   = regexp.MustCompile(`^ord_[a-z0-9]{6,48}$`)
	rePath    = regexp.MustCompile(`^/[A-Za-z0-9/_.~%-]{0,199}$`)
	reShop    = regexp.MustCompile(`^/s/[^/]+/([^/]+)/(.*)$`)
)

// ParseBeacon decodes and validates a beacon body strictly: unknown fields,
// oversized values and malformed identifiers are rejected, never stored.
func ParseBeacon(r io.Reader) (Beacon, error) {
	var b Beacon
	raw, err := io.ReadAll(io.LimitReader(r, MaxBeaconBytes+1))
	if err != nil {
		return b, err
	}
	if len(raw) > MaxBeaconBytes {
		return b, &ValidationError{[]FieldError{{"", "too_large", "El cuerpo supera 8 KB"}}}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, &ValidationError{[]FieldError{{"", "invalid_json", "JSON inválido"}}}
	}
	var fe []FieldError
	add := func(f, c, m string) { fe = append(fe, FieldError{f, c, m}) }
	if !reChannel.MatchString(b.Channel) {
		add("c", "invalid", "Canal no válido")
	}
	b.Referrer = strings.ToLower(b.Referrer)
	if b.Referrer != "" && !reHost.MatchString(b.Referrer) {
		add("r", "invalid", "El origen debe ser solo un nombre de host")
	}
	if len(b.Events) == 0 {
		add("e", "required", "Envía al menos un evento")
	}
	if len(b.Events) > MaxBeaconEvents {
		add("e", "too_many", fmt.Sprintf("Máximo %d eventos por envío", MaxBeaconEvents))
		return b, &ValidationError{fe}
	}
	for i := range b.Events {
		e := &b.Events[i]
		f := func(n string) string { return fmt.Sprintf("e[%d].%s", i, n) }
		switch e.Type {
		case "pv":
			if q := strings.IndexAny(e.Path, "?#"); q >= 0 {
				e.Path = e.Path[:q] // query strings can carry personal data: never kept
			}
			if !rePath.MatchString(e.Path) {
				add(f("p"), "invalid", "Ruta no válida")
			}
			if e.SKU != "" || e.Qty != 0 || e.Order != "" || e.Value != 0 {
				add(f("t"), "invalid", "Una vista de página solo lleva la ruta")
			}
		case "ac":
			if !reSKU.MatchString(e.SKU) {
				add(f("s"), "invalid", "SKU no válido")
			}
			if e.Qty < 1 || e.Qty > 999 {
				add(f("q"), "invalid", "Cantidad entre 1 y 999")
			}
			if e.Path != "" || e.Order != "" || e.Value != 0 {
				add(f("t"), "invalid", "Agregar al carrito solo lleva SKU y cantidad")
			}
		case "pu":
			if !reOrder.MatchString(e.Order) {
				add(f("o"), "invalid", "Pedido no válido")
			}
			if e.Value < 0 || e.Value > 1e12 {
				add(f("v"), "invalid", "Importe no válido")
			}
			if e.Path != "" || e.SKU != "" || e.Qty != 0 {
				add(f("t"), "invalid", "Una compra solo lleva pedido e importe")
			}
		default:
			add(f("t"), "invalid", "Tipo de evento desconocido (usa pv, ac o pu)")
		}
	}
	if len(fe) > 0 {
		return b, &ValidationError{fe}
	}
	return b, nil
}

var botUA = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|scrap|headless|lighthouse|pagespeed|preview|monitor|uptime|pingdom|curl|wget|python|go-http|java/|okhttp|axios|node-fetch|undici|phantom|selenium|puppeteer|playwright|httpclient|libwww|facebookexternalhit|whatsapp|postman|insomnia`)

// IsBot reports whether a user agent is not a person's browser.
func IsBot(ua string) bool {
	ua = strings.TrimSpace(ua)
	return len(ua) < 20 || len(ua) > 512 || !strings.HasPrefix(ua, "Mozilla/") || botUA.MatchString(ua)
}

// Device classifies a user agent without keeping it.
func Device(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case strings.Contains(l, "ipad") || strings.Contains(l, "tablet") || (strings.Contains(l, "android") && !strings.Contains(l, "mobile")):
		return "tablet"
	case strings.Contains(l, "mobi") || strings.Contains(l, "iphone") || strings.Contains(l, "android"):
		return "mobile"
	}
	return "desktop"
}

// Resolver maps storefront identifiers to products (the in-memory catalog).
type Resolver interface {
	ProductByHandle(handle string) (id string, ok bool)
	ProductBySKU(sku string) (id string, ok bool)
}

// Client describes the request that carried a beacon. IP and UserAgent are
// used only to derive the visitor hash and the device class.
type Client struct {
	IP, UserAgent string
	DNT           bool // Do Not Track or Global Privacy Control
}

// Outcome of an ingest call.
type Outcome struct {
	Stored  int
	Dropped string // "", "dnt", "bot"
}

// Ingest stores the events of one beacon. Requests from bots and from
// browsers that asked not to be tracked are accepted and dropped.
func (s *Service) Ingest(ctx context.Context, tenant string, b Beacon, c Client, res Resolver) (Outcome, error) {
	if c.DNT {
		return Outcome{Dropped: "dnt"}, nil
	}
	if IsBot(c.UserAgent) {
		return Outcome{Dropped: "bot"}, nil
	}
	now := s.now()
	visitor, err := s.visitor(ctx, tenant, c, now)
	if err != nil {
		return Outcome{}, err
	}
	device := Device(c.UserAgent)
	rows := make([][]any, 0, len(b.Events))
	for _, e := range b.Events {
		typ, site, path, product := "", "", "", ""
		switch e.Type {
		case "pv":
			typ, path = EvPageView, e.Path
			if m := reShop.FindStringSubmatch(e.Path); m != nil {
				site = m[1]
				switch rest := m[2]; {
				case strings.HasPrefix(rest, "p/"):
					if id, ok := res.ProductByHandle(strings.TrimPrefix(rest, "p/")); ok {
						typ, product = EvProductView, id
					}
				case rest == "checkout":
					typ = EvCheckoutStart
				}
			}
		case "ac":
			id, ok := res.ProductBySKU(e.SKU)
			if !ok {
				continue // not a SKU of this store
			}
			typ, product = EvAddToCart, id
		case "pu":
			typ = EvPurchase
		}
		rows = append(rows, []any{tenant, now, typ, visitor, b.Channel, site, path, product, e.SKU, e.Qty, e.Value, e.Order, b.Referrer, device})
	}
	if len(rows) == 0 {
		return Outcome{}, nil
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return insertRows(ctx, tx, "analytics_events",
			[]string{"tenant_id", "at", "type", "visitor", "channel", "site", "path", "product_id", "sku", "qty", "value", "order_id", "referrer", "device"}, rows)
	})
	return Outcome{Stored: len(rows)}, err
}

// visitor derives the anonymous visitor id of the day.
func (s *Service) visitor(ctx context.Context, tenant string, c Client, now time.Time) ([]byte, error) {
	salt, err := s.salt(ctx, Day(now))
	if err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, salt)
	h.Write([]byte(tenant))
	h.Write([]byte{0})
	h.Write([]byte(c.IP))
	h.Write([]byte{0})
	h.Write([]byte(c.UserAgent))
	return h.Sum(nil)[:16], nil
}

// salt returns the random salt of a store day, creating it on first use. Every
// node reads the same row, so a visitor hashes the same behind a load balancer.
func (s *Service) salt(ctx context.Context, day string) ([]byte, error) {
	s.saltMu.Lock()
	defer s.saltMu.Unlock()
	if v, ok := s.salts[day]; ok {
		return v, nil
	}
	fresh := make([]byte, 32)
	if _, err := rand.Read(fresh); err != nil {
		return nil, err
	}
	var salt []byte
	err := s.DB.System.QueryRow(ctx, `INSERT INTO analytics_salts (day, salt) VALUES ($1::date, $2)
		ON CONFLICT (day) DO UPDATE SET salt = analytics_salts.salt RETURNING salt`, day, fresh).Scan(&salt)
	if err != nil {
		return nil, err
	}
	s.salts = map[string][]byte{day: salt} // older salts leave memory with the day
	return salt, nil
}

// Purge deletes raw events past retention and salts older than two days.
func (s *Service) Purge(ctx context.Context) (int64, error) {
	if _, err := s.DB.System.Exec(ctx, `DELETE FROM analytics_salts WHERE day < ($1::date - 2)`, Day(s.now())); err != nil {
		return 0, err
	}
	var total int64
	for {
		tag, err := s.DB.System.Exec(ctx, `DELETE FROM analytics_events WHERE (tenant_id, id) IN (
			SELECT tenant_id, id FROM analytics_events WHERE at < $1 ORDER BY at LIMIT 20000)`, s.now().Add(-Retention-time.Hour))
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < 20000 {
			return total, nil
		}
	}
}

// ErrNoTenantKey means the tenant key is not loaded on this node yet.
var ErrNoTenantKey = errors.New("reports: tenant key not loaded")

// insertRows writes rows with multi-row INSERTs (COPY cannot be used on
// tables with row level security).
func insertRows(ctx context.Context, tx pgx.Tx, table string, cols []string, rows [][]any) error {
	per := 60000 / len(cols)
	for len(rows) > 0 {
		n := min(per, len(rows))
		var sb strings.Builder
		sb.WriteString("INSERT INTO " + table + " (" + strings.Join(cols, ", ") + ") VALUES ")
		args := make([]any, 0, n*len(cols))
		for i, r := range rows[:n] {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('(')
			for j := range cols {
				if j > 0 {
					sb.WriteByte(',')
				}
				fmt.Fprintf(&sb, "$%d", len(args)+j+1)
			}
			sb.WriteByte(')')
			args = append(args, r...)
		}
		if _, err := tx.Exec(ctx, sb.String(), args...); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}
