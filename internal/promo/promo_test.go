package promo

import (
	"strings"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/money"
)

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, Location)

func line(sku string, qty, unit int64, brand string, cats ...string) Line {
	return Line{SKU: sku, ProductID: "P-" + sku, Brand: brand, Categories: cats, Qty: qty, LineTotal: money.New(qty*unit, "MXN")}
}

func base(id, typ string) Promotion {
	p := Promotion{ID: id, Name: "Promo " + id, Type: typ, Status: StatusActive, Currency: "MXN"}
	p.Normalize()
	return p
}

func eval(t *testing.T, ctx Context, ps ...Promotion) Outcome {
	t.Helper()
	for i := range ps {
		ps[i].Normalize()
		if errs := ps[i].Validate(); len(errs) > 0 {
			t.Fatalf("promotion %s invalid: %+v", ps[i].ID, errs)
		}
	}
	if ctx.Now.IsZero() {
		ctx.Now = now
	}
	if ctx.Channel == "" {
		ctx.Channel = "mx"
	}
	ctx.Currency = "MXN"
	out := NewEngine(ps...).Evaluate(ctx)
	// Explainability: every promotion returns a result with a code and a reason.
	if len(out.Results) != len(ps) {
		t.Fatalf("got %d results for %d promotions", len(out.Results), len(ps))
	}
	var total int64
	for _, r := range out.Results {
		if r.Code == "" || r.Reason == "" {
			t.Fatalf("promotion %s has no explanation: %+v", r.ID, r)
		}
		if r.Applied != (r.Code == CodeApplied) {
			t.Fatalf("promotion %s: applied=%v with code %s", r.ID, r.Applied, r.Code)
		}
		if r.Applied {
			total += r.Discount.Amount
			var ls int64
			for _, l := range r.Lines {
				ls += l.Amount
			}
			if ls != r.Discount.Amount {
				t.Fatalf("promotion %s: lines add up to %d, discount %d", r.ID, ls, r.Discount.Amount)
			}
		} else if r.Discount.Amount != 0 {
			t.Fatalf("promotion %s not applied but discount %d", r.ID, r.Discount.Amount)
		}
	}
	if total != out.Discount.Amount {
		t.Fatalf("results add up to %d, outcome %d", total, out.Discount.Amount)
	}
	return out
}

func res(t *testing.T, out Outcome, id string) Result {
	t.Helper()
	for _, r := range out.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result for %s", id)
	return Result{}
}

func want(t *testing.T, r Result, applied bool, code string, discount int64) {
	t.Helper()
	if r.Applied != applied || r.Code != code || r.Discount.Amount != discount {
		t.Fatalf("%s: got applied=%v code=%s discount=%d (%s); want applied=%v code=%s discount=%d",
			r.ID, r.Applied, r.Code, r.Discount.Amount, r.Reason, applied, code, discount)
	}
}

func TestOrderPercentAndFixed(t *testing.T) {
	cart := []Line{line("A", 2, 50000, "Norte"), line("B", 1, 33333, "Sierra")}
	pct := base("pct", TypeOrderPercent)
	pct.Bps = 1000
	pct.Conditions.MinSubtotal = 100000
	out := eval(t, Context{Lines: cart}, pct)
	want(t, res(t, out, "pct"), true, CodeApplied, 13333) // 10% of 133,333 rounded

	pct.Conditions.MinSubtotal = 200000
	out = eval(t, Context{Lines: cart}, pct)
	r := res(t, out, "pct")
	want(t, r, false, CodeMinSubtotal, 0)
	if r.Missing == nil || r.Missing.Amount != 66667 || !strings.Contains(r.Reason, "$666.67") {
		t.Fatalf("missing amount not explained: %+v", r)
	}

	fix := base("fix", TypeOrderFixed)
	fix.Amount = 500000 // more than the cart: never below zero
	out = eval(t, Context{Lines: cart}, fix)
	want(t, res(t, out, "fix"), true, CodeApplied, 133333)
}

