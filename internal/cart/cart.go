// Package cart simulates a cart: prices, availability, promotions, shipping
// and taxes in one call, with an explicit grand total (audit finding H7:
// VTEX totals exclude shipping and its quantity limits are hidden).
package cart

import (
	"time"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/promo"
)

// Channel configures one sales channel. Every rule is visible through the API.
type Channel struct {
	ID            string         `json:"id"`
	Name          string         `json:"name,omitempty"`
	Currency      money.Currency `json:"currency"`
	Locale        string         `json:"locale,omitempty"`    // es-MX
	Country       string         `json:"country,omitempty"`   // MX
	TaxRegion     string         `json:"taxRegion,omitempty"` // tax region used when the destination matches no specific one
	PriceList     string         `json:"priceList,omitempty"` // reserved: price list of the channel
	TaxBps        int64          `json:"taxBps"`              // legacy rate, used only while the store has no tax regions
	MaxQtyPerItem int64          `json:"maxQtyPerItem"`
	Shipping      ShippingRate   `json:"shipping"` // legacy single rate, used only while the store has no shipping options
}

// ShippingRate is the legacy single rate of a channel. Stores that configure
// shipping options (internal/logistics) no longer use it.
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
	// ShippingOption is the option the shopper chose. Empty or unavailable:
	// the cheapest available delivery is used.
	ShippingOption string `json:"shippingOption,omitempty"`
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
	TaxClass     string       `json:"taxClass,omitempty"`
}

// Shipping is the selected delivery option.
type Shipping struct {
	ID           string              `json:"id,omitempty"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind,omitempty"`
	Carrier      string              `json:"carrier,omitempty"`
	Price        money.Money         `json:"price"`
	PriceReason  string              `json:"priceReason,omitempty"`
	EstimateDays int                 `json:"estimateDays"`
	Estimate     *logistics.Estimate `json:"estimate,omitempty"`
	WeightGrams  int64               `json:"weightGrams"`
	Quote        *logistics.Quote    `json:"-"`
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
	// ShippingOptions lists every option of the store for this cart, available
	// or not, each with its reason.
	ShippingOptions []logistics.Quote    `json:"shippingOptions"`
	Tax             *logistics.TaxResult `json:"tax,omitempty"`
	Totals          Totals               `json:"totals"`
	Messages        []Message            `json:"messages"`
}

