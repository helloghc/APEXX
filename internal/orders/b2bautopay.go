package orders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// Automatic payment of a company's credit: its administrator saves a card,
// a reminder goes out before each due date, and on the date the store
// charges what is overdue. Three declines in a row turn it off and tell them.

const autopayMaxFailures = 3

// Autopay is the automatic payment setting of a company, as shown to people.
type Autopay struct {
	On           bool       `json:"on"`
	SavedAt      *time.Time `json:"savedAt,omitempty"`
	LastTry      *time.Time `json:"lastTry,omitempty"`
	Failures     int        `json:"failures"`
	Error        string     `json:"error,omitempty"`
	ReminderDays int        `json:"reminderDays"`
}

// CompanyAutopay reads the setting.
func (s *Service) CompanyAutopay(ctx context.Context, tenant, company string) (Autopay, error) {
	var a Autopay
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT autopay, autopay_saved_at, autopay_last_try, autopay_failures, autopay_error, reminder_days FROM companies WHERE tenant_id=$1 AND id=$2`,
			tenant, company).Scan(&a.On, &a.SavedAt, &a.LastTry, &a.Failures, &a.Error, &a.ReminderDays)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return a, err
}

// SaveAutopay keeps the card (a provider token) and turns automatic payment on.
func (s *Service) SaveAutopay(ctx context.Context, tenant, actor, company, paymentMethod, email string) error {
	if !strings.HasPrefix(paymentMethod, "pm_") {
		return FieldError{"paymentMethod", "Captura la tarjeta"}
	}
	c, _, _, err := s.Company(ctx, tenant, company, false)
	if err != nil {
		return err
	}
	if c.Terms == "prepaid" {
		return FieldError{"autopay", "Una empresa de contado no tiene saldo que cobrar"}
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return err
	}
	saver, ok := acct.Provider.(payments.CardSaver)
	if !ok {
		return FieldError{"autopay", "La forma de cobro de la tienda no guarda tarjetas"}
	}
	cus, method, err := saver.SaveCard(ctx, acct.Account, paymentMethod, email, "autopay-"+company+"-"+newID(""))
	if errors.Is(err, payments.ErrDeclined) {
		return FieldError{"paymentMethod", "El banco rechazó la tarjeta. Prueba con otra."}
	}
	if err != nil {
		return err
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE companies SET autopay=true, autopay_customer=$3, autopay_method=$4, autopay_saved_at=now(), autopay_failures=0, autopay_error='', updated_at=now()
			WHERE tenant_id=$1 AND id=$2`, tenant, company, cus, method); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "company.autopay_on", company, map[string]any{})
	})
}

// StopAutopay forgets the card and turns automatic payment off.
func (s *Service) StopAutopay(ctx context.Context, tenant, actor, company string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE companies SET autopay=false, autopay_customer='', autopay_method='', autopay_failures=0, autopay_error='', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, company)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "company.autopay_off", company, map[string]any{})
	})
}

// SetReminderDays sets how many days before a due date the reminder goes out (0 = none).
func (s *Service) SetReminderDays(ctx context.Context, tenant, actor, company string, days int) error {
	if days < 0 || days > 30 {
		return FieldError{"reminderDays", "El aviso sale entre 0 y 30 días antes"}
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE companies SET reminder_days=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, company, days)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "company.reminder_days", company, map[string]any{"days": days})
	})
}

