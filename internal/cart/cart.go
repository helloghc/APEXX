// Package cart simulates a cart: prices, availability, promotions, shipping
// and taxes in one call, with an explicit grand total (audit finding H7:
// VTEX totals exclude shipping and its quantity limits are hidden).
package cart

import (
	"strings"
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
	Channel       string   `json:"channel"`
	PostalCode    string   `json:"postalCode,omitempty"`
	Items         []Item   `json:"items"`
	Coupons       []string `json:"coupons,omitempty"`       // codes typed by the shopper
	Email         string   `json:"email,omitempty"`         // optional; enables per-customer and email rules
	PaymentMethod string   `json:"paymentMethod,omitempty"` // "card" once chosen

	// Set by the server, never by clients.
	Lookup  *promo.Lookup     `json:"-"` // coupons and per-customer usage resolved from the database
	At      time.Time         `json:"-"` // evaluate as of this instant (admin simulator)
	Explain bool              `json:"-"` // include outcomes only the merchant should see
	Extra   []promo.Promotion `json:"-"` // unsaved definitions to preview
	Only    []string          `json:"-"` // evaluate only these promotions
	Group   string            `json:"-"` // customer group for price lists (placeholder)
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
	Discount     *money.Money `json:"discount,omitempty"`  // promotions that fall on this line
	PriceList    string       `json:"priceList,omitempty"` // name of the price list that set the unit price
	Gift         bool         `json:"gift,omitempty"`      // added by a promotion at no cost
	PromotionID  string       `json:"promotionId,omitempty"`
}

// Shipping is the selected delivery option.
type Shipping struct {
	Name         string       `json:"name"`
	Price        money.Money  `json:"price"`              // what the shopper pays
	Original     *money.Money `json:"original,omitempty"` // before a free-shipping promotion
	EstimateDays int          `json:"estimateDays"`
	WeightGrams  int64        `json:"weightGrams"`
}

// Totals always add up: Grand = Items - Discounts + Shipping. Shipping is
// already net of ShippingDiscount, which is reported for information.
type Totals struct {
	Items            money.Money `json:"items"`
	Discounts        money.Money `json:"discounts"`
	Shipping         money.Money `json:"shipping"`
	ShippingDiscount money.Money `json:"shippingDiscount"`
	Tax              money.Money `json:"taxIncluded"`
	Grand            money.Money `json:"grand"`
}

