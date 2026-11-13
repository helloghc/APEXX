package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// StockItem is one stock line of a bulk operation.
type StockItem struct {
	SKU      string `json:"sku"`
	Location string `json:"location"`
	OnHand   int64  `json:"onHand"`
}

// PriceItem is one base price (quantity 1) of a bulk operation; volume tiers
// above 1 are kept.
type PriceItem struct {
	SKU       string `json:"sku"`
	Amount    int64  `json:"amount"`
	CompareAt *int64 `json:"compareAt,omitempty"`
}

// BulkSetStockTx writes many stock levels in one statement.
func BulkSetStockTx(ctx context.Context, tx pgx.Tx, tenant string, items []StockItem) error {
	skus, locs, qty := make([]string, len(items)), make([]string, len(items)), make([]int64, len(items))
	for i, it := range items {
		skus[i], locs[i], qty[i] = it.SKU, it.Location, it.OnHand
	}
	if _, err := tx.Exec(ctx, `INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand)
		SELECT $1, s, l, q FROM unnest($2::text[], $3::text[], $4::bigint[]) AS x(s, l, q)
		ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET on_hand=EXCLUDED.on_hand, updated_at=now()`, tenant, skus, locs, qty); err != nil {
		return err
	}
	if err := resetSandboxStockMoves(ctx, tx, tenant, skus, locs); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, s := range skus {
		if !seen[s] {
			seen[s] = true
			if err := Emit(ctx, tx, tenant, TopicStock, s, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// BulkSetPricesTx writes many base prices of a channel in one statement.
func BulkSetPricesTx(ctx context.Context, tx pgx.Tx, tenant, actor, channel, currency string, items []PriceItem) error {
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true)`, actor); err != nil {
		return err
	}
	skus, amt, cmp := make([]string, len(items)), make([]int64, len(items)), make([]*int64, len(items))
	for i, it := range items {
		skus[i], amt[i], cmp[i] = it.SKU, it.Amount, it.CompareAt
	}
	if _, err := tx.Exec(ctx, `INSERT INTO prices (tenant_id, channel, sku, min_qty, amount, currency, compare_at)
		SELECT $1, $2, s, 1, a, $3, c FROM unnest($4::text[], $5::bigint[], $6::bigint[]) AS x(s, a, c)
		ON CONFLICT (tenant_id, channel, sku, min_qty) DO UPDATE SET amount=EXCLUDED.amount, currency=EXCLUDED.currency, compare_at=EXCLUDED.compare_at`,
		tenant, channel, currency, skus, amt, cmp); err != nil {
		return err
	}
	for _, s := range skus {
		if err := Emit(ctx, tx, tenant, TopicPrice, channel+"|"+s, nil); err != nil {
			return err
		}
	}
	return nil
}
