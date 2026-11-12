// Package store is the PostgreSQL persistence layer. Two connection pools are
// used on purpose:
//
//   - App: role apexx_app, subject to Row Level Security. Every transaction
//     sets app.tenant_id, so a bug in a query cannot read another tenant.
//   - System: role apexx_system (BYPASSRLS), used only by the projector, the
//     API-key lookup and platform administration.
package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB holds both pools.
type DB struct {
	App    *pgxpool.Pool
	System *pgxpool.Pool
}

// Open connects both pools.
func Open(ctx context.Context, appURL, systemURL string) (*DB, error) {
	app, err := pgxpool.New(ctx, appURL)
	if err != nil {
		return nil, fmt.Errorf("app pool: %w", err)
	}
	sys, err := pgxpool.New(ctx, systemURL)
	if err != nil {
		app.Close()
		return nil, fmt.Errorf("system pool: %w", err)
	}
	if err := app.Ping(ctx); err != nil {
		return nil, fmt.Errorf("app ping: %w", err)
	}
	if err := sys.Ping(ctx); err != nil {
		return nil, fmt.Errorf("system ping: %w", err)
	}
	return &DB{App: app, System: sys}, nil
}

// Close closes both pools.
func (d *DB) Close() {
	d.App.Close()
	d.System.Close()
}

// ErrNoTenant is returned when a tenant transaction is opened without a tenant.
var ErrNoTenant = errors.New("store: tenant id required")

// Tx runs fn in a transaction scoped to tenant (RLS enforced).
func (d *DB) Tx(ctx context.Context, tenant string, fn func(pgx.Tx) error) error {
	if tenant == "" {
		return ErrNoTenant
	}
	return pgx.BeginFunc(ctx, d.App, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenant); err != nil {
			return err
		}
		return fn(tx)
	})
}

// Emit writes an outbox event in the caller's transaction.
func Emit(ctx context.Context, tx pgx.Tx, tenant, topic, key string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox (tenant_id, topic, key, payload) VALUES ($1,$2,$3,$4)`, tenant, topic, key, b)
	return err
}

// Audit appends a hash-chained audit record in the caller's transaction.
func Audit(ctx context.Context, tx pgx.Tx, tenant, actor, action, target string, data any) error {
	b, err := canonicalJSON(data)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('audit:' || $1))`, tenant); err != nil {
		return err
	}
	var seq int64
	prev := make([]byte, 32)
	err = tx.QueryRow(ctx, `SELECT seq, hash FROM audit_log WHERE tenant_id=$1 ORDER BY seq DESC LIMIT 1`, tenant).Scan(&seq, &prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	seq++
	h := sha256.New()
	fmt.Fprintf(h, "%x|%d|%s|%s|%s|%s|%s", prev, seq, tenant, actor, action, target, b)
	_, err = tx.Exec(ctx, `INSERT INTO audit_log (tenant_id, seq, actor, action, target, data, prev_hash, hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, seq, actor, action, target, b, prev, h.Sum(nil))
	return err
}

type auditEntry struct {
	action, target string
	data           any
}

// auditMany appends several entries to the hash chain with one lock, one
// read and one insert, computing each hash exactly as Audit does.
func auditMany(ctx context.Context, tx pgx.Tx, tenant, actor string, es []auditEntry) error {
	if len(es) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('audit:' || $1))`, tenant); err != nil {
		return err
	}
	var seq int64
	prev := make([]byte, 32)
	err := tx.QueryRow(ctx, `SELECT seq, hash FROM audit_log WHERE tenant_id=$1 ORDER BY seq DESC LIMIT 1`, tenant).Scan(&seq, &prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	seqs := make([]int64, len(es))
	actions, targets, datas := make([]string, len(es)), make([]string, len(es)), make([]string, len(es))
	prevs, hashes := make([][]byte, len(es)), make([][]byte, len(es))
	for i, e := range es {
		b, err := canonicalJSON(e.data)
		if err != nil {
			return err
		}
		seq++
		h := sha256.New()
		fmt.Fprintf(h, "%x|%d|%s|%s|%s|%s|%s", prev, seq, tenant, actor, e.action, e.target, b)
		sum := h.Sum(nil)
		seqs[i], actions[i], targets[i], datas[i], prevs[i], hashes[i] = seq, e.action, e.target, string(b), prev, sum
		prev = sum
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log (tenant_id, seq, actor, action, target, data, prev_hash, hash)
		SELECT $1, s, $2, a, t, d::jsonb, p, h FROM unnest($3::bigint[], $4::text[], $5::text[], $6::text[], $7::bytea[], $8::bytea[]) AS x(s, a, t, d, p, h)`,
		tenant, actor, seqs, actions, targets, datas, prevs, hashes)
	return err
}

// VerifyAudit recomputes the hash chain of a tenant and returns the first broken seq (0 = intact).
func (d *DB) VerifyAudit(ctx context.Context, tenant string) (int64, error) {
	rows, err := d.System.Query(ctx, `SELECT seq, actor, action, target, data::text, prev_hash, hash
		FROM audit_log WHERE tenant_id=$1 ORDER BY seq`, tenant)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	prev := make([]byte, 32)
	for rows.Next() {
		var seq int64
		var actor, action, target, data string
		var p, hsh []byte
		if err := rows.Scan(&seq, &actor, &action, &target, &data, &p, &hsh); err != nil {
			return 0, err
		}
		// jsonb reorders keys; hash over the canonical form used when writing.
		compact, err := canonicalJSON(json.RawMessage(data))
		if err != nil {
			return seq, nil
		}
		h := sha256.New()
		fmt.Fprintf(h, "%x|%d|%s|%s|%s|%s|%s", prev, seq, tenant, actor, action, target, compact)
		if string(p) != string(prev) || string(h.Sum(nil)) != string(hsh) {
			return seq, nil
		}
		prev = hsh
	}
	return 0, rows.Err()
}

// Migrate applies pending migrations using an administrative connection and
// makes sure the application roles exist with the given passwords.
func Migrate(ctx context.Context, adminURL, appPassword, systemPassword string) ([]string, error) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var applied []string
	for _, n := range names {
		base := strings.TrimPrefix(n, "migrations/")
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name=$1)`, base).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		sqlText, _ := migrationsFS.ReadFile(n)
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, base)
			return err
		})
		if err != nil {
			return applied, fmt.Errorf("migration %s: %w", base, err)
		}
		applied = append(applied, base)
	}
	if err := ensureRoles(ctx, conn, appPassword, systemPassword); err != nil {
		return applied, err
	}
	return applied, nil
}