func TestItemPercentAndFixedWithTargets(t *testing.T) {
	cart := []Line{line("A", 2, 50000, "Norte", "Tenis"), line("B", 1, 30000, "Sierra", "Tenis"), line("C", 3, 10000, "Norte", "Calcetas")}
	p := base("tn", TypeItemPercent)
	p.Bps = 2000
	p.Target = Target{Brands: []string{"norte"}, Categories: []string{"TENIS"}}
	out := eval(t, Context{Lines: cart}, p)
	r := res(t, out, "tn")
	want(t, r, true, CodeApplied, 20000)
	if len(r.Lines) != 1 || r.Lines[0].SKU != "A" {
		t.Fatalf("discount on wrong lines: %+v", r.Lines)
	}

	f := base("fx", TypeItemFixed)
	f.Amount = 2000
	f.Target = Target{SKUs: []string{"C"}, ProductIDs: []string{"P-B"}}
	out = eval(t, Context{Lines: cart}, f)
	want(t, res(t, out, "fx"), true, CodeApplied, 3*2000+2000)

	f.Target = Target{SKUs: []string{"ZZZ"}}
	out = eval(t, Context{Lines: cart}, f)
	want(t, res(t, out, "fx"), false, CodeNoItems, 0)

	f.Target = Target{Brands: []string{"Norte"}, ExcludeSKUs: []string{"A"}}
	f.Conditions.MinQuantity = 5
	out = eval(t, Context{Lines: cart}, f)
	want(t, res(t, out, "fx"), false, CodeMinQuantity, 0)
}

func TestBuyXGetY(t *testing.T) {
	// 3x2 on socks: cheapest one free.
	p := base("3x2", TypeBuyXGetY)
	p.BuyQty, p.GetQty = 2, 1
	p.Target = Target{Categories: []string{"Calcetas"}}
	cart := []Line{line("S1", 2, 12000, "", "Calcetas"), line("S2", 1, 9000, "", "Calcetas"), line("T", 1, 90000, "", "Tenis")}
	out := eval(t, Context{Lines: cart}, p)
	want(t, res(t, out, "3x2"), true, CodeApplied, 9000)

	out = eval(t, Context{Lines: cart[:1]}, p)
	r := res(t, out, "3x2")
	want(t, r, false, CodeIncomplete, 0)
	if !strings.Contains(r.Reason, "1 pieza más") {
		t.Fatalf("reason does not say what is missing: %s", r.Reason)
	}

	// 7 units: two groups, the two cheapest are free.
	cart = []Line{line("S1", 4, 12000, "", "Calcetas"), line("S2", 3, 9000, "", "Calcetas")}
	out = eval(t, Context{Lines: cart}, p)
	want(t, res(t, out, "3x2"), true, CodeApplied, 18000)

	// Different product: buy 1 tenis, get socks at 50%.
	d := base("combo", TypeBuyXGetY)
	d.BuyQty, d.GetQty, d.GetBps = 1, 1, 5000
	d.Target = Target{Categories: []string{"Tenis"}}
	d.GetTarget = &Target{Categories: []string{"Calcetas"}}
	cart = []Line{line("T", 1, 90000, "", "Tenis"), line("S1", 2, 12000, "", "Calcetas")}
	out = eval(t, Context{Lines: cart}, d)
	want(t, res(t, out, "combo"), true, CodeApplied, 6000)
	out = eval(t, Context{Lines: cart[:1]}, d)
	want(t, res(t, out, "combo"), false, CodeIncomplete, 0)
}

func TestTiered(t *testing.T) {
	p := base("tiers", TypeTiered)
	p.TierBy = "subtotal"
	p.Tiers = []Tier{{Min: 200000, Bps: 1500}, {Min: 100000, Bps: 1000}}
	out := eval(t, Context{Lines: []Line{line("A", 1, 150000, "")}}, p)
	r := res(t, out, "tiers")
	want(t, r, true, CodeApplied, 15000)
	if !strings.Contains(r.Hint, "$500") || !strings.Contains(r.Hint, "15%") {
		t.Fatalf("next tier hint missing: %q", r.Hint)
	}
	out = eval(t, Context{Lines: []Line{line("A", 1, 250000, "")}}, p)
	want(t, res(t, out, "tiers"), true, CodeApplied, 37500)
	out = eval(t, Context{Lines: []Line{line("A", 1, 50000, "")}}, p)
	r = res(t, out, "tiers")
	want(t, r, false, CodeMinSubtotal, 0)
	if r.Missing == nil || r.Missing.Amount != 50000 {
		t.Fatalf("missing: %+v", r.Missing)
	}

	q := base("qty", TypeTiered)
	q.TierBy = "quantity"
	q.Tiers = []Tier{{Min: 3, Amount: 5000}, {Min: 6, Amount: 15000}}
	out = eval(t, Context{Lines: []Line{line("A", 6, 10000, "")}}, q)
	want(t, res(t, out, "qty"), true, CodeApplied, 15000)
	out = eval(t, Context{Lines: []Line{line("A", 2, 10000, "")}}, q)
	want(t, res(t, out, "qty"), false, CodeMinQuantity, 0)
}

