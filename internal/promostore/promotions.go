// Package promostore persists promotions, campaigns, coupons, redemptions,
// price lists and price history. PostgreSQL is the source of truth: every
// write runs in one tenant transaction with an outbox event and an audit
// record, and the projector applies each event to the in-memory engine.
package promostore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/store"
)

// Outbox topics added by this module (promotion changes reuse store.TopicPromotion).
const (
	TopicCampaign  = "campaign.changed"
	TopicPriceList = "pricelist.changed"
)

// Errors.
var (
	ErrNotFound = errors.New("promostore: not found")
	ErrExists   = errors.New("promostore: already exists")
	ErrInUse    = errors.New("promostore: in use")
	ErrChanged  = errors.New("promostore: data changed since the preview")
)

// Store wraps the database handle.
type Store struct{ DB *store.DB }

// PromotionView is a promotion with what the admin needs to list and explain it.
type PromotionView struct {
	promo.Promotion
	DisplayStatus string    `json:"displayStatus"`
	Summary       string    `json:"summary"`
	Uses          int64     `json:"uses"`
	DiscountTotal int64     `json:"discountTotal"`
	Coupons       int64     `json:"coupons"`
	CampaignName  string    `json:"campaignName,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type promoRow struct {
	id, kind, status, campaign string
	rule                       []byte
	active                     bool
}

// decode turns a stored row into a definition. The status and campaign
// columns are authoritative; rules written by older tools are converted.
func decode(r promoRow) (promo.Promotion, error) {
	var p promo.Promotion
	if promo.IsLegacyKind(r.kind) {
		var err error
		if p, err = promo.FromLegacy(r.id, r.kind, r.rule, r.active); err != nil {
			return p, err
		}
	} else {
		if err := json.Unmarshal(r.rule, &p); err != nil {
			return p, fmt.Errorf("promotion %s: %w", r.id, err)
		}
		p.Type = r.kind
	}
	p.ID, p.CampaignID = r.id, r.campaign
	p.Status = r.status
	if !r.active && p.Status == promo.StatusActive {
		p.Status = promo.StatusPaused
	}
	p.Normalize()
	return p, nil
}

const promoCols = `p.id, p.kind, p.rule, p.active, p.status, p.campaign_id, p.uses, p.discount_total, p.created_at, p.updated_at`

func scanPromo(r pgx.Row, extra ...any) (PromotionView, error) {
	var v PromotionView
	var row promoRow
	dst := append([]any{&row.id, &row.kind, &row.rule, &row.active, &row.status, &row.campaign, &v.Uses, &v.DiscountTotal, &v.CreatedAt, &v.UpdatedAt}, extra...)
	if err := r.Scan(dst...); err != nil {
		return v, err
	}
	p, err := decode(row)
	if err != nil {
		// A row that cannot be read must not hide the rest of the list.
		p = promo.Promotion{ID: row.id, Name: row.id + " (regla ilegible)", Type: row.kind, Status: promo.StatusPaused}
	}
	v.Promotion = p
	return v, nil
}

// SavePromotion creates or replaces a promotion. With create it fails with
// ErrExists when the id is taken.
func (s *Store) SavePromotion(ctx context.Context, tenant, actor string, p promo.Promotion, create bool) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if p.CampaignID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaigns WHERE tenant_id=$1 AND id=$2)`, tenant, p.CampaignID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: campaign %s", ErrNotFound, p.CampaignID)
			}
		}
		q := `INSERT INTO promotions (tenant_id, id, kind, rule, active, name, status, starts_at, ends_at, campaign_id, priority, requires_coupon)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
		if create {
			q += ` ON CONFLICT (tenant_id, id) DO NOTHING`
		} else {
			q += ` ON CONFLICT (tenant_id, id) DO UPDATE SET kind=EXCLUDED.kind, rule=EXCLUDED.rule, active=EXCLUDED.active, name=EXCLUDED.name,
				status=EXCLUDED.status, starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at, campaign_id=EXCLUDED.campaign_id,
				priority=EXCLUDED.priority, requires_coupon=EXCLUDED.requires_coupon, updated_at=now()`
		}
		tag, err := tx.Exec(ctx, q, tenant, p.ID, p.Type, b, p.Status == promo.StatusActive, p.Name, p.Status,
			p.Conditions.StartsAt, p.Conditions.EndsAt, p.CampaignID, p.Stacking.Priority, p.Conditions.RequiresCoupon)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrExists
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, p.ID, map[string]any{"status": p.Status}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "promotion.set", p.ID, p)
	})
}

// SetPromotionStatus changes the status of several promotions at once and
// returns how many changed.
func (s *Store) SetPromotionStatus(ctx context.Context, tenant, actor string, ids []string, status string) (int, error) {
	n := 0
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE promotions SET status=$3, active=($3='active'), updated_at=now()
			WHERE tenant_id=$1 AND id = ANY($2) AND (status <> $3 OR active <> ($3='active')) RETURNING id`, tenant, ids, status)
		if err != nil {
			return err
		}
		changed, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		n = len(changed)
		for _, id := range changed {
			if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, id, map[string]any{"status": status}); err != nil {
				return err
			}
		}
		if n == 0 {
			return nil
		}
		return store.Audit(ctx, tx, tenant, actor, "promotion.status", strings.Join(changed, ","), map[string]any{"status": status})
	})
	return n, err
}

