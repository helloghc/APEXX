package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// Purchase approvals: a credit order above the company's threshold, placed by
// a buyer who is not an approver, waits. The approvers of the company get a
// link; approving places the order on the company's credit, exactly once.

// PurchaseRequest is a purchase waiting for (or decided by) an approver.
type PurchaseRequest struct {
	ID        string         `json:"id"`
	Company   string         `json:"company"`
	Status    string         `json:"status"`
	Total     int64          `json:"total"`
	Currency  string         `json:"currency"`
	Requester string         `json:"requester"`
	Lines     []PurchaseLine `json:"lines"`
	OrderID   string         `json:"orderId,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
	// Level is how far it has been approved; RequiredLevel how far it must go;
	// StepLevel the level of the people the current link went to.
	Level         int       `json:"level"`
	RequiredLevel int       `json:"requiredLevel"`
	StepLevel     int       `json:"stepLevel"`
	Reason        string    `json:"reason"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Location      string    `json:"-"`
	BuyerHash     []byte    `json:"-"`
}

// PurchaseLine is what the approver sees of each product.
type PurchaseLine struct {
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Total int64  `json:"total"`
}

// requestApproval stores the purchase and emails the approvers of the first
// step. With nobody to approve it, the purchase cannot wait: CreditError.
func (s *Service) requestApproval(ctx context.Context, tenant string, req CheckoutRequest, lines []cart.Line, total int64, currency string, level int, reason string) error {
	req.CompanyID = req.Company.ID
	id := newID("apr_")
	raw, _ := json.Marshal(req)
	enc, err := s.Keys.Encrypt(tenant, raw, id)
	if err != nil {
		return err
	}
	sum := make([]PurchaseLine, 0, len(lines))
	for _, l := range lines {
		sum = append(sum, PurchaseLine{Title: l.Title, Qty: l.Qty, Total: l.Total.Amount})
	}
	sj, _ := json.Marshal(sum)
	token, hash := subToken()
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// An approver asking for more than they may authorize starts above
		// their own level: they do not approve their own purchase.
		done := 0
		if req.Company.Approver {
			done = req.Company.ApproverLevel
		}
		to, step, err := s.approversForTx(ctx, tx, tenant, req.Company.ID, done+1)
		if err != nil {
			return err
		}
		if step == 0 {
			return &CreditError{"no_approver", reason + " y tu empresa no tiene quien apruebe. Pide a tu administrador que lo configure."}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_requests (tenant_id, id, company_id, total, currency, request, summary, token_hash, required_level, step_level, reason, expires_at, buyer_hash, location_id, level)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now() + make_interval(days => (SELECT approval_days FROM companies WHERE tenant_id=$1 AND id=$3)), $12, $13, $14)`,
			tenant, id, req.Company.ID, total, currency, enc, sj, hash, level, step, reason, req.Company.BuyerHash, req.Company.Location, done); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, "storefront", "purchase.approval_requested", id, map[string]any{"company": req.Company.ID, "total": total, "level": level}); err != nil {
			return err
		}
		return s.notifyApproversTx(ctx, tx, tenant, id, token, to, req, total, currency)
	})
}

func (s *Service) notifyApproversTx(ctx context.Context, tx pgx.Tx, tenant, id, token string, to []string, req CheckoutRequest, total int64, currency string) error {
	if s.Notify == nil {
		return nil
	}
	for i, email := range to {
		if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplPurchaseApproval, To: email, DedupeKey: fmt.Sprintf("approval-%s-%x-%d", id, tokenHash(token)[:4], i), Force: true,
			Data: notify.Data{ActionURL: strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/aprobacion/" + token,
				Vars: map[string]string{"cliente.nombre": "Hola", "empresa.nombre": req.Company.Name, "solicitud.total": notify.Money(total, currency), "solicitud.quien": req.Customer.Name}}}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) requestTx(ctx context.Context, tx pgx.Tx, tenant, token string, lock bool) (PurchaseRequest, CheckoutRequest, error) {
	var v PurchaseRequest
	var req CheckoutRequest
	var enc, sum []byte
	var order *string
	q := `SELECT r.id, c.name, r.status, r.total, r.currency, r.request, r.summary, r.order_id, r.created_at,
		r.level, r.required_level, r.step_level, r.reason, COALESCE(r.expires_at, r.created_at + interval '7 days'), r.location_id, r.buyer_hash FROM purchase_requests r
		JOIN companies c ON c.tenant_id=r.tenant_id AND c.id=r.company_id WHERE r.tenant_id=$1 AND r.token_hash=$2`
	if lock {
		q += ` FOR UPDATE OF r`
	}
	err := tx.QueryRow(ctx, q, tenant, tokenHash(token)).Scan(&v.ID, &v.Company, &v.Status, &v.Total, &v.Currency, &enc, &sum, &order, &v.CreatedAt,
		&v.Level, &v.RequiredLevel, &v.StepLevel, &v.Reason, &v.ExpiresAt, &v.Location, &v.BuyerHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, req, ErrNotFound
	}
	if err != nil {
		return v, req, err
	}
	_ = json.Unmarshal(sum, &v.Lines)
	if order != nil {
		v.OrderID = *order
	}
	plain, err := s.Keys.Decrypt(tenant, enc, v.ID)
	if err != nil {
		return v, req, err
	}
	if err := json.Unmarshal(plain, &req); err != nil {
		return v, req, err
	}
	v.Requester = req.Customer.Name
	return v, req, nil
}

// PurchaseRequestByToken is what the approver's page shows.
func (s *Service) PurchaseRequestByToken(ctx context.Context, tenant, token string) (PurchaseRequest, error) {
	var v PurchaseRequest
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		v, _, err = s.requestTx(ctx, tx, tenant, token, false)
		return err
	})
	return v, err
}

// DecidePurchase approves (placing the order on credit) or rejects a request.
// A problem placing the order (credit limit, stock) leaves it pending and is
// returned as detail, so the approver knows why.
func (s *Service) DecidePurchase(ctx context.Context, tenant, token string, approve bool) (PurchaseRequest, string, error) {
	var v PurchaseRequest
	var req CheckoutRequest
	advanced := false
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if v, req, err = s.requestTx(ctx, tx, tenant, token, true); err != nil {
			return err
		}
		if v.Status == "pending" && time.Now().After(v.ExpiresAt) {
			return fmt.Errorf("%w: esta solicitud venció; quien compra puede enviarla de nuevo", ErrInvalidState)
		}
		if v.Status != "pending" {
			return fmt.Errorf("%w: esta solicitud ya fue %s", ErrInvalidState, map[string]string{"approved": "aprobada", "rejected": "rechazada", "expired": "vencida", "cancelled": "cancelada por quien compra"}[v.Status])
		}
		step, _ := json.Marshal([]Step{{Level: v.StepLevel, Action: map[bool]string{true: "approved", false: "rejected"}[approve], At: time.Now().UTC()}})
		if !approve {
			v.Status = "rejected"
			if _, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='rejected', decided_at=now(), decisions=decisions || $3::jsonb WHERE tenant_id=$1 AND id=$2`, tenant, v.ID, step); err != nil {
				return err
			}
			return store.Audit(ctx, tx, tenant, "approver", "purchase.rejected", v.ID, map[string]any{"level": v.StepLevel})
		}
		if v.StepLevel >= v.RequiredLevel {
			return nil // last step: the order is placed below
		}
		// Another level must approve: this link stops working and the next
		// approvers get their own.
		to, next, err := s.approversForTx(ctx, tx, tenant, req.CompanyID, v.StepLevel+1)
		if err != nil {
			return err
		}
		if next == 0 {
			return nil // nobody above: the most senior approval places it
		}
		token, hash := subToken()
		if _, err := tx.Exec(ctx, `UPDATE purchase_requests SET level=$3, step_level=$4, token_hash=$5, decisions=decisions || $6::jsonb WHERE tenant_id=$1 AND id=$2`,
			tenant, v.ID, v.StepLevel, next, hash, step); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, "approver", "purchase.level_approved", v.ID, map[string]any{"level": v.StepLevel, "next": next}); err != nil {
			return err
		}
		v.Level, v.StepLevel, v.Status = v.StepLevel, next, "pending"
		company := req.Company
		if company == nil {
			company = &CompanyAccess{Name: v.Company}
		}
		r2 := req
		r2.Company = company
		advanced = true
		return s.notifyApproversTx(ctx, tx, tenant, v.ID, token, to, r2, v.Total, v.Currency)
	})
	if err != nil || !approve || advanced {
		return v, "", err
	}
	access, err := s.CompanyAccessByID(ctx, tenant, req.CompanyID) // Approver: true, so the order is placed
	if err != nil {
		var ce *CreditError
		if errors.As(err, &ce) {
			return v, ce.Message, nil
		}
		return v, "", err
	}
	// The order counts against the month of the buyer and branch who asked.
	access.BuyerHash, access.Location = v.BuyerHash, v.Location
	req.Company, req.PaymentMethod, req.AcceptedTerms = access, "credit", true
	res, err := s.Checkout(ctx, tenant, "approval:"+v.ID, "approval-"+v.ID, req) // the key makes a double click harmless
	if err != nil {
		return v, "", err
	}
	if res.Status != 201 {
		var p struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(res.Body, &p)
		// Free the key: the approver can try again once the cause is solved.
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id=$1 AND key=$2`, tenant, "approval-"+v.ID)
			return e
		})
		return v, p.Detail, nil
	}
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(res.Body, &o)
	v.Status, v.OrderID, v.Level = "approved", o.ID, max(v.StepLevel, v.RequiredLevel)
	step, _ := json.Marshal([]Step{{Level: v.StepLevel, Action: "approved", At: time.Now().UTC()}})
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='approved', order_id=$3, level=$4, decided_at=now(), decisions=decisions || $5::jsonb WHERE tenant_id=$1 AND id=$2 AND status='pending'`, tenant, v.ID, o.ID, v.Level, step); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "approver", "purchase.approved", v.ID, map[string]any{"order": o.ID})
	})
	return v, "", err
}

// HashToken is the stored form of a link token (tests and tools).
func HashToken(token string) []byte { return tokenHash(token) }
