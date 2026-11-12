package cart

import "testing"

func TestFeesFor(t *testing.T) {
	rules, err := CleanFeeRules([]FeeRule{
		{ID: "manejo", Name: "Manejo", Active: true, Kind: "fixed", Amount: 2500, Delivery: "delivery"},
		{ID: "chico", Name: "Pedido chico", Active: true, Kind: "fixed", Amount: 3000, MaxItems: 50000},
		{ID: "seguro", Name: "Seguro", Active: true, Kind: "percent", Bps: 150, MinItems: 100000},
		{ID: "off", Name: "Apagado", Active: false, Kind: "fixed", Amount: 100},
		{ID: "b2b", Name: "Solo empresas", Active: true, Kind: "fixed", Amount: 700, Groups: []string{"acme"}},
		{ID: "us", Name: "Solo US", Active: true, Kind: "fixed", Amount: 900, Channels: []string{"us"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := func(f []Fee) (n int64) {
		for _, x := range f {
			n += x.Amount
		}
		return
	}
	if got := sum(feesFor(rules, "mx", "delivery", "", 40000)); got != 5500 {
		t.Errorf("small delivered order = %d", got)
	}
	if got := sum(feesFor(rules, "mx", "pickup", "", 40000)); got != 3000 {
		t.Errorf("small pickup order = %d", got)
	}
	if got := sum(feesFor(rules, "mx", "pickup", "", 200000)); got != 3000 {
		t.Errorf("big order: 1.5%% of 2000 = 30.00, got %d", got)
	}
	if got := sum(feesFor(rules, "mx", "pickup", "acme", 60000)); got != 700 {
		t.Errorf("company group = %d", got)
	}
	if got := sum(feesFor(rules, "us", "pickup", "", 60000)); got != 900 {
		t.Errorf("channel rule = %d", got)
	}
	for _, bad := range [][]FeeRule{{{Name: "", Kind: "fixed", Amount: 1}}, {{Name: "x", Kind: "fixed"}}, {{Name: "x", Kind: "percent", Bps: 9000}},
		{{Name: "x", Kind: "fixed", Amount: 1, MinItems: 500, MaxItems: 100}}, {{Name: "x", Kind: "fixed", Amount: 1, Delivery: "drone"}}} {
		if _, err := CleanFeeRules(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestDutiesFor(t *testing.T) {
	ch := Channel{ID: "us", Country: "US", DutyBps: 1000, DutyMinimis: 80000, DutyRules: []DutyRule{{HSPrefix: "61", Bps: 2000}, {HSPrefix: "6109", Bps: 3200}}}
	lines := []dutyLine{{HSCode: "6109.10", Origin: "CN", Net: 50000}, {HSCode: "6203.42", Origin: "MX", Net: 30000}, {HSCode: "8517", Origin: "us", Net: 90000}}
	// Imported: CN 500 + MX 300 = 800 = minimis: nothing.
	if f := dutiesFor(ch, lines); f != nil {
		t.Fatalf("at the minimis: %+v", f)
	}
	ch.DutyMinimis = 0
	f := dutiesFor(ch, lines)
	// CN shirt (6109) 32% of 500 = 160; MX trousers no rule beyond "62" → default 10% of 300 = 30.
	if len(f) != 1 || f[0].Amount != 16000+3000 {
		t.Fatalf("duties = %+v", f)
	}
	if dutiesFor(Channel{Country: "MX"}, lines) != nil {
		t.Error("a channel without duties charged duties")
	}
	if dutiesFor(Channel{Country: "MX", DutyBps: 1000}, []dutyLine{{Origin: "MX", Net: 100000}, {Net: 5000}}) != nil {
		t.Error("domestic products or unknown origin paid duties")
	}
}