func TestGiftAndBundleAndFreeShipping(t *testing.T) {
	g := base("gift", TypeGift)
	g.GiftSKU = "CAP"
	g.Conditions.MinSubtotal = 100000
	out := eval(t, Context{Lines: []Line{line("A", 1, 120000, "")}}, g)
	r := res(t, out, "gift")
	want(t, r, true, CodeApplied, 0)
	if len(out.Gifts) != 1 || out.Gifts[0].SKU != "CAP" || out.Gifts[0].InCart {
		t.Fatalf("gift not granted: %+v", out.Gifts)
	}
	// The gift itself does not count towards the minimum, and is free when already in the cart.
	out = eval(t, Context{Lines: []Line{line("A", 1, 120000, ""), line("CAP", 2, 20000, "")}}, g)
	want(t, res(t, out, "gift"), true, CodeApplied, 20000)
	out = eval(t, Context{Lines: []Line{line("A", 1, 90000, ""), line("CAP", 1, 20000, "")}}, g)
	want(t, res(t, out, "gift"), false, CodeMinSubtotal, 0)

	b := base("pack", TypeBundle)
	b.BundleItems = []BundleItem{{SKU: "T", Qty: 1}, {ProductID: "P-S", Qty: 2}}
	b.BundlePrice = 100000
	out = eval(t, Context{Lines: []Line{line("T", 1, 90000, ""), line("S", 2, 12000, "")}}, b)
	want(t, res(t, out, "pack"), true, CodeApplied, 14000)
	out = eval(t, Context{Lines: []Line{line("T", 2, 90000, ""), line("S", 5, 12000, "")}}, b)
	want(t, res(t, out, "pack"), true, CodeApplied, 28000)
	out = eval(t, Context{Lines: []Line{line("T", 1, 90000, ""), line("S", 1, 12000, "")}}, b)
	r = res(t, out, "pack")
	want(t, r, false, CodeIncomplete, 0)
	if !strings.Contains(r.Reason, "1 × P-S") {
		t.Fatalf("bundle reason: %s", r.Reason)
	}

	f := base("ship", TypeFreeShipping)
	f.Conditions.MinSubtotal = 50000
	out = eval(t, Context{Lines: []Line{line("A", 1, 60000, "")}}, f)
	want(t, res(t, out, "ship"), true, CodeApplied, 0)
	price := money.New(9900, "MXN")
	if d := out.ApplyShipping(&price, false); d.Amount != 9900 || !out.FreeShipping {
		t.Fatalf("shipping discount %d", d.Amount)
	}
	out = eval(t, Context{Lines: []Line{line("A", 1, 60000, "")}}, f)
	zero := money.Zero("MXN")
	out.ApplyShipping(&zero, true)
	if r := res(t, out, "ship"); r.Applied || r.Code != CodeShippingFree {
		t.Fatalf("free shipping must not be consumed when shipping is already free: %+v", r)
	}
}

