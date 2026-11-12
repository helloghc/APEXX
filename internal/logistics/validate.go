package logistics

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
)

func fe(field, code, msg string) apierr.FieldError {
	return apierr.FieldError{Field: field, Code: code, Message: msg}
}

func name(errs []apierr.FieldError, v string) []apierr.FieldError {
	if n := len([]rune(strings.TrimSpace(v))); n == 0 {
		return append(errs, fe("name", "required", "Escribe un nombre"))
	} else if n > 80 {
		return append(errs, fe("name", "too_long", "El nombre admite hasta 80 caracteres"))
	}
	return errs
}

func validateArea(errs []apierr.FieldError, countries, states []string, ranges []PostalRange) []apierr.FieldError {
	for i, c := range countries {
		if len(c) != 2 || strings.ToUpper(c) != c {
			errs = append(errs, fe(fmt.Sprintf("countries[%d]", i), "invalid", "Usa el código de país de 2 letras, por ejemplo MX"))
		}
	}
	for i, s := range states {
		if !ValidState(s) {
			errs = append(errs, fe(fmt.Sprintf("states[%d]", i), "invalid", "Estado desconocido: "+s))
		}
	}
	if len(ranges) > 500 {
		errs = append(errs, fe("postalRanges", "too_many", "Máximo 500 rangos por zona"))
	}
	for i, r := range ranges {
		f := fmt.Sprintf("postalRanges[%d]", i)
		switch {
		case !ValidPostalCode(r.From) || !ValidPostalCode(r.To):
			errs = append(errs, fe(f, "invalid", "Cada código postal debe tener 5 dígitos"))
		case r.From > r.To:
			errs = append(errs, fe(f, "invalid", "El inicio del rango ("+r.From+") no puede ser mayor que el final ("+r.To+")"))
		}
	}
	return errs
}

// Validate checks a zone.
func (z *Zone) Validate() []apierr.FieldError {
	z.Name = strings.TrimSpace(z.Name)
	if len(z.Countries) == 0 {
		z.Countries = []string{"MX"}
	}
	if z.States == nil {
		z.States = []string{}
	}
	if z.PostalRanges == nil {
		z.PostalRanges = []PostalRange{}
	}
	return validateArea(name(nil, z.Name), z.Countries, z.States, z.PostalRanges)
}

// Validate checks a profile.
func (p *Profile) Validate() []apierr.FieldError {
	p.Name = strings.TrimSpace(p.Name)
	errs := name(nil, p.Name)
	switch p.Kind {
	case "":
		p.Kind = "custom"
	case "default", "heavy", "custom":
	default:
		errs = append(errs, fe("kind", "invalid", "Tipo de perfil: default, heavy o custom"))
	}
	if len(p.Description) > 300 {
		errs = append(errs, fe("description", "too_long", "La descripción admite hasta 300 caracteres"))
	}
	return errs
}

// Validate checks a carrier.
func (c *Carrier) Validate() []apierr.FieldError {
	c.Name, c.TrackingURL = strings.TrimSpace(c.Name), strings.TrimSpace(c.TrackingURL)
	errs := name(nil, c.Name)
	if c.TrackingURL != "" {
		u, err := url.Parse(strings.ReplaceAll(c.TrackingURL, "{tracking}", "X"))
		switch {
		case err != nil || u.Scheme != "https" || u.Host == "":
			errs = append(errs, fe("trackingUrl", "invalid", "Escribe una URL https completa, por ejemplo https://rastreo.com/?guia={tracking}"))
		case !strings.Contains(c.TrackingURL, "{tracking}"):
			errs = append(errs, fe("trackingUrl", "invalid", "La URL debe incluir {tracking} donde va el número de guía"))
		case len(c.TrackingURL) > 500:
			errs = append(errs, fe("trackingUrl", "too_long", "La URL admite hasta 500 caracteres"))
		}
	}
	c.Endpoint = strings.TrimSpace(c.Endpoint)
	switch c.Provider {
	case "", "sandbox":
		c.Endpoint = ""
	case "http":
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(c.Endpoint) > 300 {
			errs = append(errs, fe("endpoint", "invalid", "Escribe la dirección https de la app de la paquetería"))
		}
	default:
		errs = append(errs, fe("provider", "invalid", "Conexión: manual, de prueba o app (https)"))
	}
	return errs
}

var clockRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Validate checks a rate.
func (r *Rate) Validate() []apierr.FieldError {
	var errs []apierr.FieldError
	if r.Brackets == nil {
		r.Brackets = []Bracket{}
	}
	if r.Amount < 0 || r.PerKg < 0 || r.FreeOver < 0 {
		errs = append(errs, fe("rate.amount", "out_of_range", "Los montos no pueden ser negativos"))
	}
	switch r.Type {
	case RateFlat, RateLive:
		r.Brackets, r.PerKg = []Bracket{}, 0
	case RateWeight, RateSubtotal:
		if r.Type == RateSubtotal && len(r.Brackets) == 0 {
			errs = append(errs, fe("rate.brackets", "required", "Agrega al menos un tramo de monto"))
		}
		if len(r.Brackets) > 30 {
			errs = append(errs, fe("rate.brackets", "too_many", "Máximo 30 tramos"))
		}
		var prev int64
		for i, b := range r.Brackets {
			f := fmt.Sprintf("rate.brackets[%d]", i)
			last := i == len(r.Brackets)-1
			switch {
			case b.Amount < 0 || b.UpTo < 0:
				errs = append(errs, fe(f, "out_of_range", "Los tramos no pueden tener valores negativos"))
			case b.UpTo == 0 && !last:
				errs = append(errs, fe(f, "invalid", "Solo el último tramo puede ser “sin límite”"))
			case b.UpTo != 0 && b.UpTo <= prev:
				errs = append(errs, fe(f, "invalid", "Cada tramo debe terminar después del anterior"))
			}
			if b.UpTo != 0 {
				prev = b.UpTo
			}
		}
	default:
		errs = append(errs, fe("rate.type", "invalid", "Tipo de tarifa: flat, weight, subtotal o live"))
	}
	return errs
}

// Validate checks an option against the rest of the configuration.
func (o *Option) Validate(c *Config) []apierr.FieldError {
	o.Name = strings.TrimSpace(o.Name)
	errs := name(nil, o.Name)
	switch o.Kind {
	case KindDelivery:
		o.LocationID = ""
		if _, ok := c.zone(o.ZoneID); !ok {
			errs = append(errs, fe("zoneId", "unknown", "Elige una zona de envío"))
		}
	case KindPickup:
		o.ZoneID, o.ProfileID = "", ""
		if l, ok := c.LocationByID(o.LocationID); !ok {
			errs = append(errs, fe("locationId", "unknown", "Elige la ubicación donde se recoge"))
		} else if !l.PickupEnabled {
			errs = append(errs, fe("locationId", "invalid", "Activa primero la recolección en la ubicación "+l.Name))
		}
	default:
		errs = append(errs, fe("kind", "invalid", "Tipo: delivery o pickup"))
	}
	if o.ProfileID != "" {
		found := false
		for _, p := range c.Profiles {
			found = found || p.ID == o.ProfileID
		}
		if !found {
			errs = append(errs, fe("profileId", "unknown", "El perfil de envío no existe"))
		}
	}
	if o.CarrierID != "" {
		if _, ok := c.carrier(o.CarrierID); !ok {
			errs = append(errs, fe("carrierId", "unknown", "La paquetería no existe"))
		}
	}
	errs = append(errs, o.Rate.Validate()...)
	e := &o.Estimate
	if e.MinDays < 0 || e.MaxDays < e.MinDays || e.MaxDays > 120 {
		errs = append(errs, fe("estimate.maxDays", "invalid", "Los días de entrega deben ir de menor a mayor (máximo 120)"))
	}
	if e.Cutoff != "" && !clockRe.MatchString(e.Cutoff) {
		errs = append(errs, fe("estimate.cutoff", "invalid", "Hora de corte en formato 24 h, por ejemplo 14:00"))
	}
	switch e.Weekend {
	case "":
		e.Weekend = "none"
	case "none", "saturday", "all":
	default:
		errs = append(errs, fe("estimate.weekend", "invalid", "Fin de semana: none, saturday o all"))
	}
	if o.MaxWeightGrams < 0 {
		errs = append(errs, fe("maxWeightGrams", "out_of_range", "El peso máximo no puede ser negativo"))
	}
	return errs
}

// Validate checks a holiday.
func (h *Holiday) Validate() []apierr.FieldError {
	h.Name = strings.TrimSpace(h.Name)
	errs := name(nil, h.Name)
	if _, err := time.Parse(dateFmt, h.Date); err != nil {
		errs = append(errs, fe("date", "invalid", "Fecha en formato AAAA-MM-DD"))
	}
	return errs
}

var phoneRe = regexp.MustCompile(`^[0-9+() .-]{7,20}$`)

// ValidPhone reports whether s looks like a phone number.
func ValidPhone(s string) bool { return phoneRe.MatchString(s) }

