package orders

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/store"
)

// B2B: a company buys with its own discount and, when the store allows it, on
// credit. Credit is one more payment of the order (provider "company_credit"),
// so refunds and the ledger treat it like any other money.

// Company is a business customer.
type Company struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	RFC         string    `json:"rfc"`
	Status      string    `json:"status"`
	Terms       string    `json:"terms"` // prepaid | net15 | net30 | net60
	CreditLimit int64     `json:"creditLimit"`
	DiscountBps int64     `json:"discountBps"`
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"createdAt"`
	Balance     int64     `json:"balance"` // what the company owes
	Overdue     int64     `json:"overdue"` // charges past their due date, not yet covered by payments
	Buyers      int       `json:"buyers"`
	// PriceGroup links the company to the price lists of that customer group
	// (price per product). ApprovalThreshold: credit orders above it by a buyer
	// who is not an approver wait for an approver of the company (0 = off).
	PriceGroup        string `json:"priceGroup"`
	ApprovalThreshold int64  `json:"approvalThreshold"`
	// TaxExempt removes tax from the company's orders while it holds a valid
	// exemption; TaxExemptUntil ("2027-12-31", optional) is when it ends.
	TaxExempt      bool   `json:"taxExempt"`
	TaxExemptUntil string `json:"taxExemptUntil"`
	// RequirePO asks the buyer for their purchase order number; CreditOnly
	// hides the card: the company always buys on its credit.
	RequirePO  bool `json:"requirePO"`
	CreditOnly bool `json:"creditOnly"`
	// ApprovalRules ask for more levels of approval (by amount, category or
	// branch); ApprovalDays is how long a request waits before it expires.
	ApprovalRules []ApprovalRule `json:"approvalRules"`
	ApprovalDays  int            `json:"approvalDays"`
	// CatalogRestricted: its buyers see and buy only the products priced by
	// a price list of its group (or its branch's).
	CatalogRestricted bool `json:"catalogRestricted"`
	// Hierarchy: the parent company, whether this one buys on the parent's
	// credit and whether it takes the parent's terms, prices and rules.
	ParentID     string         `json:"parentId"`
	ParentName   string         `json:"parentName,omitempty"`
	SharedCredit bool           `json:"sharedCredit"`
	InheritTerms bool           `json:"inheritTerms"`
	Children     []CompanyChild `json:"children,omitempty"`
	Pool         *CreditPool    `json:"pool,omitempty"`
}

// CompanyEntry is one movement of a company's account.
type CompanyEntry struct {
	ID      string     `json:"id"`
	Kind    string     `json:"kind"`
	Amount  int64      `json:"amount"`
	OrderID string     `json:"orderId,omitempty"`
	Number  int64      `json:"orderNumber,omitempty"`
	DueAt   *time.Time `json:"dueAt,omitempty"`
	Note    string     `json:"note,omitempty"`
	At      time.Time  `json:"at"`
}

// CompanyAccess is what a signed-in buyer of a company gets at checkout.
type CompanyAccess struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Terms             string `json:"terms"`
	DiscountBps       int64  `json:"discountBps"`
	Available         int64  `json:"available"` // credit left; 0 when the company pays in advance
	PriceGroup        string `json:"-"`
	ApprovalThreshold int64  `json:"approvalThreshold"`
	Approver          bool   `json:"approver"`
	TaxExempt         bool   `json:"taxExempt"`
	RequirePO         bool   `json:"requirePO"`
	CreditOnly        bool   `json:"creditOnly"`
	// The buyer: approval level (when Approver), monthly cap and what they
	// used this month, branch, and whether they manage the company's buyers.
	ApproverLevel     int            `json:"approverLevel,omitempty"`
	MonthlyLimit      int64          `json:"monthlyLimit,omitempty"`
	MonthSpent        int64          `json:"monthSpent,omitempty"`
	Location          string         `json:"location,omitempty"`
	LocationName      string         `json:"locationName,omitempty"`
	LocationLimit     int64          `json:"locationLimit,omitempty"`
	LocationSpent     int64          `json:"locationSpent,omitempty"`
	Admin             bool           `json:"admin,omitempty"`
	Rules             []ApprovalRule `json:"-"`
	BuyerHash         []byte         `json:"-"`
	CatalogRestricted bool           `json:"catalogRestricted,omitempty"`
	// What the buyer's role allows (everything when they have no role).
	CanCredit     bool   `json:"canCredit"`
	CanViewCredit bool   `json:"canViewCredit"`
	CanQuote      bool   `json:"canQuote"`
	Role          string `json:"role,omitempty"`
}

var termDays = map[string]int{"prepaid": 0, "net15": 15, "net30": 30, "net60": 60}

func (c *Company) clean() error {
	c.Name, c.RFC, c.Note = strings.TrimSpace(c.Name), strings.ToUpper(strings.TrimSpace(c.RFC)), strings.TrimSpace(c.Note)
	if c.Status == "" {
		c.Status = "active"
	}
	if c.Terms == "" {
		c.Terms = "prepaid"
	}
	if c.ApprovalDays == 0 {
		c.ApprovalDays = 7
	}
	if c.ApprovalRules == nil {
		c.ApprovalRules = []ApprovalRule{}
	}
	c.ParentID = strings.TrimSpace(c.ParentID)
	if c.ParentID == "" && (c.SharedCredit || c.InheritTerms) {
		return FieldError{"parentId", "Para compartir crédito o heredar condiciones elige la empresa matriz"}
	}
	if err := cleanRules(c.ApprovalRules); err != nil {
		return err
	}
	_, okTerms := termDays[c.Terms]
	switch {
	case c.Name == "" || len(c.Name) > 160:
		return FieldError{"name", "Escribe el nombre de la empresa"}
	case c.RFC != "" && (len(c.RFC) < 12 || len(c.RFC) > 13):
		return FieldError{"rfc", "El RFC tiene 12 o 13 caracteres"}
	case c.Status != "active" && c.Status != "suspended":
		return FieldError{"status", "Estado desconocido"}
	case !okTerms:
		return FieldError{"terms", "Plazo de pago desconocido"}
	case c.CreditLimit < 0 || c.CreditLimit > 1_000_000_000_00:
		return FieldError{"creditLimit", "Límite de crédito fuera de rango"}
	case c.Terms != "prepaid" && c.CreditLimit == 0 && !c.SharedCredit && !c.InheritTerms:
		return FieldError{"creditLimit", "Para vender a crédito define un límite mayor a cero"}
	case c.DiscountBps < 0 || c.DiscountBps > 9000:
		return FieldError{"discountBps", "El descuento debe estar entre 0% y 90%"}
	case len(c.Note) > 500:
		return FieldError{"note", "Nota de hasta 500 caracteres"}
	case len(c.PriceGroup) > 60 || strings.ContainsAny(c.PriceGroup, " \t\n"):
		return FieldError{"priceGroup", "El grupo de precios es una sola palabra de hasta 60 caracteres"}
	case c.CatalogRestricted && c.PriceGroup == "":
		return FieldError{"catalogRestricted", "Para limitar su catálogo, la empresa necesita un grupo de precios"}
	case c.CreditOnly && c.Terms == "prepaid":
		return FieldError{"creditOnly", "Una empresa de contado no puede pagar solo a crédito"}
	case c.ApprovalDays < 1 || c.ApprovalDays > 90:
		return FieldError{"approvalDays", "Una solicitud espera entre 1 y 90 días"}
	case c.TaxExemptUntil != "" && !validDate(c.TaxExemptUntil):
		return FieldError{"taxExemptUntil", "Fecha con formato AAAA-MM-DD"}
	case c.ApprovalThreshold < 0:
		return FieldError{"approvalThreshold", "El monto de aprobación no puede ser negativo"}
	}
	return nil
}

const companySelect = `SELECT c.id, c.name, c.rfc, c.status, c.terms, c.credit_limit, c.discount_bps, c.note, c.created_at,
	COALESCE(e.balance,0),
	LEAST(GREATEST(COALESCE(e.balance,0),0), GREATEST(COALESCE(e.due,0) - COALESCE(e.paid,0), 0)),
	(SELECT count(*) FROM company_buyers b WHERE b.tenant_id=c.tenant_id AND b.company_id=c.id)::int, c.price_group, c.approval_threshold, c.tax_exempt, COALESCE(to_char(c.tax_exempt_until,'YYYY-MM-DD'),''), c.require_po, c.credit_only,
	c.approval_rules, c.approval_days, c.catalog_restricted, COALESCE(c.parent_id,''), COALESCE(p.name,''), c.shared_credit, c.inherit_terms
	FROM companies c LEFT JOIN companies p ON p.tenant_id=c.tenant_id AND p.id=c.parent_id LEFT JOIN (SELECT company_id, sum(amount) AS balance,
			sum(amount) FILTER (WHERE kind='charge' AND due_at < now()) AS due,
			-sum(amount) FILTER (WHERE kind IN ('payment','refund','release') OR (kind='adjust' AND amount < 0)) AS paid
		FROM company_entries WHERE tenant_id=$1 GROUP BY company_id) e ON e.company_id=c.id
	WHERE c.tenant_id=$1`

func scanCompany(r pgx.Row) (Company, error) {
	var c Company
	return c, r.Scan(&c.ID, &c.Name, &c.RFC, &c.Status, &c.Terms, &c.CreditLimit, &c.DiscountBps, &c.Note, &c.CreatedAt, &c.Balance, &c.Overdue, &c.Buyers, &c.PriceGroup, &c.ApprovalThreshold, &c.TaxExempt, &c.TaxExemptUntil, &c.RequirePO, &c.CreditOnly, &c.ApprovalRules, &c.ApprovalDays, &c.CatalogRestricted, &c.ParentID, &c.ParentName, &c.SharedCredit, &c.InheritTerms)
}

// Companies lists the business customers of a store.
func (s *Service) Companies(ctx context.Context, tenant string) ([]Company, error) {
	var out []Company
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, companySelect+` ORDER BY c.name`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCompany(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

// Company returns a company with its buyers (emails) and its statement.
func (s *Service) Company(ctx context.Context, tenant, id string, pii bool) (Company, []string, []CompanyEntry, error) {
	c, buyers, _, entries, err := s.CompanyFull(ctx, tenant, id, pii)
	return c, buyers, entries, err
}

// CompanyFull is Company plus the emails of the buyers who may approve purchases.
func (s *Service) CompanyFull(ctx context.Context, tenant, id string, pii bool) (Company, []string, []string, []CompanyEntry, error) {
	var approvers []string
	var c Company
	var buyers []string
	var entries []CompanyEntry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		c, err = scanCompany(tx.QueryRow(ctx, companySelect+` AND c.id=$2`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if c.Children, err = childrenTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		pool, _, err := creditPoolTx(ctx, tx, tenant, id, false)
		if err != nil {
			return err
		}
		if len(pool.Members) > 1 || pool.RootID != id {
			c.Pool = &pool
		}
		if pii {
			rows, err := tx.Query(ctx, `SELECT email, approver FROM company_buyers WHERE tenant_id=$1 AND company_id=$2`, tenant, id)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var enc []byte
				var ap bool
				if err := rows.Scan(&enc, &ap); err != nil {
					return err
				}
				if plain, err := s.Keys.Decrypt(tenant, enc, "buyer:"+id); err == nil {
					buyers = append(buyers, string(plain))
					if ap {
						approvers = append(approvers, string(plain))
					}
				}
			}
			if err := rows.Err(); err != nil {
				return err
			}
			rows.Close()
			sort.Strings(buyers)
			sort.Strings(approvers)
		}
		rows, err := tx.Query(ctx, `SELECT e.id, e.kind, e.amount, COALESCE(e.order_id,''), COALESCE(o.number,0), e.due_at, e.note, e.created_at
			FROM company_entries e LEFT JOIN orders o ON o.tenant_id=e.tenant_id AND o.id=e.order_id
			WHERE e.tenant_id=$1 AND e.company_id=$2 ORDER BY e.created_at DESC, e.id DESC LIMIT 200`, tenant, id)
		if err != nil {
			return err
		}
		entries, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CompanyEntry, error) {
			var e CompanyEntry
			return e, r.Scan(&e.ID, &e.Kind, &e.Amount, &e.OrderID, &e.Number, &e.DueAt, &e.Note, &e.At)
		})
		return err
	})
	return c, buyers, approvers, entries, err
}

// SaveCompany creates (empty id) or updates a company. buyers, when not nil,
// replaces the list of people who may buy for it.
func (s *Service) SaveCompany(ctx context.Context, tenant, actor string, in Company, buyers []string) (Company, error) {
	return s.SaveCompanyWith(ctx, tenant, actor, in, buyers, nil)
}

// SaveCompanyWith also marks which buyers may approve purchases of the others.
func (s *Service) SaveCompanyWith(ctx context.Context, tenant, actor string, in Company, buyers, approvers []string) (Company, error) {
	isApprover := map[string]bool{}
	for _, a := range approvers {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" {
			isApprover[a] = true
			buyers = append(buyers, a) // an approver is also a buyer
		}
	}
	if in.ApprovalThreshold > 0 && buyers != nil && len(isApprover) == 0 {
		return Company{}, FieldError{"approvers", "Para pedir aprobación agrega al menos una persona que apruebe"}
	}
	if err := in.clean(); err != nil {
		return Company{}, err
	}
	seen := map[string]bool{}
	var emails []string
	for _, b := range buyers {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == "" || seen[b] {
			continue
		}
		if !strings.Contains(b, "@") || len(b) > 200 {
			return Company{}, FieldError{"buyers", "“" + b + "” no es un correo válido"}
		}
		seen[b] = true
		emails = append(emails, b)
	}
	if len(emails) > 500 {
		return Company{}, FieldError{"buyers", "Máximo 500 compradores por empresa"}
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if in.ID == "" {
			in.ID = newID("com_")
			if err := checkParentTx(ctx, tx, tenant, "", in.ParentID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO companies (tenant_id, id, name, rfc, status, terms, credit_limit, discount_bps, note, price_group, approval_threshold, tax_exempt, tax_exempt_until, require_po, credit_only, approval_rules, approval_days, catalog_restricted) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,'')::date,$14,$15,$16,$17,$18)`,
				tenant, in.ID, in.Name, in.RFC, in.Status, in.Terms, in.CreditLimit, in.DiscountBps, in.Note, in.PriceGroup, in.ApprovalThreshold, in.TaxExempt, in.TaxExemptUntil, in.RequirePO, in.CreditOnly, in.ApprovalRules, in.ApprovalDays, in.CatalogRestricted); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE companies SET parent_id=NULLIF($3,''), shared_credit=$4, inherit_terms=$5 WHERE tenant_id=$1 AND id=$2`, tenant, in.ID, in.ParentID, in.SharedCredit, in.InheritTerms); err != nil {
				return err
			}
		} else {
			if err := checkParentTx(ctx, tx, tenant, in.ID, in.ParentID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE companies SET parent_id=NULLIF($3,''), shared_credit=$4, inherit_terms=$5 WHERE tenant_id=$1 AND id=$2`, tenant, in.ID, in.ParentID, in.SharedCredit, in.InheritTerms); err != nil {
				return err
			}
			ct, err := tx.Exec(ctx, `UPDATE companies SET name=$3, rfc=$4, status=$5, terms=$6, credit_limit=$7, discount_bps=$8, note=$9, price_group=$10, approval_threshold=$11, tax_exempt=$12, tax_exempt_until=NULLIF($13,'')::date, require_po=$14, credit_only=$15, approval_rules=$16, approval_days=$17, catalog_restricted=$18, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				tenant, in.ID, in.Name, in.RFC, in.Status, in.Terms, in.CreditLimit, in.DiscountBps, in.Note, in.PriceGroup, in.ApprovalThreshold, in.TaxExempt, in.TaxExemptUntil, in.RequirePO, in.CreditOnly, in.ApprovalRules, in.ApprovalDays, in.CatalogRestricted)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if buyers != nil {
			// People who stay keep their level, cap, branch and role.
			hashes := make([][]byte, 0, len(emails))
			for _, e := range emails {
				hash, err := s.Keys.Blind(tenant, "email", e)
				if err != nil {
					return err
				}
				hashes = append(hashes, hash)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 AND NOT (email_hash = ANY($3))`, tenant, in.ID, hashes); err != nil {
				return err
			}
			for i, e := range emails {
				enc, err := s.Keys.Encrypt(tenant, []byte(e), "buyer:"+in.ID)
				if err != nil {
					return err
				}
				ct, err := tx.Exec(ctx, `INSERT INTO company_buyers (tenant_id, company_id, email_hash, email, approver) VALUES ($1,$2,$3,$4,$5)
					ON CONFLICT (tenant_id, email_hash) DO UPDATE SET approver=EXCLUDED.approver WHERE company_buyers.company_id=EXCLUDED.company_id`, tenant, in.ID, hashes[i], enc, isApprover[e])
				if err != nil {
					return err
				}
				if ct.RowsAffected() == 0 {
					return FieldError{"buyers", e + " ya compra para otra empresa"}
				}
			}
		}
		// A rule that asks for a level nobody approves would be signed by a
		// lower level: refuse it and say who is missing.
		need := 0
		for _, r := range in.ApprovalRules {
			need = max(need, r.Level)
		}
		if need > 0 {
			var top int
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(approver_level),0) FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 AND approver`, tenant, in.ID).Scan(&top); err != nil {
				return err
			}
			if top < need {
				return FieldError{"approvalRules", fmt.Sprintf("Una regla pide aprobación de nivel %d y nadie aprueba ese nivel. Guarda la empresa sin esa regla, asigna el nivel en “Editar compradores” y vuelve a agregarla.", need)}
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "company.save", in.ID, map[string]any{"name": in.Name, "terms": in.Terms, "creditLimit": in.CreditLimit, "discountBps": in.DiscountBps, "status": in.Status, "parentId": in.ParentID, "sharedCredit": in.SharedCredit, "inheritTerms": in.InheritTerms})
	})
	if err != nil {
		return Company{}, err
	}
	out, _, _, err := s.Company(ctx, tenant, in.ID, false)
	return out, err
}

// CompanyPayment records money received from a company (kind payment,
// amount > 0) or a correction (kind adjust, positive = owes more).
func (s *Service) CompanyPayment(ctx context.Context, tenant, actor, id, kind string, amount int64, note string) error {
	note = strings.TrimSpace(note)
	switch {
	case kind != "payment" && kind != "adjust":
		return FieldError{"kind", "Movimiento desconocido"}
	case amount == 0 || (kind == "payment" && amount < 0):
		return FieldError{"amount", "Escribe un monto mayor a cero"}
	case note == "" || len(note) > 300:
		return FieldError{"note", "Escribe la referencia del pago o el motivo"}
	}
	delta := amount
	if kind == "payment" {
		delta = -amount
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var cur string
		if err := tx.QueryRow(ctx, `SELECT id FROM companies WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&cur); err != nil {
			return ErrNotFound
		}
		entry := newID("cen_")
		if _, err := tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, note, actor) VALUES ($1,$2,$3,$4,$5,$6,$7)`, tenant, entry, id, kind, delta, note, actor); err != nil {
			return err
		}
		// Money in the bank against what the company owed (or a correction of the receivable).
		other := "manual_clearing"
		if kind == "adjust" {
			other = "receivable_adjustments"
		}
		for acct, amt := range map[string]int64{"accounts_receivable": delta, other: -delta} {
			if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency) VALUES ($1,$2,$3,$4,'MXN')`, tenant, "company-"+entry, acct, amt); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "company."+kind, id, map[string]any{"amount": amount, "note": note})
	})
}

