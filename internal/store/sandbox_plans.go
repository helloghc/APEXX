package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Plan is what a store's plan includes for sandboxes. The platform operator
// sets it (`apexx tenant plan`); a store only reads it.
type Plan struct {
	Plan               string `json:"plan"`
	Sandboxes          int    `json:"sandboxes"`
	SandboxMaxProducts int    `json:"sandboxMaxProducts"` // 0: no limit
	SandboxDemoData    bool   `json:"sandboxDemoData"`
	SandboxSchedule    bool   `json:"sandboxSchedule"`
}

// DefaultPlan applies to a store without a plan row.
var DefaultPlan = Plan{Sandboxes: 1, SandboxDemoData: true, SandboxSchedule: true}

// TenantPlan returns the plan of a production store.
func (d *DB) TenantPlan(ctx context.Context, tenant string) (Plan, error) {
	p := DefaultPlan
	err := d.System.QueryRow(ctx, `SELECT plan, sandboxes, sandbox_max_products, sandbox_demo_data, sandbox_schedule FROM tenant_plans WHERE tenant_id=$1`, tenant).
		Scan(&p.Plan, &p.Sandboxes, &p.SandboxMaxProducts, &p.SandboxDemoData, &p.SandboxSchedule)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultPlan, nil
	}
	return p, err
}

// SetTenantPlan writes a store's plan (operator only: the CLI).
func (d *DB) SetTenantPlan(ctx context.Context, tenant, actor string, p Plan) error {
	kind, _, err := d.TenantKind(ctx, tenant)
	if err != nil {
		return err
	}
	if kind != "production" {
		return errors.New("store: a plan belongs to a production store")
	}
	_, err = d.System.Exec(ctx, `INSERT INTO tenant_plans (tenant_id, plan, sandboxes, sandbox_max_products, sandbox_demo_data, sandbox_schedule, updated_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id) DO UPDATE SET plan=EXCLUDED.plan, sandboxes=EXCLUDED.sandboxes, sandbox_max_products=EXCLUDED.sandbox_max_products,
			sandbox_demo_data=EXCLUDED.sandbox_demo_data, sandbox_schedule=EXCLUDED.sandbox_schedule, updated_by=EXCLUDED.updated_by, updated_at=now()`,
		tenant, p.Plan, p.Sandboxes, p.SandboxMaxProducts, p.SandboxDemoData, p.SandboxSchedule, actor)
	return err
}

// ErrSandboxQuota means the plan does not include what was asked.
type ErrSandboxQuota struct {
	What  string // sandboxes, products, demo, schedule
	Plan  Plan
	Count int // products: how many the store or sandbox has
}

func (e *ErrSandboxQuota) Error() string {
	switch e.What {
	case "sandboxes":
		if e.Plan.Sandboxes == 0 {
			return "Tu plan no incluye sandbox."
		}
		return fmt.Sprintf("Tu plan incluye %d %s y ya los tienes. Borra uno o amplía tu plan.", e.Plan.Sandboxes, plural(e.Plan.Sandboxes, "sandbox", "sandboxes"))
	case "products":
		return fmt.Sprintf("Tu plan incluye sandboxes de hasta %d productos y aquí serían %d. Amplía tu plan o reduce el catálogo.", e.Plan.SandboxMaxProducts, e.Count)
	case "demo":
		return "Tu plan no incluye datos de prueba en el sandbox. Créalo sin clientes ni pedidos."
	case "schedule":
		return "Tu plan no incluye programar publicaciones. Publica ahora o amplía tu plan."
	}
	return "Tu plan no incluye esto."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// sandboxRoom checks, inside the writing transaction, that a sandbox stays
// within its plan's product limit. Production stores pay nothing: the check
// starts only for a sandbox id.
func (d *DB) sandboxRoom(ctx context.Context, tx pgx.Tx, tenant string) error {
	if !strings.HasPrefix(tenant, SandboxPrefix) {
		return nil
	}
	var parent string
	if err := d.System.QueryRow(ctx, `SELECT parent_id FROM sandboxes WHERE id=$1`, tenant).Scan(&parent); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	plan, err := d.TenantPlan(ctx, parent)
	if err != nil || plan.SandboxMaxProducts == 0 {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		return err
	}
	if n > plan.SandboxMaxProducts {
		return &ErrSandboxQuota{What: "products", Plan: plan, Count: n}
	}
	return nil
}

// RenameSandbox changes a sandbox's name.
func (d *DB) RenameSandbox(ctx context.Context, id, name string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandboxes SET name=$2 WHERE id=$1`, id, name)
	if isUnique(err) {
		return ErrSandboxExists
	}
	return err
}

// SetSandboxPassword stores the storefront password's hash ("" removes it).
func (d *DB) SetSandboxPassword(ctx context.Context, id, hash string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandboxes SET password_hash=$2 WHERE id=$1`, id, hash)
	return err
}

// SandboxPasswordHash returns the storefront password's hash ("" : none).
func (d *DB) SandboxPasswordHash(ctx context.Context, id string) (string, error) {
	var h string
	err := d.System.QueryRow(ctx, `SELECT password_hash FROM sandboxes WHERE id=$1`, id).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, err
}

// ---------- scheduled publishes ----------

