package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Sandbox per store (docs/fases/plan-sandbox.md). A sandbox is a tenant of
// kind "sandbox" linked to its production store. It starts as a copy of
// every table of the store, made inside one consistent snapshot; what must
// not travel (credentials, personal data, history) is left out, table by
// table, and a test fails when a table appears that has no rule.

// SandboxPrefix starts every sandbox id; production stores cannot use it.
const SandboxPrefix = "sbx-"

// Copy rules.
const (
	ruleCopy    = "copy"    // copied as is (with the overrides below)
	ruleSkip    = "skip"    // credentials, sessions, history, external links: never copied
	rulePII     = "pii"     // customers and their orders, quotes, carts…: personal data stays in production
	ruleMembers = "members" // the team: synced, without passwords or second factors
	ruleSpecial = "special" // copied by the API (encrypted with the store's key)
)

type tableRule struct {
	mode  string
	where string            // extra filter on the production rows
	set   map[string]string // column -> SQL expression in the sandbox
}

// sandboxTables has one rule for every table that holds store data.
var sandboxTables = map[string]tableRule{
	// Catalog, prices, stock and AI.
	"products": {mode: ruleCopy}, "variants": {mode: ruleCopy}, "prices": {mode: ruleCopy}, "price_lists": {mode: ruleCopy},
	"price_list_items": {mode: ruleCopy}, "stock_levels": {mode: ruleCopy}, "stock_locations": {mode: ruleCopy},
	"stock_thresholds": {mode: ruleCopy}, "categories": {mode: ruleCopy}, "brands": {mode: ruleCopy}, "collections": {mode: ruleCopy},
	"attribute_defs": {mode: ruleCopy}, "catalog_terms": {mode: ruleCopy}, "media": {mode: ruleCopy}, "bundle_components": {mode: ruleCopy},
	"product_logistics": {mode: ruleCopy}, "preorders": {mode: ruleCopy}, "digital_files": {mode: ruleCopy}, "product_coviews": {mode: ruleCopy},
	"ai_chunks": {mode: ruleCopy}, "kg_edges": {mode: ruleCopy}, "ai_suggestions": {mode: ruleCopy}, "inventory_transfers": {mode: ruleCopy},
	"inventory_transfer_lines": {mode: ruleCopy}, "reviews": {mode: ruleCopy},
	"custom_values": {mode: ruleCopy, where: "resource NOT IN ('customer','order','company','quote','subscription')", set: map[string]string{"secret": "NULL"}},
	// Promotions.
	"promotions": {mode: ruleCopy}, "coupons": {mode: ruleCopy}, "coupon_batches": {mode: ruleCopy}, "campaigns": {mode: ruleCopy},
	// Design and content.
	"sites": {mode: ruleCopy}, "site_drafts": {mode: ruleCopy}, "site_revisions": {mode: ruleCopy}, "site_releases": {mode: ruleCopy},
	"menus": {mode: ruleCopy}, "policies": {mode: ruleCopy}, "redirects": {mode: ruleCopy}, "blog_posts": {mode: ruleCopy}, "store_assets": {mode: ruleCopy},
	// Configuration.
	"store_settings": {mode: ruleCopy}, "channels": {mode: ruleCopy}, "shipping_options": {mode: ruleCopy}, "shipping_profiles": {mode: ruleCopy},
	"shipping_zones": {mode: ruleCopy}, "tax_regions": {mode: ruleCopy}, "holidays": {mode: ruleCopy}, "notification_templates": {mode: ruleCopy},
	"automations": {mode: ruleCopy}, "customer_groups": {mode: ruleCopy}, "email_campaigns": {mode: ruleCopy},
	"sellers": {mode: ruleCopy}, "seller_products": {mode: ruleCopy},
	// A connected carrier answers in test mode: no real labels from the sandbox.
	"carriers": {mode: ruleCopy, set: map[string]string{"data": `CASE WHEN data->>'provider' = 'http' THEN jsonb_set(data - 'endpoint', '{provider}', '"sandbox"') ELSE data END`}},
	// Documents, except the ones that hold credentials (sign-on, ad
	// conversions, catalog source) and history.
	"tenant_docs": {mode: ruleCopy, where: "kind NOT IN ('sso','ads_conversions','catalog_source','store_tags_history')"},
	// Encrypted with the store's key: copied by the API, decrypted with
	// production's key and encrypted again with the sandbox's.
	"entity_records": {mode: ruleSpecial},
	// The team.
	"members": {mode: ruleMembers},
	// Never copied: credentials and sessions.
	"api_keys": {mode: ruleSkip}, "apps": {mode: ruleSkip}, "sessions": {mode: ruleSkip}, "payment_accounts": {mode: ruleSkip},
	"channel_accounts": {mode: ruleSkip}, "channel_listings": {mode: ruleSkip}, "channel_orders": {mode: ruleSkip}, "tenant_keys": {mode: ruleSkip}, "tenant_plans": {mode: ruleSkip},
	"webhook_subscriptions": {mode: ruleSkip}, "webhook_deliveries": {mode: ruleSkip}, "notification_settings": {mode: ruleSkip},
	"domains": {mode: ruleSkip}, "invitations": {mode: ruleSkip}, "rep_handoffs": {mode: ruleSkip},
	// Never copied: history (the sandbox starts its own).
	"audit_log": {mode: ruleSkip}, "outbox": {mode: ruleSkip}, "idempotency_keys": {mode: ruleSkip}, "import_jobs": {mode: ruleSkip},
	"bulk_jobs": {mode: ruleSkip}, "price_history": {mode: ruleSkip}, "ledger_entries": {mode: ruleSkip}, "automation_runs": {mode: ruleSkip},
	"notification_messages": {mode: ruleSkip}, "conversion_jobs": {mode: ruleSkip}, "activity_rollup": {mode: ruleSkip}, "ai_usage": {mode: ruleSkip},
	"search_terms_daily": {mode: ruleSkip}, "search_clicks": {mode: ruleSkip}, "reservations": {mode: ruleSkip},
	"analytics_events": {mode: ruleSkip}, "analytics_funnel_daily": {mode: ruleSkip}, "analytics_order_promos": {mode: ruleSkip},
	"analytics_orders": {mode: ruleSkip}, "analytics_product_traffic_daily": {mode: ruleSkip}, "analytics_promo_daily": {mode: ruleSkip},
	"analytics_referrer_daily": {mode: ruleSkip}, "analytics_sales_daily": {mode: ruleSkip}, "analytics_sales_hourly": {mode: ruleSkip},
	"analytics_sku_daily": {mode: ruleSkip}, "analytics_traffic_hourly": {mode: ruleSkip}, "promotion_redemptions": {mode: ruleSkip},
	"seller_entries": {mode: ruleSkip}, "seller_payouts": {mode: ruleSkip},
	// Personal data and what hangs from it.
	"customers": {mode: rulePII}, "customer_addresses": {mode: rulePII}, "customer_notes": {mode: rulePII}, "customer_cards": {mode: rulePII},
	"customer_group_members": {mode: rulePII}, "shopper_sessions": {mode: rulePII}, "shopper_otps": {mode: rulePII},
	"abandoned_carts": {mode: rulePII}, "wishlist_items": {mode: rulePII}, "reorder_lists": {mode: rulePII}, "download_grants": {mode: rulePII},
	"orders": {mode: rulePII}, "order_lines": {mode: rulePII}, "order_events": {mode: rulePII}, "order_allocations": {mode: rulePII},
	"order_notes": {mode: rulePII}, "order_edits": {mode: rulePII}, "order_views": {mode: rulePII}, "payments": {mode: rulePII},
	"invoices": {mode: rulePII}, "refunds": {mode: rulePII}, "returns": {mode: rulePII}, "return_items": {mode: rulePII},
	"fulfillments": {mode: rulePII}, "fulfillment_items": {mode: rulePII}, "fulfillment_requests": {mode: rulePII},
	"draft_orders": {mode: rulePII}, "payment_links": {mode: rulePII}, "subscriptions": {mode: rulePII}, "subscription_orders": {mode: rulePII},
	"gift_cards": {mode: rulePII}, "gift_card_tx": {mode: rulePII}, "gift_registries": {mode: rulePII}, "gift_registry_items": {mode: rulePII},
	"loyalty_entries": {mode: rulePII}, "quotes": {mode: rulePII}, "quote_comments": {mode: rulePII}, "quote_templates": {mode: rulePII},
	"quote_template_orders": {mode: rulePII}, "purchase_requests": {mode: rulePII}, "punchout_orders": {mode: rulePII},
	"punchout_sessions": {mode: rulePII}, "companies": {mode: rulePII}, "company_buyers": {mode: rulePII}, "company_entries": {mode: rulePII},
	"company_files": {mode: rulePII}, "company_locations": {mode: rulePII}, "company_punchout": {mode: rulePII},
	"company_receipts": {mode: rulePII}, "company_roles": {mode: rulePII},
}

