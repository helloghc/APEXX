package customers

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Sales representatives open the store as a buyer of a company that allows
// it. The panel asks for a single-use link (two minutes); following it opens
// a one-hour session that carries the representative, so the buyer's orders
// and changes record both names. The buyer's own sessions are untouched.

const (
	HandoffTTL    = 2 * time.Minute
	RepSessionTTL = time.Hour
)

// ErrHandoff is a representative link that is unknown, used or expired.
var ErrHandoff = errors.New("customers: invalid handoff")

// RepHandoff prepares a link for a representative to act as the buyer with
// that email (an account is created if they never signed in).
func (s *Service) RepHandoff(ctx context.Context, tenant, email, companyID, memberID, memberName string) (string, string, error) {
	e, ok := NormalizeEmail(email)
	if !ok {
		return "", "", FieldError{"email", "Correo inválido"}
	}
	token, sum := newToken()
	var cid string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		cid, _, err = UpsertByEmailTx(ctx, tx, s.Keys, tenant, e, "", "")
		if err != nil {
			return err
		}
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM customers WHERE tenant_id=$1 AND id=$2`, tenant, cid).Scan(&status); err != nil {
			return err
		}
		if status != "active" {
			return ErrDisabled
		}
		if _, err := tx.Exec(ctx, `INSERT INTO rep_handoffs (tenant_id, token_hash, customer_id, company_id, member_id, member_name, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, sum, cid, companyID, memberID, memberName, s.now().Add(HandoffTTL)); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "member:"+memberID, "company.act_as", companyID, map[string]any{"customer": cid, "representative": memberName})
	})
	return token, cid, err
}

// ConsumeHandoff turns a representative link into a session, once.
func (s *Service) ConsumeHandoff(ctx context.Context, tenant, token string) (session, customerID, rep string, err error) {
	if !strings.HasPrefix(token, "apc_") {
		return "", "", "", ErrHandoff
	}
	sum := sha256.Sum256([]byte(token))
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var member string
		err := tx.QueryRow(ctx, `UPDATE rep_handoffs SET used_at=$3 WHERE tenant_id=$1 AND token_hash=$2 AND used_at IS NULL AND expires_at > $3
			RETURNING customer_id, member_id, member_name`, tenant, sum[:], s.now()).Scan(&customerID, &member, &rep)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrHandoff
		}
		if err != nil {
			return err
		}
		tok, tsum := newToken()
		if _, err := tx.Exec(ctx, `INSERT INTO shopper_sessions (tenant_id, token_hash, customer_id, expires_at, acting_member, acting_name) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, tsum, customerID, s.now().Add(RepSessionTTL), member, rep); err != nil {
			return err
		}
		session = tok
		return store.Audit(ctx, tx, tenant, "member:"+member, "customer.rep_session", customerID, map[string]any{"representative": rep})
	})
	return session, customerID, rep, err
}

// Acting is the representative behind a session ("" when the buyer themself).
type Acting struct {
	MemberID string `json:"memberId"`
	Name     string `json:"name"`
}

// SessionActing returns the customer of a session and, when a representative
// opened it, who they are.
func (s *Service) SessionActing(ctx context.Context, tenant, token string) (string, *Acting, error) {
	if !strings.HasPrefix(token, "apc_") {
		return "", nil, ErrNotFound
	}
	sum := sha256.Sum256([]byte(token))
	var id string
	var member, name *string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT s.customer_id, s.acting_member, s.acting_name FROM shopper_sessions s JOIN customers c ON c.tenant_id=s.tenant_id AND c.id=s.customer_id
			WHERE s.tenant_id=$1 AND s.token_hash=$2 AND s.expires_at > $3 AND c.status='active'`, tenant, sum[:], s.now()).Scan(&id, &member, &name)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil || member == nil {
		return id, nil, err
	}
	return id, &Acting{MemberID: *member, Name: *name}, nil
}
