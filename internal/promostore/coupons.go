package promostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/store"
)

// CouponView is a coupon as the admin lists it.
type CouponView struct {
	promo.Coupon
	PromotionName string    `json:"promotionName"`
	BatchID       string    `json:"batchId,omitempty"`
	State         string    `json:"state"` // activo | desactivado | vencido | programado | agotado
	CreatedAt     time.Time `json:"createdAt"`
}

func couponState(c promo.Coupon, now time.Time) string {
	switch {
	case c.Status != promo.StatusActive:
		return "desactivado"
	case c.EndsAt != nil && now.After(*c.EndsAt):
		return "vencido"
	case c.MaxUses > 0 && c.Uses >= c.MaxUses:
		return "agotado"
	case c.StartsAt != nil && now.Before(*c.StartsAt):
		return "programado"
	}
	return "activo"
}

const couponCols = `k.code, k.promotion_id, k.status, k.starts_at, k.ends_at, k.max_uses, k.max_uses_per_customer, k.uses, k.batch_id, k.created_at`

func scanCoupon(r pgx.Row, extra ...any) (CouponView, error) {
	var v CouponView
	c := &v.Coupon
	dst := append([]any{&c.Code, &c.PromotionID, &c.Status, &c.StartsAt, &c.EndsAt, &c.MaxUses, &c.MaxUsesPerCustomer, &c.Uses, &v.BatchID, &v.CreatedAt}, extra...)
	err := r.Scan(dst...)
	v.State = couponState(*c, time.Now())
	return v, err
}

// CouponSpec are the settings shared by a single code and a generated batch.
type CouponSpec struct {
	PromotionID        string     `json:"promotionId"`
	StartsAt           *time.Time `json:"startsAt,omitempty"`
	EndsAt             *time.Time `json:"endsAt,omitempty"`
	MaxUses            int64      `json:"maxUses"`
	MaxUsesPerCustomer int64      `json:"maxUsesPerCustomer"`
}

func promotionExists(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM promotions WHERE tenant_id=$1 AND id=$2 AND status<>'archived')`, tenant, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: promotion %s", ErrNotFound, id)
	}
	return nil
}

// CreateCoupon stores one code. ErrExists when the code is taken.
func (s *Store) CreateCoupon(ctx context.Context, tenant, actor, code string, spec CouponSpec) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := promotionExists(ctx, tx, tenant, spec.PromotionID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO coupons (tenant_id, code, promotion_id, starts_at, ends_at, max_uses, max_uses_per_customer)
			VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT DO NOTHING`, tenant, code, spec.PromotionID, spec.StartsAt, spec.EndsAt, spec.MaxUses, spec.MaxUsesPerCustomer)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrExists
		}
		return store.Audit(ctx, tx, tenant, actor, "coupon.create", code, spec)
	})
}

// codeAlphabet avoids look-alikes (0/O, 1/I/L) so codes survive being read aloud.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func randomCode(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return prefix + string(out)
}

// Batch describes a generated set of codes.
type Batch struct {
	ID            string    `json:"id"`
	PromotionID   string    `json:"promotionId"`
	PromotionName string    `json:"promotionName"`
	Prefix        string    `json:"prefix"`
	Quantity      int       `json:"quantity"`
	Used          int64     `json:"used"`
	Actor         string    `json:"actor"`
	CreatedAt     time.Time `json:"createdAt"`
}

