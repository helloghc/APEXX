package logistics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// QuoteInput is what the engine needs to know about a cart.
type QuoteInput struct {
	Country     string
	PostalCode  string
	Subtotal    int64    // after discounts, minor units
	WeightGrams int64    // total weight
	Profiles    []string // shipping profile of every line ("" = default)
	Currency    string
	Now         time.Time
}

// Estimate is a delivery promise with the reasons behind it.
type Estimate struct {
	Dispatch string   `json:"dispatch"` // day the order leaves (YYYY-MM-DD)
	From     string   `json:"from"`
	To       string   `json:"to"`
	Text     string   `json:"text"`
	Notes    []string `json:"notes"`
}

// Quote is one option evaluated for a cart: available or not, and why.
type Quote struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Carrier       string    `json:"carrier,omitempty"`
	CarrierID     string    `json:"carrierId,omitempty"`
	Zone          string    `json:"zone,omitempty"`
	LocationID    string    `json:"locationId,omitempty"`
	LocationName  string    `json:"locationName,omitempty"`
	PickupAddress string    `json:"pickupAddress,omitempty"`
	Instructions  string    `json:"instructions,omitempty"`
	Available     bool      `json:"available"`
	Reason        string    `json:"reason"`      // why it is (not) available
	Price         int64     `json:"price"`       // minor units
	PriceReason   string    `json:"priceReason"` // how the price was computed
	Free          bool      `json:"free"`
	FreeMissing   int64     `json:"freeMissing,omitempty"` // how much more to buy for free shipping
	Estimate      *Estimate `json:"estimate,omitempty"`
	EstimateDays  int       `json:"estimateDays"`
}

// FormatMoney renders minor units for explanations: 149900 → "$1,499.00".
func FormatMoney(amount int64) string {
	neg := amount < 0
	if neg {
		amount = -amount
	}
	s := strconv.FormatInt(amount/100, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	out := "$" + s + "." + fmt.Sprintf("%02d", amount%100)
	if neg {
		return "-" + out
	}
	return out
}

func kilos(grams int64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(float64(grams)/1000, 'f', 2, 64), "0"), ".") + " kg"
}

// Price returns the price of a shipment, how it was computed and whether the
// rate covers the shipment at all.
func (r Rate) Price(subtotal, weightGrams int64) (amount int64, reason string, ok bool) {
	switch r.Type {
	case RateWeight:
		if len(r.Brackets) == 0 {
			kg := (weightGrams + 999) / 1000
			amount = r.Amount + r.PerKg*kg
			reason = fmt.Sprintf("Por peso: base %s + %s × %d kg (peso %s)", FormatMoney(r.Amount), FormatMoney(r.PerKg), kg, kilos(weightGrams))
			break
		}
		found := false
		for _, b := range r.Brackets {
			if b.UpTo == 0 || weightGrams <= b.UpTo {
				amount, found = b.Amount, true
				if b.UpTo == 0 {
					reason = fmt.Sprintf("Por peso: tramo sin límite (peso %s)", kilos(weightGrams))
				} else {
					reason = fmt.Sprintf("Por peso: tramo hasta %s (peso %s)", kilos(b.UpTo), kilos(weightGrams))
				}
				break
			}
		}
		if !found {
			last := r.Brackets[len(r.Brackets)-1]
			if r.PerKg <= 0 {
				return 0, fmt.Sprintf("El peso (%s) supera el tramo máximo de %s", kilos(weightGrams), kilos(last.UpTo)), false
			}
			extra := (weightGrams - last.UpTo + 999) / 1000
			amount = last.Amount + r.PerKg*extra
			reason = fmt.Sprintf("Por peso: tramo hasta %s + %s × %d kg adicionales (peso %s)", kilos(last.UpTo), FormatMoney(r.PerKg), extra, kilos(weightGrams))
		}
	case RateSubtotal:
		found := false
		for _, b := range r.Brackets {
			if b.UpTo == 0 || subtotal <= b.UpTo {
				amount, found = b.Amount, true
				if b.UpTo == 0 {
					reason = fmt.Sprintf("Por monto: tramo sin límite (pedido de %s)", FormatMoney(subtotal))
				} else {
					reason = fmt.Sprintf("Por monto: pedidos hasta %s (pedido de %s)", FormatMoney(b.UpTo), FormatMoney(subtotal))
				}
				break
			}
		}
		if !found {
			return 0, fmt.Sprintf("No hay tarifa para un pedido de %s", FormatMoney(subtotal)), false
		}
	default:
		amount, reason = r.Amount, "Tarifa fija"
	}
	if r.FreeOver > 0 && subtotal >= r.FreeOver {
		return 0, fmt.Sprintf("Gratis: el pedido (%s) alcanza el mínimo de %s", FormatMoney(subtotal), FormatMoney(r.FreeOver)), true
	}
	return amount, reason, true
}

