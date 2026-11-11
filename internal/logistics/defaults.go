package logistics

import "time"

// nthMonday returns the n-th Monday of a month.
func nthMonday(year int, month time.Month, n int) time.Time {
	d := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Monday {
		d = d.AddDate(0, 0, 1)
	}
	return d.AddDate(0, 0, 7*(n-1))
}

// MexicanHolidays returns the mandatory rest days of the Ley Federal del
// Trabajo (art. 74) for a year, plus the days most carriers do not operate.
func MexicanHolidays(year int) []Holiday {
	f := func(t time.Time) string { return t.Format(dateFmt) }
	d := func(m time.Month, day int) string { return f(time.Date(year, m, day, 0, 0, 0, 0, time.UTC)) }
	out := []Holiday{
		{d(time.January, 1), "Año Nuevo"},
		{f(nthMonday(year, time.February, 1)), "Día de la Constitución"},
		{f(nthMonday(year, time.March, 3)), "Natalicio de Benito Juárez"},
		{d(time.May, 1), "Día del Trabajo"},
		{d(time.September, 16), "Día de la Independencia"},
		{f(nthMonday(year, time.November, 3)), "Día de la Revolución"},
		{d(time.December, 25), "Navidad"},
	}
	if (year-2024)%6 == 0 { // presidential handover every six years
		out = append(out, Holiday{d(time.October, 1), "Transmisión del Poder Ejecutivo"})
	}
	return out
}

// Defaults is a recommended starting configuration for a store in Mexico.
type Defaults struct {
	Zones    []Zone
	Profiles []Profile
	Carriers []Carrier
	Options  []Option
}

// MexicoDefaults returns zones, profiles, carriers and options that a new
// store can use as is: metro area and rest of the country, standard and
// express delivery, a heavy-goods profile and free shipping from $1,499.
func MexicoDefaults() Defaults {
	return Defaults{
		Zones: []Zone{
			{ID: "zmvm", Name: "CDMX y Estado de México", Countries: []string{"MX"}, States: []string{"CMX", "MEX"}, PostalRanges: []PostalRange{}, Active: true},
			{ID: "mexico", Name: "Resto de México", Countries: []string{"MX"}, States: []string{}, PostalRanges: []PostalRange{}, Active: true},
		},
		Profiles: []Profile{
			{ID: "default", Name: "Estándar", Kind: "default", Description: "Productos que caben en una caja normal"},
			{ID: "heavy", Name: "Pesado o voluminoso", Kind: "heavy", Description: "Muebles, línea blanca y productos de más de 30 kg"},
		},
		Carriers: []Carrier{
			{ID: "estafeta", Name: "Estafeta", TrackingURL: "https://www.estafeta.com/Herramientas/Rastreo?wayBill={tracking}", Active: true},
			{ID: "dhl", Name: "DHL Express", TrackingURL: "https://www.dhl.com/mx-es/home/rastreo.html?tracking-id={tracking}", Active: true},
			{ID: "fedex", Name: "FedEx", TrackingURL: "https://www.fedex.com/fedextrack/?trknbr={tracking}", Active: true},
			{ID: "paquetexpress", Name: "Paquetexpress", TrackingURL: "https://www.paquetexpress.com.mx/rastreo/{tracking}", Active: true},
		},
		Options: []Option{
			{ID: "zmvm-estandar", Name: "Estándar", Kind: KindDelivery, ZoneID: "zmvm", ProfileID: "default", CarrierID: "estafeta", Active: true, Position: 1,
				Rate:     Rate{Type: RateFlat, Amount: 9900, FreeOver: 149900, Brackets: []Bracket{}},
				Estimate: EstimateRule{MinDays: 1, MaxDays: 3, Cutoff: "14:00", Weekend: "none"}},
			{ID: "zmvm-express", Name: "Express día siguiente", Kind: KindDelivery, ZoneID: "zmvm", ProfileID: "default", CarrierID: "dhl", Active: true, Position: 2,
				Rate:     Rate{Type: RateFlat, Amount: 18900, Brackets: []Bracket{}},
				Estimate: EstimateRule{MinDays: 1, MaxDays: 1, Cutoff: "12:00", Weekend: "saturday"}},
			{ID: "mx-estandar", Name: "Estándar", Kind: KindDelivery, ZoneID: "mexico", ProfileID: "default", CarrierID: "estafeta", Active: true, Position: 3,
				Rate: Rate{Type: RateWeight, PerKg: 1800, FreeOver: 149900, Brackets: []Bracket{
					{UpTo: 1000, Amount: 12900}, {UpTo: 5000, Amount: 16900}, {UpTo: 10000, Amount: 22900}}},
				Estimate: EstimateRule{MinDays: 3, MaxDays: 6, Cutoff: "14:00", Weekend: "none"}},
			{ID: "mx-express", Name: "Express", Kind: KindDelivery, ZoneID: "mexico", ProfileID: "default", CarrierID: "dhl", Active: true, Position: 4,
				Rate:     Rate{Type: RateFlat, Amount: 24900, Brackets: []Bracket{}},
				Estimate: EstimateRule{MinDays: 1, MaxDays: 2, Cutoff: "12:00", Weekend: "none"}, MaxWeightGrams: 30000},
			{ID: "zmvm-pesado", Name: "Carga pesada", Kind: KindDelivery, ZoneID: "zmvm", ProfileID: "heavy", CarrierID: "paquetexpress", Active: true, Position: 5,
				Rate:     Rate{Type: RateWeight, Amount: 39900, PerKg: 900, Brackets: []Bracket{}},
				Estimate: EstimateRule{MinDays: 3, MaxDays: 6, Cutoff: "12:00", Weekend: "none"}},
			{ID: "mx-pesado", Name: "Carga pesada", Kind: KindDelivery, ZoneID: "mexico", ProfileID: "heavy", CarrierID: "paquetexpress", Active: true, Position: 6,
				Rate:     Rate{Type: RateWeight, Amount: 59900, PerKg: 1400, Brackets: []Bracket{}},
				Estimate: EstimateRule{MinDays: 5, MaxDays: 9, Cutoff: "12:00", Weekend: "none"}},
		},
	}
}

// MexicoTaxDefaults returns IVA 16% for the country and the 8% border rate
// for the main cities of the northern border region (Decreto de estímulos
// fiscales región fronteriza norte). The list is a starting point: the
// merchant confirms with their accountant which postal codes apply.
func MexicoTaxDefaults() []TaxRegion {
	return []TaxRegion{
		{ID: "mx-general", Name: "IVA general", Country: "MX", RateBps: 1600, States: []string{}, PostalRanges: []PostalRange{}, Default: true},
		{ID: "mx-frontera-norte", Name: "Frontera norte", Country: "MX", RateBps: 800, States: []string{}, PostalRanges: []PostalRange{
			{"21000", "22999"}, // Baja California: Mexicali, Tecate, Tijuana, Rosarito, Ensenada
			{"26000", "26099"}, // Piedras Negras
			{"26200", "26299"}, // Ciudad Acuña
			{"32000", "32799"}, // Ciudad Juárez
			{"83400", "83499"}, // San Luis Río Colorado
			{"84000", "84099"}, // Nogales
			{"84200", "84299"}, // Agua Prieta
			{"87300", "87499"}, // Matamoros
			{"88000", "88299"}, // Nuevo Laredo
			{"88500", "88799"}, // Reynosa
			{"88900", "88999"}, // Río Bravo
		}},
	}
}