// GenerateCoupons creates quantity unique random codes with a prefix, all in
// one transaction (all or nothing).
func (s *Store) GenerateCoupons(ctx context.Context, tenant, actor, prefix string, length, quantity int, spec CouponSpec) (Batch, error) {
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	b := Batch{ID: "lote_" + hex.EncodeToString(idb), PromotionID: spec.PromotionID, Prefix: prefix, Quantity: quantity, Actor: actor}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := promotionExists(ctx, tx, tenant, spec.PromotionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO coupon_batches (tenant_id, id, promotion_id, prefix, quantity, actor) VALUES ($1,$2,$3,$4,$5,$6)`,
			tenant, b.ID, spec.PromotionID, prefix, quantity, actor); err != nil {
			return err
		}
		left := quantity
		for round := 0; left > 0; round++ {
			if round > 20 {
				return fmt.Errorf("%w: no quedan códigos libres con ese prefijo y largo", ErrExists)
			}
			set := map[string]bool{}
			for len(set) < left {
				set[randomCode(prefix, length)] = true
			}
			codes := make([]string, 0, len(set))
			for c := range set {
				codes = append(codes, c)
			}
			tag, err := tx.Exec(ctx, `INSERT INTO coupons (tenant_id, code, promotion_id, batch_id, starts_at, ends_at, max_uses, max_uses_per_customer)
				SELECT $1, c, $3, $4, $5, $6, $7, $8 FROM unnest($2::text[]) c ON CONFLICT DO NOTHING`,
				tenant, codes, spec.PromotionID, b.ID, spec.StartsAt, spec.EndsAt, spec.MaxUses, spec.MaxUsesPerCustomer)
			if err != nil {
				return err
			}
			left -= int(tag.RowsAffected())
		}
		return store.Audit(ctx, tx, tenant, actor, "coupon.generate", b.ID, map[string]any{"promotion": spec.PromotionID, "prefix": prefix, "quantity": quantity})
	})
	return b, err
}

// ListBatches returns generated batches, newest first.
func (s *Store) ListBatches(ctx context.Context, tenant string) ([]Batch, error) {
	var out []Batch
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT b.id, b.promotion_id, COALESCE(NULLIF(p.name,''), p.rule->>'name', b.promotion_id), b.prefix, b.quantity, b.actor, b.created_at,
			(SELECT count(*) FROM coupons k WHERE k.tenant_id=b.tenant_id AND k.batch_id=b.id AND k.uses>0)
			FROM coupon_batches b LEFT JOIN promotions p ON p.tenant_id=b.tenant_id AND p.id=b.promotion_id
			WHERE b.tenant_id=$1 ORDER BY b.created_at DESC LIMIT 200`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Batch, error) {
			var b Batch
			return b, r.Scan(&b.ID, &b.PromotionID, &b.PromotionName, &b.Prefix, &b.Quantity, &b.Actor, &b.CreatedAt, &b.Used)
		})
		return err
	})
	return out, err
}

// CouponFilter narrows a coupon listing. Pagination is by code (keyset).
type CouponFilter struct {
	Promotion string
	Batch     string
	Query     string // code prefix
	State     string // "", active, disabled, used, unused
	After     string
	Limit     int
}

const couponWhere = `k.tenant_id=$1 AND ($2='' OR k.promotion_id=$2) AND ($3='' OR k.batch_id=$3) AND ($4='' OR k.code LIKE $4||'%')
	AND ($5='' OR ($5='active' AND k.status='active') OR ($5='disabled' AND k.status='disabled') OR ($5='used' AND k.uses>0) OR ($5='unused' AND k.uses=0))`