var (
	dayNames   = []string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}
	monthNames = []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
)

// HumanDate renders "mar 12 nov".
func HumanDate(t time.Time) string {
	return fmt.Sprintf("%s %d %s", dayNames[t.Weekday()], t.Day(), monthNames[t.Month()-1])
}

const dateFmt = "2006-01-02"

func parseClock(s string) (h, m int, ok bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, false
	}
	return t.Hour(), t.Minute(), true
}

// Compute returns the delivery estimate for an order placed at now.
// holidays maps YYYY-MM-DD to the holiday name; pickup changes the wording.
func (r EstimateRule) Compute(now time.Time, holidays map[string]string, pickup bool) Estimate {
	var notes []string
	business := func(d time.Time) (bool, string) {
		if name, ok := holidays[d.Format(dateFmt)]; ok {
			return false, name
		}
		switch d.Weekday() {
		case time.Saturday:
			return r.Weekend == "saturday" || r.Weekend == "all", ""
		case time.Sunday:
			return r.Weekend == "all", ""
		}
		return true, ""
	}
	skipped := map[string]bool{}
	skippedWeekend := false
	next := func(d time.Time) time.Time {
		for i := 0; i < 400; i++ {
			d = d.AddDate(0, 0, 1)
			ok, holiday := business(d)
			if ok {
				return d
			}
			if holiday != "" && !skipped[holiday] {
				skipped[holiday] = true
				notes = append(notes, fmt.Sprintf("No cuenta el feriado %s (%s)", holiday, HumanDate(d)))
			} else if holiday == "" {
				skippedWeekend = true
			}
		}
		return d
	}
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dispatch := day
	if ok, holiday := business(day); !ok {
		if holiday != "" {
			skipped[holiday] = true
			notes = append(notes, fmt.Sprintf("Hoy es feriado (%s): sale el siguiente día hábil", holiday))
		} else {
			notes = append(notes, "Hoy no es día hábil: sale el siguiente día hábil")
		}
		dispatch = next(day)
	} else if h, m, ok := parseClock(r.Cutoff); ok && (now.Hour() > h || (now.Hour() == h && now.Minute() >= m)) {
		notes = append(notes, fmt.Sprintf("Pedido después de las %s: sale el siguiente día hábil", r.Cutoff))
		dispatch = next(day)
	} else if ok {
		notes = append(notes, fmt.Sprintf("Pedido antes de las %s: sale hoy", r.Cutoff))
	}
	minD, maxD := max(r.MinDays, 0), max(r.MaxDays, r.MinDays, 0)
	from := dispatch
	for i := 0; i < minD; i++ {
		from = next(from)
	}
	to := from
	for i := minD; i < maxD; i++ {
		to = next(to)
	}
	if skippedWeekend {
		switch r.Weekend {
		case "all":
		case "saturday":
			notes = append(notes, "Los domingos no cuentan como día hábil")
		default:
			notes = append(notes, "Sábados y domingos no cuentan como día hábil")
		}
	}
	e := Estimate{Dispatch: dispatch.Format(dateFmt), From: from.Format(dateFmt), To: to.Format(dateFmt), Notes: notes}
	verb := "Llega"
	if pickup {
		verb = "Listo para recoger"
	}
	switch {
	case from.Equal(to) && from.Equal(day):
		e.Text = verb + " hoy"
	case from.Equal(to):
		e.Text = verb + " el " + HumanDate(from)
	default:
		e.Text = verb + " entre el " + HumanDate(from) + " y el " + HumanDate(to)
	}
	if e.Notes == nil {
		e.Notes = []string{}
	}
	return e
}

func (c *Config) holidayMap() map[string]string {
	m := make(map[string]string, len(c.Holidays))
	for _, h := range c.Holidays {
		m[h.Date] = h.Name
	}
	return m
}

// normProfile maps the default profile (and unknown ids) to "".
func (c *Config) normProfile(id string) string {
	if id == "" {
		return ""
	}
	for _, p := range c.Profiles {
		if p.ID == id {
			if p.Kind == "default" {
				return ""
			}
			return id
		}
	}
	return ""
}

