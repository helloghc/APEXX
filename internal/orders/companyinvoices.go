package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// Company invoices: every order on credit is an invoice with a due date.
// What is still owed per invoice comes from the company's account: movements
// tied to the order (releases, refunds, payments of that invoice) first, then
// payments not tied to an invoice pay the oldest due first. A company pays
// one or several invoices online by card; each invoice gets its payment and
// the company gets a receipt.

// CompanyInvoice is an order on credit as an invoice.
type CompanyInvoice struct {
	OrderID string    `json:"orderId"`
	Number  int64     `json:"number"`
	Amount  int64     `json:"amount"` // charged, after releases and refunds
	Paid    int64     `json:"paid"`
	Open    int64     `json:"open"`
	DueAt   time.Time `json:"dueAt"`
	Overdue bool      `json:"overdue"`
	At      time.Time `json:"at"`
}

// Receipt is an online payment of invoices.
type Receipt struct {
	ID       string           `json:"id"`
	Amount   int64            `json:"amount"`
	Currency string           `json:"currency"`
	Ref      string           `json:"ref"`
	Invoices []ReceiptInvoice `json:"invoices"`
	PaidBy   string           `json:"paidBy"`
	At       time.Time        `json:"at"`
}

// ReceiptInvoice is one invoice paid by a receipt.
type ReceiptInvoice struct {
	OrderID string `json:"orderId"`
	Number  int64  `json:"number"`
	Amount  int64  `json:"amount"`
}

// ErrNoOpenInvoices is a payment of invoices that are already paid.
var ErrNoOpenInvoices = errors.New("nothing to pay")

func invoicesTx(ctx context.Context, tx pgx.Tx, tenant, company string) ([]CompanyInvoice, error) {
	rows, err := tx.Query(ctx, `SELECT e.order_id, COALESCE(o.number,0),
			sum(e.amount) FILTER (WHERE e.kind IN ('charge','release','refund')),
			-COALESCE(sum(e.amount) FILTER (WHERE e.kind IN ('payment','adjust')),0),
			min(e.due_at) FILTER (WHERE e.kind='charge'), min(e.created_at)
		FROM company_entries e LEFT JOIN orders o ON o.tenant_id=e.tenant_id AND o.id=e.order_id
		WHERE e.tenant_id=$1 AND e.company_id=$2 AND e.order_id IS NOT NULL
		GROUP BY e.order_id, o.number HAVING bool_or(e.kind='charge')`, tenant, company)
	if err != nil {
		return nil, err
	}
	var list []CompanyInvoice
	for rows.Next() {
		var x CompanyInvoice
		var amount *int64
		var due *time.Time
		if err := rows.Scan(&x.OrderID, &x.Number, &amount, &x.Paid, &due, &x.At); err != nil {
			rows.Close()
			return nil, err
		}
		if amount != nil {
			x.Amount = *amount
		}
		if due != nil {
			x.DueAt = *due
		}
		list = append(list, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Money received without an invoice pays the oldest due first.
	var loose int64
	if err := tx.QueryRow(ctx, `SELECT -COALESCE(sum(amount),0) FROM company_entries WHERE tenant_id=$1 AND company_id=$2 AND order_id IS NULL AND amount < 0`, tenant, company).Scan(&loose); err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].DueAt.Before(list[j].DueAt) })
	now := time.Now()
	for i := range list {
		x := &list[i]
		x.Open = max(x.Amount-x.Paid, 0)
		if loose > 0 && x.Open > 0 {
			take := min(loose, x.Open)
			x.Open -= take
			x.Paid += take
			loose -= take
		}
		x.Overdue = x.Open > 0 && x.DueAt.Before(now)
	}
	return list, nil
}

// CompanyInvoices lists the invoices of a company, oldest due first.
func (s *Service) CompanyInvoices(ctx context.Context, tenant, company string) ([]CompanyInvoice, error) {
	var out []CompanyInvoice
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = invoicesTx(ctx, tx, tenant, company)
		return err
	})
	if out == nil {
		out = []CompanyInvoice{}
	}
	return out, err
}