// Validate checks an address; required says whether it must be filled in.
func (a *Address) Validate(prefix string, required bool) []apierr.FieldError {
	a.Line1, a.Line2, a.City = strings.TrimSpace(a.Line1), strings.TrimSpace(a.Line2), strings.TrimSpace(a.City)
	a.PostalCode = strings.TrimSpace(a.PostalCode)
	if a.Country == "" {
		a.Country = "MX"
	}
	var errs []apierr.FieldError
	empty := a.Line1 == "" && a.City == "" && a.PostalCode == "" && a.State == ""
	if empty && !required {
		return nil
	}
	if a.Line1 == "" {
		errs = append(errs, fe(prefix+"line1", "required", "Escribe calle y número"))
	}
	if a.City == "" {
		errs = append(errs, fe(prefix+"city", "required", "Escribe la ciudad o municipio"))
	}
	if len(a.Line1) > 200 || len(a.Line2) > 200 || len(a.City) > 100 {
		errs = append(errs, fe(prefix+"line1", "too_long", "La dirección es demasiado larga"))
	}
	if a.Country == "MX" {
		if !ValidPostalCode(a.PostalCode) {
			errs = append(errs, fe(prefix+"postalCode", "invalid", "El código postal debe tener 5 dígitos"))
		} else if st := StateOf(a.PostalCode); a.State == "" {
			a.State = st
		} else if !ValidState(a.State) {
			errs = append(errs, fe(prefix+"state", "invalid", "Elige un estado"))
		} else if st != "" && st != a.State {
			errs = append(errs, fe(prefix+"postalCode", "mismatch", "El código postal "+a.PostalCode+" es de "+StateName(st)+", no de "+StateName(a.State)))
		}
	}
	return errs
}

// Validate checks a location.
func (l *Location) Validate() []apierr.FieldError {
	l.Name = strings.TrimSpace(l.Name)
	errs := name(nil, l.Name)
	l.FulfillerEndpoint = strings.TrimSpace(l.FulfillerEndpoint)
	if l.FulfillerEndpoint != "" {
		if u, err := url.Parse(l.FulfillerEndpoint); err != nil || u.Scheme != "https" || u.Host == "" || len(l.FulfillerEndpoint) > 300 {
			errs = append(errs, fe("fulfillerEndpoint", "invalid", "Escribe la dirección https del almacén"))
		}
	}
	switch l.Kind {
	case "":
		l.Kind = "warehouse"
	case "warehouse", "store":
	default:
		errs = append(errs, fe("kind", "invalid", "Tipo: warehouse o store"))
	}
	errs = append(errs, l.Address.Validate("address.", l.PickupEnabled)...)
	l.Phone = strings.TrimSpace(l.Phone)
	if l.Phone != "" && !ValidPhone(l.Phone) {
		errs = append(errs, fe("phone", "invalid", "Teléfono no válido"))
	}
	if len(l.PickupInstructions) > 500 {
		errs = append(errs, fe("pickupInstructions", "too_long", "Las instrucciones admiten hasta 500 caracteres"))
	}
	for day, h := range l.Hours {
		known := false
		for _, d := range Weekdays {
			known = known || d == day
		}
		if !known {
			errs = append(errs, fe("hours."+day, "invalid", "Día desconocido"))
			continue
		}
		if h.Closed {
			continue
		}
		if !clockRe.MatchString(h.Open) || !clockRe.MatchString(h.Close) || h.Open >= h.Close {
			errs = append(errs, fe("hours."+day, "invalid", "Horario no válido: la apertura debe ser antes del cierre (formato 24 h)"))
		}
	}
	return errs
}

// Validate checks a tax region.
func (r *TaxRegion) Validate() []apierr.FieldError {
	r.Name = strings.TrimSpace(r.Name)
	if r.Country == "" {
		r.Country = "MX"
	}
	if r.States == nil {
		r.States = []string{}
	}
	if r.PostalRanges == nil {
		r.PostalRanges = []PostalRange{}
	}
	errs := validateArea(name(nil, r.Name), []string{r.Country}, r.States, r.PostalRanges)
	if r.RateBps < 0 || r.RateBps > 5000 {
		errs = append(errs, fe("rateBps", "out_of_range", "La tasa debe estar entre 0% y 50%"))
	}
	if r.Default && (len(r.States) > 0 || len(r.PostalRanges) > 0) {
		errs = append(errs, fe("default", "invalid", "La región predeterminada debe cubrir todo el país (sin estados ni rangos)"))
	}
	return errs
}

// ValidTaxClass reports whether c is a known tax class.
func ValidTaxClass(c string) bool { return c == TaxGeneral || c == TaxExempt || c == TaxZero }
