package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/resilience"
	"github.com/helloghc/apexx/internal/store"
)

var (
	timeNow       = time.Now
	jsonUnmarshal = json.Unmarshal
)

// PaymentAccounts resolves the Stripe provider of each store.
//   - own_keys: the store's own secret key (decrypted with its tenant key).
//   - connect:  the platform key acting on the store's connected account.
//   - Sandbox:  when set, every store uses the sandbox (development and tests).
type PaymentAccounts struct {
	DB                     *store.DB
	Keys                   *crypto.Keyring
	PlatformKey            string // STRIPE_SECRET_KEY of the platform (Connect)
	PlatformWebhookSecret  string
	PlatformPublishableKey string // pk_ of the platform, used by Stripe.js for Connect stores
	PlatformFeeBps         int64
	Sandbox                payments.Provider
	Breakers               *resilience.Breakers

	mu    sync.Mutex
	cache map[string]orders.Account
}

// Forget drops the cached account of tenant (after it changes).
func (pa *PaymentAccounts) Forget(tenant string) {
	pa.mu.Lock()
	delete(pa.cache, tenant)
	pa.mu.Unlock()
}

// Resolve implements orders.Service.Accounts.
func (pa *PaymentAccounts) Resolve(ctx context.Context, tenant string) (orders.Account, error) {
	pa.mu.Lock()
	if a, ok := pa.cache[tenant]; ok {
		pa.mu.Unlock()
		return a, nil
	}
	pa.mu.Unlock()
	row, err := pa.DB.GetPaymentAccount(ctx, tenant)
	if err != nil {
		if pa.Sandbox != nil && errors.Is(err, store.ErrNotFound) {
			return orders.Account{Provider: pa.wrap(tenant, pa.Sandbox)}, nil
		}
		if errors.Is(err, store.ErrNotFound) {
			return orders.Account{}, orders.ErrNoPaymentAccount
		}
		// The database could not answer: that is not "the store has no account".
		return orders.Account{}, err
	}
	var acct orders.Account
	switch {
	case pa.Sandbox != nil:
		acct = orders.Account{Provider: pa.wrap(tenant, pa.Sandbox), FeeBps: row.FeeBps}
	case row.Mode == "own_keys":
		sk, err := pa.Keys.Decrypt(tenant, row.SecretEnc, "stripe-secret")
		if err != nil {
			return orders.Account{}, err
		}
		acct = orders.Account{Provider: pa.wrap(tenant, payments.NewStripe(string(sk)))}
	case row.Mode == "connect" && pa.PlatformKey != "":
		acct = orders.Account{Provider: pa.wrap(tenant, payments.NewStripe(pa.PlatformKey)), Account: row.AccountID, FeeBps: row.FeeBps}
	default:
		return orders.Account{}, orders.ErrNoPaymentAccount
	}
	pa.mu.Lock()
	if pa.cache == nil {
		pa.cache = map[string]orders.Account{}
	}
	pa.cache[tenant] = acct
	pa.mu.Unlock()
	return acct, nil
}

// WebhookSecret returns the Stripe webhook signing secret for tenant.
func (pa *PaymentAccounts) WebhookSecret(ctx context.Context, tenant string) (string, error) {
	row, err := pa.DB.GetPaymentAccount(ctx, tenant)
	if err != nil {
		return "", err
	}
	if len(row.WebhookEnc) > 0 {
		b, err := pa.Keys.Decrypt(tenant, row.WebhookEnc, "stripe-webhook")
		return string(b), err
	}
	return pa.PlatformWebhookSecret, nil
}

// breakerProvider guards a provider with a per-tenant circuit breaker so a
// failing account fails fast instead of piling up requests.
type breakerProvider struct {
	payments.Provider
	b *resilience.Breaker
}

func (pa *PaymentAccounts) wrap(tenant string, p payments.Provider) payments.Provider {
	if pa.Breakers == nil {
		return p
	}
	return breakerProvider{Provider: p, b: pa.Breakers.Get(tenant + "/" + p.Name())}
}

func (bp breakerProvider) Charge(ctx context.Context, r payments.ChargeRequest) (payments.Charge, error) {
	var c payments.Charge
	err := bp.b.Do(func() error {
		var e error
		c, e = bp.Provider.Charge(ctx, r)
		return e
	})
	return c, err
}

func (bp breakerProvider) Refund(ctx context.Context, account, ref string, amount int64, key string) error {
	return bp.b.Do(func() error { return bp.Provider.Refund(ctx, account, ref, amount, key) })
}

func (bp breakerProvider) Invoice(ctx context.Context, r payments.InvoiceRequest) (payments.Invoice, error) {
	var inv payments.Invoice
	err := bp.b.Do(func() error {
		var e error
		inv, e = bp.Provider.Invoice(ctx, r)
		return e
	})
	return inv, err
}

// Transfer lets seller payouts through the same breaker. A rejected account
// is an answer, not an outage, so it does not count against the breaker.
func (bp breakerProvider) Transfer(ctx context.Context, account, destination string, amount int64, currency, key string) (string, error) {
	tr, ok := bp.Provider.(payments.Transferer)
	if !ok {
		return "", payments.ErrDeclined
	}
	var ref string
	var declined error
	err := bp.b.Do(func() error {
		var e error
		ref, e = tr.Transfer(ctx, account, destination, amount, currency, key)
		if errors.Is(e, payments.ErrDeclined) {
			declined = e
			return nil
		}
		return e
	})
	if declined != nil {
		return "", declined
	}
	return ref, err
}
