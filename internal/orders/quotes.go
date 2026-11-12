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
	"github.com/helloghc/apexx/internal/store"
)

// Negotiable quotes. The buyer asks; the store answers with an offer that is
// a draft order (its own prices, shipping and discount) plus an expiry; the
// buyer accepts, rejects or asks for changes. Accepting places the order: on
// the company's credit right away when it buys on terms, otherwise through
// the draft's payment link. Offers that expire close by themselves.

// QuoteEvent is one step of the history.
type QuoteEvent struct {
	At      time.Time `json:"at"`
	Who     string    `json:"who"`  // buyer | store
	Kind    string    `json:"kind"` // requested | offered | changes | accepted | rejected | expired | cancelled
	Message string    `json:"message,omitempty"`
	Total   int64     `json:"total,omitempty"`
}

// PriceQuote is one negotiation.
type PriceQuote struct {
	ID        string       `json:"id"`
	Number    int64        `json:"number"`
	Status    string       `json:"status"`
	Channel   string       `json:"channel"`
	CompanyID string       `json:"companyId,omitempty"`
	Customer  *Customer    `json:"customer,omitempty"`
	Items     []ItemInput  `json:"items"`
	Offer     *Draft       `json:"offer,omitempty"`
	OrderID   string       `json:"orderId,omitempty"`
	ExpiresAt *time.Time   `json:"expiresAt,omitempty"`
	History   []QuoteEvent `json:"history"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	URL       string       `json:"url,omitempty"` // the buyer's link, only right after creating or offering
}

// QuoteRequest is what a buyer sends.
type QuoteRequest struct {
	Channel  string         `json:"channel"`
	Items    []ItemInput    `json:"items"`
	Customer Customer       `json:"customer"`
	Message  string         `json:"message"`
	Company  *CompanyAccess `json:"-"`
}

// QuoteOffer is what the store answers.
type QuoteOffer struct {
	Items      []ItemInput `json:"items"` // with Unit set: the quoted unit price
	Shipping   int64       `json:"shipping"`
	Discount   int64       `json:"discount"`
	PostalCode string      `json:"postalCode"`
	Message    string      `json:"message"`
	ValidDays  int         `json:"validDays"`
}

// QuoteURL is the buyer's page of a quote.
func (s *Service) QuoteURL(tenant, token string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/s/" + tenant + "/main/cotizacion/" + token
}

const quoteCols = `id, number, status, channel, COALESCE(company_id,''), customer, items, COALESCE(draft_id,''), COALESCE(order_id,''), expires_at, history, created_at, updated_at`

type quoteRow struct {
	q       PriceQuote
	enc     []byte
	draftID string
}

func scanQuoteRow(r pgx.Row) (quoteRow, error) {
	var x quoteRow
	var items, hist []byte
	if err := r.Scan(&x.q.ID, &x.q.Number, &x.q.Status, &x.q.Channel, &x.q.CompanyID, &x.enc, &items, &x.draftID, &x.q.OrderID, &x.q.ExpiresAt, &hist, &x.q.CreatedAt, &x.q.UpdatedAt); err != nil {
		return x, err
	}
	_ = json.Unmarshal(items, &x.q.Items)
	_ = json.Unmarshal(hist, &x.q.History)
	return x, nil
}

// finish decrypts the buyer and attaches the offer (after the rows are read).
func (s *Service) finishQuote(ctx context.Context, tx pgx.Tx, tenant string, x quoteRow, pii bool) PriceQuote {
	q := x.q
	if pii {
		if plain, err := s.Keys.Decrypt(tenant, x.enc, "quote:"+q.ID); err == nil {
			var c Customer
			if json.Unmarshal(plain, &c) == nil {
				q.Customer = &c
			}
		}
	}
	if x.draftID != "" {
		if d, err := s.draftTx(ctx, tx, tenant, x.draftID, false); err == nil {
			if !pii {
				d.Customer = nil
			}
			q.Offer = &d
		}
	}
	if q.History == nil {
		q.History = []QuoteEvent{}
	}
	return q
}

// RequestQuote stores a buyer's request and returns the buyer's link.
func (s *Service) RequestQuote(ctx context.Context, tenant string, in QuoteRequest) (PriceQuote, error) {
	if in.Channel == "" {
		in.Channel = "mx"
	}
	if len(in.Items) == 0 || len(in.Items) > 200 {
		return PriceQuote{}, FieldError{"items", "Agrega entre 1 y 200 productos"}
	}
	// Prices come from the store; the buyer only says what and how many.
	for i := range in.Items {
		in.Items[i].Unit = nil
		if in.Items[i].Qty < 1 || in.Items[i].Qty > 1_000_000 {
			return PriceQuote{}, FieldError{fmt.Sprintf("items[%d].qty", i), "Cantidad inválida"}
		}
	}
	if _, err := s.priceItems(tenant, in.Channel, "items", in.Items); err != nil {
		return PriceQuote{}, err
	}
	email, ok := customers.NormalizeEmail(in.Customer.Email)
	if !ok {
		return PriceQuote{}, FieldError{"customer.email", "Escribe tu correo"}
	}
	in.Customer.Email, in.Customer.Name, in.Customer.Phone = email, strings.TrimSpace(in.Customer.Name), strings.TrimSpace(in.Customer.Phone)
	in.Message = strings.TrimSpace(in.Message)
	switch {
	case in.Customer.Name == "" || len(in.Customer.Name) > 200:
		return PriceQuote{}, FieldError{"customer.name", "Escribe tu nombre"}
	case len(in.Customer.Phone) > 30 || len(in.Message) > 2000:
		return PriceQuote{}, FieldError{"message", "Texto demasiado largo"}
	}
	q := PriceQuote{ID: newID("quo_"), Status: "requested", Channel: in.Channel, Items: in.Items}
	if in.Company != nil {
		q.CompanyID = in.Company.ID
	}
	raw, _ := json.Marshal(in.Customer)
	enc, err := s.Keys.Encrypt(tenant, raw, "quote:"+q.ID)
	if err != nil {
		return q, err
	}
	hash, err := s.Keys.BlindIndex(tenant, email)
	if err != nil {
		return q, err
	}
	token, sum := subToken()
	tokEnc, err := s.Keys.Encrypt(tenant, []byte(token), "quote-token:"+q.ID)
	if err != nil {
		return q, err
	}
	q.History = []QuoteEvent{{At: time.Now().UTC(), Who: "buyer", Kind: "requested", Message: in.Message}}
	items, _ := json.Marshal(q.Items)
	hist, _ := json.Marshal(q.History)
	var company *string
	if q.CompanyID != "" {
		company = &q.CompanyID
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO quotes (tenant_id, id, number, channel, company_id, customer, email_hash, items, token_hash, history, token)
			VALUES ($1,$2,(SELECT COALESCE(max(number),0)+1 FROM quotes WHERE tenant_id=$1),$3,$4,$5,$6,$7,$8,$9,$10) RETURNING number, created_at, updated_at`,
			tenant, q.ID, q.Channel, company, enc, []byte(hash), items, sum, hist, tokEnc).Scan(&q.Number, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicOrder, q.ID, map[string]any{"type": "quote.requested"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, "storefront", "quote.requested", q.ID, map[string]any{"items": len(q.Items)})
	})
	q.URL = s.QuoteURL(tenant, token)
	return q, err
}

