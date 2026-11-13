package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Publishing from the sandbox to production. Every publishable row has three
// versions: the base (production as it was when the sandbox was copied, or
// at the last publish), the sandbox and production. A row the sandbox
// changed and production did not is applied; a row both changed is a
// conflict and is only applied when the person says so. Every row written in
// production is first saved as it was, so a revert puts it back exactly.

// Row actions.
const (
	ActInsert = "insert"
	ActUpdate = "update"
	ActDelete = "delete"
)

// RowChange is one row the sandbox changed.
type RowChange struct {
	Table    string `json:"table"`
	PK       string `json:"pk"`
	Action   string `json:"action"`
	Conflict bool   `json:"conflict"` // production changed it too
	Label    string `json:"label,omitempty"`
	sbHash   string
	baseHash string
}

// AreaDiff is what publishing one area would do.
type AreaDiff struct {
	Area      string         `json:"area"`
	Label     string         `json:"label"`
	Inserts   int            `json:"inserts"`
	Updates   int            `json:"updates"`
	Deletes   int            `json:"deletes"`
	Conflicts int            `json:"conflicts"`
	ByTable   map[string]int `json:"byTable"`
	Sample    []RowChange    `json:"sample"` // up to 50 rows, conflicts first
	changes   []RowChange
}

// SandboxDiff compares the sandbox with production, area by area.
func (d *DB) SandboxDiff(ctx context.Context, sandboxID string, areas []string, products []string) ([]AreaDiff, error) {
	sb, err := d.SandboxByID(ctx, sandboxID)
	if err != nil {
		return nil, err
	}
	var out []AreaDiff
	err = pgx.BeginTxFunc(ctx, d.System, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		out, err = d.diffTx(ctx, tx, sb, areas, products, true)
		return err
	})
	return out, err
}

func areaByID(id string) (SandboxArea, bool) {
	for _, a := range SandboxAreas {
		if a.ID == id {
			return a, true
		}
	}
	return SandboxArea{}, false
}

// productScope narrows the catalog, prices and stock to some products.
func productScope(table string, products []string) string {
	if len(products) == 0 {
		return ""
	}
	switch table {
	case "products":
		return "x.id = ANY($4)"
	case "variants", "product_logistics", "bundle_components":
		if table == "bundle_components" {
			return "x.bundle_sku IN (SELECT sku FROM variants v WHERE v.tenant_id = x.tenant_id AND v.product_id = ANY($4))"
		}
		if table == "product_logistics" {
			return "x.product_id = ANY($4)"
		}
		return "x.product_id = ANY($4)"
	case "prices", "stock_levels", "preorders", "digital_files":
		return "x.sku IN (SELECT sku FROM variants v WHERE v.tenant_id IN ($1, $2) AND v.product_id = ANY($4))"
	case "ai_chunks":
		return "x.source_id = ANY($4)"
	case "kg_edges":
		return "(x.src_id = ANY($4) OR x.dst_id = ANY($4))"
	}
	return "false" // other tables of these areas do not travel with a product selection
}

