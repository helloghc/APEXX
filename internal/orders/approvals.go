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
}

// PurchaseLine is what the approver sees of each product.
type PurchaseLine struct {
	Title string `json:"title"`
	Qty   int64  `json:"qty"`
	Total int64  `json:"total"`
}

func needsApproval(req CheckoutRequest, total int64) bool {
	c := req.Company
	return req.PaymentMethod == "credit" && c != nil && c.ApprovalThreshold > 0 && total > c.ApprovalThreshold && !c.Approver
}

// requestApproval stores the purchase and emails the approvers of the company.
func (s *Service) requestApproval(ctx context.Context, tenant string, req CheckoutRequest, lines []cart.Line, total int64, currency string) error {
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
		if _, err := tx.Exec(ctx, `INSERT INTO purchase_requests (tenant_id, id, company_id, total, currency, request, summary, token_hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenant, id, req.Company.ID, total, currency, enc, sj, hash); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, "storefront", "purchase.approval_requested", id, map[string]any{"company": req.Company.ID, "total": total}); err != nil {
			return err
		}
		if s.Notify == nil {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT email FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 AND approver`, tenant, req.Company.ID)
		if err != nil {
			return err
		}
		encs, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
		if err != nil {
			return err
		}
		for i, e := range encs {
			plain, err := s.Keys.Decrypt(tenant, e, "buyer:"+req.Company.ID)
			if err != nil {
				continue
			}
			if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplPurchaseApproval, To: string(plain), DedupeKey: fmt.Sprintf("approval-%s-%d", id, i), Force: true,
				Data: notify.Data{ActionURL: strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/aprobacion/" + token,
					Vars: map[string]string{"cliente.nombre": "Hola", "empresa.nombre": req.Company.Name, "solicitud.total": notify.Money(total, currency), "solicitud.quien": req.Customer.Name}}}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) requestTx(ctx context.Context, tx pgx.Tx, tenant, token string, lock bool) (PurchaseRequest, CheckoutRequest, error) {
	var v PurchaseRequest
	var req CheckoutRequest
	var enc, sum []byte
	var order *string
	q := `SELECT r.id, c.name, r.status, r.total, r.currency, r.request, r.summary, r.order_id, r.created_at FROM purchase_requests r
		JOIN companies c ON c.tenant_id=r.tenant_id AND c.id=r.company_id WHERE r.tenant_id=$1 AND r.token_hash=$2`
	if lock {
		q += ` FOR UPDATE OF r`
	}
	err := tx.QueryRow(ctx, q, tenant, tokenHash(token)).Scan(&v.ID, &v.Company, &v.Status, &v.Total, &v.Currency, &enc, &sum, &order, &v.CreatedAt)
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
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if v, req, err = s.requestTx(ctx, tx, tenant, token, true); err != nil {
			return err
		}
		if v.Status != "pending" {
			return fmt.Errorf("%w: esta solicitud ya fue %s", ErrInvalidState, map[string]string{"approved": "aprobada", "rejected": "rechazada"}[v.Status])
		}
		if !approve {
			v.Status = "rejected"
			if _, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='rejected', decided_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID); err != nil {
				return err
			}
			return store.Audit(ctx, tx, tenant, "approver", "purchase.rejected", v.ID, map[string]any{})
		}
		return nil
	})
	if err != nil || !approve {
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
	v.Status, v.OrderID = "approved", o.ID
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE purchase_requests SET status='approved', order_id=$3, decided_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, v.ID, o.ID); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "approver", "purchase.approved", v.ID, map[string]any{"order": o.ID})
	})
	return v, "", err
}

// HashToken is the stored form of a link token (tests and tools).
func HashToken(token string) []byte { return tokenHash(token) }
