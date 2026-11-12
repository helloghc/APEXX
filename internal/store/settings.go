package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/sitecfg"
)

// TopicSettings is emitted when store configuration changes. The key says
// which part changed, so the projector reloads only that part.
const TopicSettings = "settings.changed"

// Keys of TopicSettings.
const (
	KeyShipping  = "shipping" // zones, profiles, carriers, options, holidays
	KeyLocations = "locations"
	KeyTax       = "tax"
	KeyProducts  = "products" // tax class and shipping profile per product
	KeyStore     = "store"
	KeyPolicies  = "policies"
	KeyMenus     = "menus"
	KeyRedirects = "redirects"
	KeyDomains   = "domains"
)

// docKind describes one table of JSON documents.
type docKind struct {
	table string
	topic string // TopicSettings key
}

var docKinds = map[string]docKind{
	"zones":     {"shipping_zones", KeyShipping},
	"profiles":  {"shipping_profiles", KeyShipping},
	"carriers":  {"carriers", KeyShipping},
	"options":   {"shipping_options", KeyShipping},
	"tax":       {"tax_regions", KeyTax},
	"locations": {"stock_locations", KeyLocations},
	"menus":     {"menus", KeyMenus},
}

// Doc is a stored JSON document.
type Doc struct {
	ID        string
	Data      json.RawMessage
	UpdatedAt time.Time
}

// PutDocTx upserts a document of kind inside tx and emits the change.
func PutDocTx(ctx context.Context, tx pgx.Tx, tenant, actor, kind, id string, data any) error {
	k, ok := docKinds[kind]
	if !ok {
		return fmt.Errorf("store: unknown document kind %q", kind)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO `+k.table+` (tenant_id, id, data) VALUES ($1,$2,$3)
		ON CONFLICT (tenant_id, id) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, tenant, id, b); err != nil {
		return err
	}
	if err := Emit(ctx, tx, tenant, TopicSettings, k.topic, map[string]any{"kind": kind, "id": id}); err != nil {
		return err
	}
	return Audit(ctx, tx, tenant, actor, "settings."+kind+".set", id, json.RawMessage(b))
}

// PutDoc upserts a document of kind.
func (d *DB) PutDoc(ctx context.Context, tenant, actor, kind, id string, data any) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error { return PutDocTx(ctx, tx, tenant, actor, kind, id, data) })
}

// DeleteDoc removes a document. guard runs first in the same transaction and
// can veto the delete (for example when the row is still referenced).
func (d *DB) DeleteDoc(ctx context.Context, tenant, actor, kind, id string, guard func(pgx.Tx) error) error {
	k, ok := docKinds[kind]
	if !ok {
		return fmt.Errorf("store: unknown document kind %q", kind)
	}
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if guard != nil {
			if err := guard(tx); err != nil {
				return err
			}
		}
		ct, err := tx.Exec(ctx, `DELETE FROM `+k.table+` WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, k.topic, map[string]any{"kind": kind, "id": id, "deleted": true}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings."+kind+".delete", id, map[string]any{})
	})
}

func docsTx(ctx context.Context, tx pgx.Tx, tenant, kind string) ([]Doc, error) {
	k, ok := docKinds[kind]
	if !ok {
		return nil, fmt.Errorf("store: unknown document kind %q", kind)
	}
	rows, err := tx.Query(ctx, `SELECT id, data, updated_at FROM `+k.table+` WHERE tenant_id=$1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Doc, error) {
		var x Doc
		var b []byte
		err := r.Scan(&x.ID, &b, &x.UpdatedAt)
		x.Data = b
		return x, err
	})
}

// docsInto decodes every document of kind into a slice of T, setting the id.
func docsInto[T any](ctx context.Context, tx pgx.Tx, tenant, kind string, setID func(*T, Doc)) ([]T, error) {
	docs, err := docsTx(ctx, tx, tenant, kind)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(docs))
	for _, x := range docs {
		var v T
		if err := json.Unmarshal(x.Data, &v); err != nil {
			return nil, fmt.Errorf("%s %s: %w", kind, x.ID, err)
		}
		setID(&v, x)
		out = append(out, v)
	}
	return out, nil
}