// PayCompanyInvoices charges the card for the open part of the chosen
// invoices and records the payment. key makes a retry charge once.
func (s *Service) PayCompanyInvoices(ctx context.Context, tenant, actor, company, paidBy, key, paymentMethod string, orderIDs []string) (Receipt, error) {
	var r Receipt
	if len(key) < 8 || len(key) > 200 {
		return r, FieldError{"Idempotency-Key", "Falta la llave de idempotencia"}
	}
	sum := sha256.Sum256([]byte(company + "|" + key))
	r.ID = "rcp_" + hex.EncodeToString(sum[:10])
	// A retry with the same key returns the same receipt.
	if prev, err := s.ReceiptByID(ctx, tenant, company, r.ID); err == nil {
		return prev, nil
	}
	want := map[string]bool{}
	for _, id := range orderIDs {
		want[id] = true
	}
	var pay []ReceiptInvoice
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		list, err := invoicesTx(ctx, tx, tenant, company)
		if err != nil {
			return err
		}
		for _, x := range list {
			if want[x.OrderID] && x.Open > 0 {
				pay = append(pay, ReceiptInvoice{OrderID: x.OrderID, Number: x.Number, Amount: x.Open})
				r.Amount += x.Open
			}
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	if r.Amount <= 0 {
		return r, ErrNoOpenInvoices
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return r, err
	}
	ch, err := acct.Provider.Charge(ctx, payments.ChargeRequest{Account: acct.Account, OrderID: "invoices-" + company, Amount: r.Amount, Currency: "MXN",
		PaymentMethod: paymentMethod, IdempotencyKey: r.ID, FeeAmount: r.Amount * acct.FeeBps / 10000})
	if err != nil {
		return r, err
	}
	if ch.Status != "captured" {
		return r, &CreditError{"payment_declined", "El pago fue rechazado: " + ch.Reason}
	}
	r.Currency, r.Ref, r.Invoices, r.PaidBy, r.At = "MXN", ch.Ref, pay, paidBy, time.Now().UTC()
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `INSERT INTO company_receipts (tenant_id, id, company_id, amount, currency, ref, invoices, paid_by, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`,
			tenant, r.ID, company, r.Amount, r.Currency, r.Ref, r.Invoices, paidBy, r.At)
		if err != nil || ct.RowsAffected() == 0 {
			return err // a concurrent retry recorded it
		}
		for i, p := range pay {
			if _, err := tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, order_id, note, actor) VALUES ($1,$2,$3,'payment',$4,$5,$6,$7)`,
				tenant, fmt.Sprintf("cen_%s_%d", r.ID, i), company, -p.Amount, p.OrderID, "Pago en línea · recibo "+r.ID, actor); err != nil {
				return err
			}
		}
		for a, amt := range map[string]int64{"accounts_receivable": -r.Amount, clearing(acct.Provider.Name()): r.Amount} {
			if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency) VALUES ($1,$2,$3,$4,'MXN')`, tenant, "company-"+r.ID, a, amt); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "company.invoices_paid", company, map[string]any{"receipt": r.ID, "amount": r.Amount, "invoices": len(pay), "ref": ch.Ref})
	})
	return r, err
}

// ReceiptByID returns a receipt of a company.
func (s *Service) ReceiptByID(ctx context.Context, tenant, company, id string) (Receipt, error) {
	var r Receipt
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, amount, currency, ref, invoices, paid_by, created_at FROM company_receipts WHERE tenant_id=$1 AND company_id=$2 AND id=$3`, tenant, company, id).
			Scan(&r.ID, &r.Amount, &r.Currency, &r.Ref, &r.Invoices, &r.PaidBy, &r.At)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return r, err
}

// Receipts lists the online payments of a company.
func (s *Service) Receipts(ctx context.Context, tenant, company string) ([]Receipt, error) {
	var out []Receipt
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, amount, currency, ref, invoices, paid_by, created_at FROM company_receipts WHERE tenant_id=$1 AND company_id=$2 ORDER BY created_at DESC LIMIT 100`, tenant, company)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (Receipt, error) {
			var r Receipt
			return r, row.Scan(&r.ID, &r.Amount, &r.Currency, &r.Ref, &r.Invoices, &r.PaidBy, &r.At)
		})
		return err
	})
	if out == nil {
		out = []Receipt{}
	}
	return out, err
}