// Quotes lists quotes, newest first.
func (s *Service) Quotes(ctx context.Context, tenant, status string, limit int, pii bool) ([]PriceQuote, error) {
	out := []PriceQuote{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+quoteCols+` FROM quotes WHERE tenant_id=$1 AND ($2='' OR status=$2) ORDER BY number DESC LIMIT $3`, tenant, status, limit)
		if err != nil {
			return err
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (quoteRow, error) { return scanQuoteRow(r) })
		if err != nil {
			return err
		}
		for _, x := range list {
			out = append(out, s.finishQuote(ctx, tx, tenant, x, pii))
		}
		return nil
	})
	return out, err
}

// GetQuote loads one quote.
func (s *Service) GetQuote(ctx context.Context, tenant, id string, pii bool) (PriceQuote, error) {
	var q PriceQuote
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		x, err := scanQuoteRow(tx.QueryRow(ctx, `SELECT `+quoteCols+` FROM quotes WHERE tenant_id=$1 AND id=$2`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		q = s.finishQuote(ctx, tx, tenant, x, pii)
		return nil
	})
	return q, err
}

// QuoteByToken loads the quote of a buyer's link (system lookup by token).
func (s *Service) QuoteByToken(ctx context.Context, tenant, token string) (PriceQuote, error) {
	var id string
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id FROM quotes WHERE tenant_id=$1 AND token_hash=$2`, tenant, tokenHash(token)).Scan(&id)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceQuote{}, ErrNotFound
	}
	if err != nil {
		return PriceQuote{}, err
	}
	q, err := s.GetQuote(ctx, tenant, id, true)
	if err == nil && q.Status == "offered" && q.ExpiresAt != nil && time.Now().After(*q.ExpiresAt) {
		q.Status = "expired" // the round may not have closed it yet
	}
	return q, err
}

func appendEvent(ctx context.Context, tx pgx.Tx, tenant, id string, ev QuoteEvent) error {
	b, _ := json.Marshal([]QuoteEvent{ev})
	_, err := tx.Exec(ctx, `UPDATE quotes SET history = history || $3::jsonb, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, b)
	return err
}