func (d *DB) diffTx(ctx context.Context, tx pgx.Tx, sb Sandbox, areas []string, products []string, labels bool) ([]AreaDiff, error) {
	tables, err := d.storeTables(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]tableInfo{}
	for _, t := range tables {
		byName[t.name] = t
	}
	var out []AreaDiff
	for _, aid := range areas {
		a, ok := areaByID(aid)
		if !ok {
			return nil, fmt.Errorf("store: unknown area %q", aid)
		}
		ad := AreaDiff{Area: a.ID, Label: a.Label, ByTable: map[string]int{}}
		for _, tn := range a.Tables {
			t, ok := byName[tn]
			if !ok {
				continue
			}
			// visible: rows that may travel (publishFilter). A row hidden in
			// the sandbox never travels; production is compared whole, so a
			// row it has hidden is updated, not inserted again; a delete
			// only reaches rows production shows.
			visible := "true"
			if f := publishFilter[tn]; f != "" {
				visible = f
			}
			scope := "true"
			if (a.ID == "catalog" || a.ID == "prices" || a.ID == "stock") && len(products) > 0 {
				if sc := productScope(tn, products); sc != "" {
					scope = sc
				}
			}
			args := []any{sb.ID, sb.ParentID, tn}
			if strings.Contains(scope, "$4") {
				args = append(args, products)
			}
			q := `WITH s AS (SELECT ` + pkExpr("x", t) + ` AS pk, ` + rowHash("x", tn) + ` AS h, (` + visible + `) AS vis FROM ` + quoteIdent(tn) + ` x WHERE x.tenant_id = $1 AND (` + scope + `)),
				p AS (SELECT ` + pkExpr("x", t) + ` AS pk, ` + rowHash("x", tn) + ` AS h, (` + visible + `) AS vis FROM ` + quoteIdent(tn) + ` x WHERE x.tenant_id = $2 AND (` + scope + `)),
				b AS (SELECT pk, hash AS h FROM sandbox_base WHERE sandbox_id = $1 AND tbl = $3)
				SELECT COALESCE(s.pk, p.pk, b.pk), COALESCE(s.h, ''), COALESCE(p.h, ''), COALESCE(b.h, '')
				FROM s FULL JOIN p ON p.pk = s.pk FULL JOIN b ON b.pk = COALESCE(s.pk, p.pk)
				WHERE COALESCE(s.vis, true) AND (s.pk IS NOT NULL OR COALESCE(p.vis, true))
				AND s.h IS DISTINCT FROM b.h AND s.h IS DISTINCT FROM p.h`
			rows, err := tx.Query(ctx, q, args...)
			if err != nil {
				return nil, fmt.Errorf("diff %s: %w", tn, err)
			}
			for rows.Next() {
				var c RowChange
				var sh, ph, bh string
				if err := rows.Scan(&c.PK, &sh, &ph, &bh); err != nil {
					rows.Close()
					return nil, err
				}
				// Without a base (a table published on its own, or rows of an
				// older sandbox), production is the reference.
				c.Table, c.sbHash, c.baseHash = tn, sh, bh
				switch {
				case sh == "":
					c.Action = ActDelete
				case ph == "":
					c.Action = ActInsert
				default:
					c.Action = ActUpdate
				}
				c.Conflict = ph != bh
				// A row production deleted and the sandbox did not touch
				// is not a sandbox change.
				if sh == bh {
					continue
				}
				ad.changes = append(ad.changes, c)
				ad.ByTable[tn]++
				switch {
				case c.Conflict:
					ad.Conflicts++
				case c.Action == ActInsert:
					ad.Inserts++
				case c.Action == ActUpdate:
					ad.Updates++
				default:
					ad.Deletes++
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, err
			}
		}
		// The sample shows conflicts first, then the main rows of the area.
		sample := slices.Clone(ad.changes)
		slices.SortStableFunc(sample, func(x, y RowChange) int {
			if x.Conflict != y.Conflict {
				if x.Conflict {
					return -1
				}
				return 1
			}
			return slices.Index(a.Tables, y.Table) - slices.Index(a.Tables, x.Table) // later tables (products) before helpers
		})
		if len(sample) > 50 {
			sample = sample[:50]
		}
		if labels {
			for i := range sample {
				sample[i].Label = d.rowLabel(ctx, tx, byName[sample[i].Table], sb, sample[i])
			}
		}
		ad.Sample = sample
		out = append(out, ad)
	}
	return out, nil
}

// rowLabel names a row for people: its title or name when it has one.
func (d *DB) rowLabel(ctx context.Context, tx pgx.Tx, t tableInfo, sb Sandbox, c RowChange) string {
	tenant := sb.ID
	if c.Action == ActDelete {
		tenant = sb.ParentID
	}
	parts := strings.Split(c.PK, "|")
	product := func(id string) string {
		var title string
		if tx.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id = $1 AND id = $2`, tenant, id).Scan(&title) != nil || title == "" {
			_ = tx.QueryRow(ctx, `SELECT title FROM products WHERE tenant_id = $1 AND id = $2`, sb.ParentID, id).Scan(&title)
		}
		if title == "" {
			return id
		}
		return title
	}
	// Rows keyed by codes: said in words.
	switch {
	case t.name == "prices" && len(parts) == 3: // channel|sku|min_qty
		if parts[2] != "1" && parts[2] != "0" {
			return parts[1] + " · canal " + parts[0] + " · desde " + parts[2] + " piezas"
		}
		return parts[1] + " · canal " + parts[0]
	case t.name == "stock_levels" && len(parts) == 2: // sku|location
		return parts[0] + " · en " + parts[1]
	case t.name == "ai_chunks" && len(parts) >= 2 && parts[0] == "product":
		return "Búsqueda con IA de «" + product(parts[1]) + "»"
	case t.name == "kg_edges":
		var src, dst, rel string
		if tx.QueryRow(ctx, `SELECT src_id, dst_id, rel FROM kg_edges WHERE tenant_id = $1 AND id = $2`, tenant, c.PK).Scan(&src, &dst, &rel) == nil {
			return "Relación «" + product(src) + "» → «" + product(dst) + "» (" + rel + ")"
		}
	}
	col := ""
	for _, cand := range []string{"title", "name", "handle", "sku", "code", "slug"} {
		if slices.Contains(t.cols, cand) {
			col = cand
			break
		}
	}
	if col == "" {
		return c.PK
	}
	var v *string
	_ = tx.QueryRow(ctx, `SELECT x.`+quoteIdent(col)+`::text FROM `+quoteIdent(t.name)+` x WHERE x.tenant_id = $1 AND `+keyEq("x", t, "$2::text", typedKey(t, c.PK)), tenant, c.PK).Scan(&v)
	if v == nil || *v == "" {
		return c.PK
	}
	return *v
}

// PublishResult is one publish to production.
type PublishResult struct {
	ID        string     `json:"id"`
	Areas     []AreaDiff `json:"areas"`
	Applied   int        `json:"applied"`
	Skipped   int        `json:"skipped"` // conflicts left as they are
	Products  []string   `json:"-"`       // product ids whose vectors changed
	MediaIDs  []string   `json:"-"`       // media whose files must be in production
	Digital   []string   `json:"-"`       // digital files (ids) whose files must be in production
	Duration  int64      `json:"ms"`
	Published time.Time  `json:"publishedAt"`
}

// NewSandboxID makes the id of a new sandbox (also its temporary address).
func NewSandboxID() string { return SandboxPrefix + randomID(10) }

// ErrNothingToPublish means the sandbox has no changes in those areas.
var ErrNothingToPublish = errors.New("store: nothing to publish")

// PublishSandbox applies the sandbox's changes of the given areas to
// production. Conflicts are applied only with force. beforeCommit runs with
// the result before the transaction commits (copying media files).
func (d *DB) PublishSandbox(ctx context.Context, sandboxID, actor string, areas, products []string, force bool, beforeCommit func(PublishResult) error) (PublishResult, error) {
	sb, err := d.SandboxByID(ctx, sandboxID)
	if err != nil {
		return PublishResult{}, err
	}
	tables, err := d.storeTables(ctx)
	if err != nil {
		return PublishResult{}, err
	}
	byName := map[string]tableInfo{}
	order := map[string]int{}
	for i, t := range tables {
		byName[t.name], order[t.name] = t, i
	}
	start := time.Now()
	res := PublishResult{ID: "pub_" + randomID(10), Published: time.Now().UTC()}
	err = pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		// One publish or revert at a time per store.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('sandbox-publish:' || $1))`, sb.ParentID); err != nil {
			return err
		}
		diffs, err := d.diffTx(ctx, tx, sb, areas, products, true)
		if err != nil {
			return err
		}
		res.Areas = diffs
		var chosen []RowChange
		for _, ad := range diffs {
			for _, c := range ad.changes {
				if c.Conflict && !force {
					res.Skipped++
					continue
				}
				chosen = append(chosen, c)
			}
		}
		if len(chosen) == 0 {
			return ErrNothingToPublish
		}
		// Rows to write go into a temporary table, so every statement below
		// works on sets (50 000 products publish in one pass per table).
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE _pub (tbl text, pk text, action text, sb_hash text, base_hash text) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"_pub"}, []string{"tbl", "pk", "action", "sb_hash", "base_hash"}, pgx.CopyFromSlice(len(chosen), func(i int) ([]any, error) {
			c := chosen[i]
			return []any{c.Table, c.PK, c.Action, c.sbHash, c.baseHash}, nil
		})); err != nil {
			return err
		}
		// A temporary table is never analyzed on its own: without its real
		// size the planner can choose a plan that is quadratic at 100 000
		// rows (minutes instead of seconds).
		if _, err := tx.Exec(ctx, `ANALYZE _pub`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sandbox_publishes (id, parent_id, sandbox_id, areas, summary, published_by) VALUES ($1,$2,$3,$4,$5,$6)`,
			res.ID, sb.ParentID, sb.ID, mustJSON(areas), mustJSON(diffs), actor); err != nil {
			return err
		}
		touched := map[string]bool{}
		for _, c := range chosen {
			touched[c.Table] = true
		}
		var tnames []string
		for tn := range touched {
			tnames = append(tnames, tn)
		}
		slices.SortFunc(tnames, func(a, b string) int { return order[a] - order[b] })
		// Keys are matched by their columns (index) unless one holds the
		// separator; the statements below then compare the text exactly.
		typed := map[string]bool{}
		for _, tn := range tnames {
			typed[tn] = typedKeys(ctx, tx, byName[tn], `SELECT pk FROM _pub WHERE tbl = $1`, tn)
		}
		pubKeys := func(action string) string { return `SELECT pk FROM _pub WHERE tbl = $3 AND action = '` + action + `'` }
		seq := 0
		save := func(t tableInfo, where string, args ...any) error {
			// Production as it is now, before this publish touches it.
			tag, err := tx.Exec(ctx, `INSERT INTO sandbox_publish_rows (publish_id, seq, tbl, pk, before, after_hash)
				SELECT $1, $2 + row_number() OVER (), $3, `+pkExpr("x", t)+`, to_jsonb(x.*), '' FROM `+quoteIdent(t.name)+` x WHERE `+where, append([]any{res.ID, seq, t.name}, args...)...)
			if err != nil {
				return err
			}
			seq += int(tag.RowsAffected())
			return nil
		}
		// Deletes, children first. Rows a delete would remove in cascade are
		// saved first, so the revert brings them back too.
		for i := len(tnames) - 1; i >= 0; i-- {
			t := byName[tnames[i]]
			if err := d.saveCascade(ctx, tx, t, sb.ParentID, &seq, res.ID, typed[t.name]); err != nil {
				return err
			}
			if err := save(t, `x.tenant_id = $4 AND `+keyIn("x", t, pubKeys("delete"), typed[t.name]), sb.ParentID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(t.name)+` x WHERE x.tenant_id = $1 AND `+keyIn("x", t, `SELECT pk FROM _pub WHERE tbl = $2 AND action = 'delete'`, typed[t.name]), sb.ParentID, t.name); err != nil {
				return fmt.Errorf("publish delete %s: %w", t.name, err)
			}
		}
		// Updates and inserts, parents first.
		for _, tn := range tnames {
			t := byName[tn]
			if err := save(t, `x.tenant_id = $4 AND `+keyIn("x", t, pubKeys("update"), typed[tn]), sb.ParentID); err != nil {
				return err
			}
			var set []string
			for _, c := range t.cols {
				if c == "tenant_id" || slices.Contains(t.pk, c) {
					continue
				}
				set = append(set, quoteIdent(c)+" = "+publishValue(tn, c, "s", "p"))
			}
			if len(set) > 0 {
				if _, err := tx.Exec(ctx, `UPDATE `+quoteIdent(tn)+` p SET `+strings.Join(set, ", ")+` FROM `+quoteIdent(tn)+` s
					WHERE p.tenant_id = $1 AND s.tenant_id = $2 AND `+sameKey("s", "p", t)+`
					AND `+keyIn("p", t, pubKeys("update"), typed[tn]), sb.ParentID, sb.ID, tn); err != nil {
					return fmt.Errorf("publish update %s: %w", tn, err)
				}
			}
			// Derived rows identified by what they say, not by their id (an
			// edge of the knowledge graph): production may have built the
			// same one with another id since the copy. The sandbox's replaces
			// it; the replaced row is saved, so a revert brings it back.
			if natural, ok := naturalKey[tn]; ok {
				cols := make([]string, len(natural))
				for i, c := range natural {
					cols[i] = quoteIdent(c)
				}
				// $3 is the table; sbx is the sandbox's placeholder.
				match := func(sbx string) string {
					return `(x.` + strings.Join(cols, ", x.") + `) IN (SELECT s.` + strings.Join(cols, ", s.") + ` FROM ` + quoteIdent(tn) + ` s
					WHERE s.tenant_id = ` + sbx + ` AND ` + keyIn("s", t, pubKeys("insert"), typed[tn]) + `)`
				}
				if err := save(t, `x.tenant_id = $4 AND `+match("$5"), sb.ParentID, sb.ID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(tn)+` x WHERE x.tenant_id = $1 AND `+match("$2"), sb.ParentID, sb.ID, tn); err != nil {
					return fmt.Errorf("publish replace %s: %w", tn, err)
				}
			}
			// An inserted row did not exist: it is saved with before = null.
			if _, err := tx.Exec(ctx, `INSERT INTO sandbox_publish_rows (publish_id, seq, tbl, pk, before, after_hash)
				SELECT $1, $2 + row_number() OVER (), $3, pk, NULL, '' FROM _pub WHERE tbl = $3 AND action = 'insert'`, res.ID, seq, tn); err != nil {
				return err
			}
			var n int
			_ = tx.QueryRow(ctx, `SELECT count(*) FROM _pub WHERE tbl = $1 AND action = 'insert'`, tn).Scan(&n)
			seq += n
			cols := make([]string, len(t.cols))
			sel := make([]string, len(t.cols))
			for i, c := range t.cols {
				cols[i] = quoteIdent(c)
				sel[i] = publishValue(tn, c, "s", "")
				if c == "tenant_id" {
					sel[i] = "$1"
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO `+quoteIdent(tn)+` (`+strings.Join(cols, ", ")+`) SELECT `+strings.Join(sel, ", ")+` FROM `+quoteIdent(tn)+` s
				WHERE s.tenant_id = $2 AND `+keyIn("s", t, pubKeys("insert"), typed[tn]), sb.ParentID, sb.ID, tn); err != nil {
				return fmt.Errorf("publish insert %s: %w", tn, err)
			}
		}
		// The rows as published, to know at revert time whether production
		// changed them since.
		for _, tn := range tnames {
			t := byName[tn]
			if _, err := tx.Exec(ctx, `UPDATE sandbox_publish_rows r SET after_hash = `+rowHash("x", tn)+` FROM `+quoteIdent(tn)+` x
				WHERE r.publish_id = $1 AND r.tbl = $2 AND x.tenant_id = $3 AND `+keyEq("x", t, "r.pk", typed[tn]), res.ID, tn, sb.ParentID); err != nil {
				return err
			}
		}
		// The base moves to what is now common to both sides; the old base
		// is kept with the publish so a revert can restore it.
		if _, err := tx.Exec(ctx, `UPDATE sandbox_publish_rows r SET base_before = q.base_hash FROM _pub q WHERE r.publish_id = $1 AND r.tbl = q.tbl AND r.pk = q.pk`, res.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM sandbox_base b USING _pub q WHERE b.sandbox_id = $1 AND b.tbl = q.tbl AND b.pk = q.pk`, sb.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sandbox_base (sandbox_id, tbl, pk, hash) SELECT $1, tbl, pk, sb_hash FROM _pub WHERE action <> 'delete'`, sb.ID); err != nil {
			return err
		}
		res.Applied = len(chosen)
		for _, c := range chosen {
			switch c.Table {
			case "ai_chunks":
				if parts := strings.Split(c.PK, "|"); len(parts) >= 2 && parts[0] == "product" {
					res.Products = append(res.Products, parts[1])
				}
			case "media":
				if c.Action != ActDelete {
					res.MediaIDs = append(res.MediaIDs, c.PK)
				}
			case "digital_files":
				if c.Action != ActDelete {
					res.Digital = append(res.Digital, c.PK)
				}
			}
		}
		res.Duration = time.Since(start).Milliseconds()
		if beforeCommit != nil {
			if err := beforeCommit(res); err != nil {
				return err
			}
		}
		if err := Audit(ctx, tx, sb.ParentID, actor, "sandbox.publish", res.ID, map[string]any{"areas": areas, "products": products, "applied": res.Applied, "skipped": res.Skipped}); err != nil {
			return err
		}
		if len(res.Products) > 0 {
			if err := Emit(ctx, tx, sb.ParentID, TopicAIVectors, joinIDs(res.Products), nil); err != nil {
				return err
			}
		}
		return Emit(ctx, tx, sb.ParentID, TopicTenantReload, "", nil)
	})
	if err == nil && res.Applied >= analyzeAfter {
		d.analyzeSandboxTables(ctx)
	}
	return res, err
}

