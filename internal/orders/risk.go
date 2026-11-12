package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// RiskRules are the fraud rules of a store. They run before the order is
// created, on top of what the payment provider already checks (Stripe Radar).
type RiskRules struct {
	Enabled            bool     `json:"enabled"`
	ReviewAmount       int64    `json:"reviewAmount"`       // orders from this total are flagged for review (0 = off)
	MaxOrdersPerHour   int      `json:"maxOrdersPerHour"`   // per email; more are refused (0 = off)
	MaxOrdersPerIPHour int      `json:"maxOrdersPerIpHour"` // per address; more are flagged (0 = off)
	BlockedEmails      []string `json:"blockedEmails"`      // full addresses or @domains
	BlockedPostalCodes []string `json:"blockedPostalCodes"`
}

// Normalize cleans the lists and reports the first problem.
func (r *RiskRules) Normalize() error {
	clean := func(in []string, lower bool) []string {
		seen := map[string]bool{}
		out := []string{}
		for _, v := range in {
			v = strings.TrimSpace(v)
			if lower {
				v = strings.ToLower(v)
			}
			if v != "" && !seen[v] && len(v) <= 200 {
				seen[v] = true
				out = append(out, v)
			}
		}
		sort.Strings(out)
		return out
	}
	r.BlockedEmails, r.BlockedPostalCodes = clean(r.BlockedEmails, true), clean(r.BlockedPostalCodes, false)
	switch {
	case r.ReviewAmount < 0 || r.MaxOrdersPerHour < 0 || r.MaxOrdersPerIPHour < 0:
		return FieldError{"reviewAmount", "Los límites no pueden ser negativos"}
	case len(r.BlockedEmails) > 2000 || len(r.BlockedPostalCodes) > 2000:
		return FieldError{"blockedEmails", "Máximo 2000 entradas por lista"}
	}
	for _, e := range r.BlockedEmails {
		if !strings.Contains(e, "@") {
			return FieldError{"blockedEmails", "“" + e + "” no es un correo ni un dominio (usa @dominio.com)"}
		}
	}
	return nil
}

// InstallmentRules configure "meses sin intereses".
type InstallmentRules struct {
	Enabled   bool  `json:"enabled"`
	Plans     []int `json:"plans"`     // 3, 6, 9, 12, 18, 24
	MinAmount int64 `json:"minAmount"` // smallest order that can be paid in instalments
}