// DeletePromotion removes a promotion that was never used. One with
// redemptions is archived instead, so order history keeps its meaning;
// archived reports which of the two happened.
func (s *Store) DeletePromotion(ctx context.Context, tenant, actor, id string) (archived bool, err error) {
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var used bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM promotion_redemptions WHERE tenant_id=$1 AND promotion_id=$2)
			FROM promotions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&used)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if used {
			archived = true
			if _, err := tx.Exec(ctx, `UPDATE promotions SET status='archived', active=false, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM promotions WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, id, map[string]any{"deleted": !archived}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "promotion.delete", id, map[string]any{"archived": archived})
	})
	return archived, err
}

// GetPromotion loads one promotion with its usage.
func (s *Store) GetPromotion(ctx context.Context, tenant, id string) (PromotionView, error) {
	var v PromotionView
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var name string
		var n int64
		var err error
		v, err = scanPromo(tx.QueryRow(ctx, `SELECT `+promoCols+`, COALESCE(c.name,''),
			(SELECT count(*) FROM coupons k WHERE k.tenant_id=p.tenant_id AND k.promotion_id=p.id)
			FROM promotions p LEFT JOIN campaigns c ON c.tenant_id=p.tenant_id AND c.id=p.campaign_id
			WHERE p.tenant_id=$1 AND p.id=$2`, tenant, id), &name, &n)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		v.CampaignName, v.Coupons = name, n
		return err
	})
	return v, err
}

// PromotionFilter narrows a listing.
type PromotionFilter struct {
	Status   string // display status: activa, programada, vencida, pausada, borrador, archivada; "" = all but archived
	Type     string
	Campaign string
	Query    string
	Sort     string // recent (default) | name | uses | priority | ends
	Offset   int
	Limit    int
}

// displayStatusSQL mirrors promo.Promotion.DisplayStatus.
const displayStatusSQL = `CASE
	WHEN p.status='paused' OR (p.status='active' AND NOT p.active) THEN 'pausada'
	WHEN p.status='draft' THEN 'borrador'
	WHEN p.status='archived' THEN 'archivada'
	WHEN p.ends_at IS NOT NULL AND now() > p.ends_at THEN 'vencida'
	WHEN p.starts_at IS NOT NULL AND now() < p.starts_at THEN 'programada'
	ELSE 'activa' END`

// ListPromotions returns one page plus the total and the count per status.
func (s *Store) ListPromotions(ctx context.Context, tenant string, f PromotionFilter) (items []PromotionView, total int, counts map[string]int, err error) {
	order := map[string]string{
		"name": "lower(COALESCE(NULLIF(p.name,''), p.id)), p.id", "uses": "p.uses DESC, p.id", "priority": "p.priority DESC, p.id",
		"ends": "p.ends_at ASC NULLS LAST, p.id", "discount": "p.discount_total DESC, p.id",
	}[f.Sort]
	if order == "" {
		order = "p.updated_at DESC, p.id"
	}
	counts = map[string]int{}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		where := `p.tenant_id=$1 AND ($2='' OR p.kind=$2 OR ($2='order_percent' AND p.kind='percent_over_threshold') OR ($2='buy_x_get_y' AND p.kind='buy_x_pay_y'))
			AND ($3='' OR p.campaign_id=$3)
			AND ($4='' OR p.id ILIKE '%'||$4||'%' OR p.name ILIKE '%'||$4||'%' OR p.rule->>'name' ILIKE '%'||$4||'%')`
		rows, err := tx.Query(ctx, `SELECT `+displayStatusSQL+` AS ds, count(*) FROM promotions p WHERE `+where+` GROUP BY 1`,
			tenant, f.Type, f.Campaign, f.Query)
		if err != nil {
			return err
		}
		for rows.Next() {
			var st string
			var n int
			if err := rows.Scan(&st, &n); err != nil {
				rows.Close()
				return err
			}
			counts[st] = n
		}
		rows.Close()
		if f.Status == "" {
			for st, n := range counts {
				if st != "archivada" {
					total += n
				}
			}
		} else {
			total = counts[f.Status]
		}
		rows, err = tx.Query(ctx, `SELECT `+promoCols+`, COALESCE(c.name,''),
			(SELECT count(*) FROM coupons k WHERE k.tenant_id=p.tenant_id AND k.promotion_id=p.id)
			FROM promotions p LEFT JOIN campaigns c ON c.tenant_id=p.tenant_id AND c.id=p.campaign_id
			WHERE `+where+` AND (($5='' AND p.status<>'archived') OR `+displayStatusSQL+`=$5)
			ORDER BY `+order+` LIMIT $6 OFFSET $7`, tenant, f.Type, f.Campaign, f.Query, f.Status, f.Limit, f.Offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var n int64
			v, err := scanPromo(rows, &name, &n)
			if err != nil {
				return err
			}
			v.CampaignName, v.Coupons = name, n
			items = append(items, v)
		}
		return rows.Err()
	})
	return items, total, counts, err
}

// Loaded is a promotion with its use counter, for the projector.
type Loaded struct {
	Promotion promo.Promotion
	Uses      int64
}

// LoadPromotions reads every promotion of a tenant that can still matter to
// a cart (system pool). Archived ones are left out.
func (s *Store) LoadPromotions(ctx context.Context, tenant string) ([]Loaded, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT id, kind, rule, active, status, campaign_id, uses FROM promotions
		WHERE tenant_id=$1 AND status <> 'archived' ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Loaded
	for rows.Next() {
		var r promoRow
		var uses int64
		if err := rows.Scan(&r.id, &r.kind, &r.rule, &r.active, &r.status, &r.campaign, &uses); err != nil {
			return nil, err
		}
		p, err := decode(r)
		if err != nil {
			continue // an unreadable rule never blocks the store from loading
		}
		out = append(out, Loaded{Promotion: p, Uses: uses})
	}
	return out, rows.Err()
}

// LoadPromotion reads one promotion (system pool); found is false when it
// does not exist or is archived.
func (s *Store) LoadPromotion(ctx context.Context, tenant, id string) (l Loaded, found bool, err error) {
	var r promoRow
	err = s.DB.System.QueryRow(ctx, `SELECT id, kind, rule, active, status, campaign_id, uses FROM promotions
		WHERE tenant_id=$1 AND id=$2 AND status <> 'archived'`, tenant, id).Scan(&r.id, &r.kind, &r.rule, &r.active, &r.status, &r.campaign, &l.Uses)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, false, nil
	}
	if err != nil {
		return l, false, err
	}
	if l.Promotion, err = decode(r); err != nil {
		return l, false, nil
	}
	return l, true, nil
}

// ---------- campaigns ----------

// CampaignView is a campaign with its promotions count.
type CampaignView struct {
	promo.Campaign
	DisplayStatus string    `json:"displayStatus"`
	Promotions    int64     `json:"promotions"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

const campCols = `c.id, c.name, c.description, c.status, c.starts_at, c.ends_at, c.budget_type, c.budget_limit, c.spent, c.used, c.currency, c.created_at, c.updated_at`

func scanCampaign(r pgx.Row, extra ...any) (CampaignView, error) {
	var v CampaignView
	c := &v.Campaign
	dst := append([]any{&c.ID, &c.Name, &c.Description, &c.Status, &c.StartsAt, &c.EndsAt, &c.BudgetType, &c.BudgetLimit, &c.Spent, &c.Used, &c.Currency, &v.CreatedAt, &v.UpdatedAt}, extra...)
	err := r.Scan(dst...)
	v.DisplayStatus = c.DisplayStatus(time.Now())
	return v, err
}

// SaveCampaign creates or updates a campaign (counters are never overwritten).
func (s *Store) SaveCampaign(ctx context.Context, tenant, actor string, c promo.Campaign, create bool) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `INSERT INTO campaigns (tenant_id, id, name, description, status, starts_at, ends_at, budget_type, budget_limit, currency)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
		if create {
			q += ` ON CONFLICT (tenant_id, id) DO NOTHING`
		} else {
			q += ` ON CONFLICT (tenant_id, id) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description, status=EXCLUDED.status,
				starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at, budget_type=EXCLUDED.budget_type, budget_limit=EXCLUDED.budget_limit, updated_at=now()`
		}
		tag, err := tx.Exec(ctx, q, tenant, c.ID, c.Name, c.Description, c.Status, c.StartsAt, c.EndsAt, c.BudgetType, c.BudgetLimit, c.Currency)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrExists
		}
		if err := store.Emit(ctx, tx, tenant, TopicCampaign, c.ID, nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "campaign.set", c.ID, c)
	})
}

// DeleteCampaign removes a campaign; its promotions stay, without campaign.
func (s *Store) DeleteCampaign(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM campaigns WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `UPDATE promotions SET campaign_id='', updated_at=now() WHERE tenant_id=$1 AND campaign_id=$2 RETURNING id`, tenant, id)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, pid := range ids {
			if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, pid, nil); err != nil {
				return err
			}
		}
		if err := store.Emit(ctx, tx, tenant, TopicCampaign, id, map[string]any{"deleted": true}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "campaign.delete", id, map[string]any{"promotions": ids})
	})
}

// ListCampaigns returns every campaign, newest first.
func (s *Store) ListCampaigns(ctx context.Context, tenant string) ([]CampaignView, error) {
	var out []CampaignView
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+campCols+`, (SELECT count(*) FROM promotions p WHERE p.tenant_id=c.tenant_id AND p.campaign_id=c.id AND p.status<>'archived')
			FROM campaigns c WHERE c.tenant_id=$1 ORDER BY c.created_at DESC, c.id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n int64
			v, err := scanCampaign(rows, &n)
			if err != nil {
				return err
			}
			v.Promotions = n
			out = append(out, v)
		}
		return rows.Err()
	})
	return out, err
}

// GetCampaign loads one campaign.
func (s *Store) GetCampaign(ctx context.Context, tenant, id string) (CampaignView, error) {
	var v CampaignView
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int64
		var err error
		v, err = scanCampaign(tx.QueryRow(ctx, `SELECT `+campCols+`, (SELECT count(*) FROM promotions p WHERE p.tenant_id=c.tenant_id AND p.campaign_id=c.id AND p.status<>'archived')
			FROM campaigns c WHERE c.tenant_id=$1 AND c.id=$2`, tenant, id), &n)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		v.Promotions = n
		return err
	})
	return v, err
}

// LoadCampaigns reads the campaigns of a tenant (system pool). With id it
// returns at most that one.
func (s *Store) LoadCampaigns(ctx context.Context, tenant, id string) ([]promo.Campaign, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT `+campCols+` FROM campaigns c WHERE c.tenant_id=$1 AND ($2='' OR c.id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []promo.Campaign
	for rows.Next() {
		v, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v.Campaign)
	}
	return out, rows.Err()
}
