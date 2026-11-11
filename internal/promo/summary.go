package promo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/helloghc/apexx/internal/money"
)

var months = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// Pesos formats minor units for reading: 150000 -> "$1,500", 149950 -> "$1,499.50".
func Pesos(minor int64) string {
	neg := ""
	if minor < 0 {
		neg, minor = "-", -minor
	}
	whole, cents := minor/100, minor%100
	s := fmt.Sprint(whole)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if cents == 0 {
		return neg + "$" + s
	}
	return fmt.Sprintf("%s$%s.%02d", neg, s, cents)
}

// Percent formats basis points: 1000 -> "10%", 1250 -> "12.5%".
func Percent(bps int64) string {
	if bps%100 == 0 {
		return fmt.Sprintf("%d%%", bps/100)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(bps)/100), "0"), ".") + "%"
}

// When formats an instant in the store timezone: "13 de noviembre, 00:00".
func When(t time.Time) string {
	t = t.In(Location)
	return fmt.Sprintf("%d de %s, %02d:%02d", t.Day(), months[t.Month()-1], t.Hour(), t.Minute())
}

func day(t time.Time, withYear bool) string {
	t = t.In(Location)
	s := fmt.Sprintf("%d de %s", t.Day(), months[t.Month()-1])
	if withYear {
		s += fmt.Sprintf(" de %d", t.Year())
	}
	if t.Hour() != 0 || t.Minute() != 0 {
		if !(t.Hour() == 23 && t.Minute() == 59) {
			s += fmt.Sprintf(" %02d:%02d", t.Hour(), t.Minute())
		}
	}
	return s
}

func times(n int64) string {
	if n == 1 {
		return "1 uso"
	}
	return fmt.Sprintf("%d usos", n)
}

func units(n int64) string {
	if n == 1 {
		return "1 pieza"
	}
	return fmt.Sprintf("%d piezas", n)
}

func domains(ds []string) string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = "@" + d
	}
	return joinOr(out)
}

func joinOr(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " o " + xs[len(xs)-1]
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	last := xs[len(xs)-1]
	conj := " y "
	if strings.HasPrefix(strings.ToLower(last), "i") || strings.HasPrefix(strings.ToLower(last), "hi") {
		conj = " e "
	}
	return strings.Join(xs[:len(xs)-1], ", ") + conj + last
}

func paymentNames(ms []string) string {
	names := map[string]string{"card": "tarjeta", "cash": "efectivo", "transfer": "transferencia"}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m
		if n, ok := names[m]; ok {
			out[i] = n
		}
	}
	return joinOr(out)
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

// Namer turns a product id or SKU into a title for summaries; nil is fine.
type Namer func(kind, id string) string

// targetPhrase describes a target: "Tenis Norte", "3 productos", "todo el catálogo".
func targetPhrase(t Target, name Namer) string {
	if t.All() {
		return "todo el catálogo"
	}
	var parts []string
	n := len(t.SKUs) + len(t.ProductIDs)
	if n > 0 {
		var names []string
		if name != nil && n <= 2 {
			for _, id := range t.ProductIDs {
				if s := name("product", id); s != "" {
					names = append(names, s)
				}
			}
			for _, id := range t.SKUs {
				if s := name("sku", id); s != "" {
					names = append(names, s)
				}
			}
		}
		switch {
		case len(names) == n:
			parts = append(parts, joinAnd(names))
		case n == 1:
			parts = append(parts, "1 producto seleccionado")
		default:
			parts = append(parts, fmt.Sprintf("%d productos seleccionados", n))
		}
	}
	cb := strings.TrimSpace(joinAnd(t.Categories) + " " + joinAnd(t.Brands))
	if cb != "" {
		parts = append(parts, cb)
	}
	if len(t.Collections) > 0 {
		parts = append(parts, "la colección "+joinAnd(t.Collections))
	}
	if len(t.Attributes) > 0 {
		keys := make([]string, 0, len(t.Attributes))
		for k := range t.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var attrs []string
		for _, k := range keys {
			attrs = append(attrs, k+" "+t.Attributes[k])
		}
		parts = append(parts, "productos con "+joinAnd(attrs))
	}
	s := strings.Join(parts, " de ")
	if s == "" {
		s = "todo el catálogo"
	}
	if len(t.ExcludeSKUs) > 0 {
		s += fmt.Sprintf(" (excepto %d)", len(t.ExcludeSKUs))
	}
	return s
}

