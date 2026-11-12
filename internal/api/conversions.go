package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// Server-side conversions: a paid order is reported to the store's ad
// platforms from the server, only when the shopper accepted advertising,
// with the email and phone hashed (SHA-256) and the order id as event id so
// the platform drops a duplicate from the browser.

var (
	conversionsHTTPClient *http.Client // tests
	metaEventsURL         = "https://graph.facebook.com/v21.0"
	ga4CollectURL         = "https://www.google-analytics.com/mp/collect"
)

type adPlatform struct {
	Active   bool   `json:"active"`
	ID       string `json:"id"`                 // Meta pixel id / GA4 measurement id
	Secret   string `json:"secret,omitempty"`   // access token / API secret, encrypted at rest
	TestCode string `json:"testCode,omitempty"` // Meta test events
}

type adConfig struct {
	Meta adPlatform `json:"meta"`
	GA4  adPlatform `json:"ga4"`
}

var (
	pixelRe = regexp.MustCompile(`^[0-9]{5,20}$`)
	ga4Re   = regexp.MustCompile(`^G-[A-Z0-9]{4,16}$`)
)

func (s *Server) routesConversions(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/ad-conversions", s.authed("settings", auth.Read, noStorefront(s.getAdConversions)))
	handle("PUT /v1/settings/ad-conversions", s.authed("settings", auth.Admin, s.putAdConversions))
}

