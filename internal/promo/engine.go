package promo

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/money"
)

// Reason codes. Clients switch on the code; the text is for people.
const (
	CodeApplied         = "applied"
	CodeDraft           = "draft"
	CodePaused          = "paused"
	CodeArchived        = "archived"
	CodeNotStarted      = "not_started"
	CodeEnded           = "ended"
	CodeChannel         = "channel_excluded"
	CodeCurrency        = "currency_mismatch"
	CodeCouponRequired  = "coupon_required"
	CodeCouponInvalid   = "coupon_invalid"
	CodeUsageLimit      = "usage_limit_reached"
	CodeCustomerLimit   = "customer_limit_reached"
	CodeEmailRequired   = "email_required"
	CodeEmailDomain     = "email_domain"
	CodeSegment         = "segment"
	CodePaymentMethod   = "payment_method"
	CodeCampaignPaused  = "campaign_paused"
	CodeCampaignWindow  = "campaign_out_of_dates"
	CodeCampaignBudget  = "campaign_budget_exhausted"
	CodeNoItems         = "no_matching_items"
	CodeMinSubtotal     = "min_subtotal"
	CodeMinQuantity     = "min_quantity"
	CodeIncomplete      = "incomplete"
	CodeExclusive       = "not_combinable"
	CodeNothingLeft     = "nothing_left"
	CodeGiftUnavailable = "gift_unavailable"
	CodeShippingFree    = "shipping_already_free"
	CodeShippingPending = "shipping_pending"

	// Coupon-only codes.
	CodeCouponNotFound  = "coupon_not_found"
	CodeCouponDisabled  = "coupon_disabled"
	CodeCouponExpired   = "coupon_expired"
	CodeCouponNotYet    = "coupon_not_started"
	CodeCouponExhausted = "coupon_usage_limit_reached"
	CodeCouponCustomer  = "coupon_customer_limit_reached"
	CodeCouponDuplicate = "coupon_duplicate"
)

// shopperHidden lists outcomes that only matter to the merchant. Shoppers see
// what applied and what they can still reach, not the store's back office.
var shopperHidden = map[string]bool{
	CodeDraft: true, CodePaused: true, CodeArchived: true, CodeNotStarted: true, CodeEnded: true, CodeChannel: true,
	CodeCurrency: true, CodeCouponRequired: true, CodeUsageLimit: true, CodeSegment: true, CodeEmailDomain: true,
	CodeEmailRequired: true, CodeCampaignPaused: true, CodeCampaignWindow: true, CodeCampaignBudget: true,
	CodeNoItems: true, CodePaymentMethod: true, CodeNothingLeft: true,
}

// ShopperVisible reports whether a result should be shown to a shopper.
// Results tied to a coupon the shopper typed are always visible.
func ShopperVisible(r Result) bool {
	return r.Applied || r.Coupon != "" || !shopperHidden[r.Code]
}

// Line is the input the engine sees for one cart line.
type Line struct {
	SKU         string
	ProductID   string
	Title       string
	Brand       string
	Categories  []string
	Collections []string
	Qty         int64
	LineTotal   money.Money
}

// Lookup carries state the caller resolved from the database: the coupons
// that were typed and how often this customer already used each promotion.
type Lookup struct {
	Coupons            map[string]Coupon // by normalized code; missing = does not exist
	CustomerKnown      bool              // an email was given, so per-customer counts are real
	CustomerUses       map[string]int64  // promotion id -> uses by this customer
	CouponCustomerUses map[string]int64  // code -> uses by this customer
}

// Context is everything a promotion can depend on.
type Context struct {
	Now           time.Time
	Channel       string
	Currency      money.Currency
	Lines         []Line
	Email         string   // optional; enables email-domain rules
	Segments      []string // placeholder: customer groups of the shopper
	PaymentMethod string   // "card" once known; empty = not chosen yet
	Coupons       []string // codes typed by the shopper
	Lookup        *Lookup
	// Extra adds or replaces definitions for this evaluation only (builder
	// preview of an unsaved promotion). Only lists what to evaluate when set.
	Extra []Promotion
	Only  []string
}

// LineDiscount is the part of a promotion's discount that falls on one line.
type LineDiscount struct {
	SKU    string `json:"sku"`
	Amount int64  `json:"amount"`
}

// Gift is a free product granted by a promotion.
type Gift struct {
	PromotionID string `json:"promotionId"`
	SKU         string `json:"sku"`
	Qty         int64  `json:"qty"`
	InCart      bool   `json:"inCart"` // the shopper already added it; it is discounted in place
}

// Result explains one promotion's outcome.
type Result struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	Applied          bool           `json:"applied"`
	Code             string         `json:"code"`
	Reason           string         `json:"reason"`
	Discount         money.Money    `json:"discount"`
	ShippingDiscount *money.Money   `json:"shippingDiscount,omitempty"`
	FreeShipping     bool           `json:"freeShipping,omitempty"`
	Coupon           string         `json:"coupon,omitempty"`
	CampaignID       string         `json:"campaignId,omitempty"`
	Lines            []LineDiscount `json:"lines,omitempty"`
	Gifts            []Gift         `json:"gifts,omitempty"`
	Missing          *money.Money   `json:"missing,omitempty"` // amount still needed to qualify
	Capped           bool           `json:"capped,omitempty"`
	Hint             string         `json:"hint,omitempty"` // what the shopper could do next

	maxDiscount    int64
	campaignBudget int64 // remaining amount budget (-1 = unlimited)
}

