package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// B2B depth: approvals that climb levels by rule, a monthly cap per buyer and
// per branch, branches with their own prices, and the company's own
// administrators who manage their buyers from the store.

const maxApprovalLevel = 5

// ApprovalRule asks for approval up to Level when an order reaches MinTotal
// and, if set, has a product of Category or comes from the branch Location.
type ApprovalRule struct {
	MinTotal int64  `json:"minTotal"`
	Level    int    `json:"level"`
	Category string `json:"category,omitempty"`
	Location string `json:"location,omitempty"`
}

func cleanRules(rules []ApprovalRule) error {
	if len(rules) > 20 {
		return FieldError{"approvalRules", "Máximo 20 reglas de aprobación"}
	}
	for i := range rules {
		r := &rules[i]
		r.Category, r.Location = strings.TrimSpace(r.Category), strings.TrimSpace(r.Location)
		switch {
		case r.Level < 1 || r.Level > maxApprovalLevel:
			return FieldError{"approvalRules", fmt.Sprintf("Regla %d: el nivel va de 1 a %d", i+1, maxApprovalLevel)}
		case r.MinTotal < 0:
			return FieldError{"approvalRules", fmt.Sprintf("Regla %d: el monto no puede ser negativo", i+1)}
		case len(r.Category) > 120 || len(r.Location) > 60:
			return FieldError{"approvalRules", fmt.Sprintf("Regla %d: categoría o sucursal demasiado larga", i+1)}
		}
	}
	return nil
}

