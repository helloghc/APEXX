package orders

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Quote templates: an agreement with a company from which its buyers order
// without renegotiating. Each line has the negotiated unit price and the
// quantities allowed per order; the order must reach a minimum total and the
// agreement is only valid between two dates. What has been bought from it is
// derived from its paid orders, so cancellations and refunds give it back.

// TemplateItem is a product of an agreement.
type TemplateItem struct {
	SKU    string `json:"sku"`
	Title  string `json:"title,omitempty"`
	Unit   int64  `json:"unit"`
	MinQty int64  `json:"minQty"`
	MaxQty int64  `json:"maxQty"` // 0 = no maximum
}

// QuoteTemplate is an agreement with a company.
type QuoteTemplate struct {
	ID            string         `json:"id"`
	CompanyID     string         `json:"companyId"`
	CompanyName   string         `json:"companyName,omitempty"`
	Name          string         `json:"name"`
	Items         []TemplateItem `json:"items"`
	MinOrderTotal int64          `json:"minOrderTotal"`
	CommitTotal   int64          `json:"commitTotal"`
	ValidFrom     string         `json:"validFrom"`
	ValidUntil    string         `json:"validUntil"`
	Status        string         `json:"status"`
	Note          string         `json:"note"`
	Used          int64          `json:"used"`   // paid and not refunded, from its orders
	Orders        int            `json:"orders"` // orders placed from it
	Live          bool           `json:"live"`   // active and within its dates
}

// ErrTemplate is an order that does not fit its agreement.
type ErrTemplate struct{ Msg string }

func (e ErrTemplate) Error() string { return e.Msg }

const templateCols = `t.id, t.company_id, c.name, t.name, t.items, t.min_order_total, t.commit_total, to_char(t.valid_from,'YYYY-MM-DD'), to_char(t.valid_until,'YYYY-MM-DD'), t.status, t.note,
	COALESCE((SELECT sum(o.grand_total - COALESCE(o.refunded_total,0)) FROM quote_template_orders x JOIN draft_orders d ON d.tenant_id=x.tenant_id AND d.id=x.draft_id
		JOIN orders o ON o.tenant_id=d.tenant_id AND o.id=d.order_id WHERE x.tenant_id=t.tenant_id AND x.template_id=t.id AND o.status IN ('paid','fulfilled','refunded')),0),
	(SELECT count(*) FROM quote_template_orders x WHERE x.tenant_id=t.tenant_id AND x.template_id=t.id)::int,
	t.status='active' AND current_date BETWEEN t.valid_from AND t.valid_until`

func scanTemplate(r pgx.CollectableRow) (QuoteTemplate, error) {
	var t QuoteTemplate
	return t, r.Scan(&t.ID, &t.CompanyID, &t.CompanyName, &t.Name, &t.Items, &t.MinOrderTotal, &t.CommitTotal, &t.ValidFrom, &t.ValidUntil, &t.Status, &t.Note, &t.Used, &t.Orders, &t.Live)
}

// QuoteTemplates lists the agreements of a company ("" = every company).
func (s *Service) QuoteTemplates(ctx context.Context, tenant, company string, onlyLive bool) ([]QuoteTemplate, error) {
	var out []QuoteTemplate
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `SELECT ` + templateCols + ` FROM quote_templates t JOIN companies c ON c.tenant_id=t.tenant_id AND c.id=t.company_id WHERE t.tenant_id=$1 AND ($2='' OR t.company_id=$2)`
		if onlyLive {
			q += ` AND t.status='active' AND current_date BETWEEN t.valid_from AND t.valid_until`
		}
		rows, err := tx.Query(ctx, q+` ORDER BY t.valid_until DESC, t.name LIMIT 200`, tenant, company)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanTemplate)
		return err
	})
	if out == nil {
		out = []QuoteTemplate{}
	}
	return out, err
}

// QuoteTemplate returns one agreement.
func (s *Service) QuoteTemplate(ctx context.Context, tenant, id string) (QuoteTemplate, error) {
	var t QuoteTemplate
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+templateCols+` FROM quote_templates t JOIN companies c ON c.tenant_id=t.tenant_id AND c.id=t.company_id WHERE t.tenant_id=$1 AND t.id=$2`, tenant, id)
		if err != nil {
			return err
		}
		t, err = pgx.CollectExactlyOneRow(rows, scanTemplate)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return t, err
}