// ---------- logistics snapshot ----------

// LoadLogistics reads every shipping, location and tax rule of a store.
func (d *DB) LoadLogistics(ctx context.Context, tenant string) (*logistics.Config, error) {
	c := &logistics.Config{Products: map[string]logistics.ProductClass{}}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if c.Zones, err = docsInto(ctx, tx, tenant, "zones", func(v *logistics.Zone, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		if c.Profiles, err = docsInto(ctx, tx, tenant, "profiles", func(v *logistics.Profile, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		if c.Carriers, err = docsInto(ctx, tx, tenant, "carriers", func(v *logistics.Carrier, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		if c.Options, err = docsInto(ctx, tx, tenant, "options", func(v *logistics.Option, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		logistics.SortOptions(c.Options)
		if c.TaxRegions, err = docsInto(ctx, tx, tenant, "tax", func(v *logistics.TaxRegion, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		if c.Locations, err = docsInto(ctx, tx, tenant, "locations", func(v *logistics.Location, x Doc) { v.ID = x.ID }); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT to_char(day, 'YYYY-MM-DD'), name FROM holidays WHERE tenant_id=$1 ORDER BY day`, tenant)
		if err != nil {
			return err
		}
		if c.Holidays, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (logistics.Holiday, error) {
			var h logistics.Holiday
			return h, r.Scan(&h.Date, &h.Name)
		}); err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT product_id, tax_class, shipping_profile, hs_code, COALESCE(origin,'') FROM product_logistics WHERE tenant_id=$1`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var pc logistics.ProductClass
			if err := rows.Scan(&id, &pc.TaxClass, &pc.ShippingProfile, &pc.HSCode, &pc.Origin); err != nil {
				return err
			}
			c.Products[id] = pc
		}
		if err := rows.Err(); err != nil {
			return err
		}
		s, _, err := settingsTx(ctx, tx, tenant)
		if err != nil {
			return err
		}
		c.Timezone = s.Location()
		return nil
	})
	return c, err
}

// SetHoliday adds or renames a holiday.
func (d *DB) SetHoliday(ctx context.Context, tenant, actor string, hs ...logistics.Holiday) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, h := range hs {
			if _, err := tx.Exec(ctx, `INSERT INTO holidays (tenant_id, day, name) VALUES ($1,$2::date,$3)
				ON CONFLICT (tenant_id, day) DO UPDATE SET name=EXCLUDED.name`, tenant, h.Date, h.Name); err != nil {
				return err
			}
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyShipping, map[string]any{"kind": "holidays"}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.holidays.set", fmt.Sprint(len(hs)), hs)
	})
}

// DeleteHoliday removes a holiday.
func (d *DB) DeleteHoliday(ctx context.Context, tenant, actor, date string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM holidays WHERE tenant_id=$1 AND day=$2::date`, tenant, date)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyShipping, map[string]any{"kind": "holidays"}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.holidays.delete", date, map[string]any{})
	})
}

// SetProductClass stores the tax class and shipping profile of a product.
// A product back on the defaults is removed from the table.
func (d *DB) SetProductClass(ctx context.Context, tenant, actor, productID string, pc logistics.ProductClass) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if pc.TaxClass == logistics.TaxGeneral && pc.ShippingProfile == "" && pc.HSCode == "" && pc.Origin == "" {
			_, err = tx.Exec(ctx, `DELETE FROM product_logistics WHERE tenant_id=$1 AND product_id=$2`, tenant, productID)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO product_logistics (tenant_id, product_id, tax_class, shipping_profile, hs_code, origin) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
				ON CONFLICT (tenant_id, product_id) DO UPDATE SET tax_class=EXCLUDED.tax_class, shipping_profile=EXCLUDED.shipping_profile, hs_code=EXCLUDED.hs_code, origin=EXCLUDED.origin, updated_at=now()`,
				tenant, productID, pc.TaxClass, pc.ShippingProfile, pc.HSCode, pc.Origin)
		}
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyProducts, map[string]any{"product": productID}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.product_class.set", productID, pc)
	})
}

// ---------- locations, transfers, thresholds ----------

// LocationUsage says how much stock a location holds.
type LocationUsage struct {
	SKUs     int64 `json:"skus"`
	OnHand   int64 `json:"onHand"`
	Reserved int64 `json:"reserved"`
}

// LocationUsages returns stock totals per location id, including ids that
// have stock but no location row yet.
func (d *DB) LocationUsages(ctx context.Context, tenant string) (map[string]LocationUsage, error) {
	out := map[string]LocationUsage{}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT location_id, count(*), COALESCE(sum(on_hand),0), COALESCE(sum(reserved),0)
			FROM stock_levels WHERE tenant_id=$1 GROUP BY location_id`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var u LocationUsage
			if err := rows.Scan(&id, &u.SKUs, &u.OnHand, &u.Reserved); err != nil {
				return err
			}
			out[id] = u
		}
		return rows.Err()
	})
	return out, err
}

// LocationEmptyTx fails with ErrInUse when the location still holds stock.
func LocationEmptyTx(ctx context.Context, tx pgx.Tx, tenant, id string) error {
	var n int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM stock_levels WHERE tenant_id=$1 AND location_id=$2 AND (on_hand > 0 OR reserved > 0)`, tenant, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d SKU con existencias", ErrInUse, n)
	}
	_, err := tx.Exec(ctx, `DELETE FROM stock_levels WHERE tenant_id=$1 AND location_id=$2`, tenant, id)
	return err
}