// OfferQuote answers a request (or changes the offer while it is open): the
// prices go into a draft order and the buyer gets an email with the link.
func (s *Service) OfferQuote(ctx context.Context, tenant, actor, id string, in QuoteOffer) (PriceQuote, error) {
	q, err := s.GetQuote(ctx, tenant, id, true)
	if err != nil {
		return q, err
	}
	if q.Status != "requested" && q.Status != "offered" {
		return q, fmt.Errorf("%w: la cotización ya está cerrada", ErrInvalidState)
	}
	if in.ValidDays < 1 || in.ValidDays > 90 {
		return q, FieldError{"validDays", "La vigencia va de 1 a 90 días"}
	}
	for i, it := range in.Items {
		if it.Unit == nil || *it.Unit < 0 {
			return q, FieldError{fmt.Sprintf("items[%d].unit", i), "Escribe el precio de cada producto"}
		}
	}
	in.Message = strings.TrimSpace(in.Message)
	if len(in.Message) > 2000 {
		return q, FieldError{"message", "Mensaje de hasta 2,000 caracteres"}
	}
	draftID := ""
	if q.Offer != nil {
		draftID = q.Offer.ID
	}
	d, err := s.SaveDraft(ctx, tenant, actor, draftID, DraftInput{Channel: q.Channel, Items: in.Items, Customer: *q.Customer, PostalCode: in.PostalCode,
		Shipping: in.Shipping, Discount: in.Discount, Note: fmt.Sprintf("Cotización #%d", q.Number)})
	if err != nil {
		return q, err
	}
	// The buyer keeps the same link for the whole negotiation.
	var token string
	expires := time.Now().Add(time.Duration(in.ValidDays) * 24 * time.Hour).UTC()
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var tokEnc []byte
		if err := tx.QueryRow(ctx, `SELECT token FROM quotes WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&tokEnc); err != nil {
			return err
		}
		plain, err := s.Keys.Decrypt(tenant, tokEnc, "quote-token:"+id)
		if err != nil {
			return err
		}
		token = string(plain)
		ct, err := tx.Exec(ctx, `UPDATE quotes SET status='offered', draft_id=$3, expires_at=$4, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status IN ('requested','offered')`,
			tenant, id, d.ID, expires)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("%w: la cotización cambió mientras la editabas", ErrInvalidState)
		}
		if err := appendEvent(ctx, tx, tenant, id, QuoteEvent{At: time.Now().UTC(), Who: "store", Kind: "offered", Message: in.Message, Total: d.GrandTotal}); err != nil {
			return err
		}
		if s.Notify != nil {
			if _, err := s.Notify.EnqueueTx(ctx, tx, tenant, notify.Message{Template: notify.TplQuoteOffered, To: q.Customer.Email, DedupeKey: "quote-" + id + "-" + d.UpdatedAt.Format(time.RFC3339Nano), Force: true,
				Data: notify.Data{ActionURL: s.QuoteURL(tenant, token), Vars: map[string]string{"cliente.nombre": firstWord(q.Customer.Name),
					"cotizacion.numero": fmt.Sprint(q.Number), "cotizacion.total": notify.Money(d.GrandTotal, d.Currency), "cotizacion.vence": expires.Format("02/01/2006")}}}); err != nil {
				return err
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "quote.offered", id, map[string]any{"total": d.GrandTotal, "validDays": in.ValidDays})
	})
	if err != nil {
		return q, err
	}
	out, err := s.GetQuote(ctx, tenant, id, true)
	out.URL = s.QuoteURL(tenant, token)
	return out, err
}

// AnswerQuote is the buyer's decision from their link: accept, reject or
// ask for changes (with a message). Accepting a quote of a company that buys
// on terms places the order on its credit; otherwise the buyer pays with the
// payment link of the offer.
func (s *Service) AnswerQuote(ctx context.Context, tenant, token, action, message string, company *CompanyAccess) (PriceQuote, string, error) {
	q, err := s.QuoteByToken(ctx, tenant, token)
	if err != nil {
		return q, "", err
	}
	message = strings.TrimSpace(message)
	if len(message) > 2000 {
		return q, "", FieldError{"message", "Mensaje de hasta 2,000 caracteres"}
	}
	if q.Status != "offered" {
		return q, "", fmt.Errorf("%w: esta cotización ya no se puede responder", ErrInvalidState)
	}
	actor := "buyer:" + q.ID
	switch action {
	case "reject", "changes":
		status, kind := "rejected", "rejected"
		if action == "changes" {
			if message == "" {
				return q, "", FieldError{"message", "Cuéntanos qué te gustaría cambiar"}
			}
			status, kind = "requested", "changes"
		}
		err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `UPDATE quotes SET status=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='offered'`, tenant, q.ID, status)
			if err != nil || ct.RowsAffected() == 0 {
				return errors.Join(err, ErrInvalidState)
			}
			return appendEvent(ctx, tx, tenant, q.ID, QuoteEvent{At: time.Now().UTC(), Who: "buyer", Kind: kind, Message: message})
		})
		if err == nil && action == "reject" && q.Offer != nil {
			_ = s.CancelDraft(ctx, tenant, actor, q.Offer.ID)
		}
		q2, _ := s.GetQuote(ctx, tenant, q.ID, true)
		return q2, "", err
	case "accept":
	default:
		return q, "", FieldError{"action", "Acción desconocida"}
	}
	if q.Offer == nil {
		return q, "", fmt.Errorf("%w: la cotización no tiene oferta", ErrInvalidState)
	}
	// Mark it accepted first: two clicks cannot place two orders.
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE quotes SET status='accepted', updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='offered' AND (expires_at IS NULL OR expires_at > now())`, tenant, q.ID)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("%w: la cotización venció o ya se respondió", ErrInvalidState)
		}
		return appendEvent(ctx, tx, tenant, q.ID, QuoteEvent{At: time.Now().UTC(), Who: "buyer", Kind: "accepted", Message: message, Total: q.Offer.GrandTotal})
	})
	if err != nil {
		return q, "", err
	}
	if company != nil && company.ID == q.CompanyID && company.Terms != "prepaid" {
		o, err := s.CompleteDraftOnCredit(ctx, tenant, actor, q.Offer.ID, company)
		if err != nil {
			// Undo the acceptance so the buyer can try again or pay by card.
			_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
				_, e := tx.Exec(ctx, `UPDATE quotes SET status='offered', updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='accepted'`, tenant, q.ID)
				return e
			})
			return q, "", err
		}
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `UPDATE quotes SET order_id=$3 WHERE tenant_id=$1 AND id=$2`, tenant, q.ID, o.ID)
			return e
		})
		q2, _ := s.GetQuote(ctx, tenant, q.ID, true)
		return q2, "", nil
	}
	d, err := s.SendDraftLink(ctx, tenant, actor, q.Offer.ID)
	if err != nil {
		return q, "", err
	}
	q2, _ := s.GetQuote(ctx, tenant, q.ID, true)
	return q2, d.PayURL, nil
}

