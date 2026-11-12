package orders

import (
	"testing"

	"github.com/helloghc/apexx/internal/cart"
)

func TestApprovalNeeded(t *testing.T) {
	lines := []cart.Line{{SKU: "A", Qty: 1, Categories: []string{"quimicos"}}, {SKU: "B", Qty: 0, Categories: []string{"armas"}}}
	base := CompanyAccess{ApprovalThreshold: 10000, Rules: []ApprovalRule{{MinTotal: 50000, Level: 3}, {MinTotal: 0, Level: 2, Category: "quimicos"}, {MinTotal: 0, Level: 4, Category: "armas"}, {MinTotal: 0, Level: 5, Location: "sur"}}}
	cases := []struct {
		name  string
		c     CompanyAccess
		pay   string
		total int64
		want  int
	}{
		{"card never waits", base, "card", 90000, 0},
		{"category rule", base, "credit", 100, 2},
		{"amount rule beats category", base, "credit", 60000, 3},
		{"line with qty 0 does not count", base, "credit", 100, 2},
		{"branch rule", func() CompanyAccess { c := base; c.Location = "sur"; return c }(), "credit", 100, 5},
		{"approver of the level buys alone", func() CompanyAccess { c := base; c.Approver, c.ApproverLevel = true, 2; return c }(), "credit", 100, 0},
		{"approver below the level waits", func() CompanyAccess { c := base; c.Approver, c.ApproverLevel = true, 2; return c }(), "credit", 60000, 3},
		{"monthly cap", CompanyAccess{MonthlyLimit: 1000, MonthSpent: 900}, "credit", 200, 1},
		{"under the monthly cap", CompanyAccess{MonthlyLimit: 1000, MonthSpent: 700}, "credit", 200, 0},
		{"branch cap", CompanyAccess{LocationLimit: 1000, LocationSpent: 999}, "credit", 2, 1},
	}
	for _, c := range cases {
		c.c.Rules = append([]ApprovalRule(nil), c.c.Rules...)
		got, why := approvalNeeded(CheckoutRequest{PaymentMethod: c.pay, Company: &c.c}, c.total, lines)
		if got != c.want || (got > 0 && why == "") {
			t.Errorf("%s: level %d (%q), want %d", c.name, got, why, c.want)
		}
	}
	if err := cleanRules([]ApprovalRule{{Level: 0}}); err == nil {
		t.Error("level 0 accepted")
	}
}