// CouponResult explains one typed code. A code is Applied only when its
// promotion actually applied; otherwise Reason says exactly why not.
type CouponResult struct {
	Code        string       `json:"code"`
	Applied     bool         `json:"applied"`
	ReasonCode  string       `json:"reasonCode"`
	Reason      string       `json:"reason"`
	PromotionID string       `json:"promotionId,omitempty"`
	Promotion   string       `json:"promotion,omitempty"`
	Discount    *money.Money `json:"discount,omitempty"`
	Missing     *money.Money `json:"missing,omitempty"`
}

// Outcome is the full, explained evaluation.
type Outcome struct {
	Results          []Result       `json:"results"`
	Coupons          []CouponResult `json:"coupons"`
	Discount         money.Money    `json:"discount"` // on items
	ShippingDiscount money.Money    `json:"shippingDiscount"`
	FreeShipping     bool           `json:"freeShipping"`
	Gifts            []Gift         `json:"gifts,omitempty"`
}

// Engine holds the promotions and campaigns of one store. It is safe for
// concurrent use: the projector updates it entity by entity while carts read.
type Engine struct {
	mu        sync.RWMutex
	promos    map[string]*Promotion
	uses      map[string]int64
	campaigns map[string]*Campaign
	sorted    []*Promotion
}

// NewEngine builds an engine with the given promotions.
func NewEngine(ps ...Promotion) *Engine {
	e := &Engine{}
	for _, p := range ps {
		e.Upsert(p, 0)
	}
	return e
}

func (e *Engine) init() {
	if e.promos == nil {
		e.promos, e.uses, e.campaigns = map[string]*Promotion{}, map[string]int64{}, map[string]*Campaign{}
	}
}

// Upsert adds or replaces a promotion with its current use count.
func (e *Engine) Upsert(p Promotion, uses int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	cp := p
	e.promos[p.ID] = &cp
	e.uses[p.ID] = uses
	e.sorted = nil
}

// Remove deletes a promotion.
func (e *Engine) Remove(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.promos, id)
	delete(e.uses, id)
	e.sorted = nil
}

// SetCampaign adds or replaces a campaign (with its budget counters).
func (e *Engine) SetCampaign(c Campaign) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	cp := c
	e.campaigns[c.ID] = &cp
}

// RemoveCampaign deletes a campaign.
func (e *Engine) RemoveCampaign(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.campaigns, id)
}

// Get returns a promotion and its use count.
func (e *Engine) Get(id string) (Promotion, int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.promos[id]
	if !ok {
		return Promotion{}, 0, false
	}
	return *p, e.uses[id], true
}

// Len returns the number of promotions.
func (e *Engine) Len() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.promos)
}

// typeRank orders promotions of equal priority: item-level benefits first, so
// order-level percentages apply to what is left.
var typeRank = map[string]int{TypeBundle: 0, TypeBuyXGetY: 1, TypeGift: 2, TypeItemFixed: 3, TypeItemPercent: 4,
	TypeTiered: 5, TypeOrderFixed: 6, TypeOrderPercent: 7, TypeFreeShipping: 8}

func less(a, b *Promotion) bool {
	if a.Stacking.Priority != b.Stacking.Priority {
		return a.Stacking.Priority > b.Stacking.Priority
	}
	if typeRank[a.Type] != typeRank[b.Type] {
		return typeRank[a.Type] < typeRank[b.Type]
	}
	return a.ID < b.ID
}