// monthSpentTx is what a buyer and a branch put on the company's credit this
// calendar month (charges minus releases of orders that did not complete).
func monthSpentTx(ctx context.Context, tx pgx.Tx, tenant, company string, buyer []byte, location string) (int64, int64, error) {
	var byBuyer, byLocation int64
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount) FILTER (WHERE buyer_hash=$3),0), COALESCE(sum(amount) FILTER (WHERE $4<>'' AND location_id=$4),0)
		FROM company_entries WHERE tenant_id=$1 AND company_id=$2 AND kind IN ('charge','release') AND created_at >= date_trunc('month', now())`,
		tenant, company, buyer, location).Scan(&byBuyer, &byLocation)
	return byBuyer, byLocation, err
}

// approvalNeeded is the level of approval a credit purchase needs (0 = none)
// and why, in words for the buyer and the approver.
func approvalNeeded(req CheckoutRequest, total int64, lines []cart.Line) (int, string) {
	c := req.Company
	if req.PaymentMethod != "credit" || c == nil {
		return 0, ""
	}
	level, why := 0, ""
	need := func(l int, reason string) {
		if l > level {
			level, why = l, reason
		} else if why == "" {
			why = reason
		}
	}
	if c.ApprovalThreshold > 0 && total > c.ApprovalThreshold {
		need(1, "Supera el monto que el comprador puede autorizar")
	}
	if c.MonthlyLimit > 0 && c.MonthSpent+total > c.MonthlyLimit {
		need(1, "Supera el tope mensual del comprador")
	}
	if c.LocationLimit > 0 && c.LocationSpent+total > c.LocationLimit {
		need(1, "Supera el tope mensual de la sucursal "+c.LocationName)
	}
	for _, r := range c.Rules {
		if total < r.MinTotal || (r.Location != "" && r.Location != c.Location) {
			continue
		}
		if r.Category != "" && !hasCategory(lines, r.Category) {
			continue
		}
		reason := "Regla de la empresa: compras desde " + notify.Money(r.MinTotal, "MXN")
		if r.Category != "" {
			reason += " con productos de la categoría " + r.Category
		}
		need(r.Level, reason)
	}
	if level == 0 || (c.Approver && c.ApproverLevel >= level) {
		return 0, "" // the buyer can authorize it themselves
	}
	return level, why
}

func hasCategory(lines []cart.Line, cat string) bool {
	for _, l := range lines {
		if l.Qty == 0 {
			continue
		}
		for _, c := range l.Categories {
			if strings.EqualFold(c, cat) {
				return true
			}
		}
	}
	return false
}

// approverRecipient is who gets the link of a level.
type approverRecipient struct {
	Email string
	Level int
}

// approversForTx picks who approves the step that starts at level from: the
// approvers of the lowest level at or above it. Someone away hands it to their
// substitute. Returns their level too: approving covers every level up to it.
func (s *Service) approversForTx(ctx context.Context, tx pgx.Tx, tenant, company string, from int) ([]string, int, error) {
	rows, err := tx.Query(ctx, `SELECT email_hash, email, approver, approver_level, substitute_hash, away_until IS NOT NULL AND away_until >= current_date
		FROM company_buyers WHERE tenant_id=$1 AND company_id=$2`, tenant, company)
	if err != nil {
		return nil, 0, err
	}
	type row struct {
		hash, email, sub []byte
		approver, away   bool
		level            int
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.hash, &x.email, &x.approver, &x.level, &x.sub, &x.away)
	})
	if err != nil {
		return nil, 0, err
	}
	step := 0
	for _, x := range all {
		if x.approver && x.level >= from && (step == 0 || x.level < step) {
			step = x.level
		}
	}
	if step == 0 {
		return nil, 0, nil
	}
	plain := func(enc []byte) string {
		p, err := s.Keys.Decrypt(tenant, enc, "buyer:"+company)
		if err != nil {
			return ""
		}
		return string(p)
	}
	seen := map[string]bool{}
	var to []string
	for _, x := range all {
		if !x.approver || x.level != step {
			continue
		}
		email := plain(x.email)
		if x.away && x.sub != nil {
			for _, y := range all {
				if bytes.Equal(y.hash, x.sub) {
					email = plain(y.email)
				}
			}
		}
		if email != "" && !seen[email] {
			seen[email] = true
			to = append(to, email)
		}
	}
	sort.Strings(to)
	return to, step, nil
}

// Member is a person who buys for a company, as the store and the company's
// administrators manage them.
type Member struct {
	Email        string `json:"email"`
	Approver     bool   `json:"approver"`
	Level        int    `json:"level"`
	MonthlyLimit int64  `json:"monthlyLimit"`
	Location     string `json:"location"`
	Admin        bool   `json:"admin"`
	Substitute   string `json:"substitute"` // email of another member who approves while this one is away
	AwayUntil    string `json:"awayUntil"`
	MonthSpent   int64  `json:"monthSpent"`
}

// Members lists the buyers of a company with their roles and month.
func (s *Service) Members(ctx context.Context, tenant, company string) ([]Member, error) {
	var out []Member
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT b.email_hash, b.email, b.approver, b.approver_level, b.monthly_limit, b.location_id, b.admin, b.substitute_hash, COALESCE(to_char(b.away_until,'YYYY-MM-DD'),''),
				COALESCE((SELECT sum(amount) FROM company_entries e WHERE e.tenant_id=b.tenant_id AND e.company_id=b.company_id AND e.buyer_hash=b.email_hash
					AND e.kind IN ('charge','release') AND e.created_at >= date_trunc('month', now())),0)
			FROM company_buyers b WHERE b.tenant_id=$1 AND b.company_id=$2`, tenant, company)
		if err != nil {
			return err
		}
		type row struct {
			m         Member
			hash, sub []byte
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			var enc []byte
			if err := r.Scan(&x.hash, &enc, &x.m.Approver, &x.m.Level, &x.m.MonthlyLimit, &x.m.Location, &x.m.Admin, &x.sub, &x.m.AwayUntil, &x.m.MonthSpent); err != nil {
				return x, err
			}
			if p, err := s.Keys.Decrypt(tenant, enc, "buyer:"+company); err == nil {
				x.m.Email = string(p)
			}
			return x, nil
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.sub != nil {
				for _, y := range list {
					if bytes.Equal(y.hash, x.sub) {
						x.m.Substitute = y.m.Email
					}
				}
			}
			if !x.m.Approver {
				x.m.Level = 0
			}
			out = append(out, x.m)
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, err
}