// ListCoupons returns one page of coupons and the total that match.
func (s *Store) ListCoupons(ctx context.Context, tenant string, f CouponFilter) (items []CouponView, total int64, err error) {
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM coupons k WHERE `+couponWhere, tenant, f.Promotion, f.Batch, f.Query, f.State).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+couponCols+`, COALESCE(NULLIF(p.name,''), p.rule->>'name', k.promotion_id)
			FROM coupons k JOIN promotions p ON p.tenant_id=k.tenant_id AND p.id=k.promotion_id
			WHERE `+couponWhere+` AND k.code > $6 ORDER BY k.code LIMIT $7`, tenant, f.Promotion, f.Batch, f.Query, f.State, f.After, f.Limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			v, err := scanCoupon(rows, &name)
			if err != nil {
				return err
			}
			v.PromotionName = name
			items = append(items, v)
		}
		return rows.Err()
	})
	return items, total, err
}

// ExportCoupons streams every coupon that matches f, in code order.
func (s *Store) ExportCoupons(ctx context.Context, tenant string, f CouponFilter, fn func(CouponView) error) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+couponCols+`, COALESCE(NULLIF(p.name,''), p.rule->>'name', k.promotion_id)
			FROM coupons k JOIN promotions p ON p.tenant_id=k.tenant_id AND p.id=k.promotion_id
			WHERE `+couponWhere+` ORDER BY k.code`, tenant, f.Promotion, f.Batch, f.Query, f.State)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			v, err := scanCoupon(rows, &name)
			if err != nil {
				return err
			}
			v.PromotionName = name
			if err := fn(v); err != nil {
				return err
			}
		}
		return rows.Err()
	})
}

// SetCouponStatus enables or disables codes; returns how many changed.
func (s *Store) SetCouponStatus(ctx context.Context, tenant, actor string, codes []string, status string) (int64, error) {
	var n int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE coupons SET status=$3 WHERE tenant_id=$1 AND code = ANY($2) AND status<>$3`, tenant, codes, status)
		if err != nil {
			return err
		}
		if n = tag.RowsAffected(); n == 0 {
			return nil
		}
		return store.Audit(ctx, tx, tenant, actor, "coupon.status", fmt.Sprintf("%d códigos", n), map[string]any{"status": status, "codes": head(codes, 50)})
	})
	return n, err
}

func head(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// DeleteCoupons removes codes that were never used; used ones are kept
// (ErrInUse when none could be deleted because all were used).
func (s *Store) DeleteCoupons(ctx context.Context, tenant, actor string, codes []string) (int64, error) {
	var n int64
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM coupons k WHERE k.tenant_id=$1 AND k.code = ANY($2) AND k.uses=0
			AND NOT EXISTS (SELECT 1 FROM promotion_redemptions r WHERE r.tenant_id=k.tenant_id AND r.coupon_code=k.code)`, tenant, codes)
		if err != nil {
			return err
		}
		if n = tag.RowsAffected(); n == 0 {
			var any bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM coupons WHERE tenant_id=$1 AND code = ANY($2))`, tenant, codes).Scan(&any); err != nil {
				return err
			}
			if any {
				return ErrInUse
			}
			return ErrNotFound
		}
		return store.Audit(ctx, tx, tenant, actor, "coupon.delete", fmt.Sprintf("%d códigos", n), map[string]any{"codes": head(codes, 50)})
	})
	return n, err
}