// StockRow is the stock of one SKU at one location.
type StockRow struct {
	SKU       string `json:"sku"`
	Location  string `json:"locationId"`
	OnHand    int64  `json:"onHand"`
	Reserved  int64  `json:"reserved"`
	Available int64  `json:"available"`
	Threshold int64  `json:"threshold"`
	Custom    bool   `json:"customThreshold"` // threshold set for this SKU (not the store default)
}

// LocationStock lists the stock of a location, filtered by SKU prefix, after a cursor SKU.
func (d *DB) LocationStock(ctx context.Context, tenant, location, q, after string, limit int) ([]StockRow, error) {
	var out []StockRow
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sku, location_id, on_hand, reserved, on_hand - reserved FROM stock_levels
			WHERE tenant_id=$1 AND location_id=$2 AND sku > $3 AND ($4 = '' OR sku ILIKE '%' || $4 || '%')
			ORDER BY sku LIMIT $5`, tenant, location, after, likeEscape(q), limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (StockRow, error) {
			var s StockRow
			return s, r.Scan(&s.SKU, &s.Location, &s.OnHand, &s.Reserved, &s.Available)
		})
		return err
	})
	return out, err
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// LowStock lists SKU/location pairs whose available units are at or under
// their threshold (per SKU and location, per SKU, or the store default).
// The cursor is "sku|location" of the last row.
func (d *DB) LowStock(ctx context.Context, tenant string, def int64, location, after string, limit int) ([]StockRow, error) {
	aSKU, aLoc, _ := strings.Cut(after, "|")
	var out []StockRow
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT * FROM (
				SELECT l.sku, l.location_id, l.on_hand, l.reserved, l.on_hand - l.reserved AS avail,
				       COALESCE(tl.threshold, ts.threshold, $2) AS threshold, (tl.threshold IS NOT NULL OR ts.threshold IS NOT NULL) AS custom
				FROM stock_levels l
				LEFT JOIN stock_thresholds tl ON tl.tenant_id=l.tenant_id AND tl.sku=l.sku AND tl.location_id=l.location_id
				LEFT JOIN stock_thresholds ts ON ts.tenant_id=l.tenant_id AND ts.sku=l.sku AND ts.location_id=''
				WHERE l.tenant_id=$1 AND ($3 = '' OR l.location_id=$3)
			) x
			WHERE avail <= threshold AND threshold > 0 AND (sku, location_id) > ($4, $5)
			ORDER BY sku, location_id LIMIT $6`, tenant, def, location, aSKU, aLoc, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (StockRow, error) {
			var s StockRow
			return s, r.Scan(&s.SKU, &s.Location, &s.OnHand, &s.Reserved, &s.Available, &s.Threshold, &s.Custom)
		})
		return err
	})
	return out, err
}

