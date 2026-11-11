// Package promo evaluates promotions and always explains the outcome: every
// promotion in scope comes back as applied or not applied, with a stable
// reason code and a plain-language reason (VTEX gives no explanation of why a
// promotion did or did not apply, and shows invalid coupons as applied).
//
// The engine is pure: it works on a Context (cart lines, channel, customer,
// coupons, usage) and returns an Outcome. Persistence, usage counters and
// redemptions live in internal/promostore.
package promo

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // the store timezone must resolve in minimal containers
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"github.com/helloghc/apexx/internal/money"
)

// Promotion types.
const (
	TypeOrderPercent = "order_percent" // % off the whole order
	TypeOrderFixed   = "order_fixed"   // fixed amount off the whole order
	TypeItemPercent  = "item_percent"  // % off the targeted items
	TypeItemFixed    = "item_fixed"    // fixed amount off each targeted unit
	TypeFreeShipping = "free_shipping" // shipping at no cost
	TypeBuyXGetY     = "buy_x_get_y"   // buy X get Y (cheapest units discounted)
	TypeTiered       = "tiered"        // spend/quantity tiers
	TypeGift         = "gift"          // gift with purchase
	TypeBundle       = "bundle"        // fixed price for a set of items
)

// Types lists every promotion type in display order.
var Types = []string{TypeOrderPercent, TypeOrderFixed, TypeItemPercent, TypeItemFixed, TypeFreeShipping,
	TypeBuyXGetY, TypeTiered, TypeGift, TypeBundle}

// Stored statuses. The status shown to merchants (activa, programada,
// vencida, pausada) is derived from the stored status and the schedule.
const (
	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusDraft    = "draft"
	StatusArchived = "archived"
)

// Stacking modes.
const (
	StackCombine   = "stack"     // combines with every non-exclusive promotion
	StackExclusive = "exclusive" // never combines
	StackGroup     = "group"     // combines only inside its group
)

// Location is the timezone used to read and explain schedules.
var Location = func() *time.Location {
	l, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		return time.FixedZone("CST", -6*3600)
	}
	return l
}()