// Result is the simulation output.
type Result struct {
	Channel    string               `json:"channel"`
	Lines      []Line               `json:"lines"`
	Promotions []promo.Result       `json:"promotions"`
	Coupons    []promo.CouponResult `json:"coupons"`
	Shipping   *Shipping            `json:"shipping,omitempty"`
	Totals     Totals               `json:"totals"`
	Messages   []Message            `json:"messages"`
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

// collections reads the catalog hook for collections: the product attribute
// "collections" (comma separated) until the catalog has them first-class.
func collections(p catalog.Product) []string {
	raw := p.Attributes["collections"]
	if raw == "" {
		raw = p.Attributes["collection"]
	}
	if raw == "" {
		return nil
	}
	var out []string
	for _, c := range strings.Split(raw, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// Simulate prices req without reserving stock.
func (s *Service) Simulate(req Request) (Result, error) {
	ch, ok := s.Channels[req.Channel]
	if !ok {
		return Result{}, ErrUnknownChannel{req.Channel}
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	if !req.At.IsZero() {
		now = req.At
	}
	cur := ch.Currency
	res := Result{Channel: ch.ID, Lines: []Line{}, Messages: []Message{}, Promotions: []promo.Result{}, Coupons: []promo.CouponResult{}}
	items := money.Zero(cur)
	var weight int64
	var promoLines []promo.Line
	var lineOf []int // promo line -> index in res.Lines

	for _, it := range req.Items {
		v, p, err := s.Catalog.VariantBySKU(it.SKU)
		if err != nil || p.Status != "active" {
			res.Messages = append(res.Messages, Message{Code: "item_not_found", SKU: it.SKU, Text: "El producto no existe o no está activo"})
			continue
		}
		q, ok := s.Pricing.ResolveAt(ch.ID, it.SKU, max(it.Qty, 1), req.Group, now)
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
			if q2, ok := s.Pricing.ResolveAt(ch.ID, it.SKU, max(qty, 1), req.Group, now); ok {
				q = q2
			}
		}
		line := Line{
			SKU: it.SKU, Title: p.Title + " " + v.Title, RequestedQty: it.Qty, Qty: qty,
			Unit: q.Unit, CompareAt: q.CompareAt, Total: q.Unit.Mul(qty), Available: avail, PriceList: q.ListName,
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
		promoLines = append(promoLines, promo.Line{SKU: it.SKU, ProductID: p.ID, Title: line.Title, Brand: p.Brand, Categories: p.Categories,
			Collections: collections(p), Qty: qty, LineTotal: line.Total})
		lineOf = append(lineOf, len(res.Lines)-1)
	}

	out := promo.Outcome{Discount: money.Zero(cur), ShippingDiscount: money.Zero(cur)}
	if s.Promos != nil {
		out = s.Promos.Evaluate(promo.Context{Now: now, Channel: ch.ID, Currency: cur, Lines: promoLines, Email: req.Email,
			PaymentMethod: req.PaymentMethod, Coupons: req.Coupons, Lookup: req.Lookup, Extra: req.Extra, Only: req.Only})
		// Gifts that are not in the cart are added as free lines, if there is stock.
		for _, g := range append([]promo.Gift(nil), out.Gifts...) {
			if g.InCart {
				continue
			}
			v, p, err := s.Catalog.VariantBySKU(g.SKU)
			avail := s.Inventory.Available(g.SKU)
			if err != nil || p.Status != "active" || avail < g.Qty {
				out.Unapply(g.PromotionID, promo.CodeGiftUnavailable, "El regalo de esta promoción está agotado por ahora.")
				continue
			}
			zero := money.Zero(cur)
			res.Lines = append(res.Lines, Line{SKU: g.SKU, Title: p.Title + " " + v.Title + " (regalo)", RequestedQty: g.Qty, Qty: g.Qty,
				Unit: zero, Total: zero, Available: avail, Gift: true, PromotionID: g.PromotionID})
			weight += v.WeightGrams * g.Qty
		}
	}
	discounts := out.Discount
	if discounts.Amount > items.Amount {
		discounts.Amount = items.Amount
	}
	afterDiscount, _ := items.Sub(discounts)

	shipping, shipDisc := money.Zero(cur), money.Zero(cur)
	if weight > 0 && req.PostalCode != "" {
		sh := ch.Shipping
		kg := (weight + 999) / 1000
		price, _ := sh.Base.Add(sh.PerKg.Mul(kg))
		free := !sh.FreeOver.IsZero() && afterDiscount.Amount >= sh.FreeOver.Amount
		if free {
			price = money.Zero(cur)
		}
		res.Shipping = &Shipping{Name: sh.Name, Price: price, EstimateDays: sh.EstimateDays, WeightGrams: weight}
		shipDisc = out.ApplyShipping(&price, free)
		if shipDisc.Amount > 0 {
			orig := price
			res.Shipping.Original = &orig
			res.Shipping.Price, _ = price.Sub(shipDisc)
		}
		shipping = res.Shipping.Price
	} else if weight > 0 {
		out.ApplyShipping(nil, false)
		res.Messages = append(res.Messages, Message{Code: "postal_code_required", Text: "Ingresa un código postal para calcular el envío"})
	}

	// Per-line discounts, so each line can show what it saved.
	perSKU := map[string]int64{}
	for _, r := range out.Results {
		if !r.Applied {
			continue
		}
		for _, l := range r.Lines {
			perSKU[l.SKU] += l.Amount
		}
	}
	for _, i := range lineOf {
		if d := perSKU[res.Lines[i].SKU]; d > 0 {
			m := money.New(d, cur)
			res.Lines[i].Discount = &m
			perSKU[res.Lines[i].SKU] = 0
		}
	}
	for _, r := range out.Results {
		if req.Explain || promo.ShopperVisible(r) {
			res.Promotions = append(res.Promotions, r)
		}
	}
	if out.Coupons != nil {
		res.Coupons = out.Coupons
	}

	grand, _ := afterDiscount.Add(shipping)
	// Tax included in a tax-inclusive price: grand * rate / (1 + rate).
	tax := money.Zero(cur)
	if ch.TaxBps > 0 {
		tax = money.New(grand.Amount*ch.TaxBps/(10000+ch.TaxBps), cur)
	}
	res.Totals = Totals{Items: items, Discounts: discounts, Shipping: shipping, ShippingDiscount: shipDisc, Tax: tax, Grand: grand}
	return res, nil
}