// SandboxArea is a part of the store that can be published from the
// sandbox to production on its own.
type SandboxArea struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Tables []string `json:"-"`
}

// SandboxAreas are the publishable parts; their rows get a base fingerprint.
var SandboxAreas = []SandboxArea{
	{"catalog", "Catálogo", []string{"categories", "brands", "collections", "attribute_defs", "catalog_terms", "media", "products", "variants",
		"bundle_components", "product_logistics", "preorders", "digital_files", "kg_edges", "ai_chunks"}},
	{"prices", "Precios", []string{"price_lists", "price_list_items", "prices"}},
	{"stock", "Existencias", []string{"stock_locations", "stock_levels", "stock_thresholds"}},
	{"promotions", "Promociones", []string{"promotions", "coupon_batches", "coupons", "campaigns"}},
	{"design", "Diseño y contenido", []string{"sites", "site_revisions", "site_releases", "site_drafts", "menus", "policies", "redirects", "blog_posts", "store_assets"}},
	{"settings", "Configuración", []string{"store_settings", "channels", "shipping_profiles", "shipping_zones", "shipping_options", "tax_regions",
		"holidays", "notification_templates", "automations", "customer_groups", "email_campaigns", "tenant_docs"}},
}

// publishFilter narrows what a publish may write in production: documents
// with credentials or that are published on their own (tags) never travel.
var publishFilter = map[string]string{
	"tenant_docs": "kind NOT IN ('sso','ads_conversions','catalog_source','store_tags','store_tags_history','payments')",
	"ai_chunks":   "source_kind = 'product'",
	// Edges the graph only proposed are rebuilt by production itself; what
	// a person decided (approved, rejected) travels.
	"kg_edges": "status <> 'proposed'",
}