// SaveMembers replaces the buyers of a company. by is the email of the
// company administrator making the change ("" = the store): they cannot take
// away their own administration, so a company never locks itself out.
func (s *Service) SaveMembers(ctx context.Context, tenant, actor, company, by string, in []Member) ([]Member, error) {
	if len(in) > 500 {
		return nil, FieldError{"members", "Máximo 500 compradores por empresa"}
	}
	by = strings.ToLower(strings.TrimSpace(by))
	seen := map[string]bool{}
	for i := range in {
		m := &in[i]
		m.Email, m.Substitute, m.Location = strings.ToLower(strings.TrimSpace(m.Email)), strings.ToLower(strings.TrimSpace(m.Substitute)), strings.TrimSpace(m.Location)
		if !strings.Contains(m.Email, "@") || len(m.Email) > 200 {
			return nil, FieldError{"members", "“" + m.Email + "” no es un correo válido"}
		}
		if seen[m.Email] {
			return nil, FieldError{"members", m.Email + " está repetido"}
		}
		seen[m.Email] = true
		if !m.Approver {
			m.Level, m.Substitute, m.AwayUntil = 1, "", ""
		}
		switch {
		case m.Level < 1 || m.Level > maxApprovalLevel:
			return nil, FieldError{"members", fmt.Sprintf("%s: el nivel de aprobación va de 1 a %d", m.Email, maxApprovalLevel)}
		case m.MonthlyLimit < 0:
			return nil, FieldError{"members", m.Email + ": el tope mensual no puede ser negativo"}
		case m.AwayUntil != "" && !validDate(m.AwayUntil):
			return nil, FieldError{"members", m.Email + ": fecha de ausencia con formato AAAA-MM-DD"}
		case m.Substitute == m.Email:
			return nil, FieldError{"members", m.Email + ": la suplencia es otra persona"}
		}
	}
	for _, m := range in {
		if m.Substitute != "" && !seen[m.Substitute] {
			return nil, FieldError{"members", m.Email + ": su suplente debe ser comprador de la empresa"}
		}
	}
	if by != "" {
		ok := false
		for _, m := range in {
			ok = ok || (m.Email == by && m.Admin)
		}
		if !ok {
			return nil, FieldError{"members", "No puedes quitarte como administrador de la empresa"}
		}
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var threshold int64
		var rules []ApprovalRule
		if err := tx.QueryRow(ctx, `SELECT approval_threshold, approval_rules FROM companies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, company).Scan(&threshold, &rules); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		locs := map[string]bool{"": true}
		rows, err := tx.Query(ctx, `SELECT id FROM company_locations WHERE tenant_id=$1 AND company_id=$2`, tenant, company)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			locs[id] = true
		}
		top := 0
		for _, m := range in {
			if !locs[m.Location] {
				return FieldError{"members", m.Email + ": esa sucursal no existe"}
			}
			if m.Approver && m.Level > top {
				top = m.Level
			}
		}
		if err := approversCover(threshold, rules, in, top); err != nil {
			return err
		}
		hash := map[string][]byte{}
		var keep [][]byte
		for _, m := range in {
			h, err := s.Keys.Blind(tenant, "email", m.Email)
			if err != nil {
				return err
			}
			hash[m.Email] = h
			keep = append(keep, h)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 AND NOT (email_hash = ANY($3))`, tenant, company, keep); err != nil {
			return err
		}
		for _, m := range in {
			enc, err := s.Keys.Encrypt(tenant, []byte(m.Email), "buyer:"+company)
			if err != nil {
				return err
			}
			var sub []byte
			if m.Substitute != "" {
				sub = hash[m.Substitute]
			}
			ct, err := tx.Exec(ctx, `INSERT INTO company_buyers (tenant_id, company_id, email_hash, email, approver, approver_level, monthly_limit, location_id, admin, substitute_hash, away_until)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')::date)
				ON CONFLICT (tenant_id, email_hash) DO UPDATE SET approver=EXCLUDED.approver, approver_level=EXCLUDED.approver_level, monthly_limit=EXCLUDED.monthly_limit,
					location_id=EXCLUDED.location_id, admin=EXCLUDED.admin, substitute_hash=EXCLUDED.substitute_hash, away_until=EXCLUDED.away_until
				WHERE company_buyers.company_id=EXCLUDED.company_id`,
				tenant, company, hash[m.Email], enc, m.Approver, m.Level, m.MonthlyLimit, m.Location, m.Admin, sub, m.AwayUntil)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return FieldError{"members", m.Email + " ya compra para otra empresa"}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE companies SET updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, company); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "company.members", company, map[string]any{"members": len(in), "by": map[bool]string{true: "company_admin", false: "store"}[by != ""]})
	})
	if err != nil {
		return nil, err
	}
	return s.Members(ctx, tenant, company)
}