// Target selects cart lines. Dimensions combine with AND; values inside a
// dimension combine with OR. An empty target matches every line.
//
// Collections is a hook for the catalog module: the cart fills Line.Collections
// from the product attribute "collections" (comma separated) until collections
// are first-class.
type Target struct {
	SKUs        []string `json:"skus,omitempty"`
	ProductIDs  []string `json:"productIds,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Brands      []string `json:"brands,omitempty"`
	Collections []string `json:"collections,omitempty"`
	ExcludeSKUs []string `json:"excludeSkus,omitempty"`
	// Attributes keeps lines whose product has every one of these attribute
	// values ("material": "Algodón"), keyed by attribute name or id.
	Attributes map[string]string `json:"attributes,omitempty"`
}

// All reports whether the target matches every line.
func (t Target) All() bool { return t.size() == 0 }

func (t Target) size() int {
	return len(t.SKUs) + len(t.ProductIDs) + len(t.Categories) + len(t.Brands) + len(t.Collections) + len(t.ExcludeSKUs) + len(t.Attributes)
}

// Fold lowercases and strips accents so "Pacífico" matches "pacifico".
func Fold(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.ToLower(strings.TrimSpace(out))
}

func hasFold(list []string, vals ...string) bool {
	for _, a := range list {
		fa := Fold(a)
		for _, v := range vals {
			if fa == Fold(v) {
				return true
			}
		}
	}
	return false
}

func has(list []string, v string) bool {
	for _, a := range list {
		if a == v {
			return true
		}
	}
	return false
}

// Matches reports whether line l is selected by the target.
func (t Target) Matches(l Line) bool {
	if has(t.ExcludeSKUs, l.SKU) {
		return false
	}
	if len(t.SKUs)+len(t.ProductIDs) > 0 && !has(t.SKUs, l.SKU) && !has(t.ProductIDs, l.ProductID) {
		return false
	}
	if len(t.Categories) > 0 && !hasFold(t.Categories, l.Categories...) {
		return false
	}
	if len(t.Brands) > 0 && !hasFold(t.Brands, l.Brand) {
		return false
	}
	if len(t.Collections) > 0 && !hasFold(t.Collections, l.Collections...) {
		return false
	}
	for k, v := range t.Attributes {
		if !strings.EqualFold(strings.TrimSpace(l.Attributes[strings.ToLower(k)]), v) {
			return false
		}
	}
	return true
}

// Conditions restrict when a promotion can apply.
type Conditions struct {
	Channels           []string   `json:"channels,omitempty"`    // empty = every channel
	MinSubtotal        int64      `json:"minSubtotal,omitempty"` // minor units, over the targeted items
	MinQuantity        int64      `json:"minQuantity,omitempty"` // units, over the targeted items
	StartsAt           *time.Time `json:"startsAt,omitempty"`    // inclusive
	EndsAt             *time.Time `json:"endsAt,omitempty"`      // inclusive
	MaxUses            int64      `json:"maxUses,omitempty"`     // 0 = unlimited
	MaxUsesPerCustomer int64      `json:"maxUsesPerCustomer,omitempty"`
	EmailDomains       []string   `json:"emailDomains,omitempty"`   // "empresa.com"
	Segments           []string   `json:"segments,omitempty"`       // placeholder until customer groups exist
	PaymentMethods     []string   `json:"paymentMethods,omitempty"` // "card"; more methods later
	RequiresCoupon     bool       `json:"requiresCoupon,omitempty"`
}

// Stacking says how a promotion combines with others. Higher priority is
// evaluated first.
type Stacking struct {
	Mode     string `json:"mode"` // stack | exclusive | group
	Group    string `json:"group,omitempty"`
	Priority int    `json:"priority"`
}

// Tier is one step of a tiered promotion: from Min (minor units or units)
// upward the benefit is Bps or Amount.
type Tier struct {
	Min    int64 `json:"min"`
	Bps    int64 `json:"bps,omitempty"`
	Amount int64 `json:"amount,omitempty"`
}

// BundleItem is one component of a fixed-price bundle.
type BundleItem struct {
	SKU       string `json:"sku,omitempty"`
	ProductID string `json:"productId,omitempty"`
	Qty       int64  `json:"qty"`
}

// Promotion is the full definition of one promotion.
type Promotion struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	CampaignID  string         `json:"campaignId,omitempty"`
	Currency    money.Currency `json:"currency"`

	// Benefit. Which fields matter depends on Type.
	Bps    int64 `json:"bps,omitempty"`    // percentage in basis points (1000 = 10%)
	Amount int64 `json:"amount,omitempty"` // fixed amount in minor units

	BuyQty    int64   `json:"buyQty,omitempty"`    // buy X…
	GetQty    int64   `json:"getQty,omitempty"`    // …get Y
	GetBps    int64   `json:"getBps,omitempty"`    // discount on the Y units (10000 = free)
	GetTarget *Target `json:"getTarget,omitempty"` // nil = same products as Target

	TierBy string `json:"tierBy,omitempty"` // subtotal | quantity
	Tiers  []Tier `json:"tiers,omitempty"`

	GiftSKU string `json:"giftSku,omitempty"`
	GiftQty int64  `json:"giftQty,omitempty"`

	BundleItems []BundleItem `json:"bundleItems,omitempty"`
	BundlePrice int64        `json:"bundlePrice,omitempty"`

	Target      Target     `json:"target"`
	Conditions  Conditions `json:"conditions"`
	Stacking    Stacking   `json:"stacking"`
	MaxDiscount int64      `json:"maxDiscount,omitempty"` // cap per order, minor units; 0 = no cap
}

// Campaign groups promotions under a schedule and a budget.
type Campaign struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"` // active | paused
	StartsAt    *time.Time `json:"startsAt,omitempty"`
	EndsAt      *time.Time `json:"endsAt,omitempty"`
	BudgetType  string     `json:"budgetType"`  // none | amount | uses
	BudgetLimit int64      `json:"budgetLimit"` // minor units or number of uses
	Spent       int64      `json:"spent"`       // discount given so far (minor units)
	Used        int64      `json:"used"`        // redemptions so far
	Currency    string     `json:"currency"`
}

// Coupon is the state of one code, resolved by the caller before evaluating.
type Coupon struct {
	Code               string     `json:"code"`
	PromotionID        string     `json:"promotionId"`
	Status             string     `json:"status"` // active | disabled
	StartsAt           *time.Time `json:"startsAt,omitempty"`
	EndsAt             *time.Time `json:"endsAt,omitempty"`
	MaxUses            int64      `json:"maxUses"`
	MaxUsesPerCustomer int64      `json:"maxUsesPerCustomer"`
	Uses               int64      `json:"uses"`
}

// FieldError points at one invalid field of a definition.
type FieldError struct {
	Field   string
	Message string
}

