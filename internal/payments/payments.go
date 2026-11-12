// Package payments talks to the payment provider. Stripe is the only provider:
// each store is a Stripe Connect account, so money settles directly to the
// merchant and Apexx takes an application fee. Card data never reaches Apexx
// (Stripe Elements / Payment Element tokenizes it in the browser), which keeps
// the platform in PCI DSS SAQ A scope.
package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Charge is the result of a payment attempt.
type Charge struct {
	Ref    string `json:"ref"`    // PaymentIntent id
	Status string `json:"status"` // captured | failed | requires_action
	Reason string `json:"reason,omitempty"`
	// Set when the card was saved (ChargeRequest.SaveCard).
	Customer      string `json:"customer,omitempty"`
	PaymentMethod string `json:"paymentMethod,omitempty"`
}

// ChargeRequest asks the provider to charge a tokenized payment method.
type ChargeRequest struct {
	Account        string // Stripe Connect account (acct_...)
	OrderID        string
	Amount         int64
	Currency       string
	PaymentMethod  string // pm_... created by Stripe.js in the browser
	FeeAmount      int64  // Apexx application fee
	IdempotencyKey string
	Email          string
	Installments   int // monthly payments without interest (0 or 1 = single payment)
	// SaveCard keeps the payment method for future charges of a subscription.
	SaveCard bool
	// Customer and OffSession charge a saved payment method without the shopper present.
	Customer   string
	OffSession bool
}

// InvoiceLine is one line of an invoice.
type InvoiceLine struct {
	Description string
	Qty         int64
	UnitAmount  int64
}

// InvoiceRequest asks for an invoice of an already-paid order.
type InvoiceRequest struct {
	Account        string
	OrderID        string
	Currency       string
	Email          string
	Name           string
	Lines          []InvoiceLine
	Shipping       int64
	Discount       int64
	IdempotencyKey string
}

// Invoice is an issued invoice.
type Invoice struct {
	Ref string `json:"ref"`
	URL string `json:"url"`
}

// Provider is implemented by Stripe and by the sandbox used in tests.
type Provider interface {
	Name() string
	Charge(ctx context.Context, r ChargeRequest) (Charge, error)
	Refund(ctx context.Context, account, ref string, amount int64, idempotencyKey string) error
	// Lookup finds the charge of an order (reconciliation after a crash).
	Lookup(ctx context.Context, account, orderID string) (Charge, bool, error)
	Invoice(ctx context.Context, r InvoiceRequest) (Invoice, error)
}

// ErrDeclined is returned when the provider declines the payment.
var ErrDeclined = errors.New("payments: declined")

// Sandbox is an in-memory provider that mimics Stripe test payment methods:
// "pm_card_visa" succeeds, "pm_card_chargeDeclined" is declined. It honours
// idempotency keys exactly like Stripe does.
type Sandbox struct {
	mu        sync.Mutex
	byKey     map[string]Charge
	byOrder   map[string]Charge
	refunds   map[string]int64
	invoices  map[string]Invoice
	transfers map[string]int64
	FailNext  error // injected failure for the next call (chaos tests)
	Calls     int
}

// NewSandbox creates a sandbox provider.
func NewSandbox() *Sandbox {
	return &Sandbox{byKey: map[string]Charge{}, byOrder: map[string]Charge{}, refunds: map[string]int64{}, invoices: map[string]Invoice{}}
}

// Name implements Provider.
func (s *Sandbox) Name() string { return "sandbox" }

func (s *Sandbox) fail() error {
	s.Calls++
	if err := s.FailNext; err != nil {
		s.FailNext = nil
		return err
	}
	return nil
}

// Charge implements Provider.
func (s *Sandbox) Charge(_ context.Context, r ChargeRequest) (Charge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail(); err != nil {
		return Charge{}, err
	}
	if c, ok := s.byKey[r.IdempotencyKey]; ok {
		return c, nil
	}
	c := Charge{Ref: "pi_sbx_" + r.OrderID, Status: "captured"}
	if r.PaymentMethod == "pm_card_chargeDeclined" {
		c.Status, c.Reason = "failed", "card_declined"
	}
	if r.SaveCard && c.Status == "captured" {
		c.Customer, c.PaymentMethod = "cus_sbx_"+r.OrderID, r.PaymentMethod
	}
	s.byKey[r.IdempotencyKey] = c
	s.byOrder[r.OrderID] = c
	return c, nil
}

// Refund implements Provider.
func (s *Sandbox) Refund(_ context.Context, _, ref string, amount int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail(); err != nil {
		return err
	}
	if _, ok := s.refunds[key]; ok {
		return nil
	}
	s.refunds[key] = amount
	return nil
}

// Refunded returns the total refunded through the sandbox.
func (s *Sandbox) Refunded() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var t int64
	for _, v := range s.refunds {
		t += v
	}
	return t
}

// Lookup implements Provider.
func (s *Sandbox) Lookup(_ context.Context, _, orderID string) (Charge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byOrder[orderID]
	return c, ok, nil
}

// Invoice implements Provider.
func (s *Sandbox) Invoice(_ context.Context, r InvoiceRequest) (Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail(); err != nil {
		return Invoice{}, err
	}
	if inv, ok := s.invoices[r.IdempotencyKey]; ok {
		return inv, nil
	}
	inv := Invoice{Ref: "in_sbx_" + r.OrderID, URL: "https://invoice.example/" + r.OrderID}
	s.invoices[r.IdempotencyKey] = inv
	return inv, nil
}

// Transferer is a provider that can send money to a third party's account
// (marketplace seller payouts). The idempotency key makes a retry safe.
type Transferer interface {
	Transfer(ctx context.Context, account, destination string, amount int64, currency, idempotencyKey string) (ref string, err error)
}

// Transfer implements Transferer. A destination that contains "fail" is
// declined, like an account Stripe cannot pay out to.
func (s *Sandbox) Transfer(_ context.Context, _, destination string, amount int64, _ string, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail(); err != nil {
		return "", err
	}
	if s.transfers == nil {
		s.transfers = map[string]int64{}
	}
	if strings.Contains(destination, "fail") {
		return "", ErrDeclined
	}
	if _, ok := s.transfers[key]; !ok {
		s.transfers[key] = amount
	}
	return "tr_" + key, nil
}

// Transferred returns the total sent to sellers through the sandbox.
func (s *Sandbox) Transferred() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, v := range s.transfers {
		n += v
	}
	return n
}

// CardSaver is a provider that keeps a card for later charges without a
// purchase (a company that pays its credit automatically at the due date).
type CardSaver interface {
	SaveCard(ctx context.Context, account, paymentMethod, email, idem string) (customer, method string, err error)
}

// SaveCard implements CardSaver. The declining test card is refused.
func (s *Sandbox) SaveCard(_ context.Context, _, pm, _, idem string) (string, string, error) {
	if pm == "" || pm == "pm_card_chargeDeclined" {
		return "", "", fmt.Errorf("%w: card_declined", ErrDeclined)
	}
	return "cus_sbx_" + idem, pm, nil
}