// Sandbox is the state of a store's sandbox.
type Sandbox struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	HasPassword bool           `json:"hasPassword"`
	ParentID    string         `json:"parentId"`
	Status      string         `json:"status"`
	DataMode    string         `json:"dataMode"`
	Progress    map[string]any `json:"progress"`
	Error       string         `json:"error,omitempty"`
	CreatedBy   string         `json:"createdBy"`
	CreatedAt   time.Time      `json:"createdAt"`
	CopiedAt    *time.Time     `json:"copiedAt,omitempty"`
	CopyMs      int            `json:"copyMs"`
	LastUsedAt  time.Time      `json:"lastUsedAt"`
	WarnedAt    *time.Time     `json:"warnedAt,omitempty"`
}

const sandboxCols = `id, name, password_hash <> '', parent_id, status, data_mode, progress, error, created_by, created_at, copied_at, copy_ms, last_used_at, warned_at`

func scanSandbox(row pgx.Row) (Sandbox, error) {
	var s Sandbox
	var prog []byte
	err := row.Scan(&s.ID, &s.Name, &s.HasPassword, &s.ParentID, &s.Status, &s.DataMode, &prog, &s.Error, &s.CreatedBy, &s.CreatedAt, &s.CopiedAt, &s.CopyMs, &s.LastUsedAt, &s.WarnedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	_ = json.Unmarshal(prog, &s.Progress)
	return s, err
}

// SandboxOf returns the first sandbox of a production store (the oldest).
func (d *DB) SandboxOf(ctx context.Context, parent string) (Sandbox, error) {
	return scanSandbox(d.System.QueryRow(ctx, `SELECT `+sandboxCols+` FROM sandboxes WHERE parent_id=$1 ORDER BY created_at, id LIMIT 1`, parent))
}

// SandboxesOf lists the sandboxes of a production store, oldest first.
func (d *DB) SandboxesOf(ctx context.Context, parent string) ([]Sandbox, error) {
	return d.listSandboxes(ctx, `SELECT `+sandboxCols+` FROM sandboxes WHERE parent_id=$1 ORDER BY created_at, id`, parent)
}

func (d *DB) listSandboxes(ctx context.Context, q string, args ...any) ([]Sandbox, error) {
	rows, err := d.System.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Sandbox{}
	for rows.Next() {
		s, err := scanSandbox(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SandboxByID returns a sandbox by its own id.
func (d *DB) SandboxByID(ctx context.Context, id string) (Sandbox, error) {
	return scanSandbox(d.System.QueryRow(ctx, `SELECT `+sandboxCols+` FROM sandboxes WHERE id=$1`, id))
}

// Sandboxes lists every sandbox (lifecycle jobs).
func (d *DB) Sandboxes(ctx context.Context) ([]Sandbox, error) {
	return d.listSandboxes(ctx, `SELECT `+sandboxCols+` FROM sandboxes ORDER BY id`)
}

// TenantKind says whether a store is production or a sandbox, and its parent.
func (d *DB) TenantKind(ctx context.Context, id string) (kind, parent string, err error) {
	var p *string
	err = d.System.QueryRow(ctx, `SELECT kind, parent_id FROM tenants WHERE id=$1`, id).Scan(&kind, &p)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if p != nil {
		parent = *p
	}
	return kind, parent, err
}

// ErrSandboxExists means the store already has a sandbox with that name.
var ErrSandboxExists = errors.New("store: the store already has a sandbox with that name")

// StartSandbox creates the sandbox tenant (with its own data key) and marks
// it as copying.
func (d *DB) StartSandbox(ctx context.Context, parent, id, name, actor, dataMode string, wrappedKey []byte) error {
	if name == "" {
		name = "Sandbox"
	}
	plan, err := d.TenantPlan(ctx, parent)
	if err != nil {
		return err
	}
	if dataMode == "demo" && !plan.SandboxDemoData {
		return &ErrSandboxQuota{What: "demo", Plan: plan}
	}
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		var kind, storeName string
		if err := tx.QueryRow(ctx, `SELECT kind, name FROM tenants WHERE id=$1 FOR UPDATE`, parent).Scan(&kind, &storeName); err != nil {
			return err
		}
		if kind != "production" {
			return errors.New("store: a sandbox cannot have its own sandbox")
		}
		// The parent row is locked: two creations at once count right.
		var n int
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT count(*), bool_or(lower(name) = lower($2)) IS TRUE FROM sandboxes WHERE parent_id=$1`, parent, name).Scan(&n, &taken); err != nil {
			return err
		}
		if taken {
			return ErrSandboxExists
		}
		if n >= plan.Sandboxes {
			return &ErrSandboxQuota{What: "sandboxes", Plan: plan}
		}
		if plan.SandboxMaxProducts > 0 {
			var products int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id=$1`, parent).Scan(&products); err != nil {
				return err
			}
			if products > plan.SandboxMaxProducts {
				return &ErrSandboxQuota{What: "products", Plan: plan, Count: products}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenants (id, name, kind, parent_id) VALUES ($1, $2, 'sandbox', $3)`, id, name+" ("+storeName+")", parent); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_keys (tenant_id, wrapped_key) VALUES ($1,$2)`, id, wrappedKey); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO sandboxes (id, name, parent_id, status, data_mode, created_by) VALUES ($1,$2,$3,'copying',$4,$5)`, id, name, parent, dataMode, actor)
		if isUnique(err) {
			return ErrSandboxExists
		}
		return err
	})
}

// SetSandboxStatus records the end of a copy (ready or failed).
func (d *DB) SetSandboxStatus(ctx context.Context, id, status, errText string, copyMs int) error {
	q := `UPDATE sandboxes SET status=$2, error=$3 WHERE id=$1`
	args := []any{id, status, errText}
	if status == "ready" {
		q = `UPDATE sandboxes SET status=$2, error=$3, copied_at=now(), copy_ms=$4 WHERE id=$1`
		args = append(args, copyMs)
	}
	_, err := d.System.Exec(ctx, q, args...)
	return err
}

// SetSandboxStatusOnly changes the status without other facts (deleting, copying).
func (d *DB) SetSandboxStatusOnly(ctx context.Context, id, status string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandboxes SET status=$2, error='' WHERE id=$1`, id, status)
	return err
}

// TouchSandbox records that someone used the sandbox (inactivity expiry).
func (d *DB) TouchSandbox(ctx context.Context, id string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandboxes SET last_used_at=now(), warned_at=NULL WHERE id=$1 AND last_used_at < now() - interval '1 minute'`, id)
	return err
}

// MarkSandboxWarned records the expiry notice.
func (d *DB) MarkSandboxWarned(ctx context.Context, id string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandboxes SET warned_at=now() WHERE id=$1`, id)
	return err
}

func (d *DB) setSandboxProgress(ctx context.Context, id string, p map[string]any) {
	b, _ := json.Marshal(p)
	_, _ = d.System.Exec(ctx, `UPDATE sandboxes SET progress=$2 WHERE id=$1`, id, b)
}

// ---------- tables ----------

type tableInfo struct {
	name    string
	cols    []string // every column, in order
	pk      []string // primary key columns without tenant_id
	pkTypes []string // their SQL types, to read a key back into columns
}

var (
	tablesMu    sync.Mutex
	tablesCache []tableInfo
)

// storeTables lists the tables that hold store data, parents before
// children (by their foreign keys), with their columns and keys.
func (d *DB) storeTables(ctx context.Context) ([]tableInfo, error) {
	tablesMu.Lock()
	defer tablesMu.Unlock()
	if tablesCache != nil {
		return tablesCache, nil
	}
	rows, err := d.System.Query(ctx, `
		SELECT c.relname,
			(SELECT array_agg(a.attname ORDER BY a.attnum) FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped AND a.attgenerated=''),
			COALESCE((SELECT array_agg(a.attname ORDER BY array_position(i.indkey, a.attnum)) FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum = ANY(i.indkey)
				WHERE i.indrelid=c.oid AND i.indisprimary AND a.attname <> 'tenant_id'), '{}'),
			COALESCE((SELECT array_agg(format_type(a.atttypid, a.atttypmod) ORDER BY array_position(i.indkey, a.attnum)) FROM pg_index i JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum = ANY(i.indkey)
				WHERE i.indrelid=c.oid AND i.indisprimary AND a.attname <> 'tenant_id'), '{}')
		FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname='public' AND c.relkind='r' AND EXISTS (SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='tenant_id')`)
	if err != nil {
		return nil, err
	}
	byName := map[string]tableInfo{}
	for rows.Next() {
		var t tableInfo
		if err := rows.Scan(&t.name, &t.cols, &t.pk, &t.pkTypes); err != nil {
			rows.Close()
			return nil, err
		}
		byName[t.name] = t
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	deps := map[string][]string{}
	frows, err := d.System.Query(ctx, `SELECT conrelid::regclass::text, confrelid::regclass::text FROM pg_constraint WHERE contype='f' AND conrelid <> confrelid`)
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var child, parent string
		if err := frows.Scan(&child, &parent); err != nil {
			frows.Close()
			return nil, err
		}
		if _, ok := byName[parent]; ok {
			if _, ok := byName[child]; ok {
				deps[child] = append(deps[child], parent)
			}
		}
	}
	frows.Close()
	// Parents first; ties by name so the order is always the same.
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	done := map[string]bool{}
	var out []tableInfo
	for len(out) < len(names) {
		progress := false
		for _, n := range names {
			if done[n] {
				continue
			}
			ready := true
			for _, p := range deps[n] {
				if !done[p] {
					ready = false
				}
			}
			if ready {
				done[n] = true
				out = append(out, byName[n])
				progress = true
			}
		}
		if !progress {
			return nil, errors.New("store: circular foreign keys between store tables")
		}
	}
	tablesCache = out
	return out, nil
}

// UnclassifiedTables lists store tables without a sandbox rule (a test
// keeps it empty, so a new table is never copied, or forgotten, by accident).
func (d *DB) UnclassifiedTables(ctx context.Context) ([]string, error) {
	ts, err := d.storeTables(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range ts {
		if _, ok := sandboxTables[t.name]; !ok {
			out = append(out, t.name)
		}
	}
	return out, nil
}

// SandboxRule says how a table travels to the sandbox (tests and docs).
func SandboxRule(table string) string { return sandboxTables[table].mode }

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// pkExpr is the text form of a row's key (without tenant_id), for diffs.
func pkExpr(alias string, t tableInfo) string {
	if len(t.pk) == 0 {
		return "md5((to_jsonb(" + alias + ".*) - 'tenant_id')::text)"
	}
	parts := make([]string, len(t.pk))
	for i, c := range t.pk {
		parts[i] = alias + "." + quoteIdent(c) + "::text"
	}
	return "concat_ws('|', " + strings.Join(parts, ", ") + ")"
}

// keyCols reads a key written by pkExpr (in SQL expression src) back into
// the key's columns, so a lookup uses the (tenant_id, key) index instead of
// computing pkExpr for every row of the table. Only valid when no key value
// holds the separator (typedKeys checks it).
func keyCols(t tableInfo, src string) []string {
	out := make([]string, len(t.pk))
	for i := range t.pk {
		out[i] = "split_part(" + src + ", '|', " + strconv.Itoa(i+1) + ")::" + t.pkTypes[i]
	}
	return out
}

// keyIn is "row alias has one of the keys" (keys: SQL returning one text
// column). typed: by columns, using the index; otherwise the exact text.
func keyIn(alias string, t tableInfo, keys string, typed bool) string {
	if !typed || len(t.pk) == 0 {
		return pkExpr(alias, t) + " IN (" + keys + ")"
	}
	cols := make([]string, len(t.pk))
	for i, c := range t.pk {
		cols[i] = alias + "." + quoteIdent(c)
	}
	return "(" + strings.Join(cols, ", ") + ") IN (SELECT " + strings.Join(keyCols(t, "k.pk"), ", ") + " FROM (" + keys + ") k(pk))"
}

// keyEq is "row alias has the key in expression src".
func keyEq(alias string, t tableInfo, src string, typed bool) string {
	if !typed || len(t.pk) == 0 {
		return pkExpr(alias, t) + " = " + src
	}
	conds := make([]string, len(t.pk))
	for i, v := range keyCols(t, src) {
		conds[i] = alias + "." + quoteIdent(t.pk[i]) + " = " + v
	}
	return strings.Join(conds, " AND ")
}

// typedKeys says whether every key in keys (SQL returning text) splits
// into exactly the key's columns: then keyIn and keyEq may use the columns.
func typedKeys(ctx context.Context, tx pgx.Tx, t tableInfo, keys string, args ...any) bool {
	if len(t.pk) == 0 || len(t.pkTypes) != len(t.pk) {
		return false
	}
	var bad bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM (`+keys+`) k(pk) WHERE array_length(string_to_array(k.pk, '|'), 1) IS DISTINCT FROM `+strconv.Itoa(len(t.pk))+`)`, args...).Scan(&bad)
	return err == nil && !bad
}

// typedKey is typedKeys for one key known in Go.
func typedKey(t tableInfo, pk string) bool {
	return len(t.pk) > 0 && len(t.pkTypes) == len(t.pk) && strings.Count(pk, "|")+1 == len(t.pk)
}

// rowHash is the fingerprint of a row's content (without tenant_id).
func rowHash(alias string) string {
	return "md5((to_jsonb(" + alias + ".*) - 'tenant_id')::text)"
}

func publishable() map[string]bool {
	m := map[string]bool{}
	for _, a := range SandboxAreas {
		for _, t := range a.Tables {
			m[t] = true
		}
	}
	return m
}

// copySQL copies one table from src to dst inside tx.
func copySQL(t tableInfo, rule tableRule) string {
	cols := make([]string, len(t.cols))
	sel := make([]string, len(t.cols))
	for i, c := range t.cols {
		cols[i] = quoteIdent(c)
		switch {
		case c == "tenant_id":
			sel[i] = "$1"
		case rule.set[c] != "":
			sel[i] = rule.set[c]
		default:
			sel[i] = quoteIdent(c)
		}
	}
	where := "tenant_id = $2"
	if rule.where != "" {
		where += " AND (" + rule.where + ")"
	}
	return "INSERT INTO " + quoteIdent(t.name) + " (" + strings.Join(cols, ", ") + ") SELECT " + strings.Join(sel, ", ") +
		" FROM " + quoteIdent(t.name) + " WHERE " + where
}

// CopySandbox copies production into the sandbox, in one snapshot, and
// records the base fingerprint of every publishable row. It returns how
// many rows each table received.
func (d *DB) CopySandbox(ctx context.Context, id string) (map[string]int64, error) {
	sb, err := d.SandboxByID(ctx, id)
	if err != nil {
		return nil, err
	}
	tables, err := d.storeTables(ctx)
	if err != nil {
		return nil, err
	}
	pub := publishable()
	counts := map[string]int64{}
	start := time.Now()
	tx, err := d.System.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var copyTables []tableInfo
	for _, t := range tables {
		if sandboxTables[t.name].mode == ruleCopy {
			copyTables = append(copyTables, t)
		}
	}
	for i, t := range copyTables {
		tag, err := tx.Exec(ctx, copySQL(t, sandboxTables[t.name]), id, sb.ParentID)
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", t.name, err)
		}
		counts[t.name] = tag.RowsAffected()
		if pub[t.name] {
			if _, err := tx.Exec(ctx, `INSERT INTO sandbox_base (sandbox_id, tbl, pk, hash) SELECT $1, $2, `+pkExpr("x", t)+`, `+rowHash("x")+` FROM `+quoteIdent(t.name)+` x WHERE x.tenant_id = $1`, id, t.name); err != nil {
				return nil, fmt.Errorf("base %s: %w", t.name, err)
			}
		}
		// Progress, outside the copy (another connection sees it at once).
		d.setSandboxProgress(ctx, id, map[string]any{"table": t.name, "done": i + 1, "total": len(copyTables), "ms": time.Since(start).Milliseconds(), "at": time.Now().Unix()})
	}
	if err := d.syncMembersTx(ctx, tx, sb.ParentID, id); err != nil {
		return nil, err
	}
	if err := Emit(ctx, tx, id, TopicTenantReload, "", nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	d.analyzeSandboxTables(ctx)
	return counts, nil
}

// analyzeSandboxTables refreshes the planner's statistics after a bulk
// change of the sandbox's own tables (a copy writes hundreds of thousands
// of base rows at once). With stale numbers Postgres can pick a plan that
// takes minutes for a revert that takes seconds.
func (d *DB) analyzeSandboxTables(ctx context.Context) {
	if _, err := d.System.Exec(ctx, `SELECT apexx_analyze_sandbox()`); err != nil {
		slog.Warn("analyze sandbox tables", "err", err) // statistics only: nothing to undo
	}
}

// SyncSandboxMembers makes the sandbox's team the production team: same
// people and permissions, no password (they enter from production, already
// signed in with their second factor) and nobody who left.
func (d *DB) SyncSandboxMembers(ctx context.Context, parent, id string) error {
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error { return d.syncMembersTx(ctx, tx, parent, id) })
}

func (d *DB) syncMembersTx(ctx context.Context, tx pgx.Tx, parent, id string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO members (tenant_id, id, email, name, password_hash, permissions, is_owner, status, created_at, totp_last_step, recovery_codes, seller_id)
		SELECT $1, id, email, name, '!sandbox', permissions, is_owner, status, created_at, 0, '[]', seller_id FROM members WHERE tenant_id=$2
		ON CONFLICT (tenant_id, id) DO UPDATE SET email=EXCLUDED.email, name=EXCLUDED.name, permissions=EXCLUDED.permissions,
			is_owner=EXCLUDED.is_owner, status=EXCLUDED.status, seller_id=EXCLUDED.seller_id`, id, parent); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE tenant_id=$1 AND member_id NOT IN (SELECT id FROM members WHERE tenant_id=$2 AND status='active')`, id, parent); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM members WHERE tenant_id=$1 AND id NOT IN (SELECT id FROM members WHERE tenant_id=$2)`, id, parent)
	return err
}