func TestStackingExclusivityGroupsAndPriority(t *testing.T) {
	cart := []Line{line("A", 1, 100000, "Norte")}
	a := base("a-item", TypeItemPercent)
	a.Bps = 2000
	b := base("b-order", TypeOrderPercent)
	b.Bps = 1000
	// Both stack: item first, the order percentage applies on what is left.
	out := eval(t, Context{Lines: cart}, a, b)
	want(t, res(t, out, "a-item"), true, CodeApplied, 20000)
	want(t, res(t, out, "b-order"), true, CodeApplied, 8000)

	// Exclusive with higher priority wins alone.
	x := base("x-excl", TypeOrderFixed)
	x.Amount = 5000
	x.Stacking = Stacking{Mode: StackExclusive, Priority: 10}
	out = eval(t, Context{Lines: cart}, a, b, x)
	want(t, res(t, out, "x-excl"), true, CodeApplied, 5000)
	r := res(t, out, "a-item")
	want(t, r, false, CodeExclusive, 0)
	if !strings.Contains(r.Reason, "Promo x-excl") {
		t.Fatalf("conflict must name the winner: %s", r.Reason)
	}
	if out.Discount.Amount != 5000 {
		t.Fatalf("total %d", out.Discount.Amount)
	}

	// Exclusive with lower priority loses to whatever applied first.
	x.Stacking.Priority = -5
	out = eval(t, Context{Lines: cart}, a, b, x)
	want(t, res(t, out, "x-excl"), false, CodeExclusive, 0)
	want(t, res(t, out, "a-item"), true, CodeApplied, 20000)

	// Groups: same group combines, different group does not.
	g1 := base("g1", TypeItemPercent)
	g1.Bps = 1000
	g1.Stacking = Stacking{Mode: StackGroup, Group: "buen-fin", Priority: 5}
	g2 := base("g2", TypeOrderFixed)
	g2.Amount = 1000
	g2.Stacking = Stacking{Mode: StackGroup, Group: "buen-fin"}
	g3 := base("g3", TypeOrderFixed)
	g3.Amount = 1000
	g3.Stacking = Stacking{Mode: StackGroup, Group: "otro"}
	out = eval(t, Context{Lines: cart}, g1, g2, g3, b)
	want(t, res(t, out, "g1"), true, CodeApplied, 10000)
	want(t, res(t, out, "g2"), true, CodeApplied, 1000)
	want(t, res(t, out, "g3"), false, CodeExclusive, 0)
	want(t, res(t, out, "b-order"), false, CodeExclusive, 0)
}

func TestCapsAndNeverBelowZero(t *testing.T) {
	cart := []Line{line("A", 3, 100000, ""), line("B", 1, 100001, "")}
	p := base("cap", TypeOrderPercent)
	p.Bps = 5000
	p.MaxDiscount = 30000
	out := eval(t, Context{Lines: cart}, p)
	r := res(t, out, "cap")
	want(t, r, true, CodeApplied, 30000)
	if !r.Capped || !strings.Contains(r.Reason, "tope de $300") {
		t.Fatalf("cap not explained: %+v", r)
	}
	// Two 100% promotions can never discount more than the cart.
	a := base("a", TypeItemPercent)
	a.Bps = 10000
	b := base("b", TypeOrderFixed)
	b.Amount = 999999
	out = eval(t, Context{Lines: cart}, a, b)
	if out.Discount.Amount != 400001 {
		t.Fatalf("discount %d exceeds cart", out.Discount.Amount)
	}
	want(t, res(t, out, "b"), false, CodeNothingLeft, 0)
}

func TestScheduleBoundariesInStoreTimezone(t *testing.T) {
	// "Del 13 al 20 de noviembre" in Mexico City, typed as wall-clock times.
	from, err := ParseTime("2026-11-13T00:00")
	if err != nil {
		t.Fatal(err)
	}
	to, _ := ParseTime("2026-11-20T23:59:59")
	if from.UTC().Hour() != 6 { // UTC-6, no daylight saving since 2022
		t.Fatalf("store timezone not applied: %s", from.UTC())
	}
	p := base("bf", TypeOrderPercent)
	p.Bps = 1000
	p.Conditions.StartsAt, p.Conditions.EndsAt = &from, &to
	cart := []Line{line("A", 1, 100000, "")}
	cases := []struct {
		at      time.Time
		applied bool
		code    string
	}{
		{from.Add(-time.Second), false, CodeNotStarted},
		{from, true, CodeApplied},
		{time.Date(2026, 11, 13, 5, 59, 59, 0, time.UTC), false, CodeNotStarted}, // still Nov 12 in Mexico
		{time.Date(2026, 11, 13, 6, 0, 0, 0, time.UTC), true, CodeApplied},
		{to, true, CodeApplied},
		{to.Add(time.Second), false, CodeEnded},
		{time.Date(2026, 11, 21, 5, 59, 59, 0, time.UTC), true, CodeApplied}, // Nov 20 23:59:59 in Mexico
		{time.Date(2026, 11, 21, 6, 0, 0, 0, time.UTC), false, CodeEnded},
	}
	for _, c := range cases {
		out := eval(t, Context{Lines: cart, Now: c.at}, p)
		r := res(t, out, "bf")
		if r.Applied != c.applied || r.Code != c.code {
			t.Fatalf("at %s: applied=%v code=%s, want %v %s", c.at.Format(time.RFC3339), r.Applied, r.Code, c.applied, c.code)
		}
	}
	if s := Summary(p, nil); !strings.Contains(s, "del 13 al 20 de noviembre") {
		t.Fatalf("summary: %s", s)
	}
	if st := p.DisplayStatus(from.Add(-time.Hour)); st != "programada" {
		t.Fatalf("status %s", st)
	}
	if st := p.DisplayStatus(to.Add(time.Hour)); st != "vencida" {
		t.Fatalf("status %s", st)
	}
}

