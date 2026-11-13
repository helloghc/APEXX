package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// Platform operators (superadmin). Every query uses the system role: the
// application role has no grant on these tables.

// Operator is a person who runs the platform.
type Operator struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	PasswordHash string     `json:"-"`
	TOTPSecret   []byte     `json:"-"` // encrypted with the platform key
	TOTPLastStep int64      `json:"-"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
	DisabledAt   *time.Time `json:"disabledAt,omitempty"`
}

const operatorCols = `id, email, name, password_hash, totp_secret, totp_last_step, created_at, last_login_at, disabled_at`

func scanOperator(row pgx.Row) (Operator, error) {
	var o Operator
	err := row.Scan(&o.ID, &o.Email, &o.Name, &o.PasswordHash, &o.TOTPSecret, &o.TOTPLastStep, &o.CreatedAt, &o.LastLoginAt, &o.DisabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// CreateOperator adds an operator (command line only).
func (d *DB) CreateOperator(ctx context.Context, email, name, passwordHash string, totpSecret []byte) (Operator, error) {
	id := "op_" + randomID(12)
	_, err := d.System.Exec(ctx, `INSERT INTO operators (id, email, name, password_hash, totp_secret) VALUES ($1, lower($2), $3, $4, $5)`, id, email, name, passwordHash, totpSecret)
	if isUnique(err) {
		return Operator{}, ErrConflict
	}
	if err != nil {
		return Operator{}, err
	}
	return d.OperatorByID(ctx, id)
}

// OperatorByEmail finds an operator (any state).
func (d *DB) OperatorByEmail(ctx context.Context, email string) (Operator, error) {
	return scanOperator(d.System.QueryRow(ctx, `SELECT `+operatorCols+` FROM operators WHERE email = lower($1)`, email))
}

// OperatorByID finds an operator (any state).
func (d *DB) OperatorByID(ctx context.Context, id string) (Operator, error) {
	return scanOperator(d.System.QueryRow(ctx, `SELECT `+operatorCols+` FROM operators WHERE id = $1`, id))
}

// UseOperatorStep records the two-step code's step; false if that step (or
// a later one) was already used: a code works once.
func (d *DB) UseOperatorStep(ctx context.Context, id string, step int64) (bool, error) {
	tag, err := d.System.Exec(ctx, `UPDATE operators SET totp_last_step = $2, last_login_at = now() WHERE id = $1 AND totp_last_step < $2 AND disabled_at IS NULL`, id, step)
	return tag.RowsAffected() == 1, err
}

// DisableOperator turns an operator off and ends their sessions.
func (d *DB) DisableOperator(ctx context.Context, email string) error {
	return pgx.BeginFunc(ctx, d.System, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `UPDATE operators SET disabled_at = now() WHERE email = lower($1) RETURNING id`, email).Scan(&id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM operator_sessions WHERE operator_id = $1`, id)
		return err
	})
}

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// StartOperatorSession stores a session (by the token's hash).
func (d *DB) StartOperatorSession(ctx context.Context, operatorID, token, ip, ua string, ttl time.Duration) error {
	_, err := d.System.Exec(ctx, `INSERT INTO operator_sessions (token_hash, operator_id, ip, user_agent, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		tokenHash(token), operatorID, ip, ua, time.Now().Add(ttl))
	return err
}

// OperatorSession returns the operator of a live session.
func (d *DB) OperatorSession(ctx context.Context, token string) (Operator, error) {
	return scanOperator(d.System.QueryRow(ctx, `SELECT `+prefixed("o.", operatorCols)+` FROM operator_sessions s JOIN operators o ON o.id = s.operator_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND o.disabled_at IS NULL`, tokenHash(token)))
}

// EndOperatorSession deletes a session (and the expired ones).
func (d *DB) EndOperatorSession(ctx context.Context, token string) error {
	_, err := d.System.Exec(ctx, `DELETE FROM operator_sessions WHERE token_hash = $1 OR expires_at < now()`, tokenHash(token))
	return err
}

// LogOperator records what an operator did.
func (d *DB) LogOperator(ctx context.Context, operatorID, action, target, ip string, data any) error {
	_, err := d.System.Exec(ctx, `INSERT INTO operator_log (operator_id, action, target, data, ip) VALUES ($1,$2,$3,$4,$5)`, operatorID, action, target, mustJSON(data), ip)
	return err
}

// OperatorLogEntry is one line of the operators' log.
type OperatorLogEntry struct {
	At       time.Time      `json:"at"`
	Operator string         `json:"operator"`
	Action   string         `json:"action"`
	Target   string         `json:"target"`
	Data     map[string]any `json:"data"`
}

// OperatorLog lists the latest operator actions.
func (d *DB) OperatorLog(ctx context.Context, limit int) ([]OperatorLogEntry, error) {
	rows, err := d.System.Query(ctx, `SELECT l.at, COALESCE(o.email, l.operator_id), l.action, l.target, l.data FROM operator_log l
		LEFT JOIN operators o ON o.id = l.operator_id ORDER BY l.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OperatorLogEntry, error) {
		var e OperatorLogEntry
		return e, r.Scan(&e.At, &e.Operator, &e.Action, &e.Target, &e.Data)
	})
	if out == nil {
		out = []OperatorLogEntry{}
	}
	return out, err
}

