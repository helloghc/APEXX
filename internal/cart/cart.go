// Package cart simulates a cart: prices, availability, promotions, shipping
// and taxes in one call, with an explicit grand total (audit finding H7:
// VTEX totals exclude shipping and its quantity limits are hidden).
package cart

import (
	"time"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/promo"
)

// Channel configures one sales channel. Every rule is visible through the API.
type Channel struct {
	ID            string         `json:"id"`
	Currency      money.Currency `json:"currency"`
	TaxBps        int64          `json:"taxBps"` // prices are tax-inclusive; tax is reported, not added
	MaxQtyPerItem int64          `json:"maxQtyPerItem"`
	Shipping      ShippingRate   `json:"shipping"`
}

// ShippingRate prices one package by total weight, not per unit.
type ShippingRate struct {
	Name         string      `json:"name"`
	Base         money.Money `json:"base"`
	PerKg        money.Money `json:"perKg"`
	FreeOver     money.Money `json:"freeOver"` // zero = never free
	EstimateDays int         `json:"estimateDays"`
}

// Request is a simulation input.
type Request struct {
	Channel    string `json:"channel"`
	PostalCode string `json:"postalCode,omitempty"`
	Items      []Item `json:"items"`
}

// Item is one requested line.
type Item struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// Message is a machine-readable notice about the cart.
type Message struct {
	Code string `json:"code"`
	SKU  string `json:"sku,omitempty"`
	Text string `json:"text"`
}

// Line is one priced cart line.
type Line struct {
	SKU          string       `json:"sku"`
	Title        string       `json:"title"`
	RequestedQty int64        `json:"requestedQty"`
	Qty          int64        `json:"qty"`
	Unit         money.Money  `json:"unit"`
	CompareAt    *money.Money `json:"compareAt,omitempty"`
	Total        money.Money  `json:"total"`
	Available    int64        `json:"available"`
}

// Shipping is the selected delivery option.
type Shipping struct {
	Name         string      `json:"name"`
	Price        money.Money `json:"price"`
	EstimateDays int         `json:"estimateDays"`
	WeightGrams  int64       `json:"weightGrams"`
}

// Totals always add up: Grand = Items - Discounts + Shipping.
type Totals struct {
	Items     money.Money `json:"items"`
	Discounts money.Money `json:"discounts"`
	Shipping  money.Money `json:"shipping"`
	Tax       money.Money `json:"taxIncluded"`
	Grand     money.Money `json:"grand"`
}

// Result is the simulation output.
type Result struct {
	Channel    string         `json:"channel"`
	Lines      []Line         `json:"lines"`
	Promotions []promo.Result `json:"promotions"`
	Shipping   *Shipping      `json:"shipping,omitempty"`
	Totals     Totals         `json:"totals"`
	Messages   []Message      `json:"messages"`
}

// Service wires the domain modules together.
type Service struct {
	Catalog   *catalog.Store
	Inventory *inventory.Service
	Pricing   *pricing.Service
	Promos    *promo.Engine
	Channels  map[string]Channel
	Now       func() time.Time
}

// ErrUnknownChannel is returned for a channel that is not configured.
type ErrUnknownChannel struct{ ID string }

func (e ErrUnknownChannel) Error() string { return "cart: unknown channel " + e.ID }

// Simulate prices req without reserving stock.
func (s *Service) Simulate(req Request) (Result, error) {
	ch, ok := s.Channels[req.Channel]
	if !ok {
		return Result{}, ErrUnknownChannel{req.Channel}
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	cur := ch.Currency
	res := Result{Channel: ch.ID, Lines: []Line{}, Messages: []Message{}}
	items := money.Zero(cur)
	var weight int64
	var promoLines []promo.Line

	for _, it := range req.Items {
		v, p, err := s.Catalog.VariantBySKU(it.SKU)
		if err != nil || p.Status != "active" {
			res.Messages = append(res.Messages, Message{Code: "item_not_found", SKU: it.SKU, Text: "El producto no existe o no está activo"})
			continue
		}
		q, ok := s.Pricing.Resolve(ch.ID, it.SKU, max(it.Qty, 1))
		if !ok {
			res.Messages = append(res.Messages, Message{Code: "item_not_priced", SKU: it.SKU, Text: "El producto no tiene precio en este canal"})
			continue
		}
		qty := it.Qty
		if ch.MaxQtyPerItem > 0 && qty > ch.MaxQtyPerItem {
			qty = ch.MaxQtyPerItem
			res.Messages = append(res.Messages, Message{Code: "max_qty_per_item", SKU: it.SKU, Text: "Se ajustó a la cantidad máxima por producto del canal"})
		}
		avail := s.Inventory.Available(it.SKU)
		if qty > avail {
			qty = avail
			res.Messages = append(res.Messages, Message{Code: "insufficient_stock", SKU: it.SKU, Text: "Se ajustó a las unidades disponibles"})
		}
		if qty != it.Qty {
			// The tier may change with the adjusted quantity.
			if q2, ok := s.Pricing.Resolve(ch.ID, it.SKU, max(qty, 1)); ok {
				q = q2
			}
		}
		line := Line{
			SKU: it.SKU, Title: p.Title + " " + v.Title, RequestedQty: it.Qty, Qty: qty,
			Unit: q.Unit, CompareAt: q.CompareAt, Total: q.Unit.Mul(qty), Available: avail,
		}
		res.Lines = append(res.Lines, line)
		if qty == 0 {
			continue
		}
		var err2 error
		if items, err2 = items.Add(line.Total); err2 != nil {
			return Result{}, err2
		}
		weight += v.WeightGrams * qty
		promoLines = append(promoLines, promo.Line{SKU: it.SKU, Qty: qty, LineTotal: line.Total})
	}

	discounts := money.Zero(cur)
	if s.Promos != nil {
		res.Promotions, discounts = s.Promos.Evaluate(now(), ch.ID, promoLines, items)
	}
	afterDiscount, _ := items.Sub(discounts)

	shipping := money.Zero(cur)
	if weight > 0 && req.PostalCode != "" {
		sh := ch.Shipping
		kg := (weight + 999) / 1000
		price, _ := sh.Base.Add(sh.PerKg.Mul(kg))
		if !sh.FreeOver.IsZero() && afterDiscount.Amount >= sh.FreeOver.Amount {
			price = money.Zero(cur)
		}
		shipping = price
		res.Shipping = &Shipping{Name: sh.Name, Price: price, EstimateDays: sh.EstimateDays, WeightGrams: weight}
	} else if weight > 0 {
		res.Messages = append(res.Messages, Message{Code: "postal_code_required", Text: "Ingresa un código postal para calcular el envío"})
	}

	grand, _ := afterDiscount.Add(shipping)
	// Tax included in a tax-inclusive price: grand * rate / (1 + rate).
	tax := money.Zero(cur)
	if ch.TaxBps > 0 {
		tax = money.New(grand.Amount*ch.TaxBps/(10000+ch.TaxBps), cur)
	}
	res.Totals = Totals{Items: items, Discounts: discounts, Shipping: shipping, Tax: tax, Grand: grand}
	if res.Promotions == nil {
		res.Promotions = []promo.Result{}
	}
	return res, nil
}