func TestConditionsChannelsLimitsCustomer(t *testing.T) {
	cart := []Line{line("A", 1, 100000, "")}
	p := base("p", TypeOrderPercent)
	p.Bps = 1000
	p.Conditions.Channels = []string{"mx"}
	want(t, res(t, eval(t, Context{Lines: cart, Channel: "us"}, p), "p"), false, CodeChannel, 0)

	p.Conditions = Conditions{MaxUses: 2}
	e := NewEngine(p)
	e.Upsert(p, 2)
	r := e.Evaluate(Context{Now: now, Currency: "MXN", Lines: cart}).Results[0]
	want(t, r, false, CodeUsageLimit, 0)

	p.Conditions = Conditions{MaxUsesPerCustomer: 1}
	lk := &Lookup{CustomerKnown: true, CustomerUses: map[string]int64{"p": 1}}
	want(t, res(t, eval(t, Context{Lines: cart, Lookup: lk}, p), "p"), false, CodeCustomerLimit, 0)
	// Unknown customer: applies, and says the limit is confirmed at checkout.
	r = res(t, eval(t, Context{Lines: cart}, p), "p")
	want(t, r, true, CodeApplied, 10000)
	if !strings.Contains(r.Hint, "por cliente") {
		t.Fatalf("hint: %q", r.Hint)
	}

	p.Conditions = Conditions{EmailDomains: []string{"@Empresa.com"}}
	want(t, res(t, eval(t, Context{Lines: cart}, p), "p"), false, CodeEmailRequired, 0)
	want(t, res(t, eval(t, Context{Lines: cart, Email: "ana@otra.com"}, p), "p"), false, CodeEmailDomain, 0)
	want(t, res(t, eval(t, Context{Lines: cart, Email: "ana@EMPRESA.com"}, p), "p"), true, CodeApplied, 10000)

	p.Conditions = Conditions{Segments: []string{"vip"}}
	want(t, res(t, eval(t, Context{Lines: cart}, p), "p"), false, CodeSegment, 0)
	want(t, res(t, eval(t, Context{Lines: cart, Segments: []string{"VIP"}}, p), "p"), true, CodeApplied, 10000)

	p.Conditions = Conditions{PaymentMethods: []string{"card"}}
	want(t, res(t, eval(t, Context{Lines: cart}, p), "p"), false, CodePaymentMethod, 0)
	want(t, res(t, eval(t, Context{Lines: cart, PaymentMethod: "card"}, p), "p"), true, CodeApplied, 10000)

	for st, code := range map[string]string{StatusPaused: CodePaused, StatusDraft: CodeDraft, StatusArchived: CodeArchived} {
		p.Conditions, p.Status = Conditions{}, st
		want(t, res(t, eval(t, Context{Lines: cart}, p), "p"), false, code, 0)
	}
}