// WipeSandbox deletes every row of the sandbox in every store table,
// children first. With keep, the sandbox's own identity stays (its key,
// domains, team and sessions) so a refresh keeps its address and the people
// inside. Outbox events stay (pruned with everyone's after 30 days); the
// sandbox's audit log goes with it on deletion and stays on a refresh.
func (d *DB) WipeSandbox(ctx context.Context, id string, keep bool) error {
	if !strings.HasPrefix(id, SandboxPrefix) {
		return errors.New("store: only a sandbox can be wiped")
	}
	tables, err := d.storeTables(ctx)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		var kind string
		if err := tx.QueryRow(ctx, `SELECT kind FROM tenants WHERE id=$1`, id).Scan(&kind); err != nil {
			return err
		}
		if kind != "sandbox" {
			return errors.New("store: only a sandbox can be wiped")
		}
		// Append-only tables allow removing a sandbox's rows only inside
		// this transaction (migration 161).
		if _, err := tx.Exec(ctx, `SELECT set_config('apexx.sandbox_wipe', 'on', true)`); err != nil {
			return err
		}
		// Deferred triggers (price history) fire now, not at commit, so the
		// second pass below sees what they write.
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return err
		}
		for i := len(tables) - 1; i >= 0; i-- {
			t := tables[i]
			if t.name == "outbox" {
				continue
			}
			if keep && slices.Contains([]string{"tenant_keys", "domains", "members", "sessions", "audit_log"}, t.name) {
				continue
			}
			if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(t.name)+` WHERE tenant_id=$1`, id); err != nil {
				return fmt.Errorf("wipe %s: %w", t.name, err)
			}
		}
		// Triggers write history while rows are deleted (price history when
		// prices go): a second pass removes what the first one created.
		if !keep {
			if _, err := tx.Exec(ctx, `DELETE FROM price_history WHERE tenant_id=$1`, id); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `DELETE FROM sandbox_base WHERE sandbox_id=$1`, id)
		return err
	})
}

// DeleteSandbox removes the sandbox entirely and tells every node to forget it.
func (d *DB) DeleteSandbox(ctx context.Context, id string) error {
	if err := d.WipeSandbox(ctx, id, false); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM sandboxes WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM tenants WHERE id=$1 AND kind='sandbox'`, id); err != nil {
			return err
		}
		return Emit(ctx, tx, id, TopicTenantRemoved, "", nil)
	})
}

// SandboxRowCounts counts the rows a store has in every store table (tests
// and the deletion check).
func (d *DB) SandboxRowCounts(ctx context.Context, tenant string) (map[string]int64, error) {
	tables, err := d.storeTables(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, t := range tables {
		var n int64
		if err := d.System.QueryRow(ctx, `SELECT count(*) FROM `+quoteIdent(t.name)+` WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			out[t.name] = n
		}
	}
	return out, nil
}