// analyzeAfter: a publish or revert of this many rows refreshes statistics.
const analyzeAfter = 1000

// saveCascade saves the production rows that deleting t's chosen rows would
// remove through ON DELETE CASCADE, when they are not deleted on their own.
// publishValue is what a publish writes in column c from the sandbox row s
// (p: production's row on an update, "" on an insert). Stock carries the
// team's adjustments only: what test orders took is added back, and
// production's reservations stay as they are.
func publishValue(table, c, s, p string) string {
	if table == "stock_levels" {
		switch c {
		case "on_hand":
			return s + ".on_hand + " + consumedBy(s)
		case "reserved":
			if p == "" {
				return "0"
			}
			return p + ".reserved"
		case "updated_at":
			return "now()"
		}
	}
	return s + "." + quoteIdent(c)
}

// sameKey is "rows a and b have the same key", by columns.
func sameKey(a, b string, t tableInfo) string {
	if len(t.pk) == 0 {
		return pkExpr(a, t) + " = " + pkExpr(b, t)
	}
	conds := make([]string, len(t.pk))
	for i, c := range t.pk {
		conds[i] = a + "." + quoteIdent(c) + " = " + b + "." + quoteIdent(c)
	}
	return strings.Join(conds, " AND ")
}

func (d *DB) saveCascade(ctx context.Context, tx pgx.Tx, parent tableInfo, tenant string, seq *int, pubID string, typed bool) error {
	rows, err := tx.Query(ctx, `SELECT conrelid::regclass::text,
			(SELECT array_agg(a.attname ORDER BY k.n) FROM unnest(conkey) WITH ORDINALITY k(attnum, n) JOIN pg_attribute a ON a.attrelid = conrelid AND a.attnum = k.attnum),
			(SELECT array_agg(a.attname ORDER BY k.n) FROM unnest(confkey) WITH ORDINALITY k(attnum, n) JOIN pg_attribute a ON a.attrelid = confrelid AND a.attnum = k.attnum)
		FROM pg_constraint WHERE contype = 'f' AND confdeltype = 'c' AND confrelid = $1::regclass`, parent.name)
	if err != nil {
		return err
	}
	type fk struct {
		child      string
		cols, refs []string
	}
	var fks []fk
	for rows.Next() {
		var f fk
		if err := rows.Scan(&f.child, &f.cols, &f.refs); err != nil {
			rows.Close()
			return err
		}
		fks = append(fks, f)
	}
	rows.Close()
	tables, err := d.storeTables(ctx)
	if err != nil {
		return err
	}
	for _, f := range fks {
		var ct tableInfo
		for _, t := range tables {
			if t.name == f.child {
				ct = t
			}
		}
		if ct.name == "" {
			continue
		}
		var on []string
		for i := range f.cols {
			on = append(on, "c."+quoteIdent(f.cols[i])+" = p."+quoteIdent(f.refs[i]))
		}
		tag, err := tx.Exec(ctx, `INSERT INTO sandbox_publish_rows (publish_id, seq, tbl, pk, before, after_hash)
			SELECT $1, $2 + row_number() OVER (), $3, `+pkExpr("c", ct)+`, to_jsonb(c.*), '' FROM `+quoteIdent(ct.name)+` c JOIN `+quoteIdent(parent.name)+` p ON `+strings.Join(on, " AND ")+`
			WHERE p.tenant_id = $4 AND `+keyIn("p", parent, `SELECT pk FROM _pub WHERE tbl = $5 AND action = 'delete'`, typed)+`
			AND NOT EXISTS (SELECT 1 FROM sandbox_publish_rows r WHERE r.publish_id = $1 AND r.tbl = $3 AND r.pk = `+pkExpr("c", ct)+`)`,
			pubID, *seq, ct.name, tenant, parent.name)
		if err != nil {
			return fmt.Errorf("save cascade %s: %w", ct.name, err)
		}
		*seq += int(tag.RowsAffected())
	}
	return nil
}