// Quotes evaluates every active option for the cart. Unavailable options are
// returned too, with the reason, so the shopper and the merchant can see why.
func (c *Config) Quotes(in QuoteInput) []Quote {
	if c == nil {
		return nil
	}
	now := in.Now.In(c.Location())
	holidays := c.holidayMap()

	// Distinct non-default profiles in the cart.
	special := map[string]bool{}
	for _, p := range in.Profiles {
		if n := c.normProfile(p); n != "" {
			special[n] = true
		}
	}
	var specialIDs []string
	for id := range special {
		specialIDs = append(specialIDs, id)
	}
	sort.Strings(specialIDs)

	// The most specific matching zone wins (postal range > state > country).
	type zm struct {
		ok     bool
		spec   int
		reason string
	}
	matches := map[string]zm{}
	best, bestName := -1, ""
	for _, z := range c.Zones {
		if !z.Active {
			continue
		}
		ok, spec, reason := z.Match(in.Country, in.PostalCode)
		matches[z.ID] = zm{ok, spec, reason}
		if ok && spec > best {
			best, bestName = spec, z.Name
		}
	}

	opts := append([]Option(nil), c.Options...)
	SortOptions(opts)
	out := make([]Quote, 0, len(opts))
	for _, o := range opts {
		if !o.Active {
			continue
		}
		q := Quote{ID: o.ID, Name: o.Name, Kind: o.Kind, EstimateDays: max(o.Estimate.MaxDays, o.Estimate.MinDays)}
		if car, ok := c.carrier(o.CarrierID); ok {
			q.Carrier, q.CarrierID = car.Name, car.ID
		}
		fail := func(reason string) {
			q.Available, q.Reason = false, reason
			out = append(out, q)
		}
		if o.Kind == KindPickup {
			loc, ok := c.LocationByID(o.LocationID)
			if !ok || !loc.Active || !loc.PickupEnabled {
				fail("La ubicación no ofrece recolección en este momento")
				continue
			}
			q.LocationID, q.LocationName, q.PickupAddress, q.Instructions = loc.ID, loc.Name, loc.Address.OneLine(), loc.PickupInstructions
			q.Reason = "Recoge tu pedido en " + loc.Name
		} else {
			z, ok := c.zone(o.ZoneID)
			m := matches[o.ZoneID]
			q.Zone = z.Name
			switch {
			case !ok || !z.Active:
				fail("La zona de esta opción no está activa")
				continue
			case !m.ok:
				fail("Zona " + z.Name + ": " + m.reason)
				continue
			case m.spec < best:
				fail("Tu código postal pertenece a una zona más específica (" + bestName + ")")
				continue
			}
			q.Reason = "Zona " + z.Name + ": " + m.reason
			op := c.normProfile(o.ProfileID)
			switch {
			case len(specialIDs) > 1:
				names := make([]string, len(specialIDs))
				for i, id := range specialIDs {
					names[i] = c.profileName(id)
				}
				fail("El carrito mezcla productos de los perfiles " + strings.Join(names, " y ") + "; se envían en pedidos separados")
				continue
			case len(specialIDs) == 1 && op != specialIDs[0]:
				fail("El carrito tiene productos del perfil " + c.profileName(specialIDs[0]) + " que esta opción no transporta")
				continue
			case len(specialIDs) == 0 && op != "":
				fail("Solo para productos del perfil " + c.profileName(op))
				continue
			}
		}
		if o.MaxWeightGrams > 0 && in.WeightGrams > o.MaxWeightGrams {
			fail(fmt.Sprintf("El peso del pedido (%s) supera el máximo de %s", kilos(in.WeightGrams), kilos(o.MaxWeightGrams)))
			continue
		}
		price, why, ok := o.Rate.Price(in.Subtotal, in.WeightGrams)
		if !ok {
			fail(why)
			continue
		}
		q.Available, q.Price, q.PriceReason, q.Free = true, price, why, price == 0
		if o.Rate.FreeOver > 0 && in.Subtotal < o.Rate.FreeOver && price > 0 {
			q.FreeMissing = o.Rate.FreeOver - in.Subtotal
		}
		est := o.Estimate.Compute(now, holidays, o.Kind == KindPickup)
		q.Estimate = &est
		out = append(out, q)
	}
	return out
}

