package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/payments"
	"github.com/helloghc/apexx/internal/store"
)

// DraftInput creates or replaces a draft order.
type DraftInput struct {
	Channel    string      `json:"channel"`
	Items      []ItemInput `json:"items"`
	Customer   Customer    `json:"customer"`
	PostalCode string      `json:"postalCode"`
	Shipping   int64       `json:"shipping"`
	Discount   int64       `json:"discount"`
	Note       string      `json:"note"`
	// WalkIn is a counter sale (set by the server, never by clients).
	WalkIn bool `json:"-"`
}

// Draft is an order prepared by staff that is not paid yet.
type Draft struct {
	ID         string         `json:"id"`
	Number     int64          `json:"number"`
	Status     string         `json:"status"` // open | sent | completed | cancelled
	Channel    string         `json:"channel"`
	Currency   string         `json:"currency"`
	Items      []ExchangeItem `json:"items"`
	Customer   *Customer      `json:"customer,omitempty"`
	PostalCode string         `json:"postalCode"`
	ItemsTotal int64          `json:"itemsTotal"`
	Discount   int64          `json:"discount"`
	Shipping   int64          `json:"shipping"`
	GrandTotal int64          `json:"grandTotal"`
	Note       string         `json:"note"`
	OrderID    string         `json:"orderId,omitempty"`
	CreatedBy  string         `json:"createdBy"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	SentAt     *time.Time     `json:"sentAt,omitempty"`
	PayURL     string         `json:"payUrl,omitempty"` // only right after sending the link
}

const draftCols = `id, number, status, channel, currency, items, customer, postal_code, items_total, discount, shipping, grand_total, note, COALESCE(order_id,''), created_by, created_at, updated_at, sent_at`

func (s *Service) scanDraft(tenant string, r pgx.Row, pii bool) (Draft, error) {
	var d Draft
	var items, enc []byte
	if err := r.Scan(&d.ID, &d.Number, &d.Status, &d.Channel, &d.Currency, &items, &enc, &d.PostalCode, &d.ItemsTotal, &d.Discount, &d.Shipping, &d.GrandTotal,
		&d.Note, &d.OrderID, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.SentAt); err != nil {
		return d, err
	}
	_ = json.Unmarshal(items, &d.Items)
	if pii {
		if plain, err := s.Keys.Decrypt(tenant, enc, d.ID); err == nil {
			var c Customer
			_ = json.Unmarshal(plain, &c)
			d.Customer = &c
		}
	}
	return d, nil
}

func (s *Service) draftTx(ctx context.Context, tx pgx.Tx, tenant, id string, lock bool) (Draft, error) {
	q := `SELECT ` + draftCols + ` FROM draft_orders WHERE tenant_id=$1 AND id=$2`
	if lock {
		q += ` FOR UPDATE`
	}
	d, err := s.scanDraft(tenant, tx.QueryRow(ctx, q, tenant, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// GetDraft loads a draft order.
func (s *Service) GetDraft(ctx context.Context, tenant, id string, pii bool) (Draft, error) {
	var d Draft
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		d, err = s.draftTx(ctx, tx, tenant, id, false)
		return err
	})
	if !pii {
		d.Customer = nil
	}
	return d, err
}

// Drafts lists draft orders, newest first.
func (s *Service) Drafts(ctx context.Context, tenant, status string, before int64, limit int, pii bool) ([]Draft, int64, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	out := []Draft{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+draftCols+` FROM draft_orders WHERE tenant_id=$1 AND ($2 = '' OR status = $2) AND ($3 = 0 OR number < $3) ORDER BY number DESC LIMIT $4`,
			tenant, status, before, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := s.scanDraft(tenant, rows, pii)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	next := int64(0)
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].Number
	}
	return out, next, err
}

func (s *Service) buildDraft(tenant string, in DraftInput) (Draft, []byte, error) {
	var d Draft
	t, ok := s.Projector.Tenant(tenant)
	if !ok {
		return d, nil, fmt.Errorf("orders: tenant %s not loaded", tenant)
	}
	if in.Channel == "" {
		in.Channel = "mx"
	}
	ch, ok := t.Channel(in.Channel)
	if !ok {
		return d, nil, FieldError{"channel", "Canal de venta desconocido"}
	}
	if len(in.Items) == 0 {
		return d, nil, FieldError{"items", "Agrega al menos un producto"}
	}
	items, err := s.priceItems(tenant, in.Channel, "items", in.Items)
	if err != nil {
		return d, nil, err
	}
	email, ok := customers.NormalizeEmail(in.Customer.Email)
	switch {
	case ok:
		in.Customer.Email = email
	case in.WalkIn && strings.TrimSpace(in.Customer.Email) == "":
		in.Customer.Email = "" // a counter sale may have no email: no customer record, no emails
	default:
		return d, nil, FieldError{"customer.email", "Escribe el correo del cliente"}
	}
	in.Customer.Name, in.Customer.Phone, in.Customer.Address = strings.TrimSpace(in.Customer.Name), strings.TrimSpace(in.Customer.Phone), strings.TrimSpace(in.Customer.Address)
	if in.Customer.Name == "" || len(in.Customer.Name) > 200 {
		return d, nil, FieldError{"customer.name", "Escribe el nombre del cliente"}
	}
	if len(in.Customer.Address) > 500 || len(in.Customer.Phone) > 30 || len(in.Note) > 500 || len(in.PostalCode) > 10 {
		return d, nil, FieldError{"customer.address", "Texto demasiado largo"}
	}
	d = Draft{Channel: in.Channel, Currency: string(ch.Currency), Items: items, PostalCode: strings.TrimSpace(in.PostalCode), Discount: in.Discount, Shipping: in.Shipping, Note: strings.TrimSpace(in.Note)}
	for _, it := range items {
		d.ItemsTotal += it.Total
	}
	if in.Discount < 0 || in.Discount > d.ItemsTotal {
		return d, nil, FieldError{"discount", "El descuento no puede pasar del total de productos"}
	}
	if in.Shipping < 0 || in.Shipping > 1e10 {
		return d, nil, FieldError{"shipping", "Costo de envío inválido"}
	}
	d.GrandTotal = d.ItemsTotal - d.Discount + d.Shipping
	c := in.Customer
	d.Customer = &c
	raw, _ := json.Marshal(c)
	return d, raw, nil
}

// SaveDraft creates (id empty) or replaces an open draft order.
func (s *Service) SaveDraft(ctx context.Context, tenant, actor, id string, in DraftInput) (Draft, error) {
	d, cust, err := s.buildDraft(tenant, in)
	if err != nil {
		return d, err
	}
	items, _ := json.Marshal(d.Items)
	create := id == ""
	if create {
		id = newID("dft_")
	}
	enc, err := s.Keys.Encrypt(tenant, cust, id)
	if err != nil {
		return d, err
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if create {
			if _, err := tx.Exec(ctx, `INSERT INTO draft_orders (tenant_id, id, number, status, channel, currency, items, customer, postal_code, items_total, discount, shipping, grand_total, note, created_by)
				VALUES ($1,$2,nextval('draft_numbers'),'open',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
				tenant, id, d.Channel, d.Currency, items, enc, d.PostalCode, d.ItemsTotal, d.Discount, d.Shipping, d.GrandTotal, d.Note, actor); err != nil {
				return err
			}
		} else {
			cur, err := s.draftTx(ctx, tx, tenant, id, true)
			if err != nil {
				return err
			}
			if cur.Status != "open" && cur.Status != "sent" {
				return fmt.Errorf("%w: el borrador ya está %s", ErrInvalidState, map[string]string{"completed": "pagado", "cancelled": "cancelado"}[cur.Status])
			}
			// A changed draft invalidates links already sent: the amount may differ.
			if _, err := tx.Exec(ctx, `DELETE FROM payment_links WHERE tenant_id=$1 AND kind='draft' AND ref_id=$2 AND used_at IS NULL`, tenant, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE draft_orders SET status='open', channel=$3, currency=$4, items=$5, customer=$6, postal_code=$7, items_total=$8, discount=$9, shipping=$10, grand_total=$11, note=$12, updated_at=now()
				WHERE tenant_id=$1 AND id=$2`, tenant, id, d.Channel, d.Currency, items, enc, d.PostalCode, d.ItemsTotal, d.Discount, d.Shipping, d.GrandTotal, d.Note); err != nil {
				return err
			}
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": "draft.saved", "total": d.GrandTotal}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "draft.saved", id, map[string]any{"total": d.GrandTotal})
	})
	if err != nil {
		return d, err
	}
	return s.GetDraft(ctx, tenant, id, true)
}

// CancelDraft discards a draft that was not paid.
func (s *Service) CancelDraft(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		d, err := s.draftTx(ctx, tx, tenant, id, true)
		if err != nil {
			return err
		}
		if d.Status != "open" && d.Status != "sent" {
			return fmt.Errorf("%w: el borrador ya no está abierto", ErrInvalidState)
		}
		if _, err := tx.Exec(ctx, `UPDATE draft_orders SET status='cancelled', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM payment_links WHERE tenant_id=$1 AND kind='draft' AND ref_id=$2 AND used_at IS NULL`, tenant, id); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": "draft.cancelled"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "draft.cancelled", id, map[string]any{})
	})
}

// SendDraftLink emails the customer a link to a hosted page where the draft
// is paid with the store's payment provider. The link lasts 7 days; sending
// again replaces it.
func (s *Service) SendDraftLink(ctx context.Context, tenant, actor, id string) (Draft, error) {
	var d Draft
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, tenant, id, true); err != nil {
			return err
		}
		if d.Status != "open" && d.Status != "sent" {
			return fmt.Errorf("%w: el borrador ya no está abierto", ErrInvalidState)
		}
		if d.GrandTotal <= 0 {
			return fmt.Errorf("%w: un borrador de $0 no necesita enlace; márcalo como pagado", ErrInvalidState)
		}
		token, err := s.linkTx(ctx, tx, tenant, "draft", id)
		if err != nil {
			return err
		}
		d.PayURL = s.LinkURL(tenant, token)
		if s.Notify != nil && d.Customer != nil {
			data := notify.Data{Vars: map[string]string{"cliente.nombre": firstWord(d.Customer.Name), "pago.monto": notify.Money(d.GrandTotal, d.Currency)}, ActionURL: d.PayURL}
			for _, it := range d.Items {
				data.Lines = append(data.Lines, notify.Line{Title: it.Title, Qty: it.Qty, Total: notify.Money(it.Total, d.Currency)})
			}
			if d.Discount > 0 {
				data.Totals = append(data.Totals, notify.Row{Label: "Descuento", Value: "-" + notify.Money(d.Discount, d.Currency)})
			}
			data.Totals = append(data.Totals, notify.Row{Label: "Envío", Value: notify.Money(d.Shipping, d.Currency)}, notify.Row{Label: "Total", Value: notify.Money(d.GrandTotal, d.Currency), Bold: true})
			if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplPaymentLink, To: d.Customer.Email, Name: d.Customer.Name, Data: data}); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `UPDATE draft_orders SET status='sent', sent_at=now(), updated_at=now() WHERE tenant_id=$1 AND id=$2 RETURNING sent_at`, tenant, id).Scan(&d.SentAt); err != nil {
			return err
		}
		d.Status = "sent"
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": "draft.link_sent"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "draft.link_sent", id, map[string]any{})
	})
	return d, err
}

// manual is the "provider" of payments received outside the platform.
type manual struct{}

func (manual) Name() string { return "manual" }
func (manual) Charge(_ context.Context, r payments.ChargeRequest) (payments.Charge, error) {
	return payments.Charge{Ref: "manual_" + r.OrderID, Status: "captured"}, nil
}
func (manual) Refund(context.Context, string, string, int64, string) error { return nil }
func (manual) Lookup(_ context.Context, _, orderID string) (payments.Charge, bool, error) {
	return payments.Charge{Ref: "manual_" + orderID, Status: "captured"}, true, nil
}
func (manual) Invoice(context.Context, payments.InvoiceRequest) (payments.Invoice, error) {
	return payments.Invoice{}, errors.New("manual payments are invoiced by the store's provider")
}

// CompleteDraft turns a draft into a paid order. paymentMethod "manual" records
// a payment received outside the platform; anything else is a pm_… token that
// is charged through the store's provider (payment link page).
func (s *Service) CompleteDraft(ctx context.Context, tenant, actor, id, paymentMethod string) (Order, error) {
	return s.completeDraft(ctx, tenant, actor, id, paymentMethod, nil)
}

// CompleteDraftOnCredit places the draft as an order charged to a company's
// credit (an accepted quote of a company with payment terms).
func (s *Service) CompleteDraftOnCredit(ctx context.Context, tenant, actor, id string, company *CompanyAccess) (Order, error) {
	return s.completeDraft(ctx, tenant, actor, id, "credit", company)
}

func (s *Service) completeDraft(ctx context.Context, tenant, actor, id, paymentMethod string, company *CompanyAccess) (Order, error) {
	var d Draft
	var orderID string
	isManual := paymentMethod == "manual" || paymentMethod == "credit"
	acct := Account{Provider: manual{}}
	if !isManual {
		var err error
		if acct, err = s.Accounts(ctx, tenant); err != nil {
			return Order{}, ErrNoPaymentAccount
		}
	}
	t, ok := s.Projector.Tenant(tenant)
	if !ok {
		return Order{}, fmt.Errorf("orders: tenant %s not loaded", tenant)
	}
	// 1. Create the order and reserve stock (or reuse the order of an interrupted attempt).
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if d, err = s.draftTx(ctx, tx, tenant, id, true); err != nil {
			return err
		}
		if d.Status == "completed" {
			orderID = d.OrderID
			return nil
		}
		if d.Status != "open" && d.Status != "sent" {
			return fmt.Errorf("%w: el borrador está cancelado", ErrInvalidState)
		}
		if d.OrderID != "" {
			var st string
			if err := tx.QueryRow(ctx, `SELECT status FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, d.OrderID).Scan(&st); err == nil && st == StatusPendingPayment {
				orderID = d.OrderID
				return nil
			}
		}
		orderID = newID("ord_")
		raw, _ := json.Marshal(d.Customer)
		enc, err := s.Keys.Encrypt(tenant, raw, orderID)
		if err != nil {
			return err
		}
		var tax int64
		if ch, ok := t.Channel(d.Channel); ok && ch.TaxBps > 0 {
			tax = d.GrandTotal * ch.TaxBps / (10000 + ch.TaxBps)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO orders (tenant_id, id, number, channel, status, currency, items_total, discounts, shipping, tax_included, grand_total, customer, postal_code, source)
			VALUES ($1,$2,nextval('order_numbers'),$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'draft')`,
			tenant, orderID, d.Channel, StatusPendingPayment, d.Currency, d.ItemsTotal, d.Discount, d.Shipping, tax, d.GrandTotal, enc, d.PostalCode); err != nil {
			return err
		}
		if d.Customer != nil {
			if err := s.linkCustomerTx(ctx, tx, tenant, orderID, *d.Customer, d.PostalCode); err != nil {
				return err
			}
		}
		for i, it := range d.Items {
			if _, err := tx.Exec(ctx, `INSERT INTO order_lines (tenant_id, order_id, line, sku, title, qty, unit, total, origin) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'draft')`,
				tenant, orderID, i+1, it.SKU, it.Title, it.Qty, it.Unit, it.Total); err != nil {
				return err
			}
			if err := store.ReserveTx(ctx, tx, tenant, orderID, it.SKU, it.Qty, s.ttl()); err != nil {
				return err
			}
		}
		if company != nil {
			if err := creditChargeTx(ctx, tx, tenant, actor, orderID, company, d.GrandTotal); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE draft_orders SET order_id=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, orderID); err != nil {
			return err
		}
		return s.event(ctx, tx, tenant, orderID, actor, "order.created", nil, StatusPendingPayment, map[string]any{"draft": id, "draftNumber": d.Number})
	})
	if errors.Is(err, store.ErrInsufficient) {
		return Order{}, FieldError{"items", "No hay existencias suficientes para surtir este borrador"}
	}
	if err != nil {
		return Order{}, err
	}
	if d.Status == "completed" {
		return s.Get(ctx, tenant, orderID, false)
	}
	// 2. Charge (idempotent by order id) and settle.
	email := ""
	if d.Customer != nil {
		email = d.Customer.Email
	}
	ch, perr := acct.Provider.Charge(ctx, payments.ChargeRequest{Account: acct.Account, OrderID: orderID, Amount: d.GrandTotal, Currency: d.Currency,
		PaymentMethod: paymentMethod, FeeAmount: d.GrandTotal * acct.FeeBps / 10000, IdempotencyKey: "charge-" + orderID, Email: email})
	if perr != nil {
		return Order{}, fmt.Errorf("%w: no se pudo confirmar el pago; intenta de nuevo", ErrDeclined)
	}
	if err := s.settle(ctx, tenant, actor, orderID, acct, ch); err != nil {
		return Order{}, err
	}
	if ch.Status != "captured" {
		return Order{}, fmt.Errorf("%w: %s", ErrDeclined, ch.Reason)
	}
	// 3. Close the draft.
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE draft_orders SET status='completed', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE payment_links SET used_at=now() WHERE tenant_id=$1 AND kind='draft' AND ref_id=$2 AND used_at IS NULL`, tenant, id); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, id, map[string]any{"type": "draft.completed", "orderId": orderID}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "draft.completed", id, map[string]any{"order": orderID, "manual": isManual})
	})
	if err != nil {
		return Order{}, err
	}
	return s.Get(ctx, tenant, orderID, false)
}