func TestCampaignWindowAndBudget(t *testing.T) {
	cart := []Line{line("A", 1, 100000, "")}
	p := base("p", TypeOrderPercent)
	p.Bps = 2000
	p.CampaignID = "bf"
	run := func(c Campaign) Result {
		e := NewEngine(p)
		e.SetCampaign(c)
		return e.Evaluate(Context{Now: now, Currency: "MXN", Lines: cart}).Results[0]
	}
	c := Campaign{ID: "bf", Name: "Buen Fin", Status: StatusActive, BudgetType: "amount", BudgetLimit: 50000, Spent: 45000}
	r := run(c)
	want(t, r, true, CodeApplied, 5000) // only what is left of the budget
	if !r.Capped {
		t.Fatal("budget cap not flagged")
	}
	c.Spent = 50000
	want(t, run(c), false, CodeCampaignBudget, 0)
	c = Campaign{ID: "bf", Name: "Buen Fin", Status: StatusActive, BudgetType: "uses", BudgetLimit: 10, Used: 10}
	want(t, run(c), false, CodeCampaignBudget, 0)
	c = Campaign{ID: "bf", Name: "Buen Fin", Status: StatusPaused}
	want(t, run(c), false, CodeCampaignPaused, 0)
	later := now.Add(time.Hour)
	c = Campaign{ID: "bf", Name: "Buen Fin", Status: StatusActive, StartsAt: &later}
	want(t, run(c), false, CodeCampaignWindow, 0)
}

func TestCouponsNeverShowAppliedWhenInvalid(t *testing.T) {
	cart := []Line{line("A", 1, 100000, "Norte")}
	p := base("cup", TypeOrderPercent)
	p.Bps = 1000
	p.Conditions.RequiresCoupon = true
	p.Conditions.MinSubtotal = 150000
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	lk := &Lookup{CustomerKnown: true, Coupons: map[string]Coupon{
		"HOLA10":  {Code: "HOLA10", PromotionID: "cup", Status: StatusActive},
		"VENCIDO": {Code: "VENCIDO", PromotionID: "cup", Status: StatusActive, EndsAt: &past},
		"PRONTO":  {Code: "PRONTO", PromotionID: "cup", Status: StatusActive, StartsAt: &future},
		"AGOTADO": {Code: "AGOTADO", PromotionID: "cup", Status: StatusActive, MaxUses: 5, Uses: 5},
		"UNAVEZ":  {Code: "UNAVEZ", PromotionID: "cup", Status: StatusActive, MaxUsesPerCustomer: 1},
		"OFF":     {Code: "OFF", PromotionID: "cup", Status: "disabled"},
	}, CouponCustomerUses: map[string]int64{"UNAVEZ": 1}}

	one := func(code string, lines []Line) (CouponResult, Outcome) {
		out := eval(t, Context{Lines: lines, Coupons: []string{code}, Lookup: lk}, p)
		if len(out.Coupons) != 1 {
			t.Fatalf("coupon results: %+v", out.Coupons)
		}
		c := out.Coupons[0]
		if c.Reason == "" || c.ReasonCode == "" {
			t.Fatalf("coupon %s without reason", code)
		}
		if c.Applied != res(t, out, "cup").Applied {
			t.Fatalf("coupon %s applied=%v but promotion applied=%v", code, c.Applied, res(t, out, "cup").Applied)
		}
		return c, out
	}
	for code, wantCode := range map[string]string{"nada": CodeCouponNotFound, "vencido": CodeCouponExpired, "PRONTO": CodeCouponNotYet,
		"AGOTADO": CodeCouponExhausted, "UNAVEZ": CodeCouponCustomer, "OFF": CodeCouponDisabled} {
		c, out := one(code, cart)
		if c.Applied || c.ReasonCode != wantCode || out.Discount.Amount != 0 {
			t.Fatalf("coupon %s: %+v", code, c)
		}
	}
	// Valid code, minimum not reached: not applied, with the missing amount.
	c, _ := one(" hola10 ", cart)
	if c.Applied || c.ReasonCode != CodeMinSubtotal || c.Missing == nil || c.Missing.Amount != 50000 || !strings.Contains(c.Reason, "$500") {
		t.Fatalf("minimum not explained: %+v", c)
	}
	// Minimum reached: applied.
	c, out := one("HOLA10", []Line{line("A", 2, 100000, "Norte")})
	if !c.Applied || c.Discount == nil || c.Discount.Amount != 20000 || out.Discount.Amount != 20000 {
		t.Fatalf("coupon should apply: %+v", c)
	}
	// Not applicable to the items in the cart.
	p.Conditions.MinSubtotal = 0
	p.Target = Target{Brands: []string{"Sierra"}}
	c, _ = one("HOLA10", cart)
	if c.Applied || c.ReasonCode != CodeNoItems {
		t.Fatalf("not applicable: %+v", c)
	}
	// Without a code the promotion says it needs one and is hidden from shoppers.
	out = eval(t, Context{Lines: cart, Lookup: lk}, p)
	r := res(t, out, "cup")
	want(t, r, false, CodeCouponRequired, 0)
	if ShopperVisible(r) {
		t.Fatal("coupon-only promotions must not leak to shoppers")
	}
}

