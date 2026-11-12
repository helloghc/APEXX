// Package carriers connects shipping companies: live rates at checkout,
// labels bought from the order, tracking that updates the shipment, and
// prepaid return labels. Every provider speaks the same small interface; a
// slow or failing provider never blocks a sale (callers get a fallback).
package carriers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Address is one end of a shipment.
type Address struct {
	Name       string `json:"name,omitempty"`
	Line1      string `json:"line1,omitempty"`
	City       string `json:"city,omitempty"`
	State      string `json:"state,omitempty"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
	Phone      string `json:"phone,omitempty"`
}

// RateRequest asks for the price of sending a parcel.
type RateRequest struct {
	From        Address `json:"from"`
	To          Address `json:"to"`
	WeightGrams int64   `json:"weightGrams"`
	Value       int64   `json:"value"` // declared value, minor units
	Currency    string  `json:"currency"`
}

// Rate is one service a carrier offers for the parcel.
type Rate struct {
	Service string `json:"service"`
	Amount  int64  `json:"amount"` // minor units, in the request currency
	Days    int    `json:"days"`
}

// LabelRequest buys a label (Return: from the customer back to the store).
type LabelRequest struct {
	RateRequest
	Reference string `json:"reference"` // order or return id
	Service   string `json:"service,omitempty"`
	Return    bool   `json:"return,omitempty"`
}

// Label is a bought shipping label.
type Label struct {
	TrackingNumber string `json:"trackingNumber"`
	LabelURL       string `json:"labelUrl"`
	Cost           int64  `json:"cost"`
	Carrier        string `json:"carrier"`
}

// Tracking is where a parcel is.
type Tracking struct {
	Status string     `json:"status"` // pre_transit | in_transit | delivered | exception
	Detail string     `json:"detail,omitempty"`
	At     *time.Time `json:"at,omitempty"`
}

// Provider is a shipping company or aggregator.
type Provider interface {
	Rates(ctx context.Context, r RateRequest) ([]Rate, error)
	BuyLabel(ctx context.Context, r LabelRequest) (Label, error)
	Track(ctx context.Context, tracking string) (Tracking, error)
	Cancel(ctx context.Context, tracking string) error
}

// ErrRejected is a request the carrier refused (bad address, no service):
// retrying will not help.
var ErrRejected = errors.New("la paquetería rechazó la solicitud")

// ---------- sandbox ----------

// Sandbox is a deterministic carrier for tests and QA: $99 plus $15 per kg,
// labels with a fake tracking number, parcels shown as delivered.
// Postal code 00000 is refused; postal code 99999 makes it time out.
type Sandbox struct{ Now func() time.Time }

func (s Sandbox) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Rates implements Provider.
func (s Sandbox) Rates(ctx context.Context, r RateRequest) ([]Rate, error) {
	switch r.To.PostalCode {
	case "00000":
		return nil, fmt.Errorf("%w: código postal sin cobertura", ErrRejected)
	case "99999":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	kg := (max(r.WeightGrams, 1) + 999) / 1000
	return []Rate{{Service: "estandar", Amount: 9900 + 1500*kg, Days: 4}, {Service: "express", Amount: 18900 + 2500*kg, Days: 1}}, nil
}

// BuyLabel implements Provider.
func (s Sandbox) BuyLabel(ctx context.Context, r LabelRequest) (Label, error) {
	if r.To.PostalCode == "00000" {
		return Label{}, fmt.Errorf("%w: código postal sin cobertura", ErrRejected)
	}
	rates, err := s.Rates(ctx, r.RateRequest)
	if err != nil {
		return Label{}, err
	}
	cost := rates[0].Amount
	if r.Service == "express" {
		cost = rates[1].Amount
	}
	sum := sha256.Sum256([]byte(r.Reference + strconv.FormatBool(r.Return)))
	tn := "SBX" + strings.ToUpper(hex.EncodeToString(sum[:6]))
	return Label{TrackingNumber: tn, LabelURL: "sandbox:" + tn, Cost: cost, Carrier: "Paquetería de prueba"}, nil
}

// Track implements Provider: every sandbox parcel shows as delivered.
func (s Sandbox) Track(_ context.Context, tracking string) (Tracking, error) {
	if !strings.HasPrefix(tracking, "SBX") {
		return Tracking{}, fmt.Errorf("%w: guía desconocida", ErrRejected)
	}
	t := s.now()
	return Tracking{Status: "delivered", Detail: "Entregado", At: &t}, nil
}

// Cancel implements Provider.
func (Sandbox) Cancel(context.Context, string) error { return nil }

// ---------- HTTP connector ----------

// HTTP talks to any carrier app that implements Apexx's published contract:
// POST {endpoint}/rates, /labels, /track and /cancel with a JSON body,
// signed with X-Apexx-Signature = hex(HMAC-SHA256(secret, timestamp + "." + body)).
type HTTP struct {
	Endpoint string
	Secret   []byte
	Client   *http.Client
}

func (h HTTP) post(ctx context.Context, path string, in, out any) error {
	body, _ := json.Marshal(in)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	m := hmac.New(sha256.New, h.Secret)
	m.Write([]byte(ts + "."))
	m.Write(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.Endpoint, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Apexx-Timestamp", ts)
	req.Header.Set("X-Apexx-Signature", hex.EncodeToString(m.Sum(nil)))
	c := h.Client
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode >= 400 && res.StatusCode < 500:
		var p struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(raw, &p)
		return fmt.Errorf("%w: %s", ErrRejected, firstNonEmpty(p.Detail, res.Status))
	case res.StatusCode >= 300:
		return fmt.Errorf("paquetería: %s", res.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Rates implements Provider.
func (h HTTP) Rates(ctx context.Context, r RateRequest) ([]Rate, error) {
	var out struct {
		Rates []Rate `json:"rates"`
	}
	return out.Rates, h.post(ctx, "/rates", r, &out)
}

// BuyLabel implements Provider.
func (h HTTP) BuyLabel(ctx context.Context, r LabelRequest) (Label, error) {
	var l Label
	if err := h.post(ctx, "/labels", r, &l); err != nil {
		return l, err
	}
	if l.TrackingNumber == "" {
		return l, fmt.Errorf("%w: la paquetería no devolvió número de guía", ErrRejected)
	}
	if u, err := url.Parse(l.LabelURL); l.LabelURL != "" && (err != nil || u.Scheme != "https") {
		l.LabelURL = "" // only https links are shown to people
	}
	return l, nil
}

// Track implements Provider.
func (h HTTP) Track(ctx context.Context, tracking string) (Tracking, error) {
	var t Tracking
	return t, h.post(ctx, "/track", map[string]string{"trackingNumber": tracking}, &t)
}

// Cancel implements Provider.
func (h HTTP) Cancel(ctx context.Context, tracking string) error {
	return h.post(ctx, "/cancel", map[string]string{"trackingNumber": tracking}, nil)
}

// ---------- live rates with a short cache ----------

// Quoter answers live rates within a time limit, from a 5-minute cache when
// it can. A failure is cached for 30 seconds so a dead carrier is not asked
// on every cart.
type Quoter struct {
	TTL, FailTTL, Timeout time.Duration
	mu                    sync.Mutex
	cache                 map[string]cached
}

type cached struct {
	rates []Rate
	ok    bool
	until time.Time
}

// Quote returns the cheapest rate of p for r, or ok=false (use the fallback).
func (q *Quoter) Quote(ctx context.Context, key string, p Provider, r RateRequest) (Rate, bool) {
	now := time.Now()
	ck := fmt.Sprintf("%s|%s|%s|%s|%d", key, r.From.PostalCode, r.To.Country, r.To.PostalCode, (r.WeightGrams+499)/500)
	q.mu.Lock()
	if q.cache == nil {
		q.cache = map[string]cached{}
	}
	c, hit := q.cache[ck]
	q.mu.Unlock()
	if !hit || now.After(c.until) {
		timeout := q.Timeout
		if timeout == 0 {
			timeout = 800 * time.Millisecond
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		rates, err := p.Rates(cctx, r)
		cancel()
		c = cached{rates: rates, ok: err == nil && len(rates) > 0}
		ttl := q.TTL
		if ttl == 0 {
			ttl = 5 * time.Minute
		}
		if !c.ok {
			ttl = q.FailTTL
			if ttl == 0 {
				ttl = 30 * time.Second
			}
		}
		c.until = now.Add(ttl)
		q.mu.Lock()
		if len(q.cache) > 50000 {
			q.cache = map[string]cached{} // bounded memory
		}
		q.cache[ck] = c
		q.mu.Unlock()
	}
	if !c.ok {
		return Rate{}, false
	}
	best := c.rates[0]
	for _, x := range c.rates[1:] {
		if x.Amount < best.Amount {
			best = x
		}
	}
	return best, true
}

// Sign is the signature an app must check (exported for apps written in Go and tests).
func Sign(secret []byte, ts string, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(ts + "."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