// Choose returns the index of the quote to use: the requested one when it is
// available, otherwise the cheapest available delivery (pickup is never
// chosen for the shopper). -1 when nothing is available.
func Choose(quotes []Quote, wanted string) int {
	if wanted != "" {
		for i, q := range quotes {
			if q.ID == wanted && q.Available {
				return i
			}
		}
	}
	best := -1
	for i, q := range quotes {
		if !q.Available || q.Kind == KindPickup {
			continue
		}
		if best < 0 || q.Price < quotes[best].Price {
			best = i
		}
	}
	if best < 0 {
		for i, q := range quotes {
			if q.Available {
				return i
			}
		}
	}
	return best
}

// ---------- taxes ----------

// TaxPart is an amount (tax included) that belongs to one tax class.
type TaxPart struct {
	Class string
	Gross int64
}

// TaxLine is the tax of one class.
type TaxLine struct {
	Class   string `json:"class"`
	Label   string `json:"label"`
	RateBps int64  `json:"rateBps"`
	Gross   int64  `json:"gross"` // amount with tax
	Net     int64  `json:"net"`   // amount without tax
	Tax     int64  `json:"tax"`
}

// TaxResult explains the tax contained in a tax-inclusive total.
type TaxResult struct {
	RegionID   string    `json:"regionId"`
	RegionName string    `json:"regionName"`
	Reason     string    `json:"reason"`
	Inclusive  bool      `json:"pricesIncludeTax"`
	Lines      []TaxLine `json:"lines"`
	Total      int64     `json:"total"`
}

// TaxRegionFor picks the tax region of a destination and says why.
// channelRegion is the region configured on the sales channel; fallbackBps is
// the legacy channel rate used when the store has no regions.
func (c *Config) TaxRegionFor(country, cp, channelRegion string, fallbackBps int64) (TaxRegion, string) {
	if c == nil || len(c.TaxRegions) == 0 {
		return TaxRegion{Name: "Canal de venta", RateBps: fallbackBps}, "Tasa configurada en el canal de venta"
	}
	var hit *TaxRegion
	bestSpec, why := 0, ""
	for i := range c.TaxRegions {
		r := &c.TaxRegions[i]
		ok, spec, reason := area{[]string{r.Country}, r.States, r.PostalRanges}.match(country, cp)
		if ok && spec > bestSpec {
			hit, bestSpec, why = r, spec, reason
		}
	}
	if hit != nil {
		return *hit, "Región " + hit.Name + ": " + why
	}
	whole := func(r TaxRegion) bool { return len(r.States) == 0 && len(r.PostalRanges) == 0 }
	for _, r := range c.TaxRegions {
		if r.ID == channelRegion && channelRegion != "" && whole(r) {
			return r, "Región " + r.Name + ": región fiscal del canal de venta"
		}
	}
	for _, r := range c.TaxRegions {
		if r.Default && whole(r) {
			return r, "Región " + r.Name + ": región predeterminada de la tienda"
		}
	}
	for _, r := range c.TaxRegions {
		if whole(r) {
			return r, "Región " + r.Name + ": cubre todo el país"
		}
	}
	return TaxRegion{Name: "Canal de venta", RateBps: fallbackBps}, "Ninguna región cubre el destino; se usa la tasa del canal"
}

// ClassRate returns the rate and label of a tax class in a region.
func ClassRate(r TaxRegion, class string) (int64, string) {
	switch class {
	case TaxExempt:
		return 0, "Exento de IVA"
	case TaxZero:
		return 0, "IVA tasa 0%"
	}
	return r.RateBps, "IVA " + percent(r.RateBps)
}

func percent(bps int64) string {
	s := strconv.FormatFloat(float64(bps)/100, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".") + "%"
}

// Tax computes the tax contained in tax-inclusive amounts, grouped by class.
func (c *Config) Tax(country, cp, channelRegion string, fallbackBps int64, parts []TaxPart) TaxResult {
	region, reason := c.TaxRegionFor(country, cp, channelRegion, fallbackBps)
	res := TaxResult{RegionID: region.ID, RegionName: region.Name, Reason: reason, Inclusive: true, Lines: []TaxLine{}}
	gross := map[string]int64{}
	for _, p := range parts {
		cl := p.Class
		if cl != TaxExempt && cl != TaxZero {
			cl = TaxGeneral
		}
		gross[cl] += p.Gross
	}
	for _, cl := range []string{TaxGeneral, TaxZero, TaxExempt} {
		g, ok := gross[cl]
		if !ok || g == 0 {
			continue
		}
		rate, label := ClassRate(region, cl)
		tax := g * rate / (10000 + rate)
		res.Lines = append(res.Lines, TaxLine{Class: cl, Label: label, RateBps: rate, Gross: g, Net: g - tax, Tax: tax})
		res.Total += tax
	}
	return res
}