// SaveQuoteTemplate creates (empty id) or updates an agreement.
func (s *Service) SaveQuoteTemplate(ctx context.Context, tenant, actor string, in QuoteTemplate) (QuoteTemplate, error) {
	in.Name, in.Note = strings.TrimSpace(in.Name), strings.TrimSpace(in.Note)
	if in.Status == "" {
		in.Status = "active"
	}
	from, e1 := time.Parse("2006-01-02", in.ValidFrom)
	until, e2 := time.Parse("2006-01-02", in.ValidUntil)
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 120:
		return in, FieldError{"name", "Ponle un nombre de hasta 120 caracteres"}
	case len(in.Items) == 0 || len(in.Items) > 300:
		return in, FieldError{"items", "Agrega de 1 a 300 productos"}
	case e1 != nil || e2 != nil:
		return in, FieldError{"validUntil", "Fechas con formato AAAA-MM-DD"}
	case until.Before(from):
		return in, FieldError{"validUntil", "La vigencia termina antes de empezar"}
	case in.Status != "active" && in.Status != "closed":
		return in, FieldError{"status", "El acuerdo está activo o cerrado"}
	case in.MinOrderTotal < 0 || in.CommitTotal < 0:
		return in, FieldError{"minOrderTotal", "Los montos no pueden ser negativos"}
	case len(in.Note) > 1000:
		return in, FieldError{"note", "Nota de hasta 1000 caracteres"}
	}
	seen := map[string]bool{}
	for i := range in.Items {
		it := &in.Items[i]
		it.SKU = strings.TrimSpace(it.SKU)
		if it.MinQty == 0 {
			it.MinQty = 1
		}
		switch {
		case it.SKU == "" || seen[it.SKU]:
			return in, FieldError{"items", fmt.Sprintf("Línea %d: SKU vacío o repetido", i+1)}
		case it.Unit <= 0:
			return in, FieldError{"items", it.SKU + ": el precio acordado debe ser mayor a cero"}
		case it.MinQty < 1 || (it.MaxQty > 0 && it.MaxQty < it.MinQty) || it.MaxQty > 1_000_000:
			return in, FieldError{"items", it.SKU + ": revisa el mínimo y el máximo por pedido"}
		}
		seen[it.SKU] = true
		it.Title = ""
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM companies WHERE tenant_id=$1 AND id=$2)`, tenant, in.CompanyID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return FieldError{"companyId", "La empresa no existe"}
		}
		if in.ID == "" {
			in.ID = newID("qtp_")
			_, err := tx.Exec(ctx, `INSERT INTO quote_templates (tenant_id, id, company_id, name, items, min_order_total, commit_total, valid_from, valid_until, status, note)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8::date,$9::date,$10,$11)`, tenant, in.ID, in.CompanyID, in.Name, in.Items, in.MinOrderTotal, in.CommitTotal, in.ValidFrom, in.ValidUntil, in.Status, in.Note)
			if err != nil {
				return err
			}
		} else {
			ct, err := tx.Exec(ctx, `UPDATE quote_templates SET name=$4, items=$5, min_order_total=$6, commit_total=$7, valid_from=$8::date, valid_until=$9::date, status=$10, note=$11, updated_at=now()
				WHERE tenant_id=$1 AND id=$2 AND company_id=$3`, tenant, in.ID, in.CompanyID, in.Name, in.Items, in.MinOrderTotal, in.CommitTotal, in.ValidFrom, in.ValidUntil, in.Status, in.Note)
			if err != nil {
				return err
			}
			if ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		return store.Audit(ctx, tx, tenant, actor, "quote_template.save", in.ID, map[string]any{"company": in.CompanyID, "name": in.Name, "items": len(in.Items), "status": in.Status})
	})
	if err != nil {
		return in, err
	}
	return s.QuoteTemplate(ctx, tenant, in.ID)
}

// TemplateLine is what a buyer asks for from an agreement.
type TemplateLine struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// OrderFromTemplate places an order from an agreement for a company buyer:
// on the company's credit when the buyer may use it, otherwise as a draft
// with a payment link. It returns the order (credit) or the draft (link).
func (s *Service) OrderFromTemplate(ctx context.Context, tenant, actor, id string, c *CompanyAccess, buyer Customer, lines []TemplateLine) (*Order, *Draft, error) {
	t, err := s.QuoteTemplate(ctx, tenant, id)
	if err != nil {
		return nil, nil, err
	}
	if c == nil || t.CompanyID != c.ID {
		return nil, nil, ErrNotFound // another company's agreement does not exist for this buyer
	}
	if !t.Live {
		return nil, nil, ErrTemplate{"Este acuerdo no está vigente"}
	}
	byS := map[string]TemplateItem{}
	for _, it := range t.Items {
		byS[it.SKU] = it
	}
	var items []ItemInput
	var total int64
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Qty == 0 {
			continue
		}
		it, ok := byS[l.SKU]
		switch {
		case !ok || seen[l.SKU]:
			return nil, nil, ErrTemplate{"El producto " + l.SKU + " no está en el acuerdo"}
		case l.Qty < it.MinQty:
			return nil, nil, ErrTemplate{fmt.Sprintf("%s: el acuerdo pide al menos %d piezas por pedido", l.SKU, it.MinQty)}
		case it.MaxQty > 0 && l.Qty > it.MaxQty:
			return nil, nil, ErrTemplate{fmt.Sprintf("%s: el acuerdo permite hasta %d piezas por pedido", l.SKU, it.MaxQty)}
		}
		seen[l.SKU] = true
		unit := it.Unit
		items = append(items, ItemInput{SKU: l.SKU, Qty: l.Qty, Unit: &unit})
		total += unit * l.Qty
	}
	if len(items) == 0 {
		return nil, nil, ErrTemplate{"Elige al menos un producto"}
	}
	if total < t.MinOrderTotal {
		return nil, nil, ErrTemplate{fmt.Sprintf("El acuerdo pide pedidos desde $%s", cents(t.MinOrderTotal))}
	}
	d, err := s.SaveDraft(ctx, tenant, actor, "", DraftInput{Channel: "mx", Items: items, Customer: buyer, Note: "Pedido del acuerdo «" + t.Name + "»"})
	if err != nil {
		return nil, nil, err
	}
	if err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO quote_template_orders (tenant_id, template_id, draft_id) VALUES ($1,$2,$3)`, tenant, t.ID, d.ID)
		return err
	}); err != nil {
		return nil, nil, err
	}
	if c.Terms != "prepaid" && c.CanCredit {
		o, err := s.CompleteDraftOnCredit(ctx, tenant, actor, d.ID, c)
		if err != nil {
			_ = s.CancelDraft(context.Background(), tenant, actor, d.ID)
			return nil, nil, err
		}
		return &o, nil, nil
	}
	sent, err := s.SendDraftLink(ctx, tenant, actor, d.ID)
	if err != nil {
		return nil, nil, err
	}
	return nil, &sent, nil
}