// snapshot returns the promotions in evaluation order plus copies of the
// counters, so one evaluation sees a consistent view.
func (e *Engine) snapshot(ctx *Context) ([]*Promotion, map[string]int64, map[string]Campaign) {
	e.mu.Lock()
	if e.sorted == nil {
		e.sorted = make([]*Promotion, 0, len(e.promos))
		for _, p := range e.promos {
			e.sorted = append(e.sorted, p)
		}
		sort.Slice(e.sorted, func(i, j int) bool { return less(e.sorted[i], e.sorted[j]) })
	}
	list := e.sorted
	uses := make(map[string]int64, len(e.uses))
	for k, v := range e.uses {
		uses[k] = v
	}
	camps := make(map[string]Campaign, len(e.campaigns))
	for k, v := range e.campaigns {
		camps[k] = *v
	}
	e.mu.Unlock()

	if len(ctx.Extra) == 0 && len(ctx.Only) == 0 {
		return list, uses, camps
	}
	extra := map[string]*Promotion{}
	for i := range ctx.Extra {
		extra[ctx.Extra[i].ID] = &ctx.Extra[i]
	}
	out := make([]*Promotion, 0, len(list)+len(extra))
	for _, p := range list {
		if _, replaced := extra[p.ID]; !replaced {
			out = append(out, p)
		}
	}
	for _, p := range extra {
		out = append(out, p)
	}
	if len(ctx.Only) > 0 {
		kept := out[:0]
		for _, p := range out {
			if has(ctx.Only, p.ID) {
				kept = append(kept, p)
			}
		}
		out = kept
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out, uses, camps
}

// state tracks what is left to discount on each line during one evaluation.
type state struct {
	lines     []Line
	remaining []int64
}

type candidate struct {
	p     *Promotion
	res   *Result
	elig  []int // indexes of lines matched by the target
	alive bool
}

// computed is what a type-specific calculation returns.
type computed struct {
	ok      bool
	alloc   []int64 // discount per line
	code    string
	reason  string
	hint    string
	missing int64
	gifts   []Gift
	freeShp bool
}

// Evaluate explains every promotion for the given cart.
func (e *Engine) Evaluate(ctx Context) Outcome {
	cur := ctx.Currency
	out := Outcome{Results: []Result{}, Coupons: []CouponResult{}, Discount: money.Zero(cur), ShippingDiscount: money.Zero(cur)}
	promos, uses, camps := e.snapshot(&ctx)
	byID := make(map[string]*Promotion, len(promos))
	for _, p := range promos {
		byID[p.ID] = p
	}
	st := &state{lines: ctx.Lines, remaining: make([]int64, len(ctx.Lines))}
	for i, l := range ctx.Lines {
		st.remaining[i] = l.LineTotal.Amount
	}

	// Coupons: validate each typed code on its own before looking at the promotion.
	couponFor := map[string]string{} // promotion id -> code
	couponIdx := map[string]int{}
	seen := map[string]bool{}
	for _, raw := range ctx.Coupons {
		code := NormalizeCode(raw)
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		cr := CouponResult{Code: code}
		c, ok := Coupon{}, false
		if ctx.Lookup != nil {
			c, ok = ctx.Lookup.Coupons[code]
		}
		switch {
		case !ok:
			cr.ReasonCode, cr.Reason = CodeCouponNotFound, "El cupón "+code+" no existe. Revisa que esté bien escrito."
		case c.Status != StatusActive:
			cr.ReasonCode, cr.Reason = CodeCouponDisabled, "El cupón "+code+" ya no está disponible."
		case c.StartsAt != nil && ctx.Now.Before(*c.StartsAt):
			cr.ReasonCode, cr.Reason = CodeCouponNotYet, "El cupón "+code+" se podrá usar a partir del "+When(*c.StartsAt)+"."
		case c.EndsAt != nil && ctx.Now.After(*c.EndsAt):
			cr.ReasonCode, cr.Reason = CodeCouponExpired, "El cupón "+code+" venció el "+When(*c.EndsAt)+"."
		case c.MaxUses > 0 && c.Uses >= c.MaxUses:
			cr.ReasonCode, cr.Reason = CodeCouponExhausted, "El cupón "+code+" ya alcanzó su límite de usos."
		case c.MaxUsesPerCustomer > 0 && ctx.Lookup.CustomerKnown && ctx.Lookup.CouponCustomerUses[code] >= c.MaxUsesPerCustomer:
			cr.ReasonCode, cr.Reason = CodeCouponCustomer, "Ya usaste el cupón "+code+" el máximo de veces permitido ("+times(c.MaxUsesPerCustomer)+")."
		case byID[c.PromotionID] == nil:
			cr.ReasonCode, cr.Reason = CodeCouponDisabled, "El cupón "+code+" ya no está disponible."
		case couponFor[c.PromotionID] != "":
			cr.PromotionID = c.PromotionID
			cr.ReasonCode, cr.Reason = CodeCouponDuplicate, "Ya aplicaste el cupón "+couponFor[c.PromotionID]+" para esta misma promoción."
		default:
			cr.PromotionID, cr.Promotion = c.PromotionID, byID[c.PromotionID].Name
			couponFor[c.PromotionID] = code
		}
		couponIdx[code] = len(out.Coupons)
		out.Coupons = append(out.Coupons, cr)
	}

	// Phase 1: who is eligible at all.
	results := make([]Result, len(promos))
	cands := make([]candidate, len(promos))
	for i, p := range promos {
		r := &results[i]
		*r = Result{ID: p.ID, Name: p.Name, Type: p.Type, Discount: money.Zero(cur), CampaignID: p.CampaignID,
			maxDiscount: p.MaxDiscount, campaignBudget: -1}
		c := candidate{p: p, res: r}
		c.elig, c.alive = e.eligible(p, &ctx, r, uses[p.ID], camps, couponFor)
		cands[i] = c
	}

	// Phase 2: walk in priority order; each promotion sees what earlier ones left.
	var applied []*candidate
	campSpent := map[string]int64{}
	for i := range cands {
		c := &cands[i]
		if !c.alive {
			continue
		}
		if other := conflict(c.p, applied); other != nil {
			c.res.Code = CodeExclusive
			c.res.Reason = conflictReason(c.p, other)
			continue
		}
		cp := c.p.compute(&ctx, st, c.elig)
		if !cp.ok {
			c.res.Code, c.res.Reason, c.res.Hint = cp.code, cp.reason, cp.hint
			if cp.missing > 0 {
				m := money.New(cp.missing, cur)
				c.res.Missing = &m
			}
			continue
		}
		total := sum(cp.alloc)
		capped := false
		if c.p.MaxDiscount > 0 && total > c.p.MaxDiscount {
			cp.alloc = distribute(c.p.MaxDiscount, cp.alloc)
			total, capped = c.p.MaxDiscount, true
		}
		if c.res.campaignBudget >= 0 {
			left := c.res.campaignBudget - campSpent[c.p.CampaignID]
			if left <= 0 && !cp.freeShp {
				c.res.Code, c.res.Reason = CodeCampaignBudget, "La campaña «"+campName(camps, c.p.CampaignID)+"» agotó su presupuesto."
				continue
			}
			if total > left {
				cp.alloc = distribute(left, cp.alloc)
				total, capped = left, true
			}
			c.res.campaignBudget = left - total
		}
		if total == 0 && !cp.freeShp && len(cp.gifts) == 0 {
			c.res.Code, c.res.Reason = CodeNothingLeft, "Otras promociones ya cubrieron el importe de estos productos."
			continue
		}
		for j, a := range cp.alloc {
			if a > 0 {
				st.remaining[j] -= a
				c.res.Lines = append(c.res.Lines, LineDiscount{SKU: st.lines[j].SKU, Amount: a})
			}
		}
		campSpent[c.p.CampaignID] += total
		c.res.Applied, c.res.Code, c.res.Reason, c.res.Hint = true, CodeApplied, cp.reason, cp.hint
		c.res.Discount = money.New(total, cur)
		c.res.Capped = capped
		c.res.FreeShipping = cp.freeShp
		c.res.Gifts = cp.gifts
		if capped {
			c.res.Reason += " (tope de " + Pesos(total) + " por pedido)"
		}
		if n := c.p.Conditions.MaxUsesPerCustomer; n > 0 && (ctx.Lookup == nil || !ctx.Lookup.CustomerKnown) {
			c.res.Hint = strings.TrimSpace(c.res.Hint + " Límite de " + times(n) + " por cliente; se confirma con tu correo al pagar.")
		}
		out.Discount.Amount += total
		out.FreeShipping = out.FreeShipping || cp.freeShp
		out.Gifts = append(out.Gifts, cp.gifts...)
		applied = append(applied, c)
	}

	// Coupons follow their promotion: applied only if the promotion applied.
	for pid, code := range couponFor {
		cr := &out.Coupons[couponIdx[code]]
		for i := range results {
			r := &results[i]
			if r.ID != pid {
				continue
			}
			r.Coupon = code
			cr.Applied, cr.ReasonCode, cr.Missing = r.Applied, r.Code, r.Missing
			if r.Applied {
				d := r.Discount
				cr.Discount = &d
				cr.Reason = "Cupón " + code + " aplicado: " + r.Reason
			} else {
				cr.Reason = "El cupón " + code + " no se aplicó: " + lowerFirst(r.Reason)
			}
		}
	}
	out.Results = results
	return out
}

// eligible runs every condition that does not depend on other promotions.
func (e *Engine) eligible(p *Promotion, ctx *Context, r *Result, uses int64, camps map[string]Campaign, couponFor map[string]string) ([]int, bool) {
	fail := func(code, reason string) ([]int, bool) {
		r.Code, r.Reason = code, reason
		return nil, false
	}
	c := p.Conditions
	switch p.Status {
	case StatusDraft:
		return fail(CodeDraft, "La promoción es un borrador; actívala para que aplique.")
	case StatusPaused:
		return fail(CodePaused, "La promoción está pausada.")
	case StatusArchived:
		return fail(CodeArchived, "La promoción está archivada.")
	}
	if c.StartsAt != nil && ctx.Now.Before(*c.StartsAt) {
		return fail(CodeNotStarted, "La promoción empieza el "+When(*c.StartsAt)+".")
	}
	if c.EndsAt != nil && ctx.Now.After(*c.EndsAt) {
		return fail(CodeEnded, "La promoción terminó el "+When(*c.EndsAt)+".")
	}
	if p.CampaignID != "" {
		if camp, ok := camps[p.CampaignID]; ok {
			switch {
			case camp.Status == StatusPaused:
				return fail(CodeCampaignPaused, "La campaña «"+camp.Name+"» está pausada.")
			case camp.StartsAt != nil && ctx.Now.Before(*camp.StartsAt):
				return fail(CodeCampaignWindow, "La campaña «"+camp.Name+"» empieza el "+When(*camp.StartsAt)+".")
			case camp.EndsAt != nil && ctx.Now.After(*camp.EndsAt):
				return fail(CodeCampaignWindow, "La campaña «"+camp.Name+"» terminó el "+When(*camp.EndsAt)+".")
			case camp.Exhausted():
				return fail(CodeCampaignBudget, "La campaña «"+camp.Name+"» agotó su presupuesto.")
			}
			if camp.BudgetType == "amount" {
				r.campaignBudget = camp.BudgetLimit - camp.Spent
			}
		}
	}
	if len(c.Channels) > 0 && !has(c.Channels, ctx.Channel) {
		return fail(CodeChannel, "El canal de venta "+ctx.Channel+" no participa en esta promoción.")
	}
	if p.Currency != "" && ctx.Currency != "" && p.Currency != ctx.Currency {
		return fail(CodeCurrency, "La moneda del carrito ("+string(ctx.Currency)+") no coincide con la de la promoción ("+string(p.Currency)+").")
	}
	if c.RequiresCoupon && couponFor[p.ID] == "" {
		return fail(CodeCouponRequired, "Requiere un cupón; no se escribió ninguno válido para esta promoción.")
	}
	if c.MaxUses > 0 && uses >= c.MaxUses {
		return fail(CodeUsageLimit, "La promoción alcanzó su límite de "+times(c.MaxUses)+".")
	}
	if c.MaxUsesPerCustomer > 0 && ctx.Lookup != nil && ctx.Lookup.CustomerKnown && ctx.Lookup.CustomerUses[p.ID] >= c.MaxUsesPerCustomer {
		return fail(CodeCustomerLimit, "Ya usaste esta promoción el máximo de veces permitido ("+times(c.MaxUsesPerCustomer)+" por cliente).")
	}
	if len(c.EmailDomains) > 0 {
		at := strings.LastIndex(ctx.Email, "@")
		if at < 0 {
			return fail(CodeEmailRequired, "Solo para correos "+domains(c.EmailDomains)+"; escribe tu correo para validarla.")
		}
		if !has(c.EmailDomains, strings.ToLower(ctx.Email[at+1:])) {
			return fail(CodeEmailDomain, "Solo para correos "+domains(c.EmailDomains)+".")
		}
	}
	if len(c.Segments) > 0 {
		ok := false
		for _, s := range ctx.Segments {
			ok = ok || has(c.Segments, strings.ToLower(s))
		}
		if !ok {
			return fail(CodeSegment, "Solo para clientes del segmento "+strings.Join(c.Segments, ", ")+".")
		}
	}
	if len(c.PaymentMethods) > 0 && !has(c.PaymentMethods, ctx.PaymentMethod) {
		return fail(CodePaymentMethod, "Aplica al pagar con "+paymentNames(c.PaymentMethods)+".")
	}

	var elig []int
	var sub, qty int64
	for i, l := range ctx.Lines {
		if l.Qty <= 0 || (p.Type == TypeGift && l.SKU == p.GiftSKU) {
			continue
		}
		if p.Target.Matches(l) {
			elig = append(elig, i)
			sub += l.LineTotal.Amount
			qty += l.Qty
		}
	}
	if len(elig) == 0 && p.Type != TypeBundle {
		if p.Target.All() {
			return fail(CodeNoItems, "El carrito está vacío.")
		}
		r.Hint = "Agrega " + targetPhrase(p.Target, nil) + " para obtenerla."
		return fail(CodeNoItems, "No hay productos participantes en el carrito.")
	}
	if c.MinSubtotal > 0 && sub < c.MinSubtotal && p.Type != TypeBundle {
		m := money.New(c.MinSubtotal-sub, ctx.Currency)
		r.Missing = &m
		what := ""
		if !p.Target.All() {
			what = " en productos participantes"
		}
		return fail(CodeMinSubtotal, "Agrega "+Pesos(m.Amount)+" más"+what+" para obtener esta promoción (mínimo "+Pesos(c.MinSubtotal)+").")
	}
	if c.MinQuantity > 0 && qty < c.MinQuantity && p.Type != TypeBundle {
		what := "al carrito"
		if !p.Target.All() {
			what = "de los productos participantes"
		}
		return fail(CodeMinQuantity, fmt.Sprintf("Agrega %s más %s para obtener esta promoción (mínimo %d).", units(c.MinQuantity-qty), what, c.MinQuantity))
	}
	return elig, true
}

// conflict returns an already applied promotion that p cannot combine with.
func conflict(p *Promotion, applied []*candidate) *Promotion {
	for _, a := range applied {
		o := a.p
		if o.Stacking.Mode == StackExclusive || p.Stacking.Mode == StackExclusive {
			return o
		}
		if (o.Stacking.Mode == StackGroup || p.Stacking.Mode == StackGroup) && o.Stacking.Group != p.Stacking.Group {
			return o
		}
	}
	return nil
}

func conflictReason(p, other *Promotion) string {
	switch {
	case other.Stacking.Mode == StackExclusive:
		return "No se combina: «" + other.Name + "» es exclusiva y tiene prioridad."
	case p.Stacking.Mode == StackExclusive:
		return "Es exclusiva y ya se aplicó «" + other.Name + "», que tiene prioridad."
	case p.Stacking.Mode == StackGroup && other.Stacking.Mode == StackGroup:
		return "No se combina con «" + other.Name + "»: pertenecen a grupos distintos (" + p.Stacking.Group + " y " + other.Stacking.Group + ")."
	case p.Stacking.Mode == StackGroup:
		return "Solo se combina con promociones del grupo " + p.Stacking.Group + "; ya se aplicó «" + other.Name + "»."
	default:
		return "No se combina con «" + other.Name + "», que solo admite promociones del grupo " + other.Stacking.Group + "."
	}
}

func campName(camps map[string]Campaign, id string) string {
	if c, ok := camps[id]; ok {
		return c.Name
	}
	return id
}

// compute calculates the discount of p on what is left in st.
func (p *Promotion) compute(ctx *Context, st *state, elig []int) computed {
	n := len(st.lines)
	alloc := make([]int64, n)
	all := make([]int, 0, n)
	for i := range st.lines {
		if st.lines[i].Qty > 0 {
			all = append(all, i)
		}
	}
	rem := func(idx []int) (t int64) {
		for _, i := range idx {
			t += st.remaining[i]
		}
		return
	}
	spread := func(total int64, idx []int) {
		w := make([]int64, n)
		for _, i := range idx {
			w[i] = st.remaining[i]
		}
		copy(alloc, distribute(total, w))
	}
	scope := ""
	if !p.Target.All() {
		scope = " en productos participantes"
	}
	switch p.Type {
	case TypeOrderPercent:
		spread(money.New(rem(all), "").Percent(p.Bps).Amount, all)
		return computed{ok: true, alloc: alloc, reason: Percent(p.Bps) + " de descuento en tu pedido" + minNote(p)}
	case TypeOrderFixed:
		spread(min(p.Amount, rem(all)), all)
		return computed{ok: true, alloc: alloc, reason: Pesos(p.Amount) + " de descuento en tu pedido" + minNote(p)}
	case TypeItemPercent:
		spread(money.New(rem(elig), "").Percent(p.Bps).Amount, elig)
		return computed{ok: true, alloc: alloc, reason: Percent(p.Bps) + " de descuento" + scope + minNote(p)}
	case TypeItemFixed:
		for _, i := range elig {
			alloc[i] = min(p.Amount*st.lines[i].Qty, st.remaining[i])
		}
		return computed{ok: true, alloc: alloc, reason: Pesos(p.Amount) + " de descuento por pieza" + scope + minNote(p)}
	case TypeFreeShipping:
		return computed{ok: true, alloc: alloc, freeShp: true, reason: "Envío gratis" + minNote(p)}
	case TypeTiered:
		return p.computeTiered(ctx, st, elig, alloc, spread, rem)
	case TypeBuyXGetY:
		return p.computeBXGY(ctx, st, elig, alloc)
	case TypeGift:
		return p.computeGift(st, alloc)
	case TypeBundle:
		return p.computeBundle(st, alloc)
	}
	return computed{code: CodeNoItems, reason: "Tipo de promoción desconocido."}
}

func minNote(p *Promotion) string {
	if p.Conditions.MinSubtotal > 0 {
		return " por comprar " + Pesos(p.Conditions.MinSubtotal) + " o más"
	}
	if p.Conditions.MinQuantity > 0 {
		return " por llevar " + units(p.Conditions.MinQuantity) + " o más"
	}
	return ""
}

func (p *Promotion) computeTiered(ctx *Context, st *state, elig []int, alloc []int64, spread func(int64, []int), rem func([]int) int64) computed {
	var value int64
	for _, i := range elig {
		if p.TierBy == "quantity" {
			value += st.lines[i].Qty
		} else {
			value += st.lines[i].LineTotal.Amount
		}
	}
	show := func(v int64) string {
		if p.TierBy == "quantity" {
			return units(v)
		}
		return Pesos(v)
	}
	benefit := func(t Tier) string {
		if t.Bps > 0 {
			return Percent(t.Bps) + " de descuento"
		}
		return Pesos(t.Amount) + " de descuento"
	}
	best := -1
	for i, t := range p.Tiers {
		if value >= t.Min {
			best = i
		}
	}
	if best < 0 {
		t := p.Tiers[0]
		c := computed{code: CodeMinQuantity, reason: "Agrega " + show(t.Min-value) + " más para obtener " + benefit(t) + "."}
		if p.TierBy != "quantity" {
			c.code, c.missing = CodeMinSubtotal, t.Min-value
		}
		return c
	}
	t := p.Tiers[best]
	if t.Bps > 0 {
		spread(money.New(rem(elig), "").Percent(t.Bps).Amount, elig)
	} else {
		spread(min(t.Amount, rem(elig)), elig)
	}
	verb := "por comprar "
	if p.TierBy == "quantity" {
		verb = "por llevar "
	}
	c := computed{ok: true, alloc: alloc, reason: benefit(t) + " " + verb + show(t.Min) + " o más"}
	if best+1 < len(p.Tiers) {
		next := p.Tiers[best+1]
		c.hint = "Agrega " + show(next.Min-value) + " más y obtén " + benefit(next) + "."
	}
	return c
}

type unitGroup struct {
	line int
	unit int64 // remaining price of one unit
	qty  int64
}

func (p *Promotion) computeBXGY(ctx *Context, st *state, elig []int, alloc []int64) computed {
	var buyUnits int64
	for _, i := range elig {
		buyUnits += st.lines[i].Qty
	}
	pool := elig
	same := p.GetTarget == nil
	if !same {
		pool = nil
		for i, l := range st.lines {
			if l.Qty > 0 && p.GetTarget.Matches(l) {
				pool = append(pool, i)
			}
		}
	}
	benefit := "gratis"
	if p.GetBps < 10000 {
		benefit = "con " + Percent(p.GetBps) + " de descuento"
	}
	var rewards int64
	if same {
		size := p.BuyQty + p.GetQty
		groups := buyUnits / size
		if groups == 0 {
			return computed{code: CodeIncomplete, reason: fmt.Sprintf("Agrega %s más para llevar %d y pagar %d.", units(size-buyUnits), size, p.BuyQty)}
		}
		rewards = groups * p.GetQty
	} else {
		groups := buyUnits / p.BuyQty
		if groups == 0 {
			return computed{code: CodeIncomplete, reason: fmt.Sprintf("Agrega %s más de los productos participantes para obtener %s %s.", units(p.BuyQty-buyUnits), units(p.GetQty), benefit)}
		}
		var have int64
		for _, i := range pool {
			have += st.lines[i].Qty
		}
		if have == 0 {
			return computed{code: CodeIncomplete, reason: "Agrega " + targetPhrase(*p.GetTarget, nil) + " al carrito: te toca " + units(p.GetQty) + " " + benefit + "."}
		}
		rewards = min(groups*p.GetQty, have)
	}
	// Cheapest units get the benefit.
	gs := make([]unitGroup, 0, len(pool))
	for _, i := range pool {
		gs = append(gs, unitGroup{line: i, unit: st.remaining[i] / st.lines[i].Qty, qty: st.lines[i].Qty})
	}
	sort.SliceStable(gs, func(a, b int) bool { return gs[a].unit < gs[b].unit })
	left := rewards
	for _, g := range gs {
		if left == 0 {
			break
		}
		take := min(left, g.qty)
		alloc[g.line] += money.New(g.unit*take, "").Percent(p.GetBps).Amount
		left -= take
	}
	got := rewards - left
	reason := fmt.Sprintf("Llevas %s %s (las de menor precio)", units(got), benefit)
	if got == 1 {
		reason = "Llevas 1 pieza " + benefit + " (la de menor precio)"
	}
	c := computed{ok: true, alloc: alloc, reason: reason}
	if same {
		if extra := buyUnits % (p.BuyQty + p.GetQty); extra > 0 {
			c.hint = fmt.Sprintf("Agrega %s más y llevas otra %s.", units(p.BuyQty+p.GetQty-extra), benefit)
		}
	}
	return c
}

func (p *Promotion) computeGift(st *state, alloc []int64) computed {
	for i, l := range st.lines {
		if l.SKU == p.GiftSKU && l.Qty > 0 {
			q := min(p.GiftQty, l.Qty)
			alloc[i] = (st.remaining[i] / l.Qty) * q
			return computed{ok: true, alloc: alloc, gifts: []Gift{{PromotionID: p.ID, SKU: p.GiftSKU, Qty: q, InCart: true}},
				reason: "Tu regalo va sin costo" + minNote(p)}
		}
	}
	return computed{ok: true, alloc: alloc, gifts: []Gift{{PromotionID: p.ID, SKU: p.GiftSKU, Qty: p.GiftQty}},
		reason: "Agregamos tu regalo sin costo" + minNote(p)}
}

func (p *Promotion) computeBundle(st *state, alloc []int64) computed {
	type comp struct {
		lines []int
		have  int64
	}
	comps := make([]comp, len(p.BundleItems))
	n := int64(-1)
	var missing []string
	for k, b := range p.BundleItems {
		for i, l := range st.lines {
			if l.Qty > 0 && ((b.SKU != "" && l.SKU == b.SKU) || (b.ProductID != "" && l.ProductID == b.ProductID)) {
				comps[k].lines = append(comps[k].lines, i)
				comps[k].have += l.Qty
			}
		}
		if comps[k].have < b.Qty {
			id := b.SKU
			if id == "" {
				id = b.ProductID
			}
			missing = append(missing, fmt.Sprintf("%d × %s", b.Qty-comps[k].have, id))
		}
		if sets := comps[k].have / b.Qty; n < 0 || sets < n {
			n = sets
		}
	}
	if len(missing) > 0 || n <= 0 {
		return computed{code: CodeIncomplete, reason: "Para completar el paquete agrega: " + strings.Join(missing, ", ") + "."}
	}
	// Take n sets from the matching lines and price them at the bundle price.
	taken := make([]int64, len(st.lines))
	used := make([]int64, len(st.lines))
	var regular int64
	for k, b := range p.BundleItems {
		need := n * b.Qty
		for _, i := range comps[k].lines {
			free := st.lines[i].Qty - used[i]
			take := min(need, free)
			if take <= 0 {
				continue
			}
			used[i] += take
			v := (st.remaining[i] / st.lines[i].Qty) * take
			taken[i] += v
			regular += v
			need -= take
		}
		if need > 0 { // two components share a line and there are not enough units
			return computed{code: CodeIncomplete, reason: "Faltan unidades para completar el paquete."}
		}
	}
	disc := regular - n*p.BundlePrice
	if disc <= 0 {
		return computed{code: CodeNothingLeft, reason: "El paquete ya cuesta " + Pesos(p.BundlePrice) + " o menos con los precios actuales."}
	}
	copy(alloc, distribute(disc, taken))
	reason := "Paquete a " + Pesos(p.BundlePrice)
	if n > 1 {
		reason = fmt.Sprintf("%d paquetes a %s cada uno", n, Pesos(p.BundlePrice))
	}
	return computed{ok: true, alloc: alloc, reason: reason}
}

// ApplyShipping resolves free-shipping results once the shipping price is
// known and returns the total shipping discount. price nil means the postal
// code is still missing; alreadyFree means the channel rule made it free.
func (o *Outcome) ApplyShipping(price *money.Money, alreadyFree bool) money.Money {
	total := int64(0)
	cur := o.Discount.Currency
	for i := range o.Results {
		r := &o.Results[i]
		if !r.Applied || !r.FreeShipping {
			continue
		}
		switch {
		case price == nil:
			r.Hint = strings.TrimSpace("Ingresa tu código postal para ver el envío. " + r.Hint)
		case alreadyFree || price.Amount-total <= 0:
			r.Applied, r.Code, r.FreeShipping = false, CodeShippingFree, false
			r.Reason = "El envío ya es gratis en este pedido; la promoción no se consume."
			o.unapplyCoupon(r)
		default:
			d := price.Amount - total
			if r.maxDiscount > 0 && d > r.maxDiscount {
				d, r.Capped = r.maxDiscount, true
				r.Reason = "Descuento de " + Pesos(d) + " en el envío (tope de la promoción)"
			}
			if r.campaignBudget >= 0 && d > r.campaignBudget {
				d, r.Capped = r.campaignBudget, true
				r.Reason = "Descuento de " + Pesos(d) + " en el envío (lo que queda del presupuesto de la campaña)"
			}
			if d <= 0 {
				r.Applied, r.Code, r.FreeShipping = false, CodeCampaignBudget, false
				r.Reason = "La campaña agotó su presupuesto."
				o.unapplyCoupon(r)
				continue
			}
			m := money.New(d, cur)
			r.ShippingDiscount = &m
			total += d
		}
	}
	o.ShippingDiscount = money.New(total, cur)
	o.FreeShipping = false
	for _, r := range o.Results {
		o.FreeShipping = o.FreeShipping || (r.Applied && r.FreeShipping)
	}
	return o.ShippingDiscount
}

// Unapply marks an applied result as not applied after the fact (for example
// a gift that is out of stock) and keeps coupons and totals consistent.
func (o *Outcome) Unapply(id, code, reason string) {
	for i := range o.Results {
		r := &o.Results[i]
		if r.ID != id || !r.Applied {
			continue
		}
		o.Discount.Amount -= r.Discount.Amount
		r.Applied, r.Code, r.Reason, r.Hint = false, code, reason, ""
		r.Discount.Amount, r.Lines, r.Gifts, r.FreeShipping = 0, nil, nil, false
		o.unapplyCoupon(r)
	}
	kept := o.Gifts[:0]
	for _, g := range o.Gifts {
		if g.PromotionID != id {
			kept = append(kept, g)
		}
	}
	o.Gifts = kept
}

func (o *Outcome) unapplyCoupon(r *Result) {
	for i := range o.Coupons {
		c := &o.Coupons[i]
		if c.PromotionID == r.ID && c.Applied {
			c.Applied, c.ReasonCode, c.Discount = false, r.Code, nil
			c.Reason = "El cupón " + c.Code + " no se aplicó: " + lowerFirst(r.Reason)
		}
	}
}

func sum(a []int64) (t int64) {
	for _, v := range a {
		t += v
	}
	return
}

// distribute splits total across weights proportionally (largest remainder),
// never giving a line more than its weight. The parts always add up to
// min(total, sum(weights)).
func distribute(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	w := sum(weights)
	if w <= 0 || total <= 0 {
		return out
	}
	if total >= w {
		copy(out, weights)
		return out
	}
	type rest struct {
		i   int
		rem int64
	}
	var rs []rest
	given := int64(0)
	for i, x := range weights {
		if x <= 0 {
			continue
		}
		// total < w and both fit in int64 minor units; use 128-bit safe split.
		q, r := mulDiv(total, x, w)
		out[i] = q
		given += q
		rs = append(rs, rest{i, r})
	}
	sort.SliceStable(rs, func(a, b int) bool { return rs[a].rem > rs[b].rem })
	for k := 0; given < total && k < len(rs); k++ {
		if out[rs[k].i] < weights[rs[k].i] {
			out[rs[k].i]++
			given++
		}
	}
	return out
}

// mulDiv returns a*b/c and the remainder without overflowing for the amounts
// a store can hold (a, b, c < 2^53).
func mulDiv(a, b, c int64) (int64, int64) {
	if a < 1<<31 && b < 1<<31 {
		return a * b / c, a * b % c
	}
	// Split a to keep intermediate products in range.
	q1, r1 := a/c, a%c
	hi := q1 * b
	lo := r1 * b // r1 < c; safe while c*b < 2^63, true for realistic amounts
	return hi + lo/c, lo % c
}