// CompanyForEmail finds the active company a verified email buys for.
func (s *Service) CompanyForEmail(ctx context.Context, tenant, email string) (*CompanyAccess, error) {
	hash, err := s.Keys.Blind(tenant, "email", strings.ToLower(strings.TrimSpace(email)))
	if err != nil {
		return nil, err
	}
	var a CompanyAccess
	var limit, balance int64
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var locGroup string
		var perms *RolePerms
		if err := tx.QueryRow(ctx, `SELECT c.id, c.name, c.terms, c.discount_bps, c.credit_limit,
				COALESCE((SELECT sum(amount) FROM company_entries e WHERE e.tenant_id=c.tenant_id AND e.company_id=c.id),0), c.price_group, c.approval_threshold, b.approver,
				c.tax_exempt AND (c.tax_exempt_until IS NULL OR c.tax_exempt_until >= current_date), c.require_po, c.credit_only,
				b.approver_level, b.monthly_limit, b.admin, c.approval_rules, c.catalog_restricted, COALESCE(l.id,''), COALESCE(l.name,''), COALESCE(l.price_group,''), COALESCE(l.monthly_limit,0),
				COALESCE(r.name,''), r.perms
			FROM company_buyers b JOIN companies c ON c.tenant_id=b.tenant_id AND c.id=b.company_id
			LEFT JOIN company_roles r ON r.tenant_id=b.tenant_id AND r.company_id=b.company_id AND r.id=b.role_id
			LEFT JOIN company_locations l ON l.tenant_id=b.tenant_id AND l.company_id=b.company_id AND l.id=b.location_id
			WHERE b.tenant_id=$1 AND b.email_hash=$2 AND c.status='active'`, tenant, hash).Scan(&a.ID, &a.Name, &a.Terms, &a.DiscountBps, &limit, &balance, &a.PriceGroup, &a.ApprovalThreshold, &a.Approver, &a.TaxExempt, &a.RequirePO, &a.CreditOnly,
			&a.ApproverLevel, &a.MonthlyLimit, &a.Admin, &a.Rules, &a.CatalogRestricted, &a.Location, &a.LocationName, &locGroup, &a.LocationLimit, &a.Role, &perms); err != nil {
			return err
		}
		p := FullPerms
		if perms != nil {
			p = *perms
		}
		a.CanCredit, a.CanViewCredit, a.CanQuote = p.BuyCredit, p.ViewCredit, p.Quote
		if locGroup != "" {
			a.PriceGroup = locGroup // the branch buys with its own price list
		}
		if err := applyHierarchyTx(ctx, tx, tenant, &a, locGroup != ""); err != nil {
			return err
		}
		if !a.Approver {
			a.ApproverLevel = 0
		}
		a.BuyerHash = hash
		var err error
		a.MonthSpent, a.LocationSpent, err = monthSpentTx(ctx, tx, tenant, a.ID, hash, a.Location)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, _ = limit, balance // the credit comes from the company's pool (applyHierarchyTx)
	return &a, nil
}