// companyContactsTx are who hear about the company's account: its
// administrators, else its approvers, else every buyer.
func (s *Service) companyContactsTx(ctx context.Context, tx pgx.Tx, tenant, company string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT email, admin, approver FROM company_buyers WHERE tenant_id=$1 AND company_id=$2 ORDER BY admin DESC, approver DESC`, tenant, company)
	if err != nil {
		return nil, err
	}
	type row struct {
		enc             []byte
		admin, approver bool
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.enc, &x.admin, &x.approver)
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, x := range list {
		if x.admin != list[0].admin || x.approver != list[0].approver {
			break // only the first group: admins, else approvers, else buyers
		}
		if p, err := s.Keys.Decrypt(tenant, x.enc, "buyer:"+company); err == nil {
			out = append(out, string(p))
		}
	}
	return out, nil
}

// RemindCompanyDues emails the companies whose credit charges fall due within
// their reminder window. One email per charge, ever (dedupe key).
func (s *Service) RemindCompanyDues(ctx context.Context) (int, error) {
	if s.Notify == nil {
		return 0, nil
	}
	rows, err := s.DB.System.Query(ctx, `SELECT e.tenant_id, e.company_id, e.id, e.due_at FROM company_entries e JOIN companies c ON c.tenant_id=e.tenant_id AND c.id=e.company_id
		WHERE e.kind='charge' AND c.reminder_days > 0 AND c.status='active' AND e.due_at > now() AND e.due_at <= now() + make_interval(days => c.reminder_days)
		AND NOT EXISTS (SELECT 1 FROM notification_messages m WHERE m.tenant_id=e.tenant_id AND m.dedupe_key='due-'||e.id) LIMIT 500`)
	if err != nil {
		return 0, err
	}
	type due struct {
		tenant, company, entry string
		at                     time.Time
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (due, error) {
		var d due
		return d, r.Scan(&d.tenant, &d.company, &d.entry, &d.at)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range list {
		c, _, _, err := s.Company(ctx, d.tenant, d.company, false)
		if err != nil || c.Balance <= 0 {
			continue // nothing owed: no reminder
		}
		a, _ := s.CompanyAutopay(ctx, d.tenant, d.company)
		how := "Puedes pagarlo por transferencia; avísanos cuando lo hagas."
		if a.On {
			how = "Lo cobraremos automáticamente a la tarjeta guardada de tu empresa ese día."
		}
		err = s.DB.Tx(ctx, d.tenant, func(tx pgx.Tx) error {
			to, err := s.companyContactsTx(ctx, tx, d.tenant, d.company)
			if err != nil {
				return err
			}
			for i, email := range to {
				key := "due-" + d.entry
				if i > 0 {
					key = fmt.Sprintf("due-%s-%d", d.entry, i)
				}
				if _, err := s.Notify.EnqueueTx(ctx, tx, d.tenant, notify.Message{Template: notify.TplCompanyDue, To: email, DedupeKey: key, Force: true,
					Data: notify.Data{ActionURL: strings.TrimRight(s.PublicURL, "/") + "/s/" + d.tenant + "/main/cuenta?ver=empresa",
						Vars: map[string]string{"cliente.nombre": "Hola", "empresa.nombre": c.Name, "saldo.monto": notify.Money(c.Balance, "MXN"),
							"saldo.vence": d.at.Format("2006-01-02"), "saldo.cobro": how}}}); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			n++
		}
	}
	return n, nil
}

// RunAutopay charges the saved card of every company with something overdue,
// once a day at most. The idempotency key is the company, day and amount: a
// retry after a crash never charges twice.
func (s *Service) RunAutopay(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id FROM companies WHERE autopay AND status='active'
		AND (autopay_last_try IS NULL OR autopay_last_try < now() - interval '20 hours') LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type co struct{ tenant, id string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (co, error) {
		var c co
		return c, r.Scan(&c.tenant, &c.id)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range list {
		ok, err := s.autopayOne(ctx, x.tenant, x.id)
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

func (s *Service) autopayOne(ctx context.Context, tenant, id string) (bool, error) {
	c, _, _, err := s.Company(ctx, tenant, id, false)
	if err != nil || c.Overdue <= 0 {
		return false, err
	}
	var cus, method string
	var paid, failures int
	if err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE companies SET autopay_last_try=now() WHERE tenant_id=$1 AND id=$2 AND autopay RETURNING autopay_customer, autopay_method, autopay_failures`, tenant, id).Scan(&cus, &method, &failures); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM company_entries WHERE tenant_id=$1 AND company_id=$2 AND kind IN ('payment','adjust','refund')`, tenant, id).Scan(&paid)
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	acct, err := s.Accounts(ctx, tenant)
	if err != nil {
		return false, err
	}
	amount := c.Overdue
	// The key is the company, the amount, how many movements already paid it
	// down and how many tries were declined: the same try after a crash gets
	// the same key (charged once); a new debt, even of the same amount, or a
	// new try after a decline (the provider remembers declines per key) gets
	// a new one.
	ref := fmt.Sprintf("autopay-%s-%d-%d-%d", id, paid, failures, amount)
	ch, err := acct.Provider.Charge(ctx, payments.ChargeRequest{Account: acct.Account, OrderID: "company-" + id, Amount: amount, Currency: "MXN",
		PaymentMethod: method, Customer: cus, OffSession: true, IdempotencyKey: ref, FeeAmount: amount * acct.FeeBps / 10000})
	if err != nil {
		return false, nil // outage: the next round tries again (the key keeps it single)
	}
	if ch.Status != "captured" {
		return false, s.autopayFailed(ctx, tenant, id, c.Name, amount, ch.Reason)
	}
	return true, s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM company_entries WHERE tenant_id=$1 AND id=$2)`, tenant, "cen_"+ref).Scan(&exists); err != nil || exists {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO company_entries (tenant_id, id, company_id, kind, amount, note, actor) VALUES ($1,$2,$3,'payment',$4,$5,'autopay')`,
			tenant, "cen_"+ref, id, -amount, "Cobro automático con tarjeta · "+ch.Ref); err != nil {
			return err
		}
		for a, amt := range map[string]int64{"accounts_receivable": -amount, clearing(acct.Provider.Name()): amount} {
			if _, err := tx.Exec(ctx, `INSERT INTO ledger_entries (tenant_id, tx_id, account, amount, currency) VALUES ($1,$2,$3,$4,'MXN')`, tenant, "company-cen_"+ref, a, amt); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE companies SET autopay_failures=0, autopay_error='' WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "autopay", "company.autopay_charged", id, map[string]any{"amount": amount, "ref": ch.Ref})
	})
}

func (s *Service) autopayFailed(ctx context.Context, tenant, id, name string, amount int64, reason string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var failures int
		if err := tx.QueryRow(ctx, `UPDATE companies SET autopay_failures=autopay_failures+1, autopay_error=$3,
				autopay = autopay_failures+1 < $4 WHERE tenant_id=$1 AND id=$2 RETURNING autopay_failures`, tenant, id, "El banco rechazó el cobro ("+reason+")", autopayMaxFailures).Scan(&failures); err != nil {
			return err
		}
		if err := store.Audit(ctx, tx, tenant, "autopay", "company.autopay_failed", id, map[string]any{"amount": amount, "failures": failures}); err != nil {
			return err
		}
		if s.Notify == nil {
			return nil
		}
		next := "Lo intentaremos de nuevo mañana. Si cambió tu tarjeta, guárdala otra vez en tu cuenta."
		if failures >= autopayMaxFailures {
			next = "Después de tres intentos apagamos el cobro automático. Guarda otra tarjeta o paga por transferencia."
		}
		to, err := s.companyContactsTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		for i, email := range to {
			if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplCompanyAutopayFail, To: email, DedupeKey: fmt.Sprintf("autopay-fail-%s-%d-%d", id, failures, i), Force: true,
				Data: notify.Data{ActionURL: strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/cuenta?ver=empresa",
					Vars: map[string]string{"cliente.nombre": "Hola", "empresa.nombre": name, "saldo.monto": notify.Money(amount, "MXN"), "saldo.cobro": next}}}); err != nil {
				return err
			}
		}
		return nil
	})
}