// execRoleRetry runs a role statement. Roles are shared by every database of
// the server, so two migrations at once (deploys, parallel tests) can collide
// with "tuple concurrently updated"; that case is simply tried again.
func execRoleRetry(ctx context.Context, conn *pgx.Conn, q string) error {
	var err error
	for i := 0; i < 5; i++ {
		if _, err = conn.Exec(ctx, q); err == nil || !strings.Contains(err.Error(), "tuple concurrently updated") {
			return err
		}
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return err
}

func ensureRoles(ctx context.Context, conn *pgx.Conn, appPassword, systemPassword string) error {
	for _, r := range []struct{ name, pass, extra string }{
		{"apexx_app", appPassword, "NOBYPASSRLS"},
		{"apexx_system", systemPassword, "BYPASSRLS"},
	} {
		if r.pass == "" {
			return fmt.Errorf("password for %s is empty", r.name)
		}
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)`, r.name).Scan(&exists); err != nil {
			return err
		}
		verb := "CREATE"
		if exists {
			verb = "ALTER"
		}
		q := fmt.Sprintf("%s ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE %s PASSWORD %s",
			verb, r.name, r.extra, quoteLiteral(r.pass))
		if err := execRoleRetry(ctx, conn, q); err != nil {
			return fmt.Errorf("role %s: %w", r.name, err)
		}
		for _, g := range []string{
			"GRANT USAGE ON SCHEMA public TO " + r.name,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + r.name,
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + r.name,
			"REVOKE ALL ON schema_migrations FROM " + r.name,
		} {
			if err := execRoleRetry(ctx, conn, g); err != nil {
				return fmt.Errorf("grant %s: %w", r.name, err)
			}
		}
	}
	// The application role never reads API key hashes or wrapped keys directly.
	for _, q := range []string{
		"REVOKE ALL ON api_keys FROM apexx_app",
		"REVOKE ALL ON sessions FROM apexx_app",
		"REVOKE ALL ON job_leases FROM apexx_app",
		"REVOKE ALL ON login_failures FROM apexx_app",
		"REVOKE INSERT, UPDATE, DELETE ON tenants FROM apexx_app",
	} {
		if _, err := conn.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// canonicalJSON marshals v with sorted object keys and exact numbers.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