// CompanyAccessByID is CompanyForEmail for trusted server-side integrations
// that already know the company.
func (s *Service) CompanyAccessByID(ctx context.Context, tenant, id string) (*CompanyAccess, error) {
	c, _, _, err := s.Company(ctx, tenant, id, false)
	if err != nil {
		return nil, err
	}
	if c.Status != "active" {
		return nil, &CreditError{"company_suspended", "La empresa está suspendida"}
	}
	// A server-side integration acts for the company itself: it needs no approval.
	a := &CompanyAccess{ID: c.ID, Name: c.Name, Terms: c.Terms, DiscountBps: c.DiscountBps, PriceGroup: c.PriceGroup, ApprovalThreshold: c.ApprovalThreshold, Approver: true, ApproverLevel: maxApprovalLevel, Rules: c.ApprovalRules, CatalogRestricted: c.CatalogRestricted,
		TaxExempt: c.TaxExempt && (c.TaxExemptUntil == "" || c.TaxExemptUntil >= time.Now().Format("2006-01-02")), RequirePO: c.RequirePO, CreditOnly: c.CreditOnly, CanCredit: true, CanViewCredit: true, CanQuote: true}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error { return applyHierarchyTx(ctx, tx, tenant, a, false) })
	return a, err
}

// CompanyPromo is the company's discount as a promotion for the cart.
func CompanyPromo(c *CompanyAccess) []promo.Promotion {
	if c == nil || c.DiscountBps <= 0 {
		return nil
	}
	return []promo.Promotion{{ID: "company", Name: "Precio de empresa: " + c.Name, Type: promo.TypeItemPercent, Status: "active", Bps: c.DiscountBps}}
}