// Summary writes the promotion in one plain sentence, for example
// "10% en Tenis Norte desde $1,500, del 13 al 20 de noviembre".
func Summary(p Promotion, name Namer) string {
	on := func() string {
		if p.Target.All() {
			return ""
		}
		return " en " + targetPhrase(p.Target, name)
	}
	var s string
	switch p.Type {
	case TypeOrderPercent:
		s = Percent(p.Bps) + " en todo el pedido"
		if !p.Target.All() {
			s += " al llevar " + targetPhrase(p.Target, name)
		}
	case TypeOrderFixed:
		s = Pesos(p.Amount) + " de descuento en el pedido"
		if !p.Target.All() {
			s += " al llevar " + targetPhrase(p.Target, name)
		}
	case TypeItemPercent:
		s = Percent(p.Bps) + on()
		if p.Target.All() {
			s += " en todos los productos"
		}
	case TypeItemFixed:
		s = Pesos(p.Amount) + " menos por pieza" + on()
	case TypeFreeShipping:
		s = "Envío gratis"
		if !p.Target.All() {
			s += " al llevar " + targetPhrase(p.Target, name)
		}
	case TypeBuyXGetY:
		benefit := "gratis"
		if p.GetBps > 0 && p.GetBps < 10000 {
			benefit = "con " + Percent(p.GetBps) + " de descuento"
		}
		if p.GetTarget == nil {
			if p.GetBps == 10000 || p.GetBps == 0 {
				s = fmt.Sprintf("%dx%d", p.BuyQty+p.GetQty, p.BuyQty) + on()
			} else {
				s = fmt.Sprintf("Compra %d y lleva %d más %s", p.BuyQty, p.GetQty, benefit) + on()
			}
			s += " (la de menor precio)"
		} else {
			s = fmt.Sprintf("Compra %d de %s y lleva %d de %s %s", p.BuyQty, targetPhrase(p.Target, name), p.GetQty, targetPhrase(*p.GetTarget, name), benefit)
		}
	case TypeTiered:
		var steps []string
		for _, t := range p.Tiers {
			b := Pesos(t.Amount)
			if t.Bps > 0 {
				b = Percent(t.Bps)
			}
			from := Pesos(t.Min)
			if p.TierBy == "quantity" {
				from = units(t.Min)
			}
			steps = append(steps, b+" desde "+from)
		}
		s = "Descuento escalonado" + on() + ": " + strings.Join(steps, ", ")
	case TypeGift:
		g := p.GiftSKU
		if name != nil {
			if n := name("sku", p.GiftSKU); n != "" {
				g = n
			}
		}
		s = "Regalo: " + g
		if p.GiftQty > 1 {
			s = fmt.Sprintf("Regalo: %d × %s", p.GiftQty, g)
		}
		if !p.Target.All() {
			s += " al llevar " + targetPhrase(p.Target, name)
		}
	case TypeBundle:
		var items []string
		for _, b := range p.BundleItems {
			id, kind := b.SKU, "sku"
			if id == "" {
				id, kind = b.ProductID, "product"
			}
			if name != nil {
				if n := name(kind, id); n != "" {
					id = n
				}
			}
			items = append(items, fmt.Sprintf("%d × %s", b.Qty, id))
		}
		s = "Paquete " + strings.Join(items, " + ") + " por " + Pesos(p.BundlePrice)
	default:
		s = p.Name
	}
	c := p.Conditions
	if c.MinSubtotal > 0 {
		s += " desde " + Pesos(c.MinSubtotal)
	}
	if c.MinQuantity > 0 {
		s += " llevando " + units(c.MinQuantity) + " o más"
	}
	if w := window(c.StartsAt, c.EndsAt); w != "" {
		s += ", " + w
	}
	var extra []string
	if c.RequiresCoupon {
		extra = append(extra, "con cupón")
	}
	if len(c.Channels) > 0 {
		extra = append(extra, "solo en el canal "+joinOr(c.Channels))
	}
	if len(c.EmailDomains) > 0 {
		extra = append(extra, "para correos "+domains(c.EmailDomains))
	}
	if len(c.Segments) > 0 {
		extra = append(extra, "para el segmento "+joinOr(c.Segments))
	}
	if len(c.PaymentMethods) > 0 {
		extra = append(extra, "pagando con "+paymentNames(c.PaymentMethods))
	}
	if p.MaxDiscount > 0 {
		extra = append(extra, "tope de "+Pesos(p.MaxDiscount)+" por pedido")
	}
	if c.MaxUses > 0 {
		extra = append(extra, "máximo "+times(c.MaxUses)+" en total")
	}
	if c.MaxUsesPerCustomer > 0 {
		extra = append(extra, times(c.MaxUsesPerCustomer)+" por cliente")
	}
	switch p.Stacking.Mode {
	case StackExclusive:
		extra = append(extra, "no se combina con otras promociones")
	case StackGroup:
		extra = append(extra, "se combina solo con el grupo "+p.Stacking.Group)
	}
	if len(extra) > 0 {
		s += ". " + upperFirst(strings.Join(extra, "; "))
	}
	return s + "."
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// window describes a schedule: "del 13 al 20 de noviembre".
func window(from, to *time.Time) string {
	switch {
	case from != nil && to != nil:
		f, t := from.In(Location), to.In(Location)
		plain := func(x time.Time) bool {
			return (x.Hour() == 0 && x.Minute() == 0) || (x.Hour() == 23 && x.Minute() == 59)
		}
		if f.Year() == t.Year() && f.Month() == t.Month() && plain(f) && plain(t) {
			return fmt.Sprintf("del %d al %d de %s", f.Day(), t.Day(), months[t.Month()-1])
		}
		return "del " + day(f, f.Year() != t.Year()) + " al " + day(t, f.Year() != t.Year())
	case from != nil:
		return "a partir del " + day(*from, false)
	case to != nil:
		return "hasta el " + day(*to, false)
	}
	return ""
}

// ---------- legacy rules (phase 0) ----------

// FromLegacy converts the two phase-0 rule kinds, still accepted by
// PUT /v1/promotions/{id} and stored by older tools, into a definition.
func FromLegacy(id, kind string, rule []byte, active bool) (Promotion, error) {
	var w struct {
		Name     string       `json:"name"`
		Channels []string     `json:"channels"`
		From     *time.Time   `json:"from"`
		To       *time.Time   `json:"to"`
		Bps      int64        `json:"bps"`
		Min      *money.Money `json:"min"`
		SKU      string       `json:"sku"`
		X        int64        `json:"x"`
		Y        int64        `json:"y"`
	}
	if err := json.Unmarshal(rule, &w); err != nil {
		return Promotion{}, fmt.Errorf("promotion %s: %w", id, err)
	}
	p := Promotion{ID: id, Name: w.Name, Status: StatusActive, Currency: "MXN"}
	if !active {
		p.Status = StatusPaused
	}
	if w.From != nil && !w.From.IsZero() {
		p.Conditions.StartsAt = w.From
	}
	if w.To != nil && !w.To.IsZero() {
		p.Conditions.EndsAt = w.To
	}
	p.Conditions.Channels = w.Channels
	switch kind {
	case "percent_over_threshold":
		p.Type, p.Bps = TypeOrderPercent, w.Bps
		if w.Min != nil {
			p.Conditions.MinSubtotal = w.Min.Amount
			if w.Min.Currency != "" {
				p.Currency = w.Min.Currency
			}
		}
	case "buy_x_pay_y":
		if w.X <= w.Y || w.Y < 1 {
			return Promotion{}, fmt.Errorf("promotion %s: x debe ser mayor que y", id)
		}
		p.Type, p.BuyQty, p.GetQty, p.GetBps = TypeBuyXGetY, w.Y, w.X-w.Y, 10000
		p.Target.SKUs = []string{w.SKU}
	default:
		return Promotion{}, fmt.Errorf("promotion %s: kind %q desconocido", id, kind)
	}
	if p.Name == "" {
		p.Name = id
	}
	p.Normalize()
	return p, nil
}

// IsLegacyKind reports whether kind is one of the phase-0 rule kinds.
func IsLegacyKind(kind string) bool { return kind == "percent_over_threshold" || kind == "buy_x_pay_y" }

// Window describes a schedule in the store timezone: "del 13 al 20 de noviembre".
func Window(from, to *time.Time) string { return window(from, to) }
