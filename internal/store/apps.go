package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// App is a third-party integration installed in a store.
type App struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Developer   string            `json:"developer"`
	URL         string            `json:"url"`
	Permissions map[string]string `json:"permissions"`
	CreatedBy   string            `json:"createdBy"`
	CreatedAt   time.Time         `json:"createdAt"`
	LastUsedAt  *time.Time        `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time        `json:"revokedAt,omitempty"`
}

const appCols = `id, name, developer, url, permissions, created_by, created_at, last_used_at, revoked_at`

func scanApp(r pgx.CollectableRow) (App, error) {
	var a App
	var perms []byte
	err := r.Scan(&a.ID, &a.Name, &a.Developer, &a.URL, &perms, &a.CreatedBy, &a.CreatedAt, &a.LastUsedAt, &a.RevokedAt)
	_ = json.Unmarshal(perms, &a.Permissions)
	return a, err
}

// CreateApp installs an app and returns its token (shown once).
func (d *DB) CreateApp(ctx context.Context, tenant, actor string, a App) (App, string, error) {
	idb, sec := make([]byte, 8), make([]byte, 24)
	_, _ = rand.Read(idb)
	_, _ = rand.Read(sec)
	a.ID = "app" + hex.EncodeToString(idb)
	secret := hex.EncodeToString(sec)
	sum := sha256.Sum256([]byte(secret))
	perms, _ := json.Marshal(a.Permissions)
	var out App
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM apps WHERE tenant_id=$1 AND revoked_at IS NULL`, tenant).Scan(&n); err != nil {
			return err
		}
		if n >= 50 {
			return errors.New("límite de 50 apps activas")
		}
		rows, err := tx.Query(ctx, `INSERT INTO apps (id, tenant_id, name, developer, url, permissions, secret_hash, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+appCols,
			a.ID, tenant, a.Name, a.Developer, a.URL, perms, sum[:], actor)
		if err != nil {
			return err
		}
		if out, err = pgx.CollectExactlyOneRow(rows, scanApp); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "app.installed", a.ID, map[string]any{"name": a.Name, "permissions": a.Permissions})
	})
	return out, "apa_" + a.ID + "_" + secret, err
}

// Apps lists the apps of a store, newest first.
func (d *DB) Apps(ctx context.Context, tenant string) ([]App, error) {
	var out []App
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+appCols+` FROM apps WHERE tenant_id=$1 ORDER BY revoked_at IS NOT NULL, created_at DESC`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanApp)
		return err
	})
	return out, err
}

// UpdateAppPermissions changes what an active app may do; it applies to its
// next request.
func (d *DB) UpdateAppPermissions(ctx context.Context, tenant, actor, id string, perms map[string]string) error {
	b, _ := json.Marshal(perms)
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE apps SET permissions=$3 WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NULL`, tenant, id, b)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return Audit(ctx, tx, tenant, actor, "app.permissions", id, map[string]any{"permissions": perms})
	})
}

// RevokeApp cuts an app's access at once and for good.
func (d *DB) RevokeApp(ctx context.Context, tenant, actor, id string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE apps SET revoked_at=now() WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NULL`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		return Audit(ctx, tx, tenant, actor, "app.revoked", id, nil)
	})
}

// LookupApp authenticates an app token. It is read on every request (no
// cache), so a revocation takes effect on the very next call.
func (d *DB) LookupApp(ctx context.Context, token string) (tenant string, a App, err error) {
	rest, ok := strings.CutPrefix(token, "apa_")
	id, secret, ok2 := strings.Cut(rest, "_")
	if !ok || !ok2 || len(id) > 40 {
		return "", App{}, ErrBadKey
	}
	var hash, perms []byte
	var revoked *time.Time
	var last *time.Time
	err = d.System.QueryRow(ctx, `SELECT tenant_id, name, permissions, secret_hash, revoked_at, last_used_at FROM apps WHERE id=$1`, id).
		Scan(&tenant, &a.Name, &perms, &hash, &revoked, &last)
	sum := sha256.Sum256([]byte(secret))
	if err != nil || revoked != nil || subtle.ConstantTimeCompare(sum[:], hash) != 1 {
		return "", App{}, ErrBadKey
	}
	a.ID = id
	_ = json.Unmarshal(perms, &a.Permissions)
	if last == nil || time.Since(*last) > time.Minute {
		_, _ = d.System.Exec(ctx, `UPDATE apps SET last_used_at=now() WHERE id=$1`, id)
	}
	return tenant, a, nil
}