// CreditError is why an order cannot go on the company's credit.
type CreditError struct{ Code, Message string }

func (e *CreditError) Error() string { return e.Message }

// creditChargeTx puts amount of an order on the company's account. The
// company row is locked, so two orders cannot use the same credit.
func creditChargeTx(ctx context.Context, tx pgx.Tx, tenant, actor, orderID string, c *CompanyAccess, amount int64) error {
	if c == nil {
		return &CreditError{"credit_unavailable", "Para pagar a crédito entra con la cuenta de un comprador de empresa"}
	}
	if !c.CanCredit {
		return &CreditError{"role_forbidden", "Tu rol en la empresa no permite comprar a crédito; paga con tarjeta o pide a quien administra tu empresa que te dé ese permiso"}
	}
	// The pool's root is locked: the parent and its subsidiaries that share
	// its credit cannot spend the same money at once.
	pool, rootStatus, err := creditPoolTx(ctx, tx, tenant, c.ID, true)
	if err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM companies WHERE tenant_id=$1 AND id=$2`, tenant, c.ID).Scan(&status); err != nil {
		return err
	}
	terms, limit, balance := pool.Terms, pool.Limit, pool.Balance
	switch {
	case status != "active" || rootStatus != "active" || terms == "prepaid":
		return &CreditError{"credit_unavailable", "Tu empresa no tiene crédito activo en esta tienda"}
	case balance+amount > limit:
		msg := fmt.Sprintf("El pedido supera el crédito disponible de tu empresa ($%s)", cents(max(limit-balance, 0)))
		if pool.RootID != c.ID {
			msg = fmt.Sprintf("El pedido supera el crédito disponible del grupo %s ($%s)", pool.RootName, cents(max(limit-balance, 0)))
		}
		return &CreditError{"credit_limit", msg}
	}
	due := time.Now().AddDate(0, 0, termDays[terms])
	_, err = tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, order_id, due_at, actor, buyer_hash, location_id) VALUES ($1,$2,$3,'charge',$4,$5,$6,$7,$8,$9)`,
		tenant, newID("cen_"), c.ID, amount, orderID, due, actor, c.BuyerHash, c.Location)
	return err
}