var (
	idRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	codeRe   = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{2,39}$`)
	domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
)

// ValidID reports whether id can identify a promotion, campaign or price list.
func ValidID(id string) bool { return idRe.MatchString(id) }

// NormalizeCode canonicalizes a coupon code: trimmed, upper case.
func NormalizeCode(c string) string { return strings.ToUpper(strings.TrimSpace(c)) }

// ValidCode reports whether a normalized code is acceptable (3–40 chars, A–Z 0–9 - _).
func ValidCode(c string) bool { return codeRe.MatchString(c) }

func cleanList(in []string, lower bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if lower {
			v = strings.ToLower(v)
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func (t *Target) normalize() {
	t.SKUs, t.ProductIDs = cleanList(t.SKUs, false), cleanList(t.ProductIDs, false)
	t.Categories, t.Brands = cleanList(t.Categories, false), cleanList(t.Brands, false)
	t.Collections, t.ExcludeSKUs = cleanList(t.Collections, false), cleanList(t.ExcludeSKUs, false)
	if len(t.Attributes) > 0 {
		clean := map[string]string{}
		for k, v := range t.Attributes {
			k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
			if k != "" && v != "" && len(clean) < 10 {
				clean[k] = v
			}
		}
		t.Attributes = clean
		if len(clean) == 0 {
			t.Attributes = nil
		}
	}
}

// Normalize trims text, applies defaults and clears the benefit fields that
// the type does not use, so stored definitions are canonical.
func (p *Promotion) Normalize() {
	p.Name, p.Description = strings.TrimSpace(p.Name), strings.TrimSpace(p.Description)
	if p.Status == "" {
		p.Status = StatusActive
	}
	if p.Currency == "" {
		p.Currency = "MXN"
	}
	p.Target.normalize()
	if p.GetTarget != nil {
		p.GetTarget.normalize()
		if p.GetTarget.All() {
			p.GetTarget = nil
		}
	}
	c := &p.Conditions
	c.Channels = cleanList(c.Channels, false)
	c.Segments = cleanList(c.Segments, true)
	c.PaymentMethods = cleanList(c.PaymentMethods, true)
	for i, d := range c.EmailDomains {
		c.EmailDomains[i] = strings.TrimPrefix(strings.TrimSpace(d), "@")
	}
	c.EmailDomains = cleanList(c.EmailDomains, true)
	if p.Stacking.Mode == "" {
		p.Stacking.Mode = StackCombine
	}
	p.Stacking.Group = strings.TrimSpace(p.Stacking.Group)
	if p.Stacking.Mode != StackGroup {
		p.Stacking.Group = ""
	}
	keep := func(types ...string) bool { return has(types, p.Type) }
	if !keep(TypeOrderPercent, TypeItemPercent) {
		p.Bps = 0
	}
	if !keep(TypeOrderFixed, TypeItemFixed) {
		p.Amount = 0
	}
	if !keep(TypeBuyXGetY) {
		p.BuyQty, p.GetQty, p.GetBps, p.GetTarget = 0, 0, 0, nil
	} else if p.GetBps == 0 {
		p.GetBps = 10000
	}
	if !keep(TypeTiered) {
		p.TierBy, p.Tiers = "", nil
	} else {
		sort.SliceStable(p.Tiers, func(i, j int) bool { return p.Tiers[i].Min < p.Tiers[j].Min })
	}
	if !keep(TypeGift) {
		p.GiftSKU, p.GiftQty = "", 0
	} else {
		p.GiftSKU = strings.TrimSpace(p.GiftSKU)
		if p.GiftQty == 0 {
			p.GiftQty = 1
		}
	}
	if !keep(TypeBundle) {
		p.BundleItems, p.BundlePrice = nil, 0
	}
}

const maxAmount = int64(1_000_000_000_00) // 1,000 million in minor units

// Validate checks a normalized definition and returns every problem found.
func (p *Promotion) Validate() []FieldError {
	var errs []FieldError
	add := func(f, m string) { errs = append(errs, FieldError{Field: f, Message: m}) }
	if !ValidID(p.ID) {
		add("id", "Usa minúsculas, números y guiones (por ejemplo buen-fin-10).")
	}
	if p.Name == "" || len(p.Name) > 120 {
		add("name", "Escribe un nombre de hasta 120 caracteres.")
	}
	if len(p.Description) > 500 {
		add("description", "La descripción admite hasta 500 caracteres.")
	}
	if !has(Types, p.Type) {
		add("type", "Tipo de promoción no válido.")
		return errs
	}
	if !has([]string{StatusActive, StatusPaused, StatusDraft, StatusArchived}, p.Status) {
		add("status", "Estado no válido: usa active, paused, draft o archived.")
	}
	if len(p.Currency) != 3 {
		add("currency", "Moneda ISO de 3 letras.")
	}
	pct := func(field string, v int64) {
		if v < 1 || v > 10000 {
			add(field, "El porcentaje debe estar entre 0.01% y 100%.")
		}
	}
	amt := func(field string, v int64) {
		if v < 1 || v > maxAmount {
			add(field, "Escribe un monto mayor a cero.")
		}
	}
	switch p.Type {
	case TypeOrderPercent, TypeItemPercent:
		pct("bps", p.Bps)
	case TypeOrderFixed, TypeItemFixed:
		amt("amount", p.Amount)
	case TypeBuyXGetY:
		if p.BuyQty < 1 || p.BuyQty > 1000 {
			add("buyQty", "La cantidad a comprar debe estar entre 1 y 1,000.")
		}
		if p.GetQty < 1 || p.GetQty > 1000 {
			add("getQty", "La cantidad con beneficio debe estar entre 1 y 1,000.")
		}
		pct("getBps", p.GetBps)
	case TypeTiered:
		if p.TierBy != "subtotal" && p.TierBy != "quantity" {
			add("tierBy", "Elige si los escalones son por monto o por cantidad.")
		}
		if len(p.Tiers) == 0 || len(p.Tiers) > 10 {
			add("tiers", "Agrega entre 1 y 10 escalones.")
		}
		for i, t := range p.Tiers {
			f := fmt.Sprintf("tiers[%d]", i)
			if t.Min < 1 {
				add(f+".min", "El mínimo del escalón debe ser mayor a cero.")
			}
			if i > 0 && t.Min == p.Tiers[i-1].Min {
				add(f+".min", "Hay dos escalones con el mismo mínimo.")
			}
			if (t.Bps > 0) == (t.Amount > 0) {
				add(f, "Cada escalón lleva un porcentaje o un monto (solo uno).")
			} else if t.Bps > 10000 || t.Amount > maxAmount || t.Bps < 0 || t.Amount < 0 {
				add(f, "El beneficio del escalón está fuera de rango.")
			}
		}
	case TypeGift:
		if p.GiftSKU == "" {
			add("giftSku", "Elige el producto de regalo.")
		}
		if p.GiftQty < 1 || p.GiftQty > 100 {
			add("giftQty", "La cantidad de regalo debe estar entre 1 y 100.")
		}
	case TypeBundle:
		if len(p.BundleItems) < 1 || len(p.BundleItems) > 20 {
			add("bundleItems", "El paquete lleva entre 1 y 20 productos.")
		}
		units, bad := int64(0), false
		for i, b := range p.BundleItems {
			f := fmt.Sprintf("bundleItems[%d]", i)
			if (b.SKU == "") == (b.ProductID == "") {
				add(f, "Cada componente es un SKU o un producto.")
				bad = true
			}
			if b.Qty < 1 || b.Qty > 1000 {
				add(f+".qty", "La cantidad debe estar entre 1 y 1,000.")
				bad = true
			}
			units += b.Qty
		}
		if units < 2 && !bad && len(p.BundleItems) > 0 {
			add("bundleItems", "Un paquete necesita al menos 2 unidades en total.")
		}
		amt("bundlePrice", p.BundlePrice)
	}
	if p.Target.size() > 2000 || (p.GetTarget != nil && p.GetTarget.size() > 2000) {
		add("target", "La selección admite hasta 2,000 elementos; usa marcas o categorías para grupos grandes.")
	}
	c := p.Conditions
	if c.MinSubtotal < 0 || c.MinSubtotal > maxAmount {
		add("conditions.minSubtotal", "El mínimo de compra no puede ser negativo.")
	}
	if c.MinQuantity < 0 || c.MinQuantity > 100000 {
		add("conditions.minQuantity", "La cantidad mínima no puede ser negativa.")
	}
	if c.StartsAt != nil && c.EndsAt != nil && !c.EndsAt.After(*c.StartsAt) {
		add("conditions.endsAt", "La fecha de fin debe ser posterior a la de inicio.")
	}
	if c.MaxUses < 0 {
		add("conditions.maxUses", "El límite de usos no puede ser negativo.")
	}
	if c.MaxUsesPerCustomer < 0 {
		add("conditions.maxUsesPerCustomer", "El límite por cliente no puede ser negativo.")
	}
	for _, d := range c.EmailDomains {
		if !domainRe.MatchString(d) {
			add("conditions.emailDomains", "“"+d+"” no es un dominio válido (ejemplo: empresa.com).")
		}
	}
	if len(c.Channels) > 50 || len(c.EmailDomains) > 50 || len(c.Segments) > 50 || len(c.PaymentMethods) > 10 {
		add("conditions", "Demasiados valores en una condición.")
	}
	switch p.Stacking.Mode {
	case StackCombine, StackExclusive:
	case StackGroup:
		if p.Stacking.Group == "" || len(p.Stacking.Group) > 40 {
			add("stacking.group", "Escribe el nombre del grupo con el que se combina.")
		}
	default:
		add("stacking.mode", "Elige cómo se combina: stack, exclusive o group.")
	}
	if p.Stacking.Priority < -1000 || p.Stacking.Priority > 1000 {
		add("stacking.priority", "La prioridad va de -1000 a 1000.")
	}
	if p.MaxDiscount < 0 || p.MaxDiscount > maxAmount {
		add("maxDiscount", "El tope de descuento no puede ser negativo.")
	}
	return errs
}

// DisplayStatus is the status merchants see: activa, programada, vencida,
// pausada, borrador or archivada.
func (p *Promotion) DisplayStatus(now time.Time) string {
	switch p.Status {
	case StatusPaused:
		return "pausada"
	case StatusDraft:
		return "borrador"
	case StatusArchived:
		return "archivada"
	}
	if p.Conditions.EndsAt != nil && now.After(*p.Conditions.EndsAt) {
		return "vencida"
	}
	if p.Conditions.StartsAt != nil && now.Before(*p.Conditions.StartsAt) {
		return "programada"
	}
	return "activa"
}

// Normalize applies defaults to a campaign.
func (c *Campaign) Normalize() {
	c.Name, c.Description = strings.TrimSpace(c.Name), strings.TrimSpace(c.Description)
	if c.Status == "" {
		c.Status = StatusActive
	}
	if c.BudgetType == "" {
		c.BudgetType = "none"
	}
	if c.BudgetType == "none" {
		c.BudgetLimit = 0
	}
	if c.Currency == "" {
		c.Currency = "MXN"
	}
}

// Validate checks a campaign.
func (c *Campaign) Validate() []FieldError {
	var errs []FieldError
	if !ValidID(c.ID) {
		errs = append(errs, FieldError{"id", "Usa minúsculas, números y guiones (por ejemplo buen-fin-2026)."})
	}
	if c.Name == "" || len(c.Name) > 120 {
		errs = append(errs, FieldError{"name", "Escribe un nombre de hasta 120 caracteres."})
	}
	if len(c.Description) > 500 {
		errs = append(errs, FieldError{"description", "La descripción admite hasta 500 caracteres."})
	}
	if c.Status != StatusActive && c.Status != StatusPaused {
		errs = append(errs, FieldError{"status", "Estado no válido: usa active o paused."})
	}
	switch c.BudgetType {
	case "none":
	case "amount", "uses":
		if c.BudgetLimit < 1 || c.BudgetLimit > maxAmount {
			errs = append(errs, FieldError{"budgetLimit", "Escribe un presupuesto mayor a cero."})
		}
	default:
		errs = append(errs, FieldError{"budgetType", "Presupuesto no válido: usa none, amount o uses."})
	}
	if c.StartsAt != nil && c.EndsAt != nil && !c.EndsAt.After(*c.StartsAt) {
		errs = append(errs, FieldError{"endsAt", "La fecha de fin debe ser posterior a la de inicio."})
	}
	return errs
}

// DisplayStatus mirrors Promotion.DisplayStatus for campaigns, plus "agotada".
func (c *Campaign) DisplayStatus(now time.Time) string {
	if c.Status == StatusPaused {
		return "pausada"
	}
	if c.EndsAt != nil && now.After(*c.EndsAt) {
		return "vencida"
	}
	if c.StartsAt != nil && now.Before(*c.StartsAt) {
		return "programada"
	}
	if c.Exhausted() {
		return "agotada"
	}
	return "activa"
}

// Exhausted reports whether the campaign budget is used up.
func (c *Campaign) Exhausted() bool {
	switch c.BudgetType {
	case "amount":
		return c.Spent >= c.BudgetLimit
	case "uses":
		return c.Used >= c.BudgetLimit
	}
	return false
}

// ParseTime reads an instant sent by a client: RFC 3339, or a wall-clock
// time without offset ("2026-11-13T00:00"), read in the store timezone.
func ParseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, Location); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("fecha no válida: %q", s)
}