func TestSummaryAndLegacy(t *testing.T) {
	from, _ := ParseTime("2026-11-13T00:00")
	to, _ := ParseTime("2026-11-20T23:59")
	p := base("x", TypeItemPercent)
	p.Bps = 1000
	p.Target = Target{Categories: []string{"Tenis"}, Brands: []string{"Norte"}}
	p.Conditions.MinSubtotal = 150000
	p.Conditions.StartsAt, p.Conditions.EndsAt = &from, &to
	if s := Summary(p, nil); s != "10% en Tenis Norte desde $1,500, del 13 al 20 de noviembre." {
		t.Fatalf("summary: %q", s)
	}
	l, err := FromLegacy("3x2", "buy_x_pay_y", []byte(`{"name":"3x2 en calcetas","sku":"S","x":3,"y":2}`), true)
	if err != nil || l.BuyQty != 2 || l.GetQty != 1 || len(l.Validate()) != 0 {
		t.Fatalf("legacy: %+v %v", l, err)
	}
	if s := Summary(l, nil); !strings.HasPrefix(s, "3x2 en 1 producto seleccionado") {
		t.Fatalf("legacy summary: %q", s)
	}
	out := eval(t, Context{Lines: []Line{line("S", 3, 10000, "")}}, l)
	want(t, res(t, out, "3x2"), true, CodeApplied, 10000)
	l2, err := FromLegacy("pct", "percent_over_threshold", []byte(`{"name":"10%","channels":["mx"],"bps":1000,"min":{"amount":300000,"currency":"MXN"}}`), true)
	if err != nil || l2.Conditions.MinSubtotal != 300000 || l2.Type != TypeOrderPercent {
		t.Fatalf("legacy: %+v %v", l2, err)
	}
}

func TestValidate(t *testing.T) {
	bad := []Promotion{
		{ID: "Bad ID", Name: "x", Type: TypeOrderPercent, Bps: 1000},
		{ID: "a", Name: "", Type: TypeOrderPercent, Bps: 1000},
		{ID: "a", Name: "x", Type: TypeOrderPercent, Bps: 10001},
		{ID: "a", Name: "x", Type: TypeOrderFixed},
		{ID: "a", Name: "x", Type: TypeBuyXGetY, BuyQty: 0, GetQty: 1},
		{ID: "a", Name: "x", Type: TypeTiered, TierBy: "subtotal"},
		{ID: "a", Name: "x", Type: TypeTiered, TierBy: "subtotal", Tiers: []Tier{{Min: 10, Bps: 10, Amount: 5}}},
		{ID: "a", Name: "x", Type: TypeGift},
		{ID: "a", Name: "x", Type: TypeBundle, BundleItems: []BundleItem{{SKU: "A", Qty: 1}}, BundlePrice: 100},
		{ID: "a", Name: "x", Type: TypeOrderPercent, Bps: 1000, Stacking: Stacking{Mode: StackGroup}},
		{ID: "a", Name: "x", Type: TypeOrderPercent, Bps: 1000, Conditions: Conditions{EmailDomains: []string{"no es dominio"}}},
		{ID: "a", Name: "x", Type: "nope"},
	}
	for i, p := range bad {
		p.Normalize()
		if len(p.Validate()) == 0 {
			t.Fatalf("case %d should be invalid: %+v", i, p)
		}
	}
}

func TestDistribute(t *testing.T) {
	got := distribute(100, []int64{100, 100, 100})
	if sum(got) != 100 {
		t.Fatalf("%v", got)
	}
	got = distribute(10, []int64{3, 3})
	if got[0] != 3 || got[1] != 3 {
		t.Fatalf("never more than the weight: %v", got)
	}
	big := distribute(50_000_000_000, []int64{70_000_000_000, 30_000_000_000})
	if sum(big) != 50_000_000_000 || big[0] != 35_000_000_000 {
		t.Fatalf("%v", big)
	}
}