// approversCover checks that every level the company may ask for has someone
// to approve it, so no purchase waits for nobody.
func approversCover(threshold int64, rules []ApprovalRule, in []Member, top int) error {
	need := 0
	if threshold > 0 {
		need = 1
	}
	for _, m := range in {
		if m.MonthlyLimit > 0 && need < 1 {
			need = 1
		}
	}
	for _, r := range rules {
		need = max(need, r.Level)
	}
	if need > top {
		return FieldError{"members", fmt.Sprintf("Las reglas de la empresa piden aprobación de nivel %d: agrega a alguien que apruebe ese nivel", need)}
	}
	return nil
}

// Location is a branch of a company.
type Location struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	PriceGroup   string            `json:"priceGroup"`
	MonthlyLimit int64             `json:"monthlyLimit"`
	Address      map[string]string `json:"address"`
	MonthSpent   int64             `json:"monthSpent"`
}

// Locations lists the branches of a company.
func (s *Service) Locations(ctx context.Context, tenant, company string) ([]Location, error) {
	var out []Location
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT l.id, l.name, l.price_group, l.monthly_limit, l.address,
				COALESCE((SELECT sum(amount) FROM company_entries e WHERE e.tenant_id=l.tenant_id AND e.company_id=l.company_id AND e.location_id=l.id
					AND e.kind IN ('charge','release') AND e.created_at >= date_trunc('month', now())),0)
			FROM company_locations l WHERE l.tenant_id=$1 AND l.company_id=$2 ORDER BY l.name`, tenant, company)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Location, error) {
			var l Location
			return l, r.Scan(&l.ID, &l.Name, &l.PriceGroup, &l.MonthlyLimit, &l.Address, &l.MonthSpent)
		})
		return err
	})
	return out, err
}

var addressKeys = map[string]bool{"street": true, "city": true, "state": true, "postalCode": true, "country": true, "phone": true, "contact": true}

// SaveLocations replaces the branches of a company. A branch that buyers
// still belong to cannot be removed.
func (s *Service) SaveLocations(ctx context.Context, tenant, actor, company string, in []Location) ([]Location, error) {
	if len(in) > 200 {
		return nil, FieldError{"locations", "Máximo 200 sucursales"}
	}
	for i := range in {
		l := &in[i]
		l.Name, l.PriceGroup = strings.TrimSpace(l.Name), strings.TrimSpace(l.PriceGroup)
		if l.ID == "" {
			l.ID = newID("loc_")
		}
		switch {
		case l.Name == "" || len(l.Name) > 120:
			return nil, FieldError{"locations", "Cada sucursal lleva un nombre de hasta 120 caracteres"}
		case len(l.PriceGroup) > 60 || strings.ContainsAny(l.PriceGroup, " \t\n"):
			return nil, FieldError{"locations", l.Name + ": el grupo de precios es una sola palabra"}
		case l.MonthlyLimit < 0:
			return nil, FieldError{"locations", l.Name + ": el tope mensual no puede ser negativo"}
		}
		clean := map[string]string{}
		for k, v := range l.Address {
			v = strings.TrimSpace(v)
			if addressKeys[k] && v != "" {
				if len(v) > 200 {
					return nil, FieldError{"locations", l.Name + ": dato de dirección demasiado largo"}
				}
				clean[k] = v
			}
		}
		l.Address = clean
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM companies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, company).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		ids := make([]string, 0, len(in))
		for _, l := range in {
			ids = append(ids, l.ID)
		}
		var used string
		err := tx.QueryRow(ctx, `SELECT l.name FROM company_locations l WHERE l.tenant_id=$1 AND l.company_id=$2 AND NOT (l.id = ANY($3))
			AND EXISTS (SELECT 1 FROM company_buyers b WHERE b.tenant_id=l.tenant_id AND b.company_id=l.company_id AND b.location_id=l.id) LIMIT 1`, tenant, company, ids).Scan(&used)
		if err == nil {
			return FieldError{"locations", "La sucursal " + used + " tiene compradores: cámbialos de sucursal antes de quitarla"}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM company_locations WHERE tenant_id=$1 AND company_id=$2 AND NOT (id = ANY($3))`, tenant, company, ids); err != nil {
			return err
		}
		for _, l := range in {
			ct, err := tx.Exec(ctx, `INSERT INTO company_locations (tenant_id, id, company_id, name, price_group, monthly_limit, address) VALUES ($1,$2,$3,$4,$5,$6,$7)
				ON CONFLICT (tenant_id, id) DO UPDATE SET name=EXCLUDED.name, price_group=EXCLUDED.price_group, monthly_limit=EXCLUDED.monthly_limit, address=EXCLUDED.address
				WHERE company_locations.company_id=EXCLUDED.company_id`, tenant, l.ID, company, l.Name, l.PriceGroup, l.MonthlyLimit, l.Address)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return FieldError{"locations", "Sucursal desconocida"}
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "company.locations", company, map[string]any{"locations": len(in)})
	})
	if err != nil {
		return nil, err
	}
	return s.Locations(ctx, tenant, company)
}

