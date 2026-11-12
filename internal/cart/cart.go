// Package cart simulates a cart: prices, availability, promotions, shipping
// and taxes in one call, with an explicit grand total (audit finding H7:
// VTEX totals exclude shipping and its quantity limits are hidden).
package cart

import (
	"fmt"
	"strings"
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

	// Market: a channel shoppers can pick in the store, with its own currency.
	Market       bool   `json:"market,omitempty"`       // offered in the storefront's country selector
	PriceFrom    string `json:"priceFrom,omitempty"`    // channel whose prices are converted when a SKU has none here
	RateMicros   int64  `json:"rateMicros,omitempty"`   // 1 unit of that currency = RateMicros/1e6 of this one
	Rounding     string `json:"rounding,omitempty"`     // "", unit, 99, ten
	TaxExclusive bool   `json:"taxExclusive,omitempty"` // prices before tax; tax is added at checkout
	Language     string `json:"language,omitempty"`     // storefront language of the market (es, en…)

	// Import duties charged at checkout (delivered duty paid) on products
	// whose origin is another country: DutyBps by default, DutyRules by
	// tariff code prefix, nothing while the items stay under DutyMinimis.
	DutyBps     int64      `json:"dutyBps,omitempty"`
	DutyMinimis int64      `json:"dutyMinimis,omitempty"`
	DutyRules   []DutyRule `json:"dutyRules,omitempty"`
}

// DutyRule is the duty rate of the tariff codes that start with HSPrefix.
type DutyRule struct {
	HSPrefix string `json:"hsPrefix"`
	Bps      int64  `json:"bps"`
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
	// RestrictGroup sells only what a price list of Group prices (a company
	// with a negotiated catalog).
	RestrictGroup bool `json:"-"`
	// TaxExempt removes tax from the order (a company with an exemption).
	TaxExempt bool `json:"-"`
	// ShippingOption is the option the shopper chose. Empty or unavailable:
	// the cheapest available delivery is used.
	ShippingOption string `json:"shippingOption,omitempty"`
}

// Item is one requested line.
type Item struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
	// Plan is the subscription frequency of the line ("1m"); "" = one-time purchase.
	Plan string `json:"plan,omitempty"`
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
	TaxClass     string       `json:"taxClass,omitempty"`
	Categories   []string     `json:"-"` // for approval rules by category
}

// Shipping is the selected delivery option.
type Shipping struct {
	ID           string              `json:"id,omitempty"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind,omitempty"`
	Carrier      string              `json:"carrier,omitempty"`
	Price        money.Money         `json:"price"`
	Original     *money.Money        `json:"original,omitempty"` // before a free-shipping promotion
	PriceReason  string              `json:"priceReason,omitempty"`
	EstimateDays int                 `json:"estimateDays"`
	Estimate     *logistics.Estimate `json:"estimate,omitempty"`
	WeightGrams  int64               `json:"weightGrams"`
	Quote        *logistics.Quote    `json:"-"`
}

// Totals always add up: Grand = Items - Discounts + Shipping + TaxAdjust + Fees.
// Shipping is already net of ShippingDiscount, which is reported for
// information. TaxAdjust is the tax added on top in markets whose prices
// exclude it, or the tax taken out for an exempt buyer (negative); it is zero
// for the usual price that already includes tax.
type Totals struct {
	Items            money.Money `json:"items"`
	Discounts        money.Money `json:"discounts"`
	Shipping         money.Money `json:"shipping"`
	ShippingDiscount money.Money `json:"shippingDiscount"`
	Tax              money.Money `json:"taxIncluded"`
	TaxAdjust        money.Money `json:"taxAdjust"`
	Fees             money.Money `json:"fees"` // extra charges and import duties
	Grand            money.Money `json:"grand"`
}