// CancelQuote closes a quote from the panel.
func (s *Service) CancelQuote(ctx context.Context, tenant, actor, id string) error {
	q, err := s.GetQuote(ctx, tenant, id, false)
	if err != nil {
		return err
	}
	if q.Status != "requested" && q.Status != "offered" {
		return fmt.Errorf("%w: la cotización ya está cerrada", ErrInvalidState)
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE quotes SET status='cancelled', updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
		if err := appendEvent(ctx, tx, tenant, id, QuoteEvent{At: time.Now().UTC(), Who: "store", Kind: "cancelled"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "quote.cancelled", id, nil)
	})
	if err == nil && q.Offer != nil {
		_ = s.CancelDraft(ctx, tenant, actor, q.Offer.ID)
	}
	return err
}

// ExpireQuotes closes offers past their date (background round).
func (s *Service) ExpireQuotes(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT tenant_id, id, COALESCE(draft_id,'') FROM quotes WHERE status='offered' AND expires_at < now() LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type x struct{ tenant, id, draft string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (x, error) {
		var v x
		return v, r.Scan(&v.tenant, &v.id, &v.draft)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, v := range list {
		err := s.DB.Tx(ctx, v.tenant, func(tx pgx.Tx) error {
			ct, err := tx.Exec(ctx, `UPDATE quotes SET status='expired', updated_at=now() WHERE tenant_id=$1 AND id=$2 AND status='offered'`, v.tenant, v.id)
			if err != nil || ct.RowsAffected() == 0 {
				return err
			}
			return appendEvent(ctx, tx, v.tenant, v.id, QuoteEvent{At: time.Now().UTC(), Who: "store", Kind: "expired"})
		})
		if err == nil {
			n++
			if v.draft != "" {
				_ = s.CancelDraft(ctx, v.tenant, "system", v.draft)
			}
		}
	}
	return n, nil
}
