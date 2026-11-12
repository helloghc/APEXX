package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TakeLease reports whether node holds the lease of job name for ttl from now:
// it takes a free or expired lease, or renews its own. Only the holder runs
// the job, so with several nodes each background job runs once.
func (d *DB) TakeLease(ctx context.Context, name, node string, ttl time.Duration) (bool, error) {
	var holder string
	err := d.System.QueryRow(ctx, `
		INSERT INTO job_leases (name, holder, expires_at) VALUES ($1, $2, now() + make_interval(secs => $3))
		ON CONFLICT (name) DO UPDATE SET holder=EXCLUDED.holder, expires_at=EXCLUDED.expires_at
			WHERE job_leases.holder = EXCLUDED.holder OR job_leases.expires_at < now()
		RETURNING holder`, name, node, ttl.Seconds()).Scan(&holder)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return holder == node, nil
}

// ReleaseLeases gives back every lease node holds (graceful shutdown), so
// another node takes the work on its next tick instead of waiting for expiry.
func (d *DB) ReleaseLeases(ctx context.Context, node string) error {
	_, err := d.System.Exec(ctx, `DELETE FROM job_leases WHERE holder=$1`, node)
	return err
}

func failureKey(key string) []byte {
	h := sha256.Sum256([]byte("apexx-login|" + key))
	return h[:]
}

// LoginFailures counts the failed sign-ins of key within window, on every node.
func (d *DB) LoginFailures(ctx context.Context, key string, window time.Duration) (int, error) {
	var n int
	err := d.System.QueryRow(ctx, `SELECT count(*) FROM login_failures WHERE key_hash=$1 AND at > now() - make_interval(secs => $2)`,
		failureKey(key), window.Seconds()).Scan(&n)
	return n, err
}

// AddLoginFailure records one failed sign-in of key.
func (d *DB) AddLoginFailure(ctx context.Context, key string) error {
	_, err := d.System.Exec(ctx, `INSERT INTO login_failures (key_hash) VALUES ($1)`, failureKey(key))
	return err
}

// PurgeLoginFailures drops attempts older than an hour.
func (d *DB) PurgeLoginFailures(ctx context.Context) error {
	_, err := d.System.Exec(ctx, `DELETE FROM login_failures WHERE at < now() - interval '1 hour'`)
	return err
}