// LowStockCount counts the rows LowStock would return.
func (d *DB) LowStockCount(ctx context.Context, tenant string, def int64, location string) (int64, error) {
	var n int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT count(*) FROM stock_levels l
			LEFT JOIN stock_thresholds tl ON tl.tenant_id=l.tenant_id AND tl.sku=l.sku AND tl.location_id=l.location_id
			LEFT JOIN stock_thresholds ts ON ts.tenant_id=l.tenant_id AND ts.sku=l.sku AND ts.location_id=''
			WHERE l.tenant_id=$1 AND ($3 = '' OR l.location_id=$3)
			  AND COALESCE(tl.threshold, ts.threshold, $2) > 0
			  AND l.on_hand - l.reserved <= COALESCE(tl.threshold, ts.threshold, $2)`, tenant, def, location).Scan(&n)
	})
	return n, err
}

// SetThreshold sets (or with threshold < 0 removes) the low-stock threshold of a SKU.
func (d *DB) SetThreshold(ctx context.Context, tenant, actor, sku, location string, threshold int64) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if threshold < 0 {
			_, err = tx.Exec(ctx, `DELETE FROM stock_thresholds WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`, tenant, sku, location)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO stock_thresholds (tenant_id, sku, location_id, threshold) VALUES ($1,$2,$3,$4)
				ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET threshold=EXCLUDED.threshold, updated_at=now()`, tenant, sku, location, threshold)
		}
		if err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "stock.threshold.set", sku, map[string]any{"location": location, "threshold": threshold})
	})
}

// TransferLine is one SKU of a transfer.
type TransferLine struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// Transfer is a movement of stock between two locations.
type Transfer struct {
	ID        string         `json:"id"`
	Number    int64          `json:"number"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Note      string         `json:"note"`
	Actor     string         `json:"actor"`
	Units     int64          `json:"units"`
	CreatedAt time.Time      `json:"createdAt"`
	Lines     []TransferLine `json:"lines,omitempty"`
}

// CreateTransfer moves stock between two locations atomically: rows are
// locked, the source must have the units available (not reserved), and both
// sides, the audit record and the events commit together or not at all.
func (d *DB) CreateTransfer(ctx context.Context, tenant, actor, from, to, note string, lines []TransferLine) (Transfer, error) {
	t := Transfer{ID: NewID("trf_"), From: from, To: to, Note: note, Actor: actor, Lines: lines}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, id := range []string{from, to} {
			var ok bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM stock_locations WHERE tenant_id=$1 AND id=$2)`, tenant, id).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: ubicación %s", ErrNotFound, id)
			}
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('transfer:' || $1))`, tenant); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(number),0)+1 FROM inventory_transfers WHERE tenant_id=$1`, tenant).Scan(&t.Number); err != nil {
			return err
		}
		for _, l := range lines {
			t.Units += l.Qty
		}
		if err := tx.QueryRow(ctx, `INSERT INTO inventory_transfers (tenant_id, id, number, from_location, to_location, note, actor, units)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, tenant, t.ID, t.Number, from, to, note, actor, t.Units).Scan(&t.CreatedAt); err != nil {
			return err
		}
		for i, l := range lines {
			var avail int64
			err := tx.QueryRow(ctx, `SELECT on_hand - reserved FROM stock_levels WHERE tenant_id=$1 AND sku=$2 AND location_id=$3 FOR UPDATE`, tenant, l.SKU, from).Scan(&avail)
			if errors.Is(err, pgx.ErrNoRows) {
				avail, err = 0, nil
			}
			if err != nil {
				return err
			}
			if avail < l.Qty {
				return fmt.Errorf("%w: %s tiene %d disponibles en %s y se pidieron %d", ErrInsufficient, l.SKU, avail, from, l.Qty)
			}
			if _, err := tx.Exec(ctx, `UPDATE stock_levels SET on_hand=on_hand-$4, updated_at=now() WHERE tenant_id=$1 AND sku=$2 AND location_id=$3`, tenant, l.SKU, from, l.Qty); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO stock_levels (tenant_id, sku, location_id, on_hand) VALUES ($1,$2,$3,$4)
				ON CONFLICT (tenant_id, sku, location_id) DO UPDATE SET on_hand=stock_levels.on_hand+EXCLUDED.on_hand, updated_at=now()`, tenant, l.SKU, to, l.Qty); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO inventory_transfer_lines (tenant_id, transfer_id, line, sku, qty) VALUES ($1,$2,$3,$4,$5)`, tenant, t.ID, i+1, l.SKU, l.Qty); err != nil {
				return err
			}
			if err := Emit(ctx, tx, tenant, TopicStock, l.SKU, nil); err != nil {
				return err
			}
		}
		return Audit(ctx, tx, tenant, actor, "stock.transfer", t.ID, map[string]any{"number": t.Number, "from": from, "to": to, "lines": lines, "note": note})
	})
	return t, err
}

// Transfers lists transfers, newest first; before pages by number (0 = newest).
func (d *DB) Transfers(ctx context.Context, tenant string, before int64, limit int) ([]Transfer, error) {
	var out []Transfer
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, number, from_location, to_location, note, actor, units, created_at FROM inventory_transfers
			WHERE tenant_id=$1 AND ($2 = 0 OR number < $2) ORDER BY number DESC LIMIT $3`, tenant, before, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanTransfer)
		return err
	})
	return out, err
}