// PlatformKey returns the platform's wrapped key, creating it with mk the
// first time.
func (d *DB) PlatformKey(ctx context.Context, id string, mk func() ([]byte, error)) ([]byte, error) {
	var w []byte
	err := d.System.QueryRow(ctx, `SELECT wrapped FROM platform_keys WHERE id = $1`, id).Scan(&w)
	if err == nil {
		return w, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if w, err = mk(); err != nil {
		return nil, err
	}
	if _, err := d.System.Exec(ctx, `INSERT INTO platform_keys (id, wrapped) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, id, w); err != nil {
		return nil, err
	}
	err = d.System.QueryRow(ctx, `SELECT wrapped FROM platform_keys WHERE id = $1`, id).Scan(&w)
	return w, err
}

// ---------- stores for the operators ----------

// SandboxUsage is one sandbox of a store, with what it takes.
type SandboxUsage struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Scope    string  `json:"scope"`
	Status   string  `json:"status"`
	Products int64   `json:"products"`
	SizeMB   float64 `json:"sizeMB"` // estimated database size
}

// StoreSummary is a production store as an operator sees it.
type StoreSummary struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	CreatedAt time.Time      `json:"createdAt"`
	Plan      Plan           `json:"plan"`
	Members   int64          `json:"members"`
	Products  int64          `json:"products"`
	SizeMB    float64        `json:"sizeMB"`
	Sandboxes []SandboxUsage `json:"sandboxes"`
}

// sizeTables: the tables that make a store's (or sandbox's) size.
var sizeTables = []string{"products", "variants", "prices", "stock_levels", "media", "ai_chunks", "kg_edges", "price_list_items", "custom_values", "site_revisions"}

// StoreSummaries lists production stores (by id or name containing q) with
// their plan, team size, products and sandboxes. Sizes are estimates: rows
// of the largest tables times their average row size.
func (d *DB) StoreSummaries(ctx context.Context, q string, limit int) ([]StoreSummary, error) {
	rows, err := d.System.Query(ctx, `SELECT t.id, t.name, t.created_at,
			COALESCE(p.plan, ''), COALESCE(p.sandboxes, 1), COALESCE(p.sandbox_max_products, 0), COALESCE(p.sandbox_demo_data, true), COALESCE(p.sandbox_schedule, true),
			(SELECT count(*) FROM members m WHERE m.tenant_id = t.id AND m.status = 'active')
		FROM tenants t LEFT JOIN tenant_plans p ON p.tenant_id = t.id
		WHERE t.kind = 'production' AND ($1 = '' OR t.id ILIKE '%' || $1 || '%' OR t.name ILIKE '%' || $1 || '%')
		ORDER BY t.created_at DESC LIMIT $2`, q, limit)
	if err != nil {
		return nil, err
	}
	stores, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (StoreSummary, error) {
		var s StoreSummary
		return s, r.Scan(&s.ID, &s.Name, &s.CreatedAt, &s.Plan.Plan, &s.Plan.Sandboxes, &s.Plan.SandboxMaxProducts, &s.Plan.SandboxDemoData, &s.Plan.SandboxSchedule, &s.Members)
	})
	if err != nil || len(stores) == 0 {
		return stores, err
	}
	ids := make([]string, len(stores))
	for i, s := range stores {
		ids[i] = s.ID
	}
	type sbx struct {
		parent string
		u      SandboxUsage
	}
	srows, err := d.System.Query(ctx, `SELECT parent_id, id, name, scope, status FROM sandboxes WHERE parent_id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	sbxs, err := pgx.CollectRows(srows, func(r pgx.CollectableRow) (sbx, error) {
		var x sbx
		return x, r.Scan(&x.parent, &x.u.ID, &x.u.Name, &x.u.Scope, &x.u.Status)
	})
	if err != nil {
		return nil, err
	}
	all := slicesConcat(ids, sbxs, func(x sbx) string { return x.u.ID })
	size, products, err := d.tenantSizes(ctx, all)
	if err != nil {
		return nil, err
	}
	for i := range stores {
		s := &stores[i]
		s.Products, s.SizeMB = products[s.ID], size[s.ID]
		s.Sandboxes = []SandboxUsage{}
		for _, x := range sbxs {
			if x.parent == s.ID {
				x.u.Products, x.u.SizeMB = products[x.u.ID], size[x.u.ID]
				s.Sandboxes = append(s.Sandboxes, x.u)
			}
		}
	}
	return stores, nil
}

func slicesConcat[T any](ids []string, more []T, id func(T) string) []string {
	out := append([]string{}, ids...)
	for _, x := range more {
		out = append(out, id(x))
	}
	return out
}

// tenantSizes estimates the database size (MB) and counts the products of
// some stores.
func (d *DB) tenantSizes(ctx context.Context, tenants []string) (map[string]float64, map[string]int64, error) {
	// The average size of a row of each table, from a sample of live rows
	// (the table's file also holds space of deleted rows: it would inflate
	// the estimate).
	avg := map[string]float64{}
	for _, t := range append(slices.Clone(sizeTables), "sandbox_base") {
		var b *float64
		if err := d.System.QueryRow(ctx, `SELECT avg(pg_column_size(x.*))::float8 FROM (SELECT * FROM `+quoteIdent(t)+` LIMIT 2000) x`).Scan(&b); err != nil {
			return nil, nil, err
		}
		if b != nil {
			avg[t] = *b
		}
	}
	size := map[string]float64{}
	products := map[string]int64{}
	for _, t := range sizeTables {
		r, err := d.System.Query(ctx, `SELECT tenant_id, count(*) FROM `+quoteIdent(t)+` WHERE tenant_id = ANY($1) GROUP BY 1`, tenants)
		if err != nil {
			return nil, nil, err
		}
		for r.Next() {
			var id string
			var n int64
			if err := r.Scan(&id, &n); err != nil {
				r.Close()
				return nil, nil, err
			}
			size[id] += float64(n) * avg[t] / 1048576
			if t == "products" {
				products[id] = n
			}
		}
		r.Close()
	}
	// A sandbox also keeps the base of its rows (for publishing).
	r, err := d.System.Query(ctx, `SELECT sandbox_id, count(*) FROM sandbox_base WHERE sandbox_id = ANY($1) GROUP BY 1`, tenants)
	if err != nil {
		return nil, nil, err
	}
	baseAvg := avg["sandbox_base"]
	for r.Next() {
		var id string
		var n int64
		if err := r.Scan(&id, &n); err != nil {
			r.Close()
			return nil, nil, err
		}
		size[id] += float64(n) * baseAvg / 1048576
	}
	r.Close()
	for k, v := range size {
		size[k] = float64(int64(v*10+0.5)) / 10
	}
	return size, products, nil
}

func prefixed(p, cols string) string {
	out := ""
	for i, c := range splitCols(cols) {
		if i > 0 {
			out += ", "
		}
		out += p + c
	}
	return out
}

func splitCols(cols string) []string {
	var out []string
	cur := ""
	for _, r := range cols {
		switch r {
		case ',':
			out = append(out, cur)
			cur = ""
		case ' ', '\n', '\t':
		default:
			cur += string(r)
		}
	}
	return append(out, cur)
}
