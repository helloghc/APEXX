package payments

import (
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
	"time"
)

// Stripe calls the Stripe REST API directly (form-encoded, no SDK).
type Stripe struct {
	Key     string // secret key (sk_live_/sk_test_), from Secret Manager
	BaseURL string // https://api.stripe.com
	HTTP    *http.Client
}

// NewStripe creates a Stripe provider.
func NewStripe(key string) *Stripe {
	return &Stripe{Key: key, BaseURL: "https://api.stripe.com", HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Name implements Provider.
func (s *Stripe) Name() string { return "stripe" }

type stripeError struct {
	Error struct {
		Type        string `json:"type"`
		Code        string `json:"code"`
		DeclineCode string `json:"decline_code"`
		Message     string `json:"message"`
	} `json:"error"`
}

func (s *Stripe) call(ctx context.Context, method, path, account, idem string, form url.Values, out any) error {
	var body io.Reader
	if form != nil && method != http.MethodGet {
		body = strings.NewReader(form.Encode())
	}
	u := s.BaseURL + path
	if method == http.MethodGet && form != nil {
		u += "?" + form.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.Key, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Stripe-Version", "2024-06-20")
	if account != "" {
		req.Header.Set("Stripe-Account", account)
	}
	if idem != "" {
		req.Header.Set("Idempotency-Key", idem)
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var se stripeError
		_ = json.Unmarshal(b, &se)
		if se.Error.Type == "card_error" {
			return fmt.Errorf("%w: %s %s", ErrDeclined, se.Error.Code, se.Error.DeclineCode)
		}
		return fmt.Errorf("stripe %s %s: %d %s", method, path, resp.StatusCode, se.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type paymentIntent struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	Customer         string `json:"customer"`
	PaymentMethod    string `json:"payment_method"`
	LastPaymentError *struct {
		Code string `json:"code"`
	} `json:"last_payment_error"`
}

func (pi paymentIntent) charge() Charge {
	c := Charge{Ref: pi.ID}
	switch pi.Status {
	case "succeeded":
		c.Status = "captured"
	case "requires_action":
		c.Status = "requires_action"
	default:
		c.Status = "failed"
		if pi.LastPaymentError != nil {
			c.Reason = pi.LastPaymentError.Code
		}
	}
	return c
}

// Charge creates and confirms a PaymentIntent on the connected account.
func (s *Stripe) Charge(ctx context.Context, r ChargeRequest) (Charge, error) {
	f := url.Values{}
	f.Set("amount", strconv.FormatInt(r.Amount, 10))
	f.Set("currency", strings.ToLower(r.Currency))
	f.Set("payment_method", r.PaymentMethod)
	f.Set("confirm", "true")
	f.Set("automatic_payment_methods[enabled]", "true")
	f.Set("automatic_payment_methods[allow_redirects]", "never")
	f.Set("metadata[apexx_order]", r.OrderID)
	if r.Email != "" {
		f.Set("receipt_email", r.Email)
	}
	if r.FeeAmount > 0 {
		f.Set("application_fee_amount", strconv.FormatInt(r.FeeAmount, 10))
	}
	if r.Installments > 1 {
		// Meses sin intereses (Mexico): Stripe declines when the card has no such plan.
		f.Set("payment_method_options[card][installments][enabled]", "true")
		f.Set("payment_method_options[card][installments][plan][count]", strconv.Itoa(r.Installments))
		f.Set("payment_method_options[card][installments][plan][interval]", "month")
		f.Set("payment_method_options[card][installments][plan][type]", "fixed_count")
	}
	switch {
	case r.SaveCard:
		// The card is attached to a customer so later deliveries can be charged.
		cf := url.Values{}
		if r.Email != "" {
			cf.Set("email", r.Email)
		}
		cf.Set("metadata[apexx_order]", r.OrderID)
		var cus struct {
			ID string `json:"id"`
		}
		if err := s.call(ctx, http.MethodPost, "/v1/customers", r.Account, "cus-"+r.IdempotencyKey, cf, &cus); err != nil {
			return Charge{}, err
		}
		f.Set("customer", cus.ID)
		f.Set("setup_future_usage", "off_session")
	case r.OffSession:
		f.Set("customer", r.Customer)
		f.Set("off_session", "true")
	}
	var pi paymentIntent
	err := s.call(ctx, http.MethodPost, "/v1/payment_intents", r.Account, r.IdempotencyKey, f, &pi)
	if errors.Is(err, ErrDeclined) {
		return Charge{Status: "failed", Reason: err.Error()}, nil
	}
	if err != nil {
		return Charge{}, err
	}
	c := pi.charge()
	if r.SaveCard {
		c.Customer, c.PaymentMethod = pi.Customer, pi.PaymentMethod
	}
	return c, nil
}

// Refund refunds amount of a PaymentIntent.
func (s *Stripe) Refund(ctx context.Context, account, ref string, amount int64, idem string) error {
	f := url.Values{}
	f.Set("payment_intent", ref)
	f.Set("amount", strconv.FormatInt(amount, 10))
	if account != "" {
		f.Set("refund_application_fee", "true")
	}
	return s.call(ctx, http.MethodPost, "/v1/refunds", account, idem, f, nil)
}

// Lookup searches the PaymentIntent of an order by metadata.
func (s *Stripe) Lookup(ctx context.Context, account, orderID string) (Charge, bool, error) {
	f := url.Values{}
	f.Set("query", fmt.Sprintf("metadata['apexx_order']:'%s'", strings.ReplaceAll(orderID, "'", "")))
	var res struct {
		Data []paymentIntent `json:"data"`
	}
	if err := s.call(ctx, http.MethodGet, "/v1/payment_intents/search", account, "", f, &res); err != nil {
		return Charge{}, false, err
	}
	for _, pi := range res.Data {
		if pi.Status == "succeeded" {
			return pi.charge(), true, nil
		}
	}
	if len(res.Data) > 0 {
		return res.Data[0].charge(), true, nil
	}
	return Charge{}, false, nil
}

// Invoice issues a Stripe invoice for an order that was already paid
// (paid_out_of_band), so the customer gets a PDF and a hosted link.
func (s *Stripe) Invoice(ctx context.Context, r InvoiceRequest) (Invoice, error) {
	var cust struct {
		ID string `json:"id"`
	}
	cf := url.Values{}
	cf.Set("email", r.Email)
	cf.Set("name", r.Name)
	cf.Set("metadata[apexx_order]", r.OrderID)
	if err := s.call(ctx, http.MethodPost, "/v1/customers", r.Account, r.IdempotencyKey+"-customer", cf, &cust); err != nil {
		return Invoice{}, err
	}
	var inv struct {
		ID  string `json:"id"`
		URL string `json:"hosted_invoice_url"`
	}
	f := url.Values{}
	f.Set("customer", cust.ID)
	f.Set("currency", strings.ToLower(r.Currency))
	f.Set("auto_advance", "false")
	f.Set("metadata[apexx_order]", r.OrderID)
	if err := s.call(ctx, http.MethodPost, "/v1/invoices", r.Account, r.IdempotencyKey+"-invoice", f, &inv); err != nil {
		return Invoice{}, err
	}
	add := func(i int, desc string, qty, unit int64) error {
		lf := url.Values{}
		lf.Set("customer", cust.ID)
		lf.Set("invoice", inv.ID)
		lf.Set("currency", strings.ToLower(r.Currency))
		lf.Set("description", desc)
		lf.Set("quantity", strconv.FormatInt(qty, 10))
		lf.Set("unit_amount_decimal", strconv.FormatInt(unit, 10))
		return s.call(ctx, http.MethodPost, "/v1/invoiceitems", r.Account, fmt.Sprintf("%s-item-%d", r.IdempotencyKey, i), lf, nil)
	}
	for i, l := range r.Lines {
		if err := add(i, l.Description, l.Qty, l.UnitAmount); err != nil {
			return Invoice{}, err
		}
	}
	if r.Shipping > 0 {
		if err := add(len(r.Lines), "Envío", 1, r.Shipping); err != nil {
			return Invoice{}, err
		}
	}
	if r.Discount > 0 {
		if err := add(len(r.Lines)+1, "Descuentos", 1, -r.Discount); err != nil {
			return Invoice{}, err
		}
	}
	invID := inv.ID
	if err := s.call(ctx, http.MethodPost, "/v1/invoices/"+invID+"/finalize", r.Account, r.IdempotencyKey+"-finalize", url.Values{}, nil); err != nil {
		return Invoice{}, err
	}
	pf := url.Values{}
	pf.Set("paid_out_of_band", "true")
	if err := s.call(ctx, http.MethodPost, "/v1/invoices/"+invID+"/pay", r.Account, r.IdempotencyKey+"-pay", pf, &inv); err != nil {
		return Invoice{}, err
	}
	return Invoice{Ref: invID, URL: inv.URL}, nil
}

// VerifyWebhook checks a Stripe-Signature header (t=...,v1=...) against the
// endpoint secret with a 5-minute tolerance.
func VerifyWebhook(payload []byte, header, secret string, now time.Time) error {
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return errors.New("stripe webhook: malformed signature header")
	}
	if d := now.Sub(time.Unix(t, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("stripe webhook: timestamp outside tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(payload)))
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err == nil && hmac.Equal(got, want) {
			return nil
		}
	}
	return errors.New("stripe webhook: signature mismatch")
}