func scanTransfer(r pgx.CollectableRow) (Transfer, error) {
	var t Transfer
	return t, r.Scan(&t.ID, &t.Number, &t.From, &t.To, &t.Note, &t.Actor, &t.Units, &t.CreatedAt)
}

// GetTransfer returns a transfer with its lines.
func (d *DB) GetTransfer(ctx context.Context, tenant, id string) (Transfer, error) {
	var t Transfer
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, number, from_location, to_location, note, actor, units, created_at FROM inventory_transfers WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		t, err = pgx.CollectExactlyOneRow(rows, scanTransfer)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT sku, qty FROM inventory_transfer_lines WHERE tenant_id=$1 AND transfer_id=$2 ORDER BY line`, tenant, id)
		if err != nil {
			return err
		}
		t.Lines, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (TransferLine, error) {
			var l TransferLine
			return l, r.Scan(&l.SKU, &l.Qty)
		})
		return err
	})
	return t, err
}

// ---------- store settings and assets ----------

func settingsTx(ctx context.Context, tx pgx.Tx, tenant string) (sitecfg.Settings, time.Time, error) {
	s := sitecfg.Default(tenant)
	var b []byte
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT data, updated_at FROM store_settings WHERE tenant_id=$1`, tenant).Scan(&b, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, at, nil
	}
	if err != nil {
		return s, at, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, at, err
	}
	if s.Social == nil {
		s.Social = map[string]string{}
	}
	return s, at, nil
}

// Settings returns the settings of a store (defaults when never saved).
func (d *DB) Settings(ctx context.Context, tenant string) (sitecfg.Settings, time.Time, error) {
	var s sitecfg.Settings
	var at time.Time
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		s, at, err = settingsTx(ctx, tx, tenant)
		return err
	})
	return s, at, err
}

// UpdateSettings applies fn to the current settings and stores the result,
// so two people editing different sections do not overwrite each other.
func (d *DB) UpdateSettings(ctx context.Context, tenant, actor string, fn func(*sitecfg.Settings) error) (sitecfg.Settings, error) {
	var s sitecfg.Settings
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('settings:' || $1))`, tenant); err != nil {
			return err
		}
		var err error
		if s, _, err = settingsTx(ctx, tx, tenant); err != nil {
			return err
		}
		if err := fn(&s); err != nil {
			return err
		}
		b, _ := json.Marshal(s)
		if _, err := tx.Exec(ctx, `INSERT INTO store_settings (tenant_id, data) VALUES ($1,$2)
			ON CONFLICT (tenant_id) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, tenant, b); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyStore, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.store.set", tenant, json.RawMessage(b))
	})
	return s, err
}

