package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeStripe(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *Stripe {
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	s := NewStripe("sk_test_dummy")
	s.BaseURL = srv.URL
	return s
}

func TestChargeSendsConnectAndIdempotency(t *testing.T) {
	s := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path != "/v1/payment_intents" || r.Header.Get("Stripe-Account") != "acct_123" ||
			r.Header.Get("Idempotency-Key") != "charge-ord_1" || r.Form.Get("amount") != "56700" ||
			r.Form.Get("currency") != "mxn" || r.Form.Get("application_fee_amount") != "567" ||
			r.Form.Get("metadata[apexx_order]") != "ord_1" || r.Form.Get("confirm") != "true" {
			t.Errorf("unexpected request %s %v %v", r.URL.Path, r.Header, r.Form)
		}
		if u, _, _ := r.BasicAuth(); u != "sk_test_dummy" {
			t.Errorf("auth %q", u)
		}
		fmt.Fprint(w, `{"id":"pi_1","status":"succeeded"}`)
	})
	c, err := s.Charge(context.Background(), ChargeRequest{Account: "acct_123", OrderID: "ord_1", Amount: 56700, Currency: "MXN",
		PaymentMethod: "pm_card_visa", FeeAmount: 567, IdempotencyKey: "charge-ord_1"})
	if err != nil || c.Status != "captured" || c.Ref != "pi_1" {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestChargeDeclined(t *testing.T) {
	s := fakeStripe(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(402)
		fmt.Fprint(w, `{"error":{"type":"card_error","code":"card_declined","decline_code":"insufficient_funds","message":"declined"}}`)
	})
	c, err := s.Charge(context.Background(), ChargeRequest{OrderID: "o", Amount: 1, Currency: "MXN", PaymentMethod: "pm_x", IdempotencyKey: "k"})
	if err != nil || c.Status != "failed" || !strings.Contains(c.Reason, "insufficient_funds") {
		t.Fatalf("%v %+v", err, c)
	}
}

func TestServerErrorIsNotADecline(t *testing.T) {
	s := fakeStripe(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, `{"error":{"type":"api_error","message":"boom"}}`)
	})
	_, err := s.Charge(context.Background(), ChargeRequest{OrderID: "o", Amount: 1, Currency: "MXN", PaymentMethod: "pm_x", IdempotencyKey: "k"})
	if err == nil || errors.Is(err, ErrDeclined) {
		t.Fatalf("a 500 must surface as an unknown outcome, got %v", err)
	}
}

func TestInvoiceFlow(t *testing.T) {
	var paths []string
	s := fakeStripe(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch {
		case r.URL.Path == "/v1/customers":
			fmt.Fprint(w, `{"id":"cus_1"}`)
		case r.URL.Path == "/v1/invoices":
			fmt.Fprint(w, `{"id":"in_1"}`)
		case strings.HasSuffix(r.URL.Path, "/pay"):
			_ = r.ParseForm()
			if r.Form.Get("paid_out_of_band") != "true" {
				t.Error("invoice must be marked paid out of band")
			}
			fmt.Fprint(w, `{"id":"in_1","hosted_invoice_url":"https://invoice.stripe.com/i/1"}`)
		default:
			fmt.Fprint(w, `{"id":"x"}`)
		}
	})
	inv, err := s.Invoice(context.Background(), InvoiceRequest{OrderID: "o", Currency: "MXN", Email: "a@b.c", Name: "A",
		Lines: []InvoiceLine{{Description: "Tenis", Qty: 2, UnitAmount: 1000}}, Shipping: 500, Discount: 100, IdempotencyKey: "invoice-o"})
	if err != nil || inv.URL == "" {
		t.Fatalf("%v %+v", err, inv)
	}
	want := "/v1/customers /v1/invoices /v1/invoiceitems /v1/invoiceitems /v1/invoiceitems /v1/invoices/in_1/finalize /v1/invoices/in_1/pay"
	if got := strings.Join(paths, " "); got != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestVerifyWebhook(t *testing.T) {
	payload := []byte(`{"type":"payment_intent.succeeded"}`)
	now := time.Unix(1_800_000_000, 0)
	mac := hmac.New(sha256.New, []byte("whsec_test"))
	fmt.Fprintf(mac, "%d.%s", now.Unix(), payload)
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
	if err := VerifyWebhook(payload, header, "whsec_test", now); err != nil {
		t.Fatal(err)
	}
	if VerifyWebhook(payload, header, "other", now) == nil {
		t.Fatal("wrong secret accepted")
	}
	if VerifyWebhook(payload, header, "whsec_test", now.Add(10*time.Minute)) == nil {
		t.Fatal("stale timestamp accepted")
	}
	if VerifyWebhook([]byte(`{}`), header, "whsec_test", now) == nil {
		t.Fatal("tampered payload accepted")
	}
}