// creditPartTx returns what an order has on company credit, and the company.
func creditPartTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) (int64, string, error) {
	var amount int64
	var company *string
	err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount),0), min(company_id) FROM company_entries WHERE tenant_id=$1 AND order_id=$2 AND kind IN ('charge','release')`, tenant, orderID).Scan(&amount, &company)
	if err != nil || company == nil {
		return 0, "", err
	}
	return amount, *company, nil
}

// releaseCreditTx undoes the charge of an order that was not completed.
func releaseCreditTx(ctx context.Context, tx pgx.Tx, tenant, actor, orderID string) error {
	amount, company, err := creditPartTx(ctx, tx, tenant, orderID)
	if err != nil || amount <= 0 {
		return err
	}
	// The release carries the buyer and branch of the charge: their month frees up.
	_, err = tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, order_id, actor, note, buyer_hash, location_id)
		SELECT $1,$2,$3,'release',$4,$5,$6,'El pedido no se completó', buyer_hash, location_id FROM company_entries WHERE tenant_id=$1 AND order_id=$5 AND kind='charge' LIMIT 1`,
		tenant, newID("cen_"), company, -amount, orderID, actor)
	return err
}

// creditRefundPart gives back to the company's account the part of a refund
// that was on credit. The entry id comes from the refund: a retry is a no-op.
func (s *Service) creditRefundPart(ctx context.Context, tenant, refundID string, p refundPart) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var orderID string
		if err := tx.QueryRow(ctx, `SELECT order_id FROM payments WHERE tenant_id=$1 AND id=$2`, tenant, p.PaymentID).Scan(&orderID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, order_id, note) VALUES ($1,$2,$3,'refund',$4,$5,'Reembolso')
			ON CONFLICT (tenant_id, id) DO NOTHING`, tenant, "cen_"+refundID+"_"+p.PaymentID, p.Ref, -p.Amount, orderID)
		return err
	})
}

// CompanyGroup is the price list group of the company ("" = store prices).
func CompanyGroup(c *CompanyAccess) string {
	if c == nil {
		return ""
	}
	return c.PriceGroup
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}
