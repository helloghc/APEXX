// Package money represents monetary amounts as integer minor units plus an
// ISO 4217 currency code. Every API in Apexx uses this shape, so a price is
// never ambiguous between "1684.38" and "168438" (audit finding H5).
package money

import (
	"errors"
	"fmt"
)

// Currency is an ISO 4217 code such as "MXN".
type Currency string

// Money is an amount in minor units (cents for MXN/USD).
type Money struct {
	Amount   int64    `json:"amount"`
	Currency Currency `json:"currency"`
}

// ErrCurrencyMismatch is returned when combining amounts in different currencies.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// New builds a Money value.
func New(amount int64, c Currency) Money { return Money{Amount: amount, Currency: c} }

// Zero returns a zero amount in the given currency.
func Zero(c Currency) Money { return Money{Currency: c} }

// Add returns m + o.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	return Money{Amount: m.Amount + o.Amount, Currency: m.Currency}, nil
}

// Sub returns m - o.
func (m Money) Sub(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s - %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}
	return Money{Amount: m.Amount - o.Amount, Currency: m.Currency}, nil
}

// Mul multiplies by an integer quantity.
func (m Money) Mul(q int64) Money { return Money{Amount: m.Amount * q, Currency: m.Currency} }

// Percent returns bps basis points of m (10000 bps = 100%), rounded half up.
func (m Money) Percent(bps int64) Money {
	v := m.Amount * bps
	r := v / 10000
	if rem := v % 10000; rem*2 >= 10000 {
		r++
	}
	return Money{Amount: r, Currency: m.Currency}
}

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.Amount == 0 }
