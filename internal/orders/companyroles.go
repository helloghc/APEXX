package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helloghc/apexx/internal/store"
)

// Company roles: a company (or the store for it) names sets of permissions
// and gives them to its people. A role sets the approval and administration
// switches of its members, plus what only roles control: buying on the
// company's credit, seeing the credit and asking for quotes. Permissions are
// checked by the server on every purchase, quote and portal view.

// RolePerms are the permissions of a company role.
type RolePerms struct {
	BuyCredit  bool `json:"buyCredit"`  // pay on the company's credit
	ViewCredit bool `json:"viewCredit"` // see the available credit and the statement
	Quote      bool `json:"quote"`      // ask for quotes
	Approve    bool `json:"approve"`    // approve the purchases of others
	Level      int  `json:"level"`      // approval level (1..5)
	Manage     bool `json:"manage"`     // manage people, branches and roles
}

// CompanyRole is a named set of permissions.
type CompanyRole struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Perms   RolePerms `json:"perms"`
	Members int       `json:"members"`
}

// FullPerms is what a member without a role may do (as before roles).
var FullPerms = RolePerms{BuyCredit: true, ViewCredit: true, Quote: true}

// CompanyRoles lists the roles of a company.
func (s *Service) CompanyRoles(ctx context.Context, tenant, company string) ([]CompanyRole, error) {
	var out []CompanyRole
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT r.id, r.name, r.perms, (SELECT count(*) FROM company_buyers b WHERE b.tenant_id=r.tenant_id AND b.company_id=r.company_id AND b.role_id=r.id)::int
			FROM company_roles r WHERE r.tenant_id=$1 AND r.company_id=$2 ORDER BY r.name`, tenant, company)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (CompanyRole, error) {
			var x CompanyRole
			return x, r.Scan(&x.ID, &x.Name, &x.Perms, &x.Members)
		})
		return err
	})
	if out == nil {
		out = []CompanyRole{}
	}
	return out, err
}

// SaveCompanyRole creates (empty id) or updates a role. by is the email of a
// company administrator acting from the store's portal ("" for the store):
// they cannot take away their own power to manage.
func (s *Service) SaveCompanyRole(ctx context.Context, tenant, actor, company, by string, in CompanyRole) (CompanyRole, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 60 {
		return in, FieldError{"name", "Ponle un nombre de hasta 60 caracteres"}
	}
	if !in.Perms.Approve {
		in.Perms.Level = 0
	} else if in.Perms.Level < 1 || in.Perms.Level > maxApprovalLevel {
		return in, FieldError{"perms.level", fmt.Sprintf("El nivel de aprobación va de 1 a %d", maxApprovalLevel)}
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM company_roles WHERE tenant_id=$1 AND company_id=$2`, tenant, company).Scan(&n); err != nil {
			return err
		}
		perms, _ := json.Marshal(in.Perms)
		if in.ID == "" {
			if n >= 20 {
				return FieldError{"name", "Una empresa puede tener hasta 20 roles"}
			}
			in.ID = newID("crl_")
			if _, err := tx.Exec(ctx, `INSERT INTO company_roles (tenant_id, company_id, id, name, perms) VALUES ($1,$2,$3,$4,$5)`, tenant, company, in.ID, in.Name, perms); err != nil {
				if isUniqueViolation(err) {
					return FieldError{"name", "Ya hay un rol con ese nombre"}
				}
				return err
			}
		} else {
			ct, err := tx.Exec(ctx, `UPDATE company_roles SET name=$4, perms=$5 WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenant, company, in.ID, in.Name, perms)
			if isUniqueViolation(err) {
				return FieldError{"name", "Ya hay un rol con ese nombre"}
			}
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotFound
			}
			// Its members follow the role at once.
			if _, err := tx.Exec(ctx, `UPDATE company_buyers SET approver=$4, approver_level=GREATEST($5,1), admin=$6 WHERE tenant_id=$1 AND company_id=$2 AND role_id=$3`,
				tenant, company, in.ID, in.Perms.Approve, in.Perms.Level, in.Perms.Manage); err != nil {
				return err
			}
			if err := s.keepAdminTx(ctx, tx, tenant, company, by); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "company.role", company, map[string]any{"role": in.ID, "name": in.Name, "perms": in.Perms, "by": by})
	})
	return in, err
}

// keepAdminTx refuses a change that leaves a company administrator acting from
// the portal without the power to manage.
func (s *Service) keepAdminTx(ctx context.Context, tx pgx.Tx, tenant, company, by string) error {
	if by == "" {
		return nil
	}
	h, err := s.Keys.Blind(tenant, "email", strings.ToLower(strings.TrimSpace(by)))
	if err != nil {
		return err
	}
	var admin bool
	if err := tx.QueryRow(ctx, `SELECT admin FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 AND email_hash=$3`, tenant, company, h).Scan(&admin); err != nil || !admin {
		return FieldError{"perms.manage", "No puedes quitarte el permiso de administrar la empresa"}
	}
	return nil
}

// DeleteCompanyRole removes a role; its members keep their switches.
func (s *Service) DeleteCompanyRole(ctx context.Context, tenant, actor, company, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM company_roles WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenant, company, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "company.role.delete", company, map[string]any{"role": id})
	})
}

// rolesByIDTx loads the roles of a company by id.
func rolesByIDTx(ctx context.Context, tx pgx.Tx, tenant, company string) (map[string]RolePerms, error) {
	rows, err := tx.Query(ctx, `SELECT id, perms FROM company_roles WHERE tenant_id=$1 AND company_id=$2`, tenant, company)
	if err != nil {
		return nil, err
	}
	out := map[string]RolePerms{}
	for rows.Next() {
		var id string
		var p RolePerms
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = p
	}
	rows.Close()
	return out, rows.Err()
}

// ErrRoleForbidden is a purchase or quote the buyer's role does not allow.
var ErrRoleForbidden = errors.New("role forbids")

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