// Asset is a small public file of the store.
type Asset struct {
	ContentType string
	Body        []byte
	SHA         string
}

// PutAsset stores an asset and returns its content hash.
func (d *DB) PutAsset(ctx context.Context, tenant, actor, name, contentType string, body []byte) (string, error) {
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:8])
	return sha, d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO store_assets (tenant_id, name, content_type, body, sha) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, name) DO UPDATE SET content_type=EXCLUDED.content_type, body=EXCLUDED.body, sha=EXCLUDED.sha, updated_at=now()`,
			tenant, name, contentType, body, sha); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.asset.set", name, map[string]any{"bytes": len(body), "sha": sha})
	})
}

// DeleteAsset removes an asset.
func (d *DB) DeleteAsset(ctx context.Context, tenant, actor, name string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM store_assets WHERE tenant_id=$1 AND name=$2`, tenant, name); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.asset.delete", name, map[string]any{})
	})
}

// GetAsset reads an asset.
func (d *DB) GetAsset(ctx context.Context, tenant, name string) (Asset, error) {
	var a Asset
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT content_type, body, sha FROM store_assets WHERE tenant_id=$1 AND name=$2`, tenant, name).Scan(&a.ContentType, &a.Body, &a.SHA)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return a, err
}

// ---------- policies and menus ----------

// Policies lists the policy pages of a store.
func (d *DB) Policies(ctx context.Context, tenant string) ([]sitecfg.Policy, error) {
	var out []sitecfg.Policy
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT slug, title, body, published, updated_at FROM policies WHERE tenant_id=$1 ORDER BY slug`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (sitecfg.Policy, error) {
			var p sitecfg.Policy
			return p, r.Scan(&p.Slug, &p.Title, &p.Body, &p.Published, &p.UpdatedAt)
		})
		return err
	})
	return out, err
}

// PutPolicy stores a policy page (already validated and sanitized).
func (d *DB) PutPolicy(ctx context.Context, tenant, actor string, p sitecfg.Policy) (sitecfg.Policy, error) {
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO policies (tenant_id, slug, title, body, published) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, slug) DO UPDATE SET title=EXCLUDED.title, body=EXCLUDED.body, published=EXCLUDED.published, updated_at=now()
			RETURNING updated_at`, tenant, p.Slug, p.Title, p.Body, p.Published).Scan(&p.UpdatedAt); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyPolicies, map[string]any{"slug": p.Slug}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.policy.set", p.Slug, map[string]any{"title": p.Title, "published": p.Published, "bytes": len(p.Body)})
	})
	return p, err
}

// DeletePolicy removes a policy page.
func (d *DB) DeletePolicy(ctx context.Context, tenant, actor, slug string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM policies WHERE tenant_id=$1 AND slug=$2`, tenant, slug)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyPolicies, map[string]any{"slug": slug, "deleted": true}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.policy.delete", slug, map[string]any{})
	})
}

// Menus lists the navigation menus of a store.
func (d *DB) Menus(ctx context.Context, tenant string) ([]sitecfg.Menu, error) {
	var out []sitecfg.Menu
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = docsInto(ctx, tx, tenant, "menus", func(v *sitecfg.Menu, x Doc) { v.ID, v.UpdatedAt = x.ID, x.UpdatedAt })
		return err
	})
	return out, err
}

// ---------- redirects ----------

const redirectCols = `id, from_path, target, code, active, created_at`

func scanRedirect(r pgx.CollectableRow) (sitecfg.Redirect, error) {
	var x sitecfg.Redirect
	return x, r.Scan(&x.ID, &x.From, &x.Target, &x.Code, &x.Active, &x.CreatedAt)
}

