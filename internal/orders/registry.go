package orders

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// registrySyncTx keeps what a gift registry shows as bought equal to the
// paid, not cancelled and not returned units of the orders placed for it, so
// a cancellation or a return puts the gift back on the list.
func registrySyncTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	var reg string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(registry_id,'') FROM orders WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&reg); err != nil || reg == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE gift_registry_items i SET bought = COALESCE((
			SELECT sum(l.qty - l.cancelled_qty - l.returned_qty) FROM order_lines l JOIN orders o ON o.tenant_id=l.tenant_id AND o.id=l.order_id
			WHERE o.tenant_id=$1 AND o.registry_id=$2 AND o.status IN ('paid','fulfilled') AND l.sku=i.sku), 0)
		WHERE i.tenant_id=$1 AND i.registry_id=$2`, tenant, reg)
	return err
}
