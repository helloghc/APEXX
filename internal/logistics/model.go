// Package logistics decides how an order can be delivered and how much tax
// it carries: zones by country, state and postal-code range, shipping options
// with flat, weight, subtotal and free-over rates, carriers, delivery
// estimates with cut-off time and holidays, store pickup and tax regions.
//
// Everything is a pure function of a Config snapshot. The projector swaps the
// snapshot when a rule changes, so a new rate applies to the next simulation
// (VTEX needs a reindex for logistics changes). Every answer explains itself:
// each option says why it is or is not available and how its price and date
// were computed.
package logistics

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// PostalRange is an inclusive range of 5-digit Mexican postal codes.
type PostalRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Contains reports whether cp is inside the range.
func (r PostalRange) Contains(cp string) bool { return cp >= r.From && cp <= r.To }

// Zone is a delivery area.
type Zone struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Countries    []string      `json:"countries"`    // ISO 3166-1 alpha-2
	States       []string      `json:"states"`       // state codes (see States)
	PostalRanges []PostalRange `json:"postalRanges"` // empty + no states = whole country
	Active       bool          `json:"active"`
}

// Profile groups products that ship the same way.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // default | heavy | custom
	Description string `json:"description,omitempty"`
}

// Carrier is a delivery company.
type Carrier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TrackingURL string `json:"trackingUrl"` // template with {tracking}
	Active      bool   `json:"active"`
	// Connection: "" (manual: tracking typed by hand), "sandbox" (test
	// carrier) or "http" (a carrier app at Endpoint speaking Apexx's contract).
	// Connected carriers give live rates, labels and automatic tracking.
	Provider string `json:"provider,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// Connected tells whether Apexx can ask this carrier for rates and labels.
func (c Carrier) Connected() bool {
	return c.Provider == "sandbox" || (c.Provider == "http" && c.Endpoint != "")
}

// TrackingLink fills the carrier template for a tracking number.
func (c Carrier) TrackingLink(tracking string) string {
	if c.TrackingURL == "" || tracking == "" {
		return ""
	}
	return strings.ReplaceAll(c.TrackingURL, "{tracking}", tracking)
}

// Bracket prices everything up to UpTo (grams or minor units). UpTo 0 = no limit.
type Bracket struct {
	UpTo   int64 `json:"upTo"`
	Amount int64 `json:"amount"`
}

// Rate types.
const (
	RateFlat     = "flat"
	RateWeight   = "weight"
	RateSubtotal = "subtotal"
	// RateLive asks the option's carrier at checkout; Amount is the fallback
	// charged when the carrier does not answer in time.
	RateLive = "live"
)

// Rate prices one shipment. Amounts are minor units of the channel currency.
type Rate struct {
	Type     string    `json:"type"`
	Amount   int64     `json:"amount"`   // flat price; base price for weight without brackets
	PerKg    int64     `json:"perKg"`    // weight: per started kg (no brackets) or per started kg over the last bracket
	Brackets []Bracket `json:"brackets"` // weight (grams) or subtotal (minor units), ascending
	FreeOver int64     `json:"freeOver"` // subtotal after discounts from which it is free; 0 = never
}

// EstimateRule describes how long a delivery takes.
type EstimateRule struct {
	MinDays int    `json:"minDays"` // business days after dispatch
	MaxDays int    `json:"maxDays"`
	Cutoff  string `json:"cutoff"`  // "14:00" local time; orders after it leave the next business day
	Weekend string `json:"weekend"` // none | saturday | all: which weekend days count as business days
}

// Option kinds.
const (
	KindDelivery = "delivery"
	KindPickup   = "pickup"
)

// Option is one way to receive an order.
type Option struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Kind           string       `json:"kind"`
	ZoneID         string       `json:"zoneId,omitempty"`
	ProfileID      string       `json:"profileId"` // "" = default profile
	CarrierID      string       `json:"carrierId,omitempty"`
	LocationID     string       `json:"locationId,omitempty"` // pickup
	Rate           Rate         `json:"rate"`
	Estimate       EstimateRule `json:"estimate"`
	MaxWeightGrams int64        `json:"maxWeightGrams"` // 0 = no limit
	Active         bool         `json:"active"`
	Position       int          `json:"position"`
}

// Holiday is a day without dispatch or delivery.
type Holiday struct {
	Date string `json:"date"` // 2026-12-25
	Name string `json:"name"`
}

// Address of a location or of the store.
type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
}

// OneLine renders the address for people.
func (a Address) OneLine() string {
	parts := []string{}
	for _, p := range []string{a.Line1, a.Line2, a.City, StateName(a.State), a.PostalCode} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, strings.TrimSpace(p))
		}
	}
	return strings.Join(parts, ", ")
}

// Hours of one weekday.
type Hours struct {
	Open   string `json:"open"`
	Close  string `json:"close"`
	Closed bool   `json:"closed"`
}

// Location is a warehouse or a store that holds stock.
type Location struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Kind               string           `json:"kind"` // warehouse | store
	Address            Address          `json:"address"`
	Phone              string           `json:"phone,omitempty"`
	Hours              map[string]Hours `json:"hours,omitempty"` // mon..sun
	PickupEnabled      bool             `json:"pickupEnabled"`
	PickupInstructions string           `json:"pickupInstructions,omitempty"`
	Active             bool             `json:"active"`
	// FulfillerEndpoint: a third-party warehouse (3PL) runs this location;
	// paid orders with stock here are sent to it, signed, to be shipped.
	FulfillerEndpoint string `json:"fulfillerEndpoint,omitempty"`
}

// Weekdays in the order used by Location.Hours.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Tax classes of a product.
const (
	TaxGeneral = "general"
	TaxExempt  = "exento"
	TaxZero    = "tasa0"
)

// TaxRegion is an area with one general rate.
type TaxRegion struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Country      string        `json:"country"`
	RateBps      int64         `json:"rateBps"`
	States       []string      `json:"states"`
	PostalRanges []PostalRange `json:"postalRanges"`
	Default      bool          `json:"default"`
}

// ProductClass is the tax class and shipping profile of one product.
type ProductClass struct {
	TaxClass        string `json:"taxClass"`
	ShippingProfile string `json:"shippingProfile"`
	// Customs: tariff code (HS, 6 to 10 digits) and country of origin, for
	// import duties when the product is sold into another country.
	HSCode string `json:"hsCode,omitempty"`
	Origin string `json:"origin,omitempty"`
}

// Config is an immutable snapshot of every rule of one store.
type Config struct {
	Zones      []Zone
	Profiles   []Profile
	Carriers   []Carrier
	Options    []Option
	Holidays   []Holiday
	Locations  []Location
	TaxRegions []TaxRegion
	Products   map[string]ProductClass // product id -> class
	Timezone   *time.Location
}

// Location returns the time zone of the store (Mexico City by default).
func (c *Config) Location() *time.Location {
	if c != nil && c.Timezone != nil {
		return c.Timezone
	}
	return MexicoCity
}

// MexicoCity is UTC-6 all year (Mexico dropped daylight saving time in 2022).
var MexicoCity = func() *time.Location {
	if l, err := time.LoadLocation("America/Mexico_City"); err == nil {
		return l
	}
	return time.FixedZone("America/Mexico_City", -6*3600)
}()

// ProductClassOf returns the class of a product (general tax, default profile).
func (c *Config) ProductClassOf(productID string) ProductClass {
	if c != nil {
		if pc, ok := c.Products[productID]; ok {
			if pc.TaxClass == "" {
				pc.TaxClass = TaxGeneral
			}
			return pc
		}
	}
	return ProductClass{TaxClass: TaxGeneral}
}

func (c *Config) zone(id string) (Zone, bool) {
	for _, z := range c.Zones {
		if z.ID == id {
			return z, true
		}
	}
	return Zone{}, false
}

func (c *Config) carrier(id string) (Carrier, bool) {
	for _, x := range c.Carriers {
		if x.ID == id {
			return x, true
		}
	}
	return Carrier{}, false
}

func (c *Config) profileName(id string) string {
	for _, p := range c.Profiles {
		if p.ID == id {
			return p.Name
		}
	}
	if id == "" {
		return "Estándar"
	}
	return id
}

// LocationByID returns a location.
func (c *Config) LocationByID(id string) (Location, bool) {
	if c == nil {
		return Location{}, false
	}
	for _, l := range c.Locations {
		if l.ID == id {
			return l, true
		}
	}
	return Location{}, false
}

// HasOptions reports whether the store configured at least one active option.
func (c *Config) HasOptions() bool {
	if c == nil {
		return false
	}
	for _, o := range c.Options {
		if o.Active {
			return true
		}
	}
	return false
}

var cpRe = regexp.MustCompile(`^[0-9]{5}$`)

// ValidPostalCode reports whether cp is a 5-digit postal code.
func ValidPostalCode(cp string) bool { return cpRe.MatchString(cp) }

// State is a Mexican state with the postal-code prefixes it owns.
type State struct {
	Code string `json:"code"`
	Name string `json:"name"`
	from int    // first two digits of the postal code, inclusive
	to   int
}

// States lists the 32 Mexican states by postal-code prefix (SEPOMEX).
var States = []State{
	{"CMX", "Ciudad de México", 1, 16}, {"AGU", "Aguascalientes", 20, 20}, {"BCN", "Baja California", 21, 22},
	{"BCS", "Baja California Sur", 23, 23}, {"CAM", "Campeche", 24, 24}, {"COA", "Coahuila", 25, 27},
	{"COL", "Colima", 28, 28}, {"CHP", "Chiapas", 29, 30}, {"CHH", "Chihuahua", 31, 33}, {"DUR", "Durango", 34, 35},
	{"GUA", "Guanajuato", 36, 38}, {"GRO", "Guerrero", 39, 41}, {"HID", "Hidalgo", 42, 43}, {"JAL", "Jalisco", 44, 49},
	{"MEX", "Estado de México", 50, 57}, {"MIC", "Michoacán", 58, 61}, {"MOR", "Morelos", 62, 62}, {"NAY", "Nayarit", 63, 63},
	{"NLE", "Nuevo León", 64, 67}, {"OAX", "Oaxaca", 68, 71}, {"PUE", "Puebla", 72, 75}, {"QUE", "Querétaro", 76, 76},
	{"ROO", "Quintana Roo", 77, 77}, {"SLP", "San Luis Potosí", 78, 79}, {"SIN", "Sinaloa", 80, 82}, {"SON", "Sonora", 83, 85},
	{"TAB", "Tabasco", 86, 86}, {"TAM", "Tamaulipas", 87, 89}, {"TLA", "Tlaxcala", 90, 90}, {"VER", "Veracruz", 91, 96},
	{"YUC", "Yucatán", 97, 97}, {"ZAC", "Zacatecas", 98, 99},
}

// StateOf returns the state code of a Mexican postal code ("" if unknown).
func StateOf(cp string) string {
	if !ValidPostalCode(cp) {
		return ""
	}
	p := int(cp[0]-'0')*10 + int(cp[1]-'0')
	for _, s := range States {
		if p >= s.from && p <= s.to {
			return s.Code
		}
	}
	return ""
}

// StateName returns the name of a state code (the code itself if unknown).
func StateName(code string) string {
	for _, s := range States {
		if s.Code == code {
			return s.Name
		}
	}
	return code
}

// ValidState reports whether code is a known state.
func ValidState(code string) bool {
	for _, s := range States {
		if s.Code == code {
			return true
		}
	}
	return false
}

// area is what zones and tax regions share.
type area struct {
	countries []string
	states    []string
	ranges    []PostalRange
}

// match reports whether (country, cp) is inside the area, how specific the
// match is (2 postal range, 1 state, 0 whole country) and why.
func (a area) match(country, cp string) (ok bool, specificity int, reason string) {
	if country == "" {
		country = "MX"
	}
	in := len(a.countries) == 0
	for _, c := range a.countries {
		in = in || strings.EqualFold(c, country)
	}
	if !in {
		return false, 0, "no cubre el país " + strings.ToUpper(country)
	}
	if len(a.states) == 0 && len(a.ranges) == 0 {
		return true, 0, "cubre todo el país"
	}
	if cp == "" {
		return false, 0, "falta el código postal"
	}
	for _, r := range a.ranges {
		if r.Contains(cp) {
			return true, 2, "el código postal " + cp + " está en el rango " + r.From + "–" + r.To
		}
	}
	if st := StateOf(cp); st != "" {
		for _, s := range a.states {
			if s == st {
				return true, 1, "el código postal " + cp + " es de " + StateName(st)
			}
		}
	}
	return false, 0, "el código postal " + cp + " está fuera de la zona"
}

// Match reports whether the zone covers the destination, how specific the
// match is and why.
func (z Zone) Match(country, cp string) (bool, int, string) {
	return area{z.Countries, z.States, z.PostalRanges}.match(country, cp)
}

// SortOptions orders options by position, then name.
func SortOptions(o []Option) {
	sort.SliceStable(o, func(i, j int) bool {
		if o[i].Position != o[j].Position {
			return o[i].Position < o[j].Position
		}
		return o[i].Name < o[j].Name
	})
}