// Redirects lists redirects ordered by path. q filters by substring of either
// side; after is the last path of the previous page; limit 0 = all.
func (d *DB) Redirects(ctx context.Context, tenant, q, after string, limit int) ([]sitecfg.Redirect, int64, error) {
	var out []sitecfg.Redirect
	var total int64
	if limit <= 0 {
		limit = 1 << 30
	}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		const where = ` WHERE tenant_id=$1 AND ($2 = '' OR from_path ILIKE '%' || $2 || '%' OR target ILIKE '%' || $2 || '%')`
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM redirects`+where, tenant, likeEscape(q)).Scan(&total); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+redirectCols+` FROM redirects`+where+` AND from_path > $3 ORDER BY from_path LIMIT $4`, tenant, likeEscape(q), after, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanRedirect)
		return err
	})
	return out, total, err
}

// SaveRedirects creates (ID empty) or updates redirects in one transaction.
// A repeated origin path is ErrConflict; a redirect loop is rejected by check,
// which sees the whole set as it will be after the change.
func (d *DB) SaveRedirects(ctx context.Context, tenant, actor string, rs []sitecfg.Redirect, check func(all map[string]sitecfg.Redirect) error) ([]sitecfg.Redirect, error) {
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for i := range rs {
			r := &rs[i]
			var err error
			if r.ID == "" {
				r.ID = NewID("rd_")
				err = tx.QueryRow(ctx, `INSERT INTO redirects (tenant_id, id, from_path, target, code, active) VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
					tenant, r.ID, r.From, r.Target, r.Code, r.Active).Scan(&r.CreatedAt)
			} else {
				err = tx.QueryRow(ctx, `UPDATE redirects SET from_path=$3, target=$4, code=$5, active=$6 WHERE tenant_id=$1 AND id=$2 RETURNING created_at`,
					tenant, r.ID, r.From, r.Target, r.Code, r.Active).Scan(&r.CreatedAt)
				if errors.Is(err, pgx.ErrNoRows) {
					return ErrNotFound
				}
			}
			if isUnique(err) {
				return fmt.Errorf("%w: ya existe una redirección desde %s", ErrConflict, r.From)
			}
			if err != nil {
				return err
			}
		}
		if check != nil {
			rows, err := tx.Query(ctx, `SELECT `+redirectCols+` FROM redirects WHERE tenant_id=$1`, tenant)
			if err != nil {
				return err
			}
			all, err := pgx.CollectRows(rows, scanRedirect)
			if err != nil {
				return err
			}
			m := make(map[string]sitecfg.Redirect, len(all))
			for _, x := range all {
				m[x.From] = x
			}
			if err := check(m); err != nil {
				return err
			}
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyRedirects, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.redirects.set", fmt.Sprint(len(rs)), rs)
	})
	return rs, err
}

// DeleteRedirects removes redirects by id and returns how many existed.
func (d *DB) DeleteRedirects(ctx context.Context, tenant, actor string, ids []string) (int64, error) {
	var n int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM redirects WHERE tenant_id=$1 AND id = ANY($2)`, tenant, ids)
		if err != nil {
			return err
		}
		n = ct.RowsAffected()
		if n == 0 {
			return nil
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyRedirects, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.redirects.delete", fmt.Sprint(n), ids)
	})
	return n, err
}

// ---------- domains ----------

const domainCols = `id, hostname, site, status, token, is_primary, last_error, last_checked_at, verified_at, created_at`

func scanDomain(r pgx.CollectableRow) (sitecfg.Domain, error) {
	var x sitecfg.Domain
	return x, r.Scan(&x.ID, &x.Hostname, &x.Site, &x.Status, &x.Token, &x.Primary, &x.LastError, &x.LastCheckedAt, &x.VerifiedAt, &x.CreatedAt)
}

// Domains lists the custom domains of a store.
func (d *DB) Domains(ctx context.Context, tenant string) ([]sitecfg.Domain, error) {
	var out []sitecfg.Domain
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+domainCols+` FROM domains WHERE tenant_id=$1 ORDER BY created_at, hostname`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanDomain)
		return err
	})
	return out, err
}

// GetDomain returns one domain.
func (d *DB) GetDomain(ctx context.Context, tenant, id string) (sitecfg.Domain, error) {
	var out sitecfg.Domain
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+domainCols+` FROM domains WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		out, err = pgx.CollectExactlyOneRow(rows, scanDomain)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return out, err
}