// Result is the simulation output.
type Result struct {
	Channel    string               `json:"channel"`
	Lines      []Line               `json:"lines"`
	Promotions []promo.Result       `json:"promotions"`
	Coupons    []promo.CouponResult `json:"coupons"`
	Shipping   *Shipping            `json:"shipping,omitempty"`
	// ShippingOptions lists every option of the store for this cart, available
	// or not, each with its reason.
	ShippingOptions []logistics.Quote `json:"shippingOptions"`
	// NoShipping: nothing in the cart has weight (digital products, services),
	// so there is nothing to deliver and no address is needed.
	NoShipping bool                 `json:"noShipping,omitempty"`
	Tax        *logistics.TaxResult `json:"tax,omitempty"`
	Fees       []Fee                `json:"fees,omitempty"`
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
	Logistics *logistics.Config    // zones, options, estimates and tax regions; nil = legacy channel rules
	Taxo      *catalog.Taxonomy    // attribute names, for promotions that target attribute values
	QtyRules  map[string][]QtyRule // product id -> quantity rules (general and per customer group)
	FeeRules  []FeeRule            // extra charges per order (handling, packaging, small order)
	// LiveRate asks a connected carrier for its price, in the store currency
	// (nil: no connected carriers). It must answer within its own time limit.
	LiveRate func(carrierID, country, postalCode string, weightGrams, value int64) (amount int64, days int, ok bool)
	// ExternalTax asks the store's tax service (nil or ok=false: Apexx's
	// own calculation). It must answer within its own time limit.
	ExternalTax func(country, postalCode, currency string, exclusive bool, parts []logistics.TaxPart) (logistics.TaxResult, bool)
	Now         func() time.Time
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
	res := Result{Channel: ch.ID, Lines: []Line{}, Messages: []Message{}, Promotions: []promo.Result{}, Coupons: []promo.CouponResult{}, ShippingOptions: []logistics.Quote{}}
	var profiles []string
	var lineClass []string                   // tax class of each priced line, parallel to promoLines
	var lineCustoms []logistics.ProductClass // tariff code and origin, parallel to promoLines
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
		if req.RestrictGroup && !s.Pricing.GroupCovers(ch.ID, it.SKU, req.Group, now) {
			res.Messages = append(res.Messages, Message{Code: "not_in_company_catalog", SKU: it.SKU, Text: "Este producto no está en el catálogo de tu empresa"})
			continue
		}
		qty := it.Qty
		rule, hasRule := s.qtyRule(p.ID, req.Group)
		if r, ok := rule, hasRule; ok {
			if q2, code, text := r.Apply(qty); q2 != qty {
				qty = q2
				res.Messages = append(res.Messages, Message{Code: code, SKU: it.SKU, Text: text})
			}
		}
		if ch.MaxQtyPerItem > 0 && qty > ch.MaxQtyPerItem {
			qty = ch.MaxQtyPerItem
			res.Messages = append(res.Messages, Message{Code: "max_qty_per_item", SKU: it.SKU, Text: "Se ajustó a la cantidad máxima por producto del canal"})
		}
		avail := s.Inventory.Available(it.SKU)
		if qty > avail {
			qty = avail
			// Lowering to the stock must still follow the rule: whole boxes
			// only, and never below the minimum.
			if hasRule {
				step := max(rule.Step, 1)
				qty = qty / step * step
				if qty < max(rule.Min, 1) {
					qty = 0
				}
			}
			if qty == 0 && hasRule {
				res.Messages = append(res.Messages, Message{Code: "qty_unavailable", SKU: it.SKU, Text: "No hay suficientes piezas para la compra mínima de este producto"})
			} else {
				res.Messages = append(res.Messages, Message{Code: "insufficient_stock", SKU: it.SKU, Text: "Se ajustó a las unidades disponibles"})
			}
		}
		if hasRule && qty > 0 {
			// Any lowering above (channel maximum) must also keep whole multiples.
			step := max(rule.Step, 1)
			if qty%step != 0 {
				qty = qty / step * step
				if qty < max(rule.Min, 1) {
					qty = 0
					res.Messages = append(res.Messages, Message{Code: "qty_unavailable", SKU: it.SKU, Text: "No hay suficientes piezas para la compra mínima de este producto"})
				}
			}
		}
		if qty != it.Qty {
			// The tier may change with the adjusted quantity.
			if q2, ok := s.Pricing.ResolveAt(ch.ID, it.SKU, max(qty, 1), req.Group, now); ok {
				q = q2
			}
		}
		line := Line{
			SKU: it.SKU, Title: p.Title + " " + v.Title, RequestedQty: it.Qty, Qty: qty,
			Unit: q.Unit, CompareAt: q.CompareAt, Total: q.Unit.Mul(qty), Available: avail, PriceList: q.ListName, Categories: p.Categories,
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
		promoLines = append(promoLines, promo.Line{SKU: it.SKU, ProductID: p.ID, Title: line.Title, Brand: p.Brand, Categories: p.Categories,
			Collections: collections(p), Attributes: s.attributesOf(&p), Qty: qty, LineTotal: line.Total})
		lineOf = append(lineOf, len(res.Lines)-1)
		profiles = append(profiles, class.ShippingProfile)
		lineClass = append(lineClass, class.TaxClass)
		lineCustoms = append(lineCustoms, class)
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
	// Shipping rules are written in the currency of the store. A market that
	// converts its prices converts them too: thresholds are compared in the
	// store currency and the resulting price is shown in the market's.
	conv := s.conversion(ch)
	baseSubtotal := afterDiscount.Amount
	if conv != nil {
		baseSubtotal = conv.Back(afterDiscount.Amount)
	}
	shipCur := func(v int64) int64 {
		if conv != nil {
			return conv.Plain(v)
		}
		return v
	}
	// promoShipping applies free-shipping promotions to the chosen option.
	promoShipping := func(free bool) {
		price := res.Shipping.Price
		shipDisc = out.ApplyShipping(&price, free)
		if shipDisc.Amount > 0 {
			orig := price
			res.Shipping.Original = &orig
			res.Shipping.Price, _ = price.Sub(shipDisc)
		}
		shipping = res.Shipping.Price
	}
	switch {
	case weight == 0:
		res.NoShipping = len(res.Lines) > 0
	case s.Logistics.HasOptions():
		res.ShippingOptions = s.Logistics.Quotes(logistics.QuoteInput{
			Country: ch.Country, PostalCode: req.PostalCode, Subtotal: baseSubtotal, WeightGrams: weight,
			Profiles: profiles, Currency: string(cur), Now: now, Live: s.liveFor(ch.Country, req.PostalCode, weight, baseSubtotal),
		})
		for i := range res.ShippingOptions {
			res.ShippingOptions[i].Price = shipCur(res.ShippingOptions[i].Price)
		}
		if i := logistics.Choose(res.ShippingOptions, req.ShippingOption); i >= 0 {
			q := res.ShippingOptions[i]
			// Without a postal code only pickup can be chosen, and only explicitly.
			if req.PostalCode != "" || (q.Kind == logistics.KindPickup && q.ID == req.ShippingOption) {
				res.Shipping = &Shipping{ID: q.ID, Name: q.Name, Kind: q.Kind, Carrier: q.Carrier, Price: money.New(q.Price, cur), PriceReason: q.PriceReason,
					EstimateDays: q.EstimateDays, Estimate: q.Estimate, WeightGrams: weight, Quote: &q}
				promoShipping(q.Price == 0)
				if req.ShippingOption != "" && req.ShippingOption != q.ID {
					res.Messages = append(res.Messages, Message{Code: "shipping_option_changed", Text: "La opción de entrega que elegiste ya no está disponible; se eligió " + q.Name})
				}
			}
		}
		switch {
		case res.Shipping != nil:
		case req.PostalCode == "":
			out.ApplyShipping(nil, false)
			res.Messages = append(res.Messages, Message{Code: "postal_code_required", Text: "Ingresa un código postal para calcular el envío"})
		default:
			out.ApplyShipping(nil, false)
			res.Messages = append(res.Messages, Message{Code: "no_shipping_option", Text: "No hay opciones de entrega para el código postal " + req.PostalCode})
		}
	case req.PostalCode != "":
		// Legacy: one rate per channel, by weight.
		sh := ch.Shipping
		if sh.Base.Currency == "" && conv != nil {
			if src, ok := s.Channels[ch.PriceFrom]; ok && src.Shipping.Base.Currency != "" {
				sh = src.Shipping
				sh.Base, sh.PerKg = money.New(shipCur(sh.Base.Amount), cur), money.New(shipCur(sh.PerKg.Amount), cur)
				if !sh.FreeOver.IsZero() {
					sh.FreeOver = money.New(shipCur(sh.FreeOver.Amount), cur)
				}
			}
		}
		if sh.Base.Currency == "" {
			sh.Base, sh.PerKg, sh.FreeOver = money.Zero(cur), money.Zero(cur), money.Zero(cur)
		}
		kg := (weight + 999) / 1000
		price, _ := sh.Base.Add(sh.PerKg.Mul(kg))
		reason := "Por peso: base " + logistics.FormatMoney(sh.Base.Amount) + " + " + logistics.FormatMoney(sh.PerKg.Amount) + " por kg"
		free := !sh.FreeOver.IsZero() && afterDiscount.Amount >= sh.FreeOver.Amount
		if free {
			price = money.Zero(cur)
			reason = "Gratis: el pedido alcanza el mínimo de " + logistics.FormatMoney(sh.FreeOver.Amount)
		}
		q := logistics.Quote{ID: "channel", Name: sh.Name, Kind: logistics.KindDelivery, Available: true, Reason: "Tarifa única del canal de venta",
			Price: price.Amount, PriceReason: reason, Free: price.IsZero(), EstimateDays: sh.EstimateDays}
		res.ShippingOptions = []logistics.Quote{q}
		res.Shipping = &Shipping{ID: q.ID, Name: sh.Name, Kind: q.Kind, Price: price, PriceReason: reason, EstimateDays: sh.EstimateDays, WeightGrams: weight, Quote: &q}
		promoShipping(free)
	default:
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
	// Prices include tax. The tax inside each amount depends on the class of
	// the product and on the region of the destination. Discounts are spread
	// over the lines in proportion to their totals.
	parts := make([]logistics.TaxPart, 0, len(promoLines)+1)
	duty := make([]dutyLine, 0, len(promoLines))
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
		duty = append(duty, dutyLine{HSCode: lineCustoms[i].HSCode, Origin: lineCustoms[i].Origin, Net: pl.LineTotal.Amount - share})
	}
	parts = append(parts, logistics.TaxPart{Class: logistics.TaxGeneral, Gross: shipping.Amount})
	// Extra charges of the store carry tax like shipping; import duties do not.
	delivery := ""
	if res.Shipping != nil {
		delivery = res.Shipping.Kind
	}
	// Fee rules are in the store currency, like shipping rules: a market that
	// converts its prices compares thresholds and charges fixed amounts converted.
	res.Fees = feesFor(s.FeeRules, ch.ID, delivery, req.Group, baseSubtotal)
	for i := range res.Fees {
		res.Fees[i].Amount = shipCur(res.Fees[i].Amount)
	}
	var feeTotal int64
	for _, f := range res.Fees {
		feeTotal += f.Amount
		parts = append(parts, logistics.TaxPart{Class: logistics.TaxGeneral, Gross: f.Amount})
	}
	for _, d := range dutiesFor(ch, duty) {
		res.Fees = append(res.Fees, d)
		feeTotal += d.Amount
	}
	grand.Amount += feeTotal
	taxRes := s.Logistics.TaxFor(ch.Country, req.PostalCode, ch.TaxRegion, ch.TaxBps, parts, ch.TaxExclusive)
	if s.ExternalTax != nil && !req.TaxExempt && req.PostalCode != "" {
		if ext, ok := s.ExternalTax(ch.Country, req.PostalCode, string(cur), ch.TaxExclusive, parts); ok {
			taxRes = ext
		}
	}
	adjust := money.Zero(cur)
	switch {
	case req.TaxExempt:
		if !ch.TaxExclusive {
			adjust = money.New(-taxRes.Total, cur) // the price included tax the buyer does not owe
		}
		taxRes = logistics.Exempt(taxRes)
	case ch.TaxExclusive:
		adjust = money.New(taxRes.Total, cur)
	}
	grand.Amount += adjust.Amount
	res.Tax = &taxRes
	tax := money.New(taxRes.Total, cur)
	res.Totals = Totals{Items: items, Discounts: discounts, Shipping: shipping, ShippingDiscount: shipDisc, Tax: tax, TaxAdjust: adjust, Fees: money.New(feeTotal, cur), Grand: grand}
	return res, nil
}

// conversion returns how a market converts from the channel it takes prices
// from, or nil when the channel has its own prices in its own currency.
func (s *Service) conversion(ch Channel) *pricing.Conversion {
	if ch.PriceFrom == "" || ch.RateMicros <= 0 {
		return nil
	}
	src, ok := s.Channels[ch.PriceFrom]
	if !ok || src.Currency == ch.Currency && ch.RateMicros == 1_000_000 {
		return nil
	}
	return &pricing.Conversion{From: ch.PriceFrom, Currency: ch.Currency, RateMicros: ch.RateMicros}
}

// attributesOf returns the attribute values of a product by id and by
// lower-case name, so a promotion can name the attribute either way.
func (s *Service) attributesOf(p *catalog.Product) map[string]string {
	if len(p.Attributes) == 0 {
		return nil
	}
	out := make(map[string]string, len(p.Attributes)*2)
	for id, v := range p.Attributes {
		out[strings.ToLower(id)] = v
		if s.Taxo != nil {
			if d, ok := s.Taxo.Attribute(id); ok {
				out[strings.ToLower(d.Name)] = v
			}
		}
	}
	return out
}

// QtyRule limits how many units of a product can be bought in one order:
// at least Min, at most Max (0 = no limit) and in multiples of Step (a box of
// 12). Group "" applies to everyone; a group rule applies to the companies
// with that price group and wins over the general one.
type QtyRule struct {
	Group string `json:"group"`
	Min   int64  `json:"min"`
	Max   int64  `json:"max"`
	Step  int64  `json:"step"`
}

// Apply returns the nearest quantity that follows the rule, with the reason.
func (r QtyRule) Apply(qty int64) (int64, string, string) {
	step := max(r.Step, 1)
	min := max(r.Min, 1)
	if min%step != 0 {
		min = (min/step + 1) * step
	}
	switch {
	case qty < min:
		return min, "qty_min", fmt.Sprintf("Se compra a partir de %d piezas", min)
	case qty%step != 0:
		up := (qty/step + 1) * step
		if r.Max > 0 && up > r.Max {
			up = qty / step * step
		}
		return up, "qty_step", fmt.Sprintf("Se vende en múltiplos de %d", step)
	case r.Max > 0 && qty > r.Max:
		return r.Max / step * step, "qty_max", fmt.Sprintf("Máximo %d piezas por pedido", r.Max/step*step)
	}
	return qty, "", ""
}

func (s *Service) qtyRule(productID, group string) (QtyRule, bool) {
	var general *QtyRule
	for i, r := range s.QtyRules[productID] {
		if group != "" && r.Group == group {
			return r, true
		}
		if r.Group == "" {
			general = &s.QtyRules[productID][i]
		}
	}
	if general != nil {
		return *general, true
	}
	return QtyRule{}, false
}

// liveFor binds the cart to the carrier rate function (nil without postal code).
func (s *Service) liveFor(country, postal string, weight, value int64) func(string) (int64, int, bool) {
	if s.LiveRate == nil || postal == "" {
		return nil
	}
	return func(carrierID string) (int64, int, bool) {
		return s.LiveRate(carrierID, country, postal, weight, value)
	}
}