// Normalize validates the plans.
func (r *InstallmentRules) Normalize() error {
	ok := map[int]bool{3: true, 6: true, 9: true, 12: true, 18: true, 24: true}
	seen := map[int]bool{}
	out := []int{}
	for _, p := range r.Plans {
		if !ok[p] {
			return FieldError{"plans", fmt.Sprintf("%d meses no es un plazo disponible", p)}
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	r.Plans = out
	if r.MinAmount < 0 {
		return FieldError{"minAmount", "El monto mínimo no puede ser negativo"}
	}
	if r.Enabled && len(out) == 0 {
		return FieldError{"plans", "Elige al menos un plazo"}
	}
	return nil
}

// Allows reports whether an order of total can be paid in n instalments.
func (r InstallmentRules) Allows(n int, total int64) bool {
	if n <= 1 {
		return true
	}
	if !r.Enabled || total < r.MinAmount {
		return false
	}
	for _, p := range r.Plans {
		if p == n {
			return true
		}
	}
	return false
}

// Risk is the outcome stored with the order.
type Risk struct {
	Level     string   `json:"level,omitempty"` // review | cleared
	Reasons   []string `json:"reasons,omitempty"`
	ClearedBy string   `json:"clearedBy,omitempty"`
}

// PaymentRules loads the instalment and risk rules of a store.
func (s *Service) PaymentRules(ctx context.Context, tenant string) (InstallmentRules, RiskRules, error) {
	var inst InstallmentRules
	var risk RiskRules
	if _, err := s.DB.GetDoc(ctx, tenant, "payments", "installments", &inst); err != nil {
		return inst, risk, err
	}
	_, err := s.DB.GetDoc(ctx, tenant, "payments", "risk", &risk)
	return inst, risk, err
}

// evaluateRisk returns the flags of the order, or blocked != "" when the
// order must be refused (the reason is for the merchant, never the shopper).
func (s *Service) evaluateRisk(ctx context.Context, tenant string, rules RiskRules, req CheckoutRequest, total int64) (risk Risk, ipHash []byte, blocked string, err error) {
	if req.ClientIP != "" {
		if ipHash, err = s.Keys.Blind(tenant, "ip", req.ClientIP); err != nil {
			return
		}
	}
	if !rules.Enabled {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Customer.Email))
	domain := ""
	if i := strings.LastIndex(email, "@"); i >= 0 {
		domain = email[i:]
	}
	for _, b := range rules.BlockedEmails {
		if b == email || (strings.HasPrefix(b, "@") && b == domain) {
			blocked = "Correo en la lista de bloqueo"
			return
		}
	}
	for _, b := range rules.BlockedPostalCodes {
		if b == strings.TrimSpace(req.PostalCode) {
			blocked = "Código postal en la lista de bloqueo"
			return
		}
	}
	var byEmail, byIP int
	emailHash, herr := s.Keys.Blind(tenant, "email", email)
	if herr != nil {
		err = herr
		return
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM orders o JOIN customers c ON c.tenant_id=o.tenant_id AND c.id=o.customer_id
			WHERE o.tenant_id=$1 AND c.email_hash=$2 AND o.created_at > now() - interval '1 hour'`, tenant, emailHash).Scan(&byEmail); err != nil {
			return err
		}
		if ipHash == nil {
			return nil
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id=$1 AND ip_hash=$2 AND created_at > now() - interval '1 hour'`, tenant, ipHash).Scan(&byIP)
	})
	if err != nil {
		return
	}
	if rules.MaxOrdersPerHour > 0 && byEmail >= rules.MaxOrdersPerHour {
		blocked = fmt.Sprintf("Más de %d pedidos del mismo correo en una hora", rules.MaxOrdersPerHour)
		return
	}
	if rules.ReviewAmount > 0 && total >= rules.ReviewAmount {
		risk.Reasons = append(risk.Reasons, "El total supera $"+cents(rules.ReviewAmount))
	}
	if rules.MaxOrdersPerIPHour > 0 && byIP >= rules.MaxOrdersPerIPHour {
		risk.Reasons = append(risk.Reasons, fmt.Sprintf("%d pedidos desde la misma conexión en la última hora", byIP))
	}
	if len(risk.Reasons) > 0 {
		risk.Level = "review"
	}
	return
}

// afterCreateTx stores what the checkout decided about risk and instalments.
func (s *Service) afterCreateTx(ctx context.Context, tx pgx.Tx, tenant, id string, req CheckoutRequest, risk Risk, ipHash []byte) error {
	b, _ := json.Marshal(risk)
	n := req.Installments
	if n < 2 {
		n = 0
	}
	_, err := tx.Exec(ctx, `UPDATE orders SET risk=$3, installments=$4, ip_hash=$5 WHERE tenant_id=$1 AND id=$2`, tenant, id, b, n, ipHash)
	return err
}

// ClearRisk records that a person reviewed a flagged order and it is fine.
func (s *Service) ClearRisk(ctx context.Context, tenant, actor, actorName, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT risk FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&raw); err != nil {
			return err
		}
		var r Risk
		_ = json.Unmarshal(raw, &r)
		if r.Level != "review" {
			return fmt.Errorf("%w: el pedido no está en revisión", ErrInvalidState)
		}
		r.Level, r.ClearedBy = "cleared", actorName
		b, _ := json.Marshal(r)
		if _, err := tx.Exec(ctx, `UPDATE orders SET risk=$3, version=version+1, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, b); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, actor, "order.risk_cleared", id, map[string]any{}); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, id, actor, "risk.cleared", &o.Status, o.Status, map[string]any{"by": actorName})
	})
}

// PaymentMethodSettings are the payment methods besides card a store accepts.
type PaymentMethodSettings struct {
	OXXO bool `json:"oxxo"`
	SPEI bool `json:"spei"`
}

// Allows tells whether a voucher method is on.
func (p PaymentMethodSettings) Allows(method string) bool {
	return (method == "oxxo" && p.OXXO) || (method == "spei" && p.SPEI)
}

// PaymentMethods reads the store's methods (all off by default).
func (s *Service) PaymentMethods(ctx context.Context, tenant string) (PaymentMethodSettings, error) {
	var p PaymentMethodSettings
	_, err := s.DB.GetDoc(ctx, tenant, "payments", "methods", &p)
	return p, err
}
