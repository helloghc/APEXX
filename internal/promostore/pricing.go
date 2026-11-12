package promostore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/helloghc/apexx/internal/money"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/promo"
	"github.com/helloghc/apexx/internal/store"
)

// PriceListItem is one SKU of a price list. Amount is the override price;
// it is ignored (null) for percentage lists.
type PriceListItem struct {
	SKU    string `json:"sku"`
	Amount *int64 `json:"amount,omitempty"`
	// MinQty: the price applies from this quantity upward (volume pricing).
	MinQty int64 `json:"minQty,omitempty"`
}

// PriceList is a scheduled override or percentage adjustment.
type PriceList struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Description   string          `json:"description,omitempty"`
	Status        string          `json:"status"` // active | paused
	Channel       string          `json:"channel"`
	CustomerGroup string          `json:"customerGroup"`
	Kind          string          `json:"kind"`      // override | adjust
	AdjustBps     int64           `json:"adjustBps"` // adjust: -1000 = 10% cheaper
	AppliesTo     string          `json:"appliesTo"` // all | items
	Priority      int             `json:"priority"`
	StartsAt      *time.Time      `json:"startsAt,omitempty"`
	EndsAt        *time.Time      `json:"endsAt,omitempty"`
	Currency      string          `json:"currency"`
	Items         []PriceListItem `json:"items"`
	ItemCount     int             `json:"itemCount"`
	DisplayStatus string          `json:"displayStatus"`
	Summary       string          `json:"summary"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// MaxListItems bounds one price list.
const MaxListItems = 5000

// Normalize applies defaults.
func (l *PriceList) Normalize() {
	l.Name, l.Description = strings.TrimSpace(l.Name), strings.TrimSpace(l.Description)
	l.Channel, l.CustomerGroup = strings.TrimSpace(l.Channel), strings.ToLower(strings.TrimSpace(l.CustomerGroup))
	if l.Status == "" {
		l.Status = promo.StatusActive
	}
	if l.Currency == "" {
		l.Currency = "MXN"
	}
	if l.Kind == "override" {
		l.AdjustBps, l.AppliesTo = 0, "items"
	} else if l.AppliesTo == "" {
		l.AppliesTo = "all"
	}
	seen := map[string]bool{}
	items := l.Items[:0:0]
	for _, it := range l.Items {
		it.SKU = strings.TrimSpace(it.SKU)
		if it.MinQty < 1 {
			it.MinQty = 1
		}
		// The same line sent twice is kept once; two prices for the same SKU
		// and quantity are left for Validate to report.
		amt := int64(-1)
		if it.Amount != nil {
			amt = *it.Amount
		}
		k := fmt.Sprintf("%s|%d|%d", it.SKU, it.MinQty, amt)
		if it.SKU == "" || seen[k] {
			continue
		}
		seen[k] = true
		if l.Kind != "override" {
			it.Amount = nil
		}
		items = append(items, it)
	}
	if l.Kind == "adjust" && l.AppliesTo == "all" {
		items = nil
	}
	l.Items = items
}

// Validate checks a normalized list.
func (l *PriceList) Validate() []promo.FieldError {
	var errs []promo.FieldError
	add := func(f, m string) { errs = append(errs, promo.FieldError{Field: f, Message: m}) }
	if !promo.ValidID(l.ID) {
		add("id", "Usa minúsculas, números y guiones (por ejemplo mayoreo-2026).")
	}
	if l.Name == "" || len(l.Name) > 120 {
		add("name", "Escribe un nombre de hasta 120 caracteres.")
	}
	if l.Status != promo.StatusActive && l.Status != promo.StatusPaused {
		add("status", "Estado no válido: usa active o paused.")
	}
	switch l.Kind {
	case "override":
		if len(l.Items) == 0 {
			add("items", "Agrega al menos un producto con su precio.")
		}
		seen := map[string]bool{}
		for i, it := range l.Items {
			if it.Amount == nil || *it.Amount < 0 || *it.Amount > 1_000_000_000_00 {
				add(fmt.Sprintf("items[%d].amount", i), "Escribe el precio de "+it.SKU+".")
			}
			if it.MinQty < 0 || it.MinQty > 1_000_000 {
				add(fmt.Sprintf("items[%d].minQty", i), "La cantidad mínima va de 1 a 1,000,000.")
			}
			k := fmt.Sprintf("%s|%d", it.SKU, max(it.MinQty, 1))
			if seen[k] {
				add(fmt.Sprintf("items[%d].minQty", i), "Ya hay un precio de "+it.SKU+" desde esa cantidad.")
			}
			seen[k] = true
		}
	case "adjust":
		for i, it := range l.Items {
			if it.MinQty > 1 {
				add(fmt.Sprintf("items[%d].minQty", i), "Los precios por volumen solo existen en listas de precio fijo.")
			}
		}
		if l.AdjustBps == 0 || l.AdjustBps < -10000 || l.AdjustBps > 100000 {
			add("adjustBps", "El ajuste va de -100% a +1000% y no puede ser 0.")
		}
		if l.AppliesTo != "all" && l.AppliesTo != "items" {
			add("appliesTo", "Elige todo el catálogo o productos seleccionados.")
		}
		if l.AppliesTo == "items" && len(l.Items) == 0 {
			add("items", "Agrega al menos un producto.")
		}
	default:
		add("kind", "Elige precio fijo (override) o ajuste porcentual (adjust).")
	}
	if len(l.Items) > MaxListItems {
		add("items", fmt.Sprintf("Una lista admite hasta %d productos.", MaxListItems))
	}
	if l.StartsAt != nil && l.EndsAt != nil && !l.EndsAt.After(*l.StartsAt) {
		add("endsAt", "La fecha de fin debe ser posterior a la de inicio.")
	}
	if l.Priority < -1000 || l.Priority > 1000 {
		add("priority", "La prioridad va de -1000 a 1000.")
	}
	return errs
}

func (l *PriceList) decorate(now time.Time) {
	switch {
	case l.Status == promo.StatusPaused:
		l.DisplayStatus = "pausada"
	case l.EndsAt != nil && now.After(*l.EndsAt):
		l.DisplayStatus = "vencida"
	case l.StartsAt != nil && now.Before(*l.StartsAt):
		l.DisplayStatus = "programada"
	default:
		l.DisplayStatus = "activa"
	}
	var s string
	if l.Kind == "override" {
		s = fmt.Sprintf("Precio especial en %s", countWord(l.ItemCount))
	} else {
		dir := "más barato"
		bps := l.AdjustBps
		if bps > 0 {
			dir = "más caro"
		} else {
			bps = -bps
		}
		s = promo.Percent(bps) + " " + dir
		if l.AppliesTo == "all" {
			s += " en todo el catálogo"
		} else {
			s += " en " + countWord(l.ItemCount)
		}
	}
	if l.Channel != "" {
		s += ", canal " + l.Channel
	}
	if l.CustomerGroup != "" {
		s += ", grupo " + l.CustomerGroup
	}
	if w := promo.Window(l.StartsAt, l.EndsAt); w != "" {
		s += ", " + w
	}
	l.Summary = s + "."
}

func countWord(n int) string {
	if n == 1 {
		return "1 producto"
	}
	return fmt.Sprintf("%d productos", n)
}

const listCols = `l.id, l.name, l.description, l.status, l.channel, l.customer_group, l.kind, l.adjust_bps, l.applies_to, l.priority, l.starts_at, l.ends_at, l.created_at, l.updated_at,
	(SELECT count(*) FROM price_list_items i WHERE i.tenant_id=l.tenant_id AND i.list_id=l.id)`

func scanList(r pgx.Row) (PriceList, error) {
	var l PriceList
	err := r.Scan(&l.ID, &l.Name, &l.Description, &l.Status, &l.Channel, &l.CustomerGroup, &l.Kind, &l.AdjustBps, &l.AppliesTo, &l.Priority,
		&l.StartsAt, &l.EndsAt, &l.CreatedAt, &l.UpdatedAt, &l.ItemCount)
	l.Currency = "MXN"
	l.Items = []PriceListItem{}
	l.decorate(time.Now())
	return l, err
}

// SavePriceList creates or replaces a list with its items.
func (s *Store) SavePriceList(ctx context.Context, tenant, actor string, l PriceList, create bool) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `INSERT INTO price_lists (tenant_id, id, name, description, status, channel, customer_group, kind, adjust_bps, applies_to, priority, starts_at, ends_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
		if create {
			q += ` ON CONFLICT (tenant_id, id) DO NOTHING`
		} else {
			q += ` ON CONFLICT (tenant_id, id) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description, status=EXCLUDED.status, channel=EXCLUDED.channel,
				customer_group=EXCLUDED.customer_group, kind=EXCLUDED.kind, adjust_bps=EXCLUDED.adjust_bps, applies_to=EXCLUDED.applies_to, priority=EXCLUDED.priority,
				starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at, updated_at=now()`
		}
		tag, err := tx.Exec(ctx, q, tenant, l.ID, l.Name, l.Description, l.Status, l.Channel, l.CustomerGroup, l.Kind, l.AdjustBps, l.AppliesTo, l.Priority, l.StartsAt, l.EndsAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrExists
		}
		if _, err := tx.Exec(ctx, `DELETE FROM price_list_items WHERE tenant_id=$1 AND list_id=$2`, tenant, l.ID); err != nil {
			return err
		}
		skus := make([]string, len(l.Items))
		amounts := make([]*int64, len(l.Items))
		mins := make([]int64, len(l.Items))
		for i, it := range l.Items {
			skus[i], amounts[i], mins[i] = it.SKU, it.Amount, max(it.MinQty, 1)
		}
		if len(skus) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO price_list_items (tenant_id, list_id, sku, amount, currency, min_qty)
				SELECT $1, $2, x.sku, x.amount, $5, x.min_qty FROM unnest($3::text[], $4::bigint[], $6::bigint[]) AS x(sku, amount, min_qty)`, tenant, l.ID, skus, amounts, l.Currency, mins); err != nil {
				return err
			}
		}
		if err := store.Emit(ctx, tx, tenant, TopicPriceList, l.ID, nil); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "pricelist.set", l.ID, map[string]any{"name": l.Name, "kind": l.Kind, "status": l.Status,
			"adjustBps": l.AdjustBps, "items": len(l.Items), "channel": l.Channel, "priority": l.Priority})
	})
}

// DeletePriceList removes a list.
func (s *Store) DeletePriceList(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM price_lists WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, TopicPriceList, id, map[string]any{"deleted": true}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "pricelist.delete", id, nil)
	})
}

// ListPriceLists returns every list without items.
func (s *Store) ListPriceLists(ctx context.Context, tenant string) ([]PriceList, error) {
	var out []PriceList
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+listCols+` FROM price_lists l WHERE l.tenant_id=$1 ORDER BY l.priority DESC, l.id`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceList, error) { return scanList(r) })
		return err
	})
	return out, err
}

// GetPriceList loads a list with its items.
func (s *Store) GetPriceList(ctx context.Context, tenant, id string) (PriceList, error) {
	var l PriceList
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		l, err = scanList(tx.QueryRow(ctx, `SELECT `+listCols+` FROM price_lists l WHERE l.tenant_id=$1 AND l.id=$2`, tenant, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT sku, amount, min_qty FROM price_list_items WHERE tenant_id=$1 AND list_id=$2 ORDER BY sku, min_qty`, tenant, id)
		if err != nil {
			return err
		}
		l.Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceListItem, error) {
			var it PriceListItem
			return it, r.Scan(&it.SKU, &it.Amount, &it.MinQty)
		})
		return err
	})
	return l, err
}

