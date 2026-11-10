// Package promo evaluates promotions and always explains the outcome: every
// promotion in scope comes back as applied or not applied, with the reason
// (VTEX gives no explanation of why a promotion did or did not apply).
package promo

import (
	"fmt"
	"time"

	"github.com/helloghc/apexx/internal/money"
)

// Line is the input the engine sees for one cart line.
type Line struct {
	SKU       string
	Qty       int64
	LineTotal money.Money
}

// Result explains one promotion's outcome.
type Result struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Applied  bool        `json:"applied"`
	Reason   string      `json:"reason"`
	Discount money.Money `json:"discount"`
}

// Rule is a promotion type.
type Rule interface {
	Evaluate(now time.Time, channel string, lines []Line, subtotal money.Money) Result
}

// Window limits when and where a promotion runs.
type Window struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Channels []string  `json:"channels,omitempty"` // empty = all channels
	From     time.Time `json:"from,omitzero"`
	To       time.Time `json:"to,omitzero"`
}

func (w Window) check(now time.Time, channel string) (string, bool) {
	if !w.From.IsZero() && now.Before(w.From) {
		return "La promoción todavía no empieza (" + w.From.Format(time.RFC3339) + ")", false
	}
	if !w.To.IsZero() && now.After(w.To) {
		return "La promoción terminó (" + w.To.Format(time.RFC3339) + ")", false
	}
	if len(w.Channels) > 0 {
		for _, c := range w.Channels {
			if c == channel {
				return "", true
			}
		}
		return "El canal de venta " + channel + " no participa", false
	}
	return "", true
}

// PercentOverThreshold gives Bps basis points off the subtotal when it reaches Min.
type PercentOverThreshold struct {
	Window
	Bps int64       `json:"bps"`
	Min money.Money `json:"min"`
}

// Evaluate implements Rule.
func (p PercentOverThreshold) Evaluate(now time.Time, channel string, _ []Line, subtotal money.Money) Result {
	r := Result{ID: p.ID, Name: p.Name, Discount: money.Zero(subtotal.Currency)}
	if why, ok := p.check(now, channel); !ok {
		r.Reason = why
		return r
	}
	if subtotal.Currency != p.Min.Currency {
		r.Reason = "La moneda del carrito no coincide con la de la promoción"
		return r
	}
	if subtotal.Amount < p.Min.Amount {
		r.Reason = fmt.Sprintf("Faltan %d (unidades menores) para alcanzar el mínimo", p.Min.Amount-subtotal.Amount)
		return r
	}
	r.Applied = true
	r.Discount = subtotal.Percent(p.Bps)
	r.Reason = fmt.Sprintf("%.2f%% de descuento por superar el mínimo", float64(p.Bps)/100)
	return r
}

// BuyXPayY makes every X units of SKU cost Y units (e.g. 3x2).
type BuyXPayY struct {
	Window
	SKU string `json:"sku"`
	X   int64  `json:"x"`
	Y   int64  `json:"y"`
}

// Evaluate implements Rule.
func (b BuyXPayY) Evaluate(now time.Time, channel string, lines []Line, subtotal money.Money) Result {
	r := Result{ID: b.ID, Name: b.Name, Discount: money.Zero(subtotal.Currency)}
	if why, ok := b.check(now, channel); !ok {
		r.Reason = why
		return r
	}
	for _, l := range lines {
		if l.SKU != b.SKU {
			continue
		}
		groups := l.Qty / b.X
		if groups == 0 {
			r.Reason = fmt.Sprintf("Agrega %d unidad(es) más de %s para activar %dx%d", b.X-l.Qty%b.X, b.SKU, b.X, b.Y)
			return r
		}
		unit := l.LineTotal.Amount / l.Qty
		r.Applied = true
		r.Discount = money.New(groups*(b.X-b.Y)*unit, subtotal.Currency)
		r.Reason = fmt.Sprintf("%dx%d aplicado %d vez/veces", b.X, b.Y, groups)
		return r
	}
	r.Reason = "El carrito no contiene " + b.SKU
	return r
}

// Engine evaluates all rules in order.
type Engine struct{ Rules []Rule }

// Evaluate returns one Result per rule and the total discount.
func (e *Engine) Evaluate(now time.Time, channel string, lines []Line, subtotal money.Money) ([]Result, money.Money) {
	total := money.Zero(subtotal.Currency)
	out := make([]Result, 0, len(e.Rules))
	for _, rule := range e.Rules {
		res := rule.Evaluate(now, channel, lines, subtotal)
		if res.Applied {
			if t, err := total.Add(res.Discount); err == nil {
				total = t
			}
		}
		out = append(out, res)
	}
	if total.Amount > subtotal.Amount {
		total.Amount = subtotal.Amount
	}
	return out, total
}