// CreateDomain registers a pending domain with a fresh verification token.
func (d *DB) CreateDomain(ctx context.Context, tenant, actor, hostname, site string) (sitecfg.Domain, error) {
	tok := make([]byte, 16)
	_, _ = rand.Read(tok)
	dom := sitecfg.Domain{ID: NewID("dom_"), Hostname: hostname, Site: site, Status: "pending", Token: hex.EncodeToString(tok)}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO domains (tenant_id, id, hostname, site, token) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
			tenant, dom.ID, hostname, site, dom.Token).Scan(&dom.CreatedAt)
		if isUnique(err) {
			return fmt.Errorf("%w: el dominio ya está en tu lista", ErrConflict)
		}
		if err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.domain.create", hostname, map[string]any{"site": site})
	})
	return dom, err
}

// SetDomainStatus records the result of a verification. A domain already
// verified by another store is ErrConflict.
func (d *DB) SetDomainStatus(ctx context.Context, tenant, actor, id string, verified bool, lastError string) (sitecfg.Domain, error) {
	var out sitecfg.Domain
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		status := "failed"
		if verified {
			status = "verified"
		}
		rows, err := tx.Query(ctx, `UPDATE domains SET status=$3, last_error=$4, last_checked_at=now(),
				verified_at=CASE WHEN $3='verified' THEN COALESCE(verified_at, now()) ELSE NULL END,
				is_primary=CASE WHEN $3='verified' THEN is_primary ELSE false END
			WHERE tenant_id=$1 AND id=$2 RETURNING `+domainCols, tenant, id, status, lastError)
		if err != nil {
			return err
		}
		out, err = pgx.CollectExactlyOneRow(rows, scanDomain)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if isUnique(err) {
			return fmt.Errorf("%w: otra tienda ya verificó este dominio", ErrConflict)
		}
		if err != nil {
			return err
		}
		if verified {
			// The first verified domain becomes the primary one.
			var has bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM domains WHERE tenant_id=$1 AND is_primary)`, tenant).Scan(&has); err != nil {
				return err
			}
			if !has {
				if _, err := tx.Exec(ctx, `UPDATE domains SET is_primary=true WHERE tenant_id=$1 AND id=$2`, tenant, id); err != nil {
					return err
				}
				out.Primary = true
			}
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyDomains, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.domain.verify", out.Hostname, map[string]any{"status": status, "error": lastError})
	})
	return out, err
}

// SetPrimaryDomain makes a verified domain the canonical one.
func (d *DB) SetPrimaryDomain(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var status, host string
		err := tx.QueryRow(ctx, `SELECT status, hostname FROM domains WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&status, &host)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "verified" {
			return fmt.Errorf("%w: verifica el dominio antes de hacerlo principal", ErrConflict)
		}
		if _, err := tx.Exec(ctx, `UPDATE domains SET is_primary=(id=$2) WHERE tenant_id=$1`, tenant, id); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyDomains, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.domain.primary", host, map[string]any{})
	})
}

// DeleteDomain removes a domain; its traffic stops being routed at once.
func (d *DB) DeleteDomain(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var host string
		err := tx.QueryRow(ctx, `DELETE FROM domains WHERE tenant_id=$1 AND id=$2 RETURNING hostname`, tenant, id).Scan(&host)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicSettings, KeyDomains, nil); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "settings.domain.delete", host, map[string]any{})
	})
}

// HostTarget is where a verified hostname points.
type HostTarget struct {
	Tenant  string
	Site    string
	Primary bool
}

// VerifiedHosts returns every verified hostname of the platform (system pool:
// routing by Host happens before any tenant is known).
func (d *DB) VerifiedHosts(ctx context.Context) (map[string]HostTarget, error) {
	rows, err := d.System.Query(ctx, `SELECT hostname, tenant_id, site, is_primary FROM domains WHERE status='verified'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]HostTarget{}
	for rows.Next() {
		var h string
		var t HostTarget
		if err := rows.Scan(&h, &t.Tenant, &t.Site, &t.Primary); err != nil {
			return nil, err
		}
		out[h] = t
	}
	return out, rows.Err()
}
