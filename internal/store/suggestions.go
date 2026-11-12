package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Suggestion is something an AI feature proposes (plan IA-5): what it
// would change, the value before, why, and what the merchant decided.
type Suggestion struct {
	ID         string          `json:"id"`
	Feature    string          `json:"feature"` // attributes, category, seo, duplicate…
	TargetKind string          `json:"targetKind"`
	TargetID   string          `json:"targetId"`
	Proposal   json.RawMessage `json:"proposal"`
	Before     json.RawMessage `json:"before,omitempty"`
	Status     string          `json:"status"` // proposed, applied, rejected, undone
	Model      string          `json:"model"`
	Sources    json.RawMessage `json:"sources"` // why: [{"text": "…"}]
	CreatedAt  time.Time       `json:"createdAt"`
	DecidedBy  string          `json:"decidedBy,omitempty"`
	DecidedAt  *time.Time      `json:"decidedAt,omitempty"`
}

const sugCols = `id, feature, target_kind, target_id, proposal, before, status, model, sources, created_at, decided_by, decided_at`

func scanSug(r pgx.CollectableRow) (Suggestion, error) {
	var s Suggestion
	err := r.Scan(&s.ID, &s.Feature, &s.TargetKind, &s.TargetID, &s.Proposal, &s.Before, &s.Status, &s.Model, &s.Sources, &s.CreatedAt, &s.DecidedBy, &s.DecidedAt)
	return s, err
}

// ProposeSuggestions stores new proposals. A proposal identical to one
// already proposed, applied or rejected for the same target and feature is
// not stored again: the merchant never sees the same suggestion twice.
func (d *DB) ProposeSuggestions(ctx context.Context, tenant string, ss []Suggestion) (int, error) {
	n := 0
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		for _, s := range ss {
			tag, err := tx.Exec(ctx, `INSERT INTO ai_suggestions (tenant_id, id, feature, target_kind, target_id, proposal, model, sources)
				SELECT $1,$2,$3,$4,$5,$6,$7,$8
				WHERE NOT EXISTS (SELECT 1 FROM ai_suggestions WHERE tenant_id=$1 AND feature=$3 AND target_kind=$4 AND target_id=$5 AND proposal=$6::jsonb)`,
				tenant, NewID("sug_"), s.Feature, s.TargetKind, s.TargetID, s.Proposal, s.Model, s.Sources)
			if err != nil {
				return err
			}
			n += int(tag.RowsAffected())
		}
		return nil
	})
	return n, err
}

// Suggestions lists suggestions of a target (or of a feature), newest first.
func (d *DB) Suggestions(ctx context.Context, tenant, targetKind, targetID, feature, status string, limit int) ([]Suggestion, error) {
	var out []Suggestion
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+sugCols+` FROM ai_suggestions WHERE tenant_id=$1
			AND ($2='' OR target_kind=$2) AND ($3='' OR target_id=$3) AND ($4='' OR feature=$4) AND ($5='' OR status=$5)
			ORDER BY created_at DESC, id LIMIT $6`, tenant, targetKind, targetID, feature, status, limit)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanSug)
		return err
	})
	return out, err
}

// Suggestion returns one suggestion.
func (d *DB) Suggestion(ctx context.Context, tenant, id string) (Suggestion, error) {
	var s Suggestion
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+sugCols+` FROM ai_suggestions WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		s, err = pgx.CollectExactlyOneRow(rows, scanSug)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return s, err
}

// ErrStale means the target changed since the suggestion was made or
// applied: applying or undoing it would overwrite someone's edit.
var ErrStale = errors.New("store: the target changed since")

// ProductField is one product field a suggestion may change.
type ProductField struct {
	Attributes     map[string]string `json:"attributes,omitempty"`
	Categories     []string          `json:"categories,omitempty"`
	SEOTitle       *string           `json:"seoTitle,omitempty"`
	SEODescription *string           `json:"seoDescription,omitempty"`
}

// ApplyProductSuggestion writes the fields of a suggestion to its product
// (only those fields, with the product's version as a guard), records the
// value before, marks the suggestion and audits it, in one transaction.
// undo writes the value before back instead.
func (d *DB) ApplyProductSuggestion(ctx context.Context, tenant, actor, id string, version int64, set ProductField, before json.RawMessage, status string) (int64, error) {
	var nv int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var pid string
		if err := tx.QueryRow(ctx, `SELECT target_id FROM ai_suggestions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, id).Scan(&pid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		attrs, _ := json.Marshal(set.Attributes)
		err := tx.QueryRow(ctx, `UPDATE products SET
				attributes = CASE WHEN $4::jsonb IS NULL OR $4::jsonb = 'null'::jsonb THEN attributes ELSE $4::jsonb END,
				categories = COALESCE($5, categories),
				seo_title = COALESCE($6, seo_title),
				seo_description = COALESCE($7, seo_description),
				version = version + 1, updated_at = now()
			WHERE tenant_id=$1 AND id=$2 AND version=$3 RETURNING version`,
			tenant, pid, version, attrs, set.Categories, set.SEOTitle, set.SEODescription).Scan(&nv)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE ai_suggestions SET status=$3, before=COALESCE($4, before), decided_by=$5, decided_at=now() WHERE tenant_id=$1 AND id=$2`,
			tenant, id, status, before, actor); err != nil {
			return err
		}
		if err := Emit(ctx, tx, tenant, TopicProduct, pid, map[string]any{"version": nv}); err != nil {
			return err
		}
		return Audit(ctx, tx, tenant, actor, "ai.suggestion."+status, id, map[string]any{"product": pid, "version": nv})
	})
	return nv, err
}

// DecideSuggestion marks a suggestion without changing its target
// (rejected, or reviewed for those that only inform).
func (d *DB) DecideSuggestion(ctx context.Context, tenant, actor, id, status string) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE ai_suggestions SET status=$3, decided_by=$4, decided_at=now() WHERE tenant_id=$1 AND id=$2 AND status='proposed'`, tenant, id, status, actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return Audit(ctx, tx, tenant, actor, "ai.suggestion."+status, id, map[string]any{})
	})
}