// Publish is a past publish to production.
type Publish struct {
	ID          string          `json:"id"`
	SandboxID   string          `json:"sandboxId"`
	Areas       []string        `json:"areas"`
	Summary     json.RawMessage `json:"summary"`
	PublishedBy string          `json:"publishedBy"`
	PublishedAt time.Time       `json:"publishedAt"`
	RevertedBy  *string         `json:"revertedBy,omitempty"`
	RevertedAt  *time.Time      `json:"revertedAt,omitempty"`
	Rows        int             `json:"rows"`
}

// Publishes lists a store's publishes, newest first.
func (d *DB) Publishes(ctx context.Context, parent string, limit int) ([]Publish, error) {
	rows, err := d.System.Query(ctx, `SELECT p.id, p.sandbox_id, p.areas, p.summary, p.published_by, p.published_at, p.reverted_by, p.reverted_at,
		(SELECT count(*) FROM sandbox_publish_rows r WHERE r.publish_id = p.id)
		FROM sandbox_publishes p WHERE p.parent_id = $1 ORDER BY p.published_at DESC LIMIT $2`, parent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Publish
	for rows.Next() {
		var p Publish
		var areas []byte
		if err := rows.Scan(&p.ID, &p.SandboxID, &areas, &p.Summary, &p.PublishedBy, &p.PublishedAt, &p.RevertedBy, &p.RevertedAt, &p.Rows); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(areas, &p.Areas)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrRevertConflict means production changed some published rows since.
type ErrRevertConflict struct{ Rows []string }

func (e *ErrRevertConflict) Error() string {
	return fmt.Sprintf("store: %d published rows changed in production since", len(e.Rows))
}

// ErrAlreadyReverted means the publish was already reverted.
var ErrAlreadyReverted = errors.New("store: already reverted")

// RevertPublish puts production back exactly as it was before a publish.
// Rows production changed after the publish are reported and only
// overwritten with force.
func (d *DB) RevertPublish(ctx context.Context, parent, publishID, actor string, force bool) (int, []string, error) {
	tables, err := d.storeTables(ctx)
	if err != nil {
		return 0, nil, err
	}
	byName := map[string]tableInfo{}
	for _, t := range tables {
		byName[t.name] = t
	}
	n := 0
	var products []string
	err = pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('sandbox-publish:' || $1))`, parent); err != nil {
			return err
		}
		var sandboxID string
		var reverted *time.Time
		if err := tx.QueryRow(ctx, `SELECT sandbox_id, reverted_at FROM sandbox_publishes WHERE id = $1 AND parent_id = $2 FOR UPDATE`, publishID, parent).Scan(&sandboxID, &reverted); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if reverted != nil {
			return ErrAlreadyReverted
		}
		// What changed in production since the publish.
		type row struct {
			seq        int
			tbl, pk    string
			before     []byte
			afterHash  string
			baseBefore *string
		}
		rows, err := tx.Query(ctx, `SELECT seq, tbl, pk, before, after_hash, base_before FROM sandbox_publish_rows WHERE publish_id = $1 ORDER BY seq DESC`, publishID)
		if err != nil {
			return err
		}
		var all []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.seq, &r.tbl, &r.pk, &r.before, &r.afterHash, &r.baseBefore); err != nil {
				rows.Close()
				return err
			}
			all = append(all, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		var changed []string
		for tn := range groupTables(all, func(r row) string { return r.tbl }) {
			t := byName[tn]
			typed := typedKeys(ctx, tx, t, `SELECT pk FROM sandbox_publish_rows WHERE publish_id = $1 AND tbl = $2`, publishID, tn)
			crow, err := tx.Query(ctx, `SELECT r.pk FROM sandbox_publish_rows r LEFT JOIN `+quoteIdent(tn)+` x ON x.tenant_id = $3 AND `+keyEq("x", t, "r.pk", typed)+`
				WHERE r.publish_id = $1 AND r.tbl = $2 AND COALESCE(`+rowHash("x", tn)+`, '') <> r.after_hash AND NOT (x.tenant_id IS NULL AND r.after_hash = '')`, publishID, tn, parent)
			if err != nil {
				return err
			}
			for crow.Next() {
				var pk string
				if err := crow.Scan(&pk); err != nil {
					crow.Close()
					return err
				}
				changed = append(changed, tn+":"+pk)
			}
			crow.Close()
		}
		if len(changed) > 0 && !force {
			return &ErrRevertConflict{Rows: changed}
		}
		// Back, newest first, in runs of the same table and kind.
		for i := 0; i < len(all); {
			j := i
			for j < len(all) && all[j].tbl == all[i].tbl && (all[j].before == nil) == (all[i].before == nil) {
				j++
			}
			run := all[i:j]
			t := byName[run[0].tbl]
			pks := make([]string, len(run))
			for k, r := range run {
				pks[k] = r.pk
			}
			if run[0].before == nil && appendOnly[t.name] {
				// A site revision is immutable and content-addressed: it
				// stays, and production goes back to its previous release.
				n += len(run)
				i = j
				continue
			}
			if run[0].before == nil {
				// Rows the publish added: removed (their own children were
				// added by the same publish and go with them).
				typed := typedKeys(ctx, tx, t, `SELECT unnest($1::text[])`, pks)
				if _, err := tx.Exec(ctx, `DELETE FROM `+quoteIdent(t.name)+` x WHERE x.tenant_id = $1 AND `+keyIn("x", t, `SELECT unnest($2::text[])`, typed), parent, pks); err != nil {
					return fmt.Errorf("revert %s: %w", t.name, err)
				}
			} else {
				// Rows it changed or deleted: written back as they were, in
				// place, so no row that hangs from them is touched.
				befores := make([][]byte, len(run))
				for k, r := range run {
					befores[k] = r.before
				}
				conflict := []string{"tenant_id"}
				var set []string
				for _, c := range t.pk {
					conflict = append(conflict, quoteIdent(c))
				}
				for _, c := range t.cols {
					// Production's reservations are live orders: a revert of
					// stock never writes them back.
					if c != "tenant_id" && !slices.Contains(t.pk, c) && !(t.name == "stock_levels" && c == "reserved") {
						set = append(set, quoteIdent(c)+" = EXCLUDED."+quoteIdent(c))
					}
				}
				q := `INSERT INTO ` + quoteIdent(t.name) + ` SELECT (jsonb_populate_record(NULL::` + quoteIdent(t.name) + `, b)).* FROM unnest($1::jsonb[]) AS b ON CONFLICT (` + strings.Join(conflict, ", ") + `) DO `
				if len(set) > 0 && len(t.pk) > 0 {
					q += `UPDATE SET ` + strings.Join(set, ", ")
				} else {
					q += `NOTHING`
				}
				if _, err := tx.Exec(ctx, q, befores); err != nil {
					return fmt.Errorf("revert restore %s: %w", t.name, err)
				}
			}
			for _, r := range run {
				if r.tbl == "ai_chunks" {
					if parts := strings.Split(r.pk, "|"); len(parts) >= 2 && parts[0] == "product" {
						products = append(products, parts[1])
					}
				}
			}
			n += len(run)
			i = j
		}
		// The sandbox's base goes back too: its changes count as changes again.
		if _, err := tx.Exec(ctx, `DELETE FROM sandbox_base b USING sandbox_publish_rows r WHERE r.publish_id = $1 AND b.sandbox_id = $2 AND b.tbl = r.tbl AND b.pk = r.pk`, publishID, sandboxID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sandbox_base (sandbox_id, tbl, pk, hash) SELECT DISTINCT ON (r.tbl, r.pk) $2, r.tbl, r.pk, r.base_before FROM sandbox_publish_rows r
			JOIN sandboxes s ON s.id = $2 WHERE r.publish_id = $1 AND COALESCE(r.base_before, '') <> '' ON CONFLICT DO NOTHING`, publishID, sandboxID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE sandbox_publishes SET reverted_by = $2, reverted_at = now() WHERE id = $1`, publishID, actor); err != nil {
			return err
		}
		if err := Audit(ctx, tx, parent, actor, "sandbox.revert", publishID, map[string]any{"rows": n, "forced": len(changed)}); err != nil {
			return err
		}
		if len(products) > 0 {
			if err := Emit(ctx, tx, parent, TopicAIVectors, joinIDs(slices.Compact(products)), nil); err != nil {
				return err
			}
		}
		return Emit(ctx, tx, parent, TopicTenantReload, "", nil)
	})
	var conflict *ErrRevertConflict
	if errors.As(err, &conflict) {
		return 0, conflict.Rows, err
	}
	if err == nil && n >= analyzeAfter {
		d.analyzeSandboxTables(ctx)
	}
	return n, nil, err
}

// naturalKey: derived tables whose rows are identified by their content
// (a unique key besides the id), so the same row can exist on both sides
// with different ids.
var naturalKey = map[string][]string{"kg_edges": {"src_kind", "src_id", "rel", "dst_kind", "dst_id"}}

// appendOnly tables never lose a row in production.
var appendOnly = map[string]bool{"site_revisions": true, "audit_log": true, "order_events": true, "ledger_entries": true, "price_history": true}

func groupTables[T any](rows []T, key func(T) string) map[string]bool {
	m := map[string]bool{}
	for _, r := range rows {
		m[key(r)] = true
	}
	return m
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func randomID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