// Service wires the domain modules together.
type Service struct {
	Catalog   *catalog.Store
	Inventory *inventory.Service
	Pricing   *pricing.Service
	Promos    *promo.Engine
	Channels  map[string]Channel
	Logistics *logistics.Config // zones, options, estimates and tax regions; nil = legacy channel rules
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
	res := Result{Channel: ch.ID, Lines: []Line{}, Messages: []Message{}, ShippingOptions: []logistics.Quote{}}
	var profiles []string
	var lineClass []string // tax class of each priced line, parallel to promoLines
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
		class := s.Logistics.ProductClassOf(p.ID)
		if class.TaxClass != logistics.TaxGeneral {
			line.TaxClass = class.TaxClass
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
		profiles = append(profiles, class.ShippingProfile)
		lineClass = append(lineClass, class.TaxClass)
	}

	discounts := money.Zero(cur)
	if s.Promos != nil {
		res.Promotions, discounts = s.Promos.Evaluate(now(), ch.ID, promoLines, items)
	}
	afterDiscount, _ := items.Sub(discounts)

	shipping := money.Zero(cur)
	switch {
	case weight == 0:
	case s.Logistics.HasOptions():
		res.ShippingOptions = s.Logistics.Quotes(logistics.QuoteInput{
			Country: ch.Country, PostalCode: req.PostalCode, Subtotal: afterDiscount.Amount, WeightGrams: weight,
			Profiles: profiles, Currency: string(cur), Now: now(),
		})
		if i := logistics.Choose(res.ShippingOptions, req.ShippingOption); i >= 0 {
			q := res.ShippingOptions[i]
			// Without a postal code only pickup can be chosen, and only explicitly.
			if req.PostalCode != "" || (q.Kind == logistics.KindPickup && q.ID == req.ShippingOption) {
				shipping = money.New(q.Price, cur)
				res.Shipping = &Shipping{ID: q.ID, Name: q.Name, Kind: q.Kind, Carrier: q.Carrier, Price: shipping, PriceReason: q.PriceReason,
					EstimateDays: q.EstimateDays, Estimate: q.Estimate, WeightGrams: weight, Quote: &q}
				if req.ShippingOption != "" && req.ShippingOption != q.ID {
					res.Messages = append(res.Messages, Message{Code: "shipping_option_changed", Text: "La opción de entrega que elegiste ya no está disponible; se eligió " + q.Name})
				}
			}
		}
		switch {
		case res.Shipping != nil:
		case req.PostalCode == "":
			res.Messages = append(res.Messages, Message{Code: "postal_code_required", Text: "Ingresa un código postal para calcular el envío"})
		default:
			res.Messages = append(res.Messages, Message{Code: "no_shipping_option", Text: "No hay opciones de entrega para el código postal " + req.PostalCode})
		}
	case req.PostalCode != "":
		// Legacy: one rate per channel, by weight.
		sh := ch.Shipping
		kg := (weight + 999) / 1000
		price, _ := sh.Base.Add(sh.PerKg.Mul(kg))
		reason := "Por peso: base " + logistics.FormatMoney(sh.Base.Amount) + " + " + logistics.FormatMoney(sh.PerKg.Amount) + " por kg"
		if !sh.FreeOver.IsZero() && afterDiscount.Amount >= sh.FreeOver.Amount {
			price = money.Zero(cur)
			reason = "Gratis: el pedido alcanza el mínimo de " + logistics.FormatMoney(sh.FreeOver.Amount)
		}
		shipping = price
		q := logistics.Quote{ID: "channel", Name: sh.Name, Kind: logistics.KindDelivery, Available: true, Reason: "Tarifa única del canal de venta",
			Price: price.Amount, PriceReason: reason, Free: price.IsZero(), EstimateDays: sh.EstimateDays}
		res.ShippingOptions = []logistics.Quote{q}
		res.Shipping = &Shipping{ID: q.ID, Name: sh.Name, Kind: q.Kind, Price: price, PriceReason: reason, EstimateDays: sh.EstimateDays, WeightGrams: weight, Quote: &q}
	default:
		res.Messages = append(res.Messages, Message{Code: "postal_code_required", Text: "Ingresa un código postal para calcular el envío"})
	}

	grand, _ := afterDiscount.Add(shipping)
	// Prices include tax. The tax inside each amount depends on the class of
	// the product and on the region of the destination. Discounts are spread
	// over the lines in proportion to their totals.
	parts := make([]logistics.TaxPart, 0, len(promoLines)+1)
	left := discounts.Amount
	for i, pl := range promoLines {
		share := int64(0)
		if items.Amount > 0 {
			share = discounts.Amount * pl.LineTotal.Amount / items.Amount
		}
		if i == len(promoLines)-1 {
			share = left
		}
		left -= share
		parts = append(parts, logistics.TaxPart{Class: lineClass[i], Gross: pl.LineTotal.Amount - share})
	}
	parts = append(parts, logistics.TaxPart{Class: logistics.TaxGeneral, Gross: shipping.Amount})
	taxRes := s.Logistics.Tax(ch.Country, req.PostalCode, ch.TaxRegion, ch.TaxBps, parts)
	res.Tax = &taxRes
	tax := money.New(taxRes.Total, cur)
	res.Totals = Totals{Items: items, Discounts: discounts, Shipping: shipping, Tax: tax, Grand: grand}
	if res.Promotions == nil {
		res.Promotions = []promo.Result{}
	}
	return res, nil
}