// Lookup resolves what the engine needs from the database for one cart: the
// typed coupons and, when the customer is known, how often they already used
// each promotion and code. It is only called when a coupon or an email is
// present, so anonymous carts stay on the in-memory path.
func (s *Store) Lookup(ctx context.Context, tenant string, codes []string, customerHash string) (*promo.Lookup, error) {
	lk := &promo.Lookup{Coupons: map[string]promo.Coupon{}, CustomerKnown: customerHash != "",
		CustomerUses: map[string]int64{}, CouponCustomerUses: map[string]int64{}}
	norm := make([]string, 0, len(codes))
	for _, c := range codes {
		if c = promo.NormalizeCode(c); c != "" {
			norm = append(norm, c)
		}
	}
	if len(norm) == 0 && customerHash == "" {
		return lk, nil
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if len(norm) > 0 {
			rows, err := tx.Query(ctx, `SELECT `+couponCols+` FROM coupons k WHERE k.tenant_id=$1 AND k.code = ANY($2)`, tenant, norm)
			if err != nil {
				return err
			}
			for rows.Next() {
				v, err := scanCoupon(rows)
				if err != nil {
					rows.Close()
					return err
				}
				lk.Coupons[v.Code] = v.Coupon
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
		if customerHash == "" {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT promotion_id, coupon_code FROM promotion_redemptions
			WHERE tenant_id=$1 AND customer_hash=$2 AND status<>'released' LIMIT 5000`, tenant, customerHash)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var pid, code string
			if err := rows.Scan(&pid, &code); err != nil {
				return err
			}
			lk.CustomerUses[pid]++
			if code != "" {
				lk.CouponCustomerUses[code]++
			}
		}
		return rows.Err()
	})
	return lk, err
}

// ---------- redemptions ----------

// RedemptionReq is one applied promotion to record for an order.
type RedemptionReq struct {
	PromotionID string
	Name        string
	Coupon      string
	CampaignID  string
	Amount      int64 // discount on items plus shipping, minor units
}

// LimitError says a promotion or coupon could not be reserved because a
// limit was reached between the simulation and the order.
type LimitError struct {
	PromotionID string
	Coupon      string
	Code        string // promo reason code
	Reason      string
}

func (e *LimitError) Error() string { return e.Reason }

// Redemption is the stored record of a promotion used by an order.
type Redemption struct {
	OrderID       string    `json:"orderId"`
	OrderNumber   int64     `json:"orderNumber,omitempty"`
	PromotionID   string    `json:"promotionId"`
	PromotionName string    `json:"promotionName"`
	Coupon        string    `json:"coupon,omitempty"`
	Amount        int64     `json:"amount"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
}

// ReserveTx records the promotions an order uses, inside the transaction that
// creates the order. It locks each promotion, coupon and campaign row (always
// in the same order, so concurrent checkouts cannot deadlock), re-checks every
// limit against committed data and increments the counters. When two
// checkouts race for the last use, exactly one gets it; the other receives a
// *LimitError and its order is never created.
func ReserveTx(ctx context.Context, tx pgx.Tx, tenant, orderID, customerHash, currency string, reqs []RedemptionReq) error {
	if len(reqs) == 0 {
		return nil
	}
	reqs = append([]RedemptionReq(nil), reqs...)
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].PromotionID < reqs[j].PromotionID })

	// 1. Promotions.
	for _, r := range reqs {
		var row promoRow
		var uses int64
		err := tx.QueryRow(ctx, `SELECT id, kind, rule, active, status, campaign_id, uses FROM promotions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, r.PromotionID).
			Scan(&row.id, &row.kind, &row.rule, &row.active, &row.status, &row.campaign, &uses)
		if errors.Is(err, pgx.ErrNoRows) {
			return &LimitError{PromotionID: r.PromotionID, Code: promo.CodeArchived, Reason: "La promoción «" + r.Name + "» ya no está disponible."}
		}
		if err != nil {
			return err
		}
		p, err := decode(row)
		if err != nil || p.Status != promo.StatusActive {
			return &LimitError{PromotionID: r.PromotionID, Code: promo.CodePaused, Reason: "La promoción «" + r.Name + "» ya no está disponible."}
		}
		c := p.Conditions
		if c.MaxUses > 0 && uses >= c.MaxUses {
			return &LimitError{PromotionID: p.ID, Code: promo.CodeUsageLimit, Reason: "La promoción «" + p.Name + "» alcanzó su límite de usos justo antes de tu compra."}
		}
		if c.MaxUsesPerCustomer > 0 {
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM promotion_redemptions WHERE tenant_id=$1 AND promotion_id=$2 AND customer_hash=$3 AND status<>'released'`,
				tenant, p.ID, customerHash).Scan(&n); err != nil {
				return err
			}
			if n >= c.MaxUsesPerCustomer {
				return &LimitError{PromotionID: p.ID, Code: promo.CodeCustomerLimit, Reason: "Ya usaste la promoción «" + p.Name + "» el máximo de veces permitido por cliente."}
			}
		}
	}
	// 2. Coupons.
	byCode := append([]RedemptionReq(nil), reqs...)
	sort.Slice(byCode, func(i, j int) bool { return byCode[i].Coupon < byCode[j].Coupon })
	for _, r := range byCode {
		if r.Coupon == "" {
			continue
		}
		v, err := scanCoupon(tx.QueryRow(ctx, `SELECT `+couponCols+` FROM coupons k WHERE k.tenant_id=$1 AND k.code=$2 FOR UPDATE`, tenant, r.Coupon))
		bad := func(code, reason string) error {
			return &LimitError{PromotionID: r.PromotionID, Coupon: r.Coupon, Code: code, Reason: reason}
		}
		now := time.Now()
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return bad(promo.CodeCouponNotFound, "El cupón "+r.Coupon+" no existe.")
		case err != nil:
			return err
		case v.PromotionID != r.PromotionID || v.Status != promo.StatusActive:
			return bad(promo.CodeCouponDisabled, "El cupón "+r.Coupon+" ya no está disponible.")
		case v.EndsAt != nil && now.After(*v.EndsAt):
			return bad(promo.CodeCouponExpired, "El cupón "+r.Coupon+" venció.")
		case v.StartsAt != nil && now.Before(*v.StartsAt):
			return bad(promo.CodeCouponNotYet, "El cupón "+r.Coupon+" todavía no se puede usar.")
		case v.MaxUses > 0 && v.Uses >= v.MaxUses:
			return bad(promo.CodeCouponExhausted, "El cupón "+r.Coupon+" alcanzó su límite de usos justo antes de tu compra.")
		}
		if v.MaxUsesPerCustomer > 0 {
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM promotion_redemptions WHERE tenant_id=$1 AND coupon_code=$2 AND customer_hash=$3 AND status<>'released'`,
				tenant, r.Coupon, customerHash).Scan(&n); err != nil {
				return err
			}
			if n >= v.MaxUsesPerCustomer {
				return bad(promo.CodeCouponCustomer, "Ya usaste el cupón "+r.Coupon+" el máximo de veces permitido.")
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE coupons SET uses=uses+1 WHERE tenant_id=$1 AND code=$2`, tenant, r.Coupon); err != nil {
			return err
		}
	}
	// 3. Campaign budgets.
	type budget struct{ amount, uses int64 }
	camps := map[string]*budget{}
	var campIDs []string
	for _, r := range reqs {
		if r.CampaignID == "" {
			continue
		}
		if camps[r.CampaignID] == nil {
			camps[r.CampaignID] = &budget{}
			campIDs = append(campIDs, r.CampaignID)
		}
		camps[r.CampaignID].amount += r.Amount
		camps[r.CampaignID].uses++
	}
	sort.Strings(campIDs)
	for _, id := range campIDs {
		var name, btype string
		var limit, spent, used int64
		err := tx.QueryRow(ctx, `SELECT name, budget_type, budget_limit, spent, used FROM campaigns WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).
			Scan(&name, &btype, &limit, &spent, &used)
		if errors.Is(err, pgx.ErrNoRows) {
			continue // the campaign was deleted; the promotion stands on its own
		}
		if err != nil {
			return err
		}
		b := camps[id]
		if (btype == "amount" && spent+b.amount > limit) || (btype == "uses" && used+b.uses > limit) {
			return &LimitError{Code: promo.CodeCampaignBudget, Reason: "La campaña «" + name + "» agotó su presupuesto justo antes de tu compra."}
		}
		if _, err := tx.Exec(ctx, `UPDATE campaigns SET spent=spent+$3, used=used+$4, updated_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, id, b.amount, b.uses); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, TopicCampaign, id, map[string]any{"usage": true}); err != nil {
			return err
		}
	}
	// 4. Counters and records.
	for _, r := range reqs {
		if _, err := tx.Exec(ctx, `UPDATE promotions SET uses=uses+1, discount_total=discount_total+$3 WHERE tenant_id=$1 AND id=$2`, tenant, r.PromotionID, r.Amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO promotion_redemptions (tenant_id, order_id, promotion_id, promotion_name, coupon_code, campaign_id, customer_hash, amount, currency, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'reserved')`, tenant, orderID, r.PromotionID, r.Name, r.Coupon, r.CampaignID, customerHash, r.Amount, currency); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, r.PromotionID, map[string]any{"usage": true, "order": orderID}); err != nil {
			return err
		}
	}
	return nil
}

// ConfirmTx marks the reservations of a paid order as redeemed. Idempotent.
func ConfirmTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) error {
	_, err := tx.Exec(ctx, `UPDATE promotion_redemptions SET status='redeemed', updated_at=now() WHERE tenant_id=$1 AND order_id=$2 AND status='reserved'`, tenant, orderID)
	return err
}

// ReleaseTx gives back every use an order holds (declined payment,
// cancellation or refund) inside the order's transaction. Idempotent: a
// second call finds nothing to release and changes no counter.
func ReleaseTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) error {
	rows, err := tx.Query(ctx, `UPDATE promotion_redemptions SET status='released', updated_at=now()
		WHERE tenant_id=$1 AND order_id=$2 AND status IN ('reserved','redeemed') RETURNING promotion_id, coupon_code, campaign_id, amount`, tenant, orderID)
	if err != nil {
		return err
	}
	rel, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RedemptionReq, error) {
		var x RedemptionReq
		return x, r.Scan(&x.PromotionID, &x.Coupon, &x.CampaignID, &x.Amount)
	})
	if err != nil || len(rel) == 0 {
		return err
	}
	sort.Slice(rel, func(i, j int) bool { return rel[i].PromotionID < rel[j].PromotionID })
	for _, r := range rel {
		if _, err := tx.Exec(ctx, `UPDATE promotions SET uses=GREATEST(uses-1,0), discount_total=GREATEST(discount_total-$3,0) WHERE tenant_id=$1 AND id=$2`,
			tenant, r.PromotionID, r.Amount); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, store.TopicPromotion, r.PromotionID, map[string]any{"usage": true, "order": orderID}); err != nil {
			return err
		}
	}
	byCode := append([]RedemptionReq(nil), rel...)
	sort.Slice(byCode, func(i, j int) bool { return byCode[i].Coupon < byCode[j].Coupon })
	for _, r := range byCode {
		if r.Coupon == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE coupons SET uses=GREATEST(uses-1,0) WHERE tenant_id=$1 AND code=$2`, tenant, r.Coupon); err != nil {
			return err
		}
	}
	byCamp := append([]RedemptionReq(nil), rel...)
	sort.Slice(byCamp, func(i, j int) bool { return byCamp[i].CampaignID < byCamp[j].CampaignID })
	for _, r := range byCamp {
		if r.CampaignID == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE campaigns SET spent=GREATEST(spent-$3,0), used=GREATEST(used-1,0), updated_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, r.CampaignID, r.Amount); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, TopicCampaign, r.CampaignID, map[string]any{"usage": true}); err != nil {
			return err
		}
	}
	return nil
}

// Redemptions lists the latest uses of a promotion (or of an order when
// promotionID is empty and orderID is set).
func (s *Store) Redemptions(ctx context.Context, tenant, promotionID, orderID string, limit int) ([]Redemption, error) {
	var out []Redemption
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT r.order_id, COALESCE(o.number,0), r.promotion_id, r.promotion_name, r.coupon_code, r.amount, r.currency, r.status, r.created_at
			FROM promotion_redemptions r LEFT JOIN orders o ON o.tenant_id=r.tenant_id AND o.id=r.order_id
			WHERE r.tenant_id=$1 AND ($2='' OR r.promotion_id=$2) AND ($3='' OR r.order_id=$3) ORDER BY r.created_at DESC LIMIT $4`, tenant, promotionID, orderID, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Redemption, error) {
			var x Redemption
			return x, r.Scan(&x.OrderID, &x.OrderNumber, &x.PromotionID, &x.PromotionName, &x.Coupon, &x.Amount, &x.Currency, &x.Status, &x.CreatedAt)
		})
		return err
	})
	return out, err
}