// Schedule is a publish to production set for later.
type Schedule struct {
	ID        string     `json:"id"`
	ParentID  string     `json:"parentId"`
	SandboxID string     `json:"sandboxId"`
	Sandbox   string     `json:"sandbox"` // its name ("" if deleted)
	Areas     []string   `json:"areas"`
	Products  []string   `json:"products"`
	Force     bool       `json:"force"`
	RunAt     time.Time  `json:"runAt"`
	Status    string     `json:"status"`
	PublishID string     `json:"publishId,omitempty"`
	Result    string     `json:"result,omitempty"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	RanAt     *time.Time `json:"ranAt,omitempty"`
}

const scheduleCols = `s.id, s.parent_id, s.sandbox_id, COALESCE(b.name, ''), s.areas, s.products, s.force, s.run_at, s.status, s.publish_id, s.result, s.created_by, s.created_at, s.ran_at`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var x Schedule
	var areas, products []byte
	err := row.Scan(&x.ID, &x.ParentID, &x.SandboxID, &x.Sandbox, &areas, &products, &x.Force, &x.RunAt, &x.Status, &x.PublishID, &x.Result, &x.CreatedBy, &x.CreatedAt, &x.RanAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	_ = json.Unmarshal(areas, &x.Areas)
	_ = json.Unmarshal(products, &x.Products)
	if x.Products == nil {
		x.Products = []string{}
	}
	return x, err
}

// CreateSchedule sets a publish for later.
func (d *DB) CreateSchedule(ctx context.Context, x Schedule) (Schedule, error) {
	plan, err := d.TenantPlan(ctx, x.ParentID)
	if err != nil {
		return x, err
	}
	if !plan.SandboxSchedule {
		return x, &ErrSandboxQuota{What: "schedule", Plan: plan}
	}
	x.ID = "sch_" + randomID(10)
	if x.Products == nil {
		x.Products = []string{}
	}
	if _, err := d.System.Exec(ctx, `INSERT INTO sandbox_schedules (id, parent_id, sandbox_id, areas, products, force, run_at, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		x.ID, x.ParentID, x.SandboxID, mustJSON(x.Areas), mustJSON(x.Products), x.Force, x.RunAt, x.CreatedBy); err != nil {
		return x, err
	}
	return d.ScheduleByID(ctx, x.ParentID, x.ID)
}

// ScheduleByID returns one scheduled publish of a store.
func (d *DB) ScheduleByID(ctx context.Context, parent, id string) (Schedule, error) {
	return scanSchedule(d.System.QueryRow(ctx, `SELECT `+scheduleCols+` FROM sandbox_schedules s LEFT JOIN sandboxes b ON b.id = s.sandbox_id WHERE s.parent_id=$1 AND s.id=$2`, parent, id))
}

// Schedules lists a store's scheduled publishes, newest first.
func (d *DB) Schedules(ctx context.Context, parent string, limit int) ([]Schedule, error) {
	rows, err := d.System.Query(ctx, `SELECT `+scheduleCols+` FROM sandbox_schedules s LEFT JOIN sandboxes b ON b.id = s.sandbox_id
		WHERE s.parent_id=$1 ORDER BY (s.status = 'pending') DESC, s.run_at DESC LIMIT $2`, parent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		x, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// CancelSchedule cancels a publish that has not run.
func (d *DB) CancelSchedule(ctx context.Context, parent, id string) error {
	ct, err := d.System.Exec(ctx, `UPDATE sandbox_schedules SET status='canceled', ran_at=now() WHERE parent_id=$1 AND id=$2 AND status='pending'`, parent, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		if _, err := d.ScheduleByID(ctx, parent, id); err != nil {
			return err
		}
		return ErrScheduleDone
	}
	return nil
}

// ErrScheduleDone means the publish already ran or was canceled.
var ErrScheduleDone = errors.New("store: the scheduled publish already ran or was canceled")

// ClaimDueSchedules marks the due publishes as running (each is claimed by
// one node only: SKIP LOCKED) and returns them.
func (d *DB) ClaimDueSchedules(ctx context.Context, limit int) ([]Schedule, error) {
	rows, err := d.System.Query(ctx, `WITH due AS (
			SELECT id FROM sandbox_schedules WHERE status='pending' AND run_at <= now() ORDER BY run_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		UPDATE sandbox_schedules s SET status='running', ran_at=now() FROM due WHERE s.id = due.id RETURNING s.id, s.parent_id`, limit)
	if err != nil {
		return nil, err
	}
	type key struct{ id, parent string }
	keys, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (key, error) {
		var k key
		return k, r.Scan(&k.id, &k.parent)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Schedule, 0, len(keys))
	for _, k := range keys {
		x, err := d.ScheduleByID(ctx, k.parent, k.id)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// FinishSchedule records how a scheduled publish ended.
func (d *DB) FinishSchedule(ctx context.Context, id, status, publishID, result string) error {
	_, err := d.System.Exec(ctx, `UPDATE sandbox_schedules SET status=$2, publish_id=$3, result=$4, ran_at=now() WHERE id=$1`, id, status, publishID, result)
	return err
}

// FailStuckSchedules closes publishes a stopped node left running for
// more than 15 minutes (a publish takes seconds).
func (d *DB) FailStuckSchedules(ctx context.Context) error {
	_, err := d.System.Exec(ctx, `UPDATE sandbox_schedules SET status='failed', result='El nodo que publicaba se detuvo. Programa la publicación de nuevo.' WHERE status='running' AND ran_at < now() - interval '15 minutes'`)
	return err
}
