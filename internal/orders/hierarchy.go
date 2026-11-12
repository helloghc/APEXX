package orders

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Company hierarchy. A subsidiary points to its parent (three levels at
// most). With InheritTerms it buys with the terms, discount, price list,
// catalog and rules of the nearest ancestor that sets its own; with
// SharedCredit it draws on its parent's credit, so the parent and the
// subsidiaries that share it form one pool with one limit. A charge locks the
// pool's root company, so two subsidiaries cannot spend the same credit.

const maxCompanyDepth = 3

// CompanyChild is a subsidiary as its parent sees it.
type CompanyChild struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Balance      int64  `json:"balance"`
	SharedCredit bool   `json:"sharedCredit"`
	InheritTerms bool   `json:"inheritTerms"`
	Children     int    `json:"children"`
}

// CreditPool is the credit a company buys on: its own, or its parent's.
type CreditPool struct {
	RootID    string   `json:"rootId"`
	RootName  string   `json:"rootName"`
	Limit     int64    `json:"limit"`
	Balance   int64    `json:"balance"` // owed by every company of the pool
	Available int64    `json:"available"`
	Terms     string   `json:"terms"`
	Members   []string `json:"members"`
}

// ErrHierarchy is a parent that would break the hierarchy.
var ErrHierarchy = errors.New("hierarchy")

// creditPoolTx finds the pool of a company; lock holds its root until the
// transaction ends.
func creditPoolTx(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (CreditPool, string, error) {
	var p CreditPool
	var status string
	err := tx.QueryRow(ctx, `WITH RECURSIVE up AS (
			SELECT id, parent_id, shared_credit, 0 AS d FROM companies WHERE tenant_id=$1 AND id=$2
			UNION ALL SELECT c.id, c.parent_id, c.shared_credit, up.d+1 FROM companies c JOIN up ON c.tenant_id=$1 AND c.id=up.parent_id AND up.shared_credit WHERE up.d < 5)
		SELECT id FROM up ORDER BY d DESC LIMIT 1`, tenant, id).Scan(&p.RootID)
	if err != nil {
		return p, "", err
	}
	q := `SELECT name, credit_limit, terms, status FROM companies WHERE tenant_id=$1 AND id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	if err := tx.QueryRow(ctx, q, tenant, p.RootID).Scan(&p.RootName, &p.Limit, &p.Terms, &status); err != nil {
		return p, "", err
	}
	rows, err := tx.Query(ctx, `WITH RECURSIVE down AS (
			SELECT id, 0 AS d FROM companies WHERE tenant_id=$1 AND id=$2
			UNION ALL SELECT c.id, down.d+1 FROM companies c JOIN down ON c.tenant_id=$1 AND c.parent_id=down.id AND c.shared_credit WHERE down.d < 5)
		SELECT id FROM down`, tenant, p.RootID)
	if err != nil {
		return p, "", err
	}
	p.Members, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return p, "", err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount),0) FROM company_entries WHERE tenant_id=$1 AND company_id = ANY($2)`, tenant, p.Members).Scan(&p.Balance); err != nil {
		return p, "", err
	}
	if p.Terms != "prepaid" {
		p.Available = max(p.Limit-p.Balance, 0)
	}
	return p, status, nil
}

// inheritedRules are what a subsidiary takes from its parent.
type inheritedRules struct {
	Terms             string
	DiscountBps       int64
	PriceGroup        string
	CatalogRestricted bool
	TaxExempt         bool
	RequirePO         bool
	CreditOnly        bool
	From              string // the company that sets them
}

// effectiveRulesTx walks up while companies inherit; ok is false when the
// company sets its own rules.
func effectiveRulesTx(ctx context.Context, tx pgx.Tx, tenant, id string) (inheritedRules, bool, error) {
	var r inheritedRules
	cur := id
	inherited := false
	for depth := 0; depth <= maxCompanyDepth+1; depth++ {
		var parent *string
		var inherit bool
		err := tx.QueryRow(ctx, `SELECT parent_id, inherit_terms, terms, discount_bps, price_group, catalog_restricted,
				tax_exempt AND (tax_exempt_until IS NULL OR tax_exempt_until >= current_date), require_po, credit_only
			FROM companies WHERE tenant_id=$1 AND id=$2`, tenant, cur).Scan(&parent, &inherit, &r.Terms, &r.DiscountBps, &r.PriceGroup, &r.CatalogRestricted, &r.TaxExempt, &r.RequirePO, &r.CreditOnly)
		if err != nil {
			return r, false, err
		}
		if !inherit || parent == nil {
			r.From = cur
			return r, inherited, nil
		}
		inherited, cur = true, *parent
	}
	return r, false, ErrHierarchy
}

