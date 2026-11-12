package cart

import (
	"fmt"
	"sort"
	"strings"

	"github.com/helloghc/apexx/internal/logistics"
)

// Extra charges of an order: rules of the store (handling, packaging, small
// order) and import duties of a market. Both add to the total, are shown one
// by one with their reason, and are kept on the order.

// FeeRule is an extra charge the store adds when its conditions hold.
type FeeRule struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Active   bool     `json:"active"`
	Kind     string   `json:"kind"`               // fixed | percent
	Amount   int64    `json:"amount,omitempty"`   // fixed, in the channel's currency
	Bps      int64    `json:"bps,omitempty"`      // percent of the items after discounts
	Channels []string `json:"channels,omitempty"` // empty = every channel
	MinItems int64    `json:"minItems,omitempty"` // applies from this items total
	MaxItems int64    `json:"maxItems,omitempty"` // and below this one (0 = no top): a small-order fee
	Delivery string   `json:"delivery,omitempty"` // "" any | delivery | pickup
	Groups   []string `json:"groups,omitempty"`   // customer or company price groups; empty = everyone
}

// Fee is one extra charge on a cart or order.
type Fee struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // fee | duty
	Name   string `json:"name"`
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

// CleanFeeRules validates the rules of a store.
func CleanFeeRules(in []FeeRule) ([]FeeRule, error) {
	if len(in) > 50 {
		return nil, fmt.Errorf("máximo 50 cargos")
	}
	seen := map[string]bool{}
	out := make([]FeeRule, 0, len(in))
	for i, r := range in {
		r.ID, r.Name = strings.TrimSpace(r.ID), strings.TrimSpace(r.Name)
		if r.ID == "" {
			r.ID = fmt.Sprintf("cargo-%d", i+1)
		}
		switch {
		case seen[r.ID]:
			return nil, fmt.Errorf("el cargo %s está repetido", r.ID)
		case r.Name == "" || len(r.Name) > 80:
			return nil, fmt.Errorf("cargo %d: escribe un nombre de hasta 80 caracteres", i+1)
		case r.Kind != "fixed" && r.Kind != "percent":
			return nil, fmt.Errorf("%s: el tipo es monto fijo o porcentaje", r.Name)
		case r.Kind == "fixed" && (r.Amount <= 0 || r.Amount > 100_000_00):
			return nil, fmt.Errorf("%s: el monto va de 0.01 a 100,000", r.Name)
		case r.Kind == "percent" && (r.Bps <= 0 || r.Bps > 5000):
			return nil, fmt.Errorf("%s: el porcentaje va de 0.01%% a 50%%", r.Name)
		case r.MinItems < 0 || r.MaxItems < 0 || (r.MaxItems > 0 && r.MaxItems <= r.MinItems):
			return nil, fmt.Errorf("%s: el rango de montos no es válido", r.Name)
		case r.Delivery != "" && r.Delivery != logistics.KindDelivery && r.Delivery != logistics.KindPickup:
			return nil, fmt.Errorf("%s: la entrega es a domicilio o retiro en tienda", r.Name)
		}
		seen[r.ID] = true
		if r.Kind == "fixed" {
			r.Bps = 0
		} else {
			r.Amount = 0
		}
		out = append(out, r)
	}
	return out, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// feesFor applies the store's rules to a cart. items is after discounts.
func feesFor(rules []FeeRule, channel, delivery, group string, items int64) []Fee {
	var out []Fee
	for _, r := range rules {
		if !r.Active || (len(r.Channels) > 0 && !contains(r.Channels, channel)) || (len(r.Groups) > 0 && !contains(r.Groups, group)) {
			continue
		}
		if items < r.MinItems || (r.MaxItems > 0 && items >= r.MaxItems) || (r.Delivery != "" && r.Delivery != delivery) {
			continue
		}
		amount, reason := r.Amount, "Cargo fijo por pedido"
		if r.Kind == "percent" {
			amount = (items*r.Bps + 5000) / 10000
			reason = fmt.Sprintf("%s%% del pedido", logistics.FormatBps(r.Bps))
		}
		if r.MaxItems > 0 {
			reason += " · pedidos menores a " + logistics.FormatMoney(r.MaxItems)
		}
		if amount > 0 {
			out = append(out, Fee{ID: r.ID, Kind: "fee", Name: r.Name, Amount: amount, Reason: reason})
		}
	}
	return out
}

// dutyLine is what duties need to know of a line.
type dutyLine struct {
	HSCode, Origin string
	Net            int64 // line total after its share of discounts
}

// dutiesFor computes the import duties of a market (delivered duty paid).
// Lines made in the destination country pay nothing; under the de minimis
// threshold nothing is charged.
func dutiesFor(ch Channel, lines []dutyLine) []Fee {
	if ch.Country == "" || (ch.DutyBps == 0 && len(ch.DutyRules) == 0) {
		return nil
	}
	var imported int64
	for _, l := range lines {
		if l.Origin != "" && !strings.EqualFold(l.Origin, ch.Country) {
			imported += l.Net
		}
	}
	if imported == 0 || imported <= ch.DutyMinimis {
		return nil
	}
	rules := append([]DutyRule(nil), ch.DutyRules...)
	sort.SliceStable(rules, func(i, j int) bool { return len(rules[i].HSPrefix) > len(rules[j].HSPrefix) })
	var total int64
	for _, l := range lines {
		if l.Origin == "" || strings.EqualFold(l.Origin, ch.Country) {
			continue
		}
		bps := ch.DutyBps
		code := strings.ReplaceAll(l.HSCode, ".", "")
		for _, r := range rules {
			if r.HSPrefix != "" && strings.HasPrefix(code, r.HSPrefix) {
				bps = r.Bps
				break
			}
		}
		total += (l.Net*bps + 5000) / 10000
	}
	if total <= 0 {
		return nil
	}
	return []Fee{{ID: "duties", Kind: "duty", Name: "Impuestos de importación", Amount: total,
		Reason: "Productos hechos fuera de " + ch.Country + "; se pagan aquí y no al recibir"}}
}