func (s *Server) adConfig(ctx context.Context, tenant string, decrypt bool) (adConfig, error) {
	var c adConfig
	if _, err := s.DB.GetDoc(ctx, tenant, "ads_conversions", "config", &c); err != nil {
		return c, err
	}
	for _, p := range []*adPlatform{&c.Meta, &c.GA4} {
		if p.Secret == "" {
			continue
		}
		if !decrypt {
			p.Secret = "set"
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(p.Secret)
		if err != nil {
			return c, err
		}
		plain, err := s.Keys.Decrypt(tenant, raw, "ads-secret")
		if err != nil {
			return c, err
		}
		p.Secret = string(plain)
	}
	return c, nil
}

func (s *Server) getAdConversions(w http.ResponseWriter, r *http.Request) {
	c, err := s.adConfig(r.Context(), principal(r).Tenant, false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"meta": map[string]any{"active": c.Meta.Active, "id": c.Meta.ID, "hasSecret": c.Meta.Secret != "", "testCode": c.Meta.TestCode},
		"ga4": map[string]any{"active": c.GA4.Active, "id": c.GA4.ID, "hasSecret": c.GA4.Secret != ""}}
	var stats struct{ Sent, Pending, Failed int }
	_ = s.DB.Tx(r.Context(), principal(r).Tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE status='sent'), count(*) FILTER (WHERE status='pending'), count(*) FILTER (WHERE status='failed')
			FROM conversion_jobs WHERE tenant_id=$1 AND created_at > now() - interval '30 days'`, principal(r).Tenant).Scan(&stats.Sent, &stats.Pending, &stats.Failed)
	})
	out["last30Days"] = stats
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) putAdConversions(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in adConfig
	if !decode(w, r, &in) {
		return
	}
	old, err := s.adConfig(r.Context(), pr.Tenant, false)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	var stored adConfig
	_, _ = s.DB.GetDoc(r.Context(), pr.Tenant, "ads_conversions", "config", &stored)
	var errs []apierr.FieldError
	in.Meta.ID, in.GA4.ID = strings.TrimSpace(in.Meta.ID), strings.ToUpper(strings.TrimSpace(in.GA4.ID))
	if in.Meta.ID != "" && !pixelRe.MatchString(in.Meta.ID) {
		errs = append(errs, apierr.FieldError{Field: "meta.id", Code: "invalid", Message: "El identificador del píxel son solo números"})
	}
	if in.GA4.ID != "" && !ga4Re.MatchString(in.GA4.ID) {
		errs = append(errs, apierr.FieldError{Field: "ga4.id", Code: "invalid", Message: "El identificador de medición empieza con G-"})
	}
	for _, x := range []struct {
		name     string
		p        *adPlatform
		prev     string
		hadSaved bool
	}{{"meta", &in.Meta, stored.Meta.Secret, old.Meta.Secret != ""}, {"ga4", &in.GA4, stored.GA4.Secret, old.GA4.Secret != ""}} {
		x.p.Secret = strings.TrimSpace(x.p.Secret)
		switch {
		case x.p.Secret == "":
			x.p.Secret = x.prev // keep the saved one
		default:
			enc, err := s.Keys.Encrypt(pr.Tenant, []byte(x.p.Secret), "ads-secret")
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
			x.p.Secret = base64.StdEncoding.EncodeToString(enc)
		}
		if x.p.Active && (x.p.ID == "" || x.p.Secret == "") {
			errs = append(errs, apierr.FieldError{Field: x.name + ".id", Code: "required", Message: "Para activarlo escribe el identificador y la llave"})
		}
	}
	if len(in.Meta.TestCode) > 40 {
		errs = append(errs, apierr.FieldError{Field: "meta.testCode", Code: "invalid", Message: "Código de prueba muy largo"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "ads_conversions", "config", "", in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.getAdConversions(w, r)
}

func sha(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// RunConversions sends the due conversion jobs, with retries.
func (s *Server) RunConversions(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (SELECT tenant_id, id FROM conversion_jobs WHERE status='pending' AND next_attempt <= now() ORDER BY next_attempt LIMIT 100 FOR UPDATE SKIP LOCKED)
		UPDATE conversion_jobs j SET next_attempt = now() + interval '2 minutes' FROM due WHERE j.tenant_id=due.tenant_id AND j.id=due.id
		RETURNING j.tenant_id, j.id, j.order_id, j.platform, j.attempts`)
	if err != nil {
		return 0, err
	}
	type job struct {
		tenant, id, order, platform string
		attempts                    int
	}
	var list []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.tenant, &j.id, &j.order, &j.platform, &j.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, j)
	}
	rows.Close()
	for _, j := range list {
		err := s.sendConversion(ctx, j.tenant, j.order, j.platform)
		_ = s.DB.Tx(ctx, j.tenant, func(tx pgx.Tx) error {
			if err == nil {
				_, e := tx.Exec(ctx, `UPDATE conversion_jobs SET status='sent', sent_at=now(), attempts=attempts+1, last_error='' WHERE tenant_id=$1 AND id=$2`, j.tenant, j.id)
				return e
			}
			status := "pending"
			if j.attempts+1 >= 6 {
				status = "failed"
			}
			_, e := tx.Exec(ctx, `UPDATE conversion_jobs SET status=$3, attempts=attempts+1, last_error=$4, next_attempt=now() + make_interval(mins => $5) WHERE tenant_id=$1 AND id=$2`,
				j.tenant, j.id, status, truncateStr(err.Error(), 300), 1<<min(j.attempts, 8))
			return e
		})
	}
	return len(list), nil
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) sendConversion(ctx context.Context, tenant, orderID, platform string) error {
	cfg, err := s.adConfig(ctx, tenant, true)
	if err != nil {
		return err
	}
	o, err := s.Orders.Get(ctx, tenant, orderID, true)
	if err != nil {
		return err
	}
	value := float64(o.GrandTotal-o.RefundedTotal) / 100
	client := conversionsHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	var req *http.Request
	switch platform {
	case "meta":
		if !cfg.Meta.Active {
			return nil
		}
		contents := []map[string]any{}
		for _, l := range o.Lines {
			contents = append(contents, map[string]any{"id": l.SKU, "quantity": l.Qty})
		}
		user := map[string]any{}
		if o.Customer != nil {
			if h := sha(o.Customer.Email); h != "" {
				user["em"] = []string{h}
			}
			if h := sha(strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, o.Customer.Phone)); h != "" {
				user["ph"] = []string{h}
			}
		}
		if h := sha(o.PostalCode); h != "" {
			user["zp"] = []string{h}
		}
		ev := map[string]any{"event_name": "Purchase", "event_time": o.CreatedAt.Unix(), "event_id": o.ID, "action_source": "website", "user_data": user,
			"custom_data": map[string]any{"currency": o.Currency, "value": value, "contents": contents, "content_type": "product", "order_id": o.DisplayNumber}}
		body := map[string]any{"data": []any{ev}}
		if cfg.Meta.TestCode != "" {
			body["test_event_code"] = cfg.Meta.TestCode
		}
		b, _ := json.Marshal(body)
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, metaEventsURL+"/"+url.PathEscape(cfg.Meta.ID)+"/events?access_token="+url.QueryEscape(cfg.Meta.Secret), bytes.NewReader(b))
	case "ga4":
		if !cfg.GA4.Active {
			return nil
		}
		items := []map[string]any{}
		for _, l := range o.Lines {
			items = append(items, map[string]any{"item_id": l.SKU, "item_name": l.Title, "quantity": l.Qty, "price": float64(l.Unit) / 100})
		}
		b, _ := json.Marshal(map[string]any{"client_id": "apexx." + o.ID, "events": []any{map[string]any{"name": "purchase",
			"params": map[string]any{"transaction_id": o.ID, "value": value, "currency": o.Currency, "shipping": float64(o.Shipping) / 100, "items": items}}}})
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, ga4CollectURL+"?measurement_id="+url.QueryEscape(cfg.GA4.ID)+"&api_secret="+url.QueryEscape(cfg.GA4.Secret), bytes.NewReader(b))
	default:
		return fmt.Errorf("plataforma desconocida %s", platform)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sin respuesta de %s", req.URL.Host)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("%s respondió %d", req.URL.Host, res.StatusCode)
	}
	return nil
}

var _ = orders.ErrNotFound