// checkParentTx refuses a parent that is missing, itself, a descendant, or
// that would make the hierarchy deeper than three levels.
func checkParentTx(ctx context.Context, tx pgx.Tx, tenant, id, parent string) error {
	if parent == "" {
		return nil
	}
	if parent == id {
		return FieldError{"parentId", "Una empresa no puede ser su propia matriz"}
	}
	// Levels above the new parent, including it.
	var above int
	var cycle bool
	err := tx.QueryRow(ctx, `WITH RECURSIVE up AS (
			SELECT id, parent_id, 1 AS d FROM companies WHERE tenant_id=$1 AND id=$2
			UNION ALL SELECT c.id, c.parent_id, up.d+1 FROM companies c JOIN up ON c.tenant_id=$1 AND c.id=up.parent_id WHERE up.d < 10)
		SELECT COALESCE(max(d),0), COALESCE(bool_or(id = $3), false) FROM up`, tenant, parent, id).Scan(&above, &cycle)
	if err != nil {
		return err
	}
	if above == 0 {
		return FieldError{"parentId", "La empresa matriz no existe"}
	}
	if cycle {
		return FieldError{"parentId", "Esa empresa ya depende de esta; no puede ser su matriz"}
	}
	// Levels below this company, including it.
	below := 1
	if id != "" {
		if err := tx.QueryRow(ctx, `WITH RECURSIVE down AS (
				SELECT id, 1 AS d FROM companies WHERE tenant_id=$1 AND id=$2
				UNION ALL SELECT c.id, down.d+1 FROM companies c JOIN down ON c.tenant_id=$1 AND c.parent_id=down.id WHERE down.d < 10)
			SELECT COALESCE(max(d),1) FROM down`, tenant, id).Scan(&below); err != nil {
			return err
		}
	}
	if above+below > maxCompanyDepth {
		return FieldError{"parentId", "La jerarquía admite hasta tres niveles: matriz, filial y sucursal de la filial"}
	}
	return nil
}

// childrenTx lists the direct subsidiaries of a company.
func childrenTx(ctx context.Context, tx pgx.Tx, tenant, id string) ([]CompanyChild, error) {
	rows, err := tx.Query(ctx, `SELECT c.id, c.name, c.status, COALESCE((SELECT sum(amount) FROM company_entries e WHERE e.tenant_id=c.tenant_id AND e.company_id=c.id),0),
			c.shared_credit, c.inherit_terms, (SELECT count(*) FROM companies k WHERE k.tenant_id=c.tenant_id AND k.parent_id=c.id)::int
		FROM companies c WHERE c.tenant_id=$1 AND c.parent_id=$2 ORDER BY c.name`, tenant, id)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CompanyChild, error) {
		var c CompanyChild
		return c, r.Scan(&c.ID, &c.Name, &c.Status, &c.Balance, &c.SharedCredit, &c.InheritTerms, &c.Children)
	})
	if out == nil {
		out = []CompanyChild{}
	}
	return out, err
}

// Subsidiaries lists the direct subsidiaries of a company.
func (s *Service) Subsidiaries(ctx context.Context, tenant, id string) ([]CompanyChild, error) {
	var out []CompanyChild
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = childrenTx(ctx, tx, tenant, id)
		return err
	})
	return out, err
}

// applyHierarchyTx fills the access of a buyer with inherited rules and the
// credit pool.
func applyHierarchyTx(ctx context.Context, tx pgx.Tx, tenant string, a *CompanyAccess, branchGroup bool) error {
	r, inherited, err := effectiveRulesTx(ctx, tx, tenant, a.ID)
	if err != nil {
		return err
	}
	if inherited {
		a.Terms, a.DiscountBps, a.CatalogRestricted, a.TaxExempt, a.RequirePO, a.CreditOnly = r.Terms, r.DiscountBps, r.CatalogRestricted, r.TaxExempt, r.RequirePO, r.CreditOnly
		if !branchGroup {
			a.PriceGroup = r.PriceGroup // a branch with its own price list keeps it
		}
	}
	pool, _, err := creditPoolTx(ctx, tx, tenant, a.ID, false)
	if err != nil {
		return err
	}
	if pool.RootID != a.ID {
		a.Terms = pool.Terms // shared credit follows the pool's terms
	}
	a.Available = 0
	if a.Terms != "prepaid" {
		a.Available = pool.Available
	}
	return nil
}