// RequestRow is a purchase request in a list (panel, company portal, buyer).
type RequestRow struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"`
	Total         int64     `json:"total"`
	Currency      string    `json:"currency"`
	Level         int       `json:"level"`
	RequiredLevel int       `json:"requiredLevel"`
	Reason        string    `json:"reason"`
	Requester     string    `json:"requester"`
	Location      string    `json:"location,omitempty"`
	OrderID       string    `json:"orderId,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Decisions     []Step    `json:"decisions"`
	BuyerHash     []byte    `json:"-"`
}

// Step is one decision on a request.
type Step struct {
	Level  int       `json:"level"`
	Action string    `json:"action"` // approved | rejected | expired | cancelled
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// PurchaseRequests lists the requests of a company, newest first; buyer, when
// set, keeps only that buyer's own.
func (s *Service) PurchaseRequests(ctx context.Context, tenant, company string, buyer []byte) ([]RequestRow, error) {
	var out []RequestRow
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `SELECT id, status, total, currency, level, required_level, reason, request, location_id, COALESCE(order_id,''), created_at, COALESCE(expires_at, created_at), decisions, buyer_hash
			FROM purchase_requests WHERE tenant_id=$1 AND company_id=$2`
		args := []any{tenant, company}
		if buyer != nil {
			q += ` AND buyer_hash=$3`
			args = append(args, buyer)
		}
		rows, err := tx.Query(ctx, q+` ORDER BY created_at DESC LIMIT 200`, args...)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (RequestRow, error) {
			var v RequestRow
			var enc []byte
			if err := r.Scan(&v.ID, &v.Status, &v.Total, &v.Currency, &v.Level, &v.RequiredLevel, &v.Reason, &enc, &v.Location, &v.OrderID, &v.CreatedAt, &v.ExpiresAt, &v.Decisions, &v.BuyerHash); err != nil {
				return v, err
			}
			if plain, err := s.Keys.Decrypt(tenant, enc, v.ID); err == nil {
				var req CheckoutRequest
				if json.Unmarshal(plain, &req) == nil {
					v.Requester = req.Customer.Name
				}
			}
			return v, nil
		})
		return err
	})
	return out, err
}

// CancelPurchaseRequest lets the buyer withdraw a request still waiting.
func (s *Service) CancelPurchaseRequest(ctx context.Context, tenant, company, id string, buyer []byte) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		step, _ := json.Marshal([]Step{{Action: "cancelled", By: "comprador", At: time.Now().UTC()}})
		ct, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='cancelled', decided_at=now(), decisions=decisions || $5::jsonb
			WHERE tenant_id=$1 AND company_id=$2 AND id=$3 AND buyer_hash=$4 AND status='pending'`, tenant, company, id, buyer, step)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("%w: la solicitud ya no está en espera", ErrInvalidState)
		}
		return store.Audit(ctx, tx, tenant, "storefront", "purchase.cancelled", id, map[string]any{})
	})
}

// ExpirePurchaseRequests closes the requests nobody decided in time, in every
// store (background round).
func (s *Service) ExpirePurchaseRequests(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT DISTINCT tenant_id FROM purchase_requests WHERE status='pending' AND expires_at < now() LIMIT 200`)
	if err != nil {
		return 0, err
	}
	tenants, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	step, _ := json.Marshal([]Step{{Action: "expired", At: time.Now().UTC()}})
	for _, tenant := range tenants {
		err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='expired', decided_at=now(), decisions=decisions || $2::jsonb
				WHERE tenant_id=$1 AND status='pending' AND expires_at < now()`, tenant, step)
			if err != nil || ct.RowsAffected() == 0 {
				return err
			}
			n += int(ct.RowsAffected())
			return store.Audit(ctx, tx, tenant, "system", "purchase.expired", "", map[string]any{"count": ct.RowsAffected()})
		})
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
