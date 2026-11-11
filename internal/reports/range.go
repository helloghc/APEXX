package reports

import (
	"errors"
	"fmt"
	"time"
)

// Range is a run of whole store days: [From, To) in instants.
type Range struct {
	Preset string
	From   time.Time
	To     time.Time
}

// MaxRangeDays bounds custom ranges.
const MaxRangeDays = 731

// ErrRange is returned for an invalid range request.
var ErrRange = errors.New("reports: invalid range")

// Presets accepted by ParseRange, in display order.
var Presets = []string{"today", "yesterday", "7d", "30d", "90d", "mtd", "lastmonth", "12m", "ytd", "custom"}

// ParseRange resolves a preset (or custom from/to as YYYY-MM-DD, inclusive)
// against now, in the store timezone.
func ParseRange(preset, from, to string, now time.Time) (Range, error) {
	today := dayStart(now)
	tomorrow := today.AddDate(0, 0, 1)
	back := func(n int) Range { return Range{preset, today.AddDate(0, 0, -(n - 1)), tomorrow} }
	switch preset {
	case "today":
		return back(1), nil
	case "yesterday":
		return Range{preset, today.AddDate(0, 0, -1), today}, nil
	case "", "7d":
		return Range{"7d", today.AddDate(0, 0, -6), tomorrow}, nil
	case "30d":
		return back(30), nil
	case "90d":
		return back(90), nil
	case "12m":
		return back(365), nil
	case "mtd":
		return Range{preset, time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, Zone), tomorrow}, nil
	case "lastmonth":
		first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, Zone)
		return Range{preset, first.AddDate(0, -1, 0), first}, nil
	case "ytd":
		return Range{preset, time.Date(today.Year(), 1, 1, 0, 0, 0, 0, Zone), tomorrow}, nil
	case "custom":
		f, err1 := time.ParseInLocation("2006-01-02", from, Zone)
		t, err2 := time.ParseInLocation("2006-01-02", to, Zone)
		if err1 != nil || err2 != nil {
			return Range{}, fmt.Errorf("%w: from y to deben ser fechas AAAA-MM-DD", ErrRange)
		}
		if t.Before(f) {
			return Range{}, fmt.Errorf("%w: la fecha final es anterior a la inicial", ErrRange)
		}
		r := Range{preset, f, t.AddDate(0, 0, 1)}
		if r.Days() > MaxRangeDays {
			return Range{}, fmt.Errorf("%w: el periodo máximo es de %d días", ErrRange, MaxRangeDays)
		}
		return r, nil
	}
	return Range{}, fmt.Errorf("%w: periodo desconocido %q", ErrRange, preset)
}

// DayList returns every store day of the range as YYYY-MM-DD.
func (r Range) DayList() []string {
	var out []string
	for d := r.From; d.Before(r.To); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out
}

// Days is the number of store days in the range.
func (r Range) Days() int {
	n := 0
	for d := r.From; d.Before(r.To); d = d.AddDate(0, 0, 1) {
		n++
	}
	return n
}

// FromDay is the first day; ToDay the last one (inclusive).
func (r Range) FromDay() string { return r.From.Format("2006-01-02") }

// ToDay is the last day of the range (inclusive).
func (r Range) ToDay() string { return r.To.AddDate(0, 0, -1).Format("2006-01-02") }

// Previous is the period of the same length right before the range.
func (r Range) Previous() Range {
	n := r.Days()
	return Range{"previous", r.From.AddDate(0, 0, -n), r.From}
}

// RangeInfo is the JSON form of a range.
type RangeInfo struct {
	Preset string `json:"preset"`
	From   string `json:"from"`
	To     string `json:"to"`
	Days   int    `json:"days"`
	Zone   string `json:"timezone"`
}

// Info describes the range for API responses.
func (r Range) Info() RangeInfo {
	return RangeInfo{r.Preset, r.FromDay(), r.ToDay(), r.Days(), ZoneName}
}