// LoadPriceLists reads lists for the projector (system pool). With id it
// returns at most that one.
func (s *Store) LoadPriceLists(ctx context.Context, tenant, id string) ([]pricing.List, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT id, name, status, channel, customer_group, kind, adjust_bps, applies_to, priority, starts_at, ends_at
		FROM price_lists WHERE tenant_id=$1 AND ($2='' OR id=$2)`, tenant, id)
	if err != nil {
		return nil, err
	}
	lists, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pricing.List, error) {
		var l pricing.List
		var status, applies string
		err := r.Scan(&l.ID, &l.Name, &status, &l.Channel, &l.CustomerGroup, &l.Kind, &l.AdjustBps, &applies, &l.Priority, &l.StartsAt, &l.EndsAt)
		l.Active, l.All = status == promo.StatusActive, applies == "all"
		l.Items = map[string]int64{}
		return l, err
	})
	if err != nil {
		return nil, err
	}
	for i := range lists {
		rows, err := s.DB.System.Query(ctx, `SELECT sku, COALESCE(amount,0), currency, min_qty FROM price_list_items WHERE tenant_id=$1 AND list_id=$2 ORDER BY sku, min_qty`, tenant, lists[i].ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sku, cur string
			var amt, minQty int64
			if err := rows.Scan(&sku, &amt, &cur, &minQty); err != nil {
				rows.Close()
				return nil, err
			}
			if minQty <= 1 {
				lists[i].Items[sku] = amt
			} else {
				if lists[i].Tiers == nil {
					lists[i].Tiers = map[string][]pricing.ListTier{}
				}
				lists[i].Tiers[sku] = append(lists[i].Tiers[sku], pricing.ListTier{MinQty: minQty, Amount: amt})
			}
			lists[i].Currency = money.Currency(cur)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return lists, nil
}

// ---------- bulk price change ----------

// BulkChange describes how to change prices in bulk.
type BulkChange struct {
	Channel   string `json:"channel"`
	Mode      string `json:"mode"`      // percent | fixed | set
	Value     int64  `json:"value"`     // percent: signed bps; fixed: signed minor units; set: minor units
	Rounding  string `json:"rounding"`  // none | peso | ten | ninety
	CompareAt string `json:"compareAt"` // keep | previous | clear
	Tiers     string `json:"tiers"`     // all | base (only the price from 1 unit)
}

// MaxBulkSKUs bounds one bulk change.
const MaxBulkSKUs = 20000

// Validate checks the change.
func (c *BulkChange) Validate() []promo.FieldError {
	var errs []promo.FieldError
	add := func(f, m string) { errs = append(errs, promo.FieldError{Field: f, Message: m}) }
	if c.Channel == "" {
		add("channel", "Elige el canal de venta.")
	}
	switch c.Mode {
	case "percent":
		if c.Value == 0 || c.Value < -9900 || c.Value > 100000 {
			add("value", "El porcentaje va de -99% a +1000% y no puede ser 0.")
		}
	case "fixed":
		if c.Value == 0 || c.Value < -1_000_000_000_00 || c.Value > 1_000_000_000_00 {
			add("value", "Escribe un monto distinto de cero.")
		}
	case "set":
		if c.Value < 0 || c.Value > 1_000_000_000_00 {
			add("value", "Escribe un precio válido.")
		}
	default:
		add("mode", "Elige porcentaje, monto fijo o precio nuevo.")
	}
	if c.Rounding == "" {
		c.Rounding = "none"
	}
	if c.CompareAt == "" {
		c.CompareAt = "keep"
	}
	if c.Tiers == "" {
		c.Tiers = "all"
	}
	if !in(c.Rounding, "none", "peso", "ten", "ninety") {
		add("rounding", "Redondeo no válido.")
	}
	if !in(c.CompareAt, "keep", "previous", "clear") {
		add("compareAt", "Opción de precio anterior no válida.")
	}
	if !in(c.Tiers, "all", "base") {
		add("tiers", "Opción de escalas no válida.")
	}
	return errs
}

func in(v string, opts ...string) bool {
	for _, o := range opts {
		if v == o {
			return true
		}
	}
	return false
}

// Apply returns the new amount for old.
func (c *BulkChange) Apply(old int64) int64 {
	var n int64
	switch c.Mode {
	case "percent":
		n = old + roundDiv(old*c.Value, 10000)
	case "fixed":
		n = old + c.Value
	default:
		n = c.Value
	}
	if n < 0 {
		n = 0
	}
	switch c.Rounding {
	case "peso":
		n = roundDiv(n, 100) * 100
	case "ten":
		n = roundDiv(n, 1000) * 1000
	case "ninety": // ends in .90: 1,234.56 -> 1,234.90
		n = (n/100)*100 + 90
	}
	return n
}

// roundDiv divides rounding half away from zero.
func roundDiv(a, b int64) int64 {
	if a >= 0 {
		return (a + b/2) / b
	}
	return -((-a + b/2) / b)
}

// BulkRow is one price that would change.
type BulkRow struct {
	SKU        string `json:"sku"`
	Title      string `json:"title,omitempty"`
	MinQty     int64  `json:"minQty"`
	Old        int64  `json:"old"`
	New        int64  `json:"new"`
	OldCompare *int64 `json:"oldCompareAt,omitempty"`
	NewCompare *int64 `json:"newCompareAt,omitempty"`
	Currency   string `json:"currency"`
}

// BulkResult is a preview or the outcome of applying a bulk change.
type BulkResult struct {
	ID         string    `json:"id,omitempty"` // set once applied; shown in the price history
	Hash       string    `json:"hash"`         // fingerprint of the diff; apply must send it back
	SKUs       int       `json:"skus"`         // SKUs in scope
	Changed    int       `json:"changed"`      // prices that change
	Unchanged  int       `json:"unchanged"`
	NoPrice    int       `json:"noPrice"` // SKUs in scope without a price in the channel
	Increases  int       `json:"increases"`
	Decreases  int       `json:"decreases"`
	OldTotal   int64     `json:"oldTotal"`
	NewTotal   int64     `json:"newTotal"`
	Rows       []BulkRow `json:"rows"` // first rows of the diff
	Truncated  bool      `json:"truncated"`
	Applied    bool      `json:"applied"`
	MaxPreview int       `json:"-"`
}

// BulkPrices computes the diff of a bulk change over skus and, when
// expectedHash is not empty, applies it atomically: the diff is recomputed
// inside the transaction with the rows locked and must match the preview the
// person approved (ErrChanged otherwise). Every change lands in the price
// history with the same reference.
func (s *Store) BulkPrices(ctx context.Context, tenant, actor string, c BulkChange, skus []string, titles map[string]string, expectedHash string) (BulkResult, error) {
	res := BulkResult{SKUs: len(skus), Rows: []BulkRow{}}
	apply := expectedHash != ""
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		q := `SELECT sku, min_qty, amount, compare_at, currency FROM prices WHERE tenant_id=$1 AND channel=$2 AND sku = ANY($3) ORDER BY sku, min_qty`
		if apply {
			q += ` FOR UPDATE`
		}
		rows, err := tx.Query(ctx, q, tenant, c.Channel, skus)
		if err != nil {
			return err
		}
		all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BulkRow, error) {
			var b BulkRow
			return b, r.Scan(&b.SKU, &b.MinQty, &b.Old, &b.OldCompare, &b.Currency)
		})
		if err != nil {
			return err
		}
		priced := map[string]bool{}
		h := sha256.New()
		var changed []BulkRow
		for _, b := range all {
			priced[b.SKU] = true
			if c.Tiers == "base" && b.MinQty != 1 {
				continue
			}
			b.New = c.Apply(b.Old)
			b.NewCompare = b.OldCompare
			switch c.CompareAt {
			case "clear":
				b.NewCompare = nil
			case "previous":
				if b.New < b.Old {
					prev := b.Old
					if b.OldCompare != nil && *b.OldCompare > prev {
						prev = *b.OldCompare
					}
					b.NewCompare = &prev
				} else {
					b.NewCompare = nil
				}
			}
			if b.NewCompare != nil && *b.NewCompare <= b.New {
				b.NewCompare = nil // a "before" price must be higher than the price
			}
			same := b.New == b.Old && ((b.NewCompare == nil) == (b.OldCompare == nil)) && (b.NewCompare == nil || *b.NewCompare == *b.OldCompare)
			if same {
				res.Unchanged++
				continue
			}
			b.Title = titles[b.SKU]
			res.OldTotal += b.Old
			res.NewTotal += b.New
			if b.New > b.Old {
				res.Increases++
			} else if b.New < b.Old {
				res.Decreases++
			}
			cmp := int64(-1)
			if b.NewCompare != nil {
				cmp = *b.NewCompare
			}
			fmt.Fprintf(h, "%s|%d|%d|%d|%d\n", b.SKU, b.MinQty, b.Old, b.New, cmp)
			changed = append(changed, b)
		}
		res.Changed = len(changed)
		res.NoPrice = len(skus) - len(priced)
		res.Hash = hex.EncodeToString(h.Sum(nil))
		limit := res.MaxPreview
		if limit <= 0 {
			limit = 200
		}
		res.Rows = changed
		if len(changed) > limit {
			res.Rows, res.Truncated = changed[:limit], true
		}
		if !apply {
			return nil
		}
		if res.Hash != expectedHash {
			return ErrChanged
		}
		if len(changed) == 0 {
			return nil
		}
		idb := make([]byte, 6)
		_, _ = rand.Read(idb)
		res.ID = "cambio_" + hex.EncodeToString(idb)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true), set_config('app.price_source', 'bulk', true), set_config('app.price_ref', $2, true)`, actor, res.ID); err != nil {
			return err
		}
		n := len(changed)
		uskus, mins, news := make([]string, n), make([]int64, n), make([]int64, n)
		cmps := make([]*int64, n)
		touched := map[string]bool{}
		for i, b := range changed {
			uskus[i], mins[i], news[i], cmps[i] = b.SKU, b.MinQty, b.New, b.NewCompare
			touched[b.SKU] = true
		}
		if _, err := tx.Exec(ctx, `UPDATE prices p SET amount=x.amount, compare_at=x.cmp
			FROM unnest($3::text[], $4::bigint[], $5::bigint[], $6::bigint[]) AS x(sku, min_qty, amount, cmp)
			WHERE p.tenant_id=$1 AND p.channel=$2 AND p.sku=x.sku AND p.min_qty=x.min_qty`, tenant, c.Channel, uskus, mins, news, cmps); err != nil {
			return err
		}
		keys := make([]string, 0, len(touched))
		for sku := range touched {
			keys = append(keys, c.Channel+"|"+sku)
		}
		sort.Strings(keys)
		if _, err := tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, key, payload) SELECT $1, $2, k, '{}' FROM unnest($3::text[]) k`, tenant, store.TopicPrice, keys); err != nil {
			return err
		}
		res.Applied = true
		return store.Audit(ctx, tx, tenant, actor, "price.bulk", res.ID, map[string]any{"change": c, "skus": len(touched), "prices": n, "hash": res.Hash})
	})
	return res, err
}

// PriceChange is one entry of the price history.
type PriceChange struct {
	ID         int64     `json:"id"`
	SKU        string    `json:"sku"`
	Channel    string    `json:"channel"`
	MinQty     int64     `json:"minQty"`
	Old        *int64    `json:"old"`
	New        *int64    `json:"new"`
	OldCompare *int64    `json:"oldCompareAt"`
	NewCompare *int64    `json:"newCompareAt"`
	Currency   string    `json:"currency"`
	Source     string    `json:"source"`
	Ref        string    `json:"ref,omitempty"`
	Actor      string    `json:"actor"`
	At         time.Time `json:"at"`
}

// PriceHistory returns changes, newest first. sku, channel and ref filter
// when not empty; before (id, 0 = newest) pages backwards.
func (s *Store) PriceHistory(ctx context.Context, tenant, sku, channel, ref string, before int64, limit int) ([]PriceChange, error) {
	var out []PriceChange
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, sku, channel, min_qty, old_amount, new_amount, old_compare, new_compare, currency, source, ref, actor, at
			FROM price_history WHERE tenant_id=$1 AND ($2='' OR sku=$2) AND ($3='' OR channel=$3) AND ($4='' OR ref=$4) AND ($5=0 OR id<$5)
			ORDER BY id DESC LIMIT $6`, tenant, sku, channel, ref, before, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (PriceChange, error) {
			var c PriceChange
			return c, r.Scan(&c.ID, &c.SKU, &c.Channel, &c.MinQty, &c.Old, &c.New, &c.OldCompare, &c.NewCompare, &c.Currency, &c.Source, &c.Ref, &c.Actor, &c.At)
		})
		return err
	})
	return out, err
}
