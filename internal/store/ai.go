package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// TopicAIVectors announces vectors written for products: the key lists the
// product ids (comma separated, one event per batch) so every node loads
// them into its in-memory index.
const TopicAIVectors = "ai.vectors"

// Chunk is one embedded piece of text.
type Chunk struct {
	SourceKind string
	SourceID   string
	No         int
	Body       string
	Context    string
	Model      string
	Vec        []byte // int8, packed
	Hash       []byte
}

func dropAIOf(ctx context.Context, tx pgx.Tx, tenant, kind, id string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM ai_chunks WHERE tenant_id=$1 AND source_kind=$2 AND source_id=$3`, tenant, kind, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM kg_edges WHERE tenant_id=$1 AND ((src_kind=$2 AND src_id=$3) OR (dst_kind=$2 AND dst_id=$3))`, tenant, kind, id)
	return err
}

// PutChunks writes chunks (replacing the ones of the same source and
// number) and announces them with one event. A source that no longer exists
// (a product deleted while it was being embedded) is skipped, so no vector
// outlives its source.
func (d *DB) PutChunks(ctx context.Context, tenant string, cs []Chunk) (written []string, err error) {
	if len(cs) == 0 {
		return nil, nil
	}
	err = d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		written = written[:0]
		n := len(cs)
		kinds, ids, bodies, ctxs, models := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
		nos := make([]int32, n)
		vecs, hashes := make([][]byte, n), make([][]byte, n)
		for i, c := range cs {
			kinds[i], ids[i], bodies[i], ctxs[i], models[i], nos[i], vecs[i], hashes[i] = c.SourceKind, c.SourceID, c.Body, c.Context, c.Model, int32(c.No), c.Vec, c.Hash
		}
		rows, err := tx.Query(ctx, `
			INSERT INTO ai_chunks (tenant_id, source_kind, source_id, chunk_no, body, context, model, vec, source_hash, updated_at)
			SELECT $1, k, i, n, b, c, m, v, h, now()
			FROM unnest($2::text[], $3::text[], $4::int[], $5::text[], $6::text[], $7::text[], $8::bytea[], $9::bytea[]) AS x(k, i, n, b, c, m, v, h)
			WHERE k <> 'product' OR EXISTS (SELECT 1 FROM products p WHERE p.tenant_id=$1 AND p.id=x.i)
			ON CONFLICT (tenant_id, source_kind, source_id, chunk_no) DO UPDATE
				SET body=EXCLUDED.body, context=EXCLUDED.context, model=EXCLUDED.model, vec=EXCLUDED.vec, source_hash=EXCLUDED.source_hash, updated_at=now()
			RETURNING source_id`, tenant, kinds, ids, nos, bodies, ctxs, models, vecs, hashes)
		if err != nil {
			return err
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		written = got
		if len(got) == 0 {
			return nil
		}
		return Emit(ctx, tx, tenant, TopicAIVectors, joinIDs(got), nil)
	})
	return written, err
}

func joinIDs(ids []string) string {
	n := 0
	for _, id := range ids {
		n += len(id) + 1
	}
	b := make([]byte, 0, n)
	for i, id := range ids {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, id...)
	}
	return string(b)
}

// ProductChunks loads the chunk 0 of the given products (nil: all of the
// store) made with model, for the in-memory index.
func (d *DB) ProductChunks(ctx context.Context, tenant, model string, ids []string, fn func(id string, vec, hash []byte)) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT source_id, vec, source_hash FROM ai_chunks
			WHERE tenant_id=$1 AND source_kind='product' AND chunk_no=0 AND model=$2 AND ($3::text[] IS NULL OR source_id = ANY($3))`, tenant, model, ids)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var v, h []byte
			if err := rows.Scan(&id, &v, &h); err != nil {
				return err
			}
			fn(id, v, h)
		}
		return rows.Err()
	})
}

// DropChunksNotIn removes product chunks whose product is gone or that were
// made with another model (self-healing sweep). It returns how many went.
func (d *DB) DropStaleChunks(ctx context.Context, tenant, model string) (int64, error) {
	var n int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM ai_chunks c WHERE c.tenant_id=$1 AND c.source_kind='product'
			AND (c.model <> $2 OR NOT EXISTS (SELECT 1 FROM products p WHERE p.tenant_id=$1 AND p.id=c.source_id))`, tenant, model)
		n = tag.RowsAffected()
		return err
	})
	return n, err
}

// AIUsage is one row of usage.
type AIUsage struct {
	Day          string `json:"day"`
	Feature      string `json:"feature"`
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CostMicros   int64  `json:"costMicros"`
}

// AddAIUsage adds to today's usage of feature and model.
func (d *DB) AddAIUsage(ctx context.Context, tenant, feature, model string, requests, in, out, cost int64) error {
	return d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO ai_usage (tenant_id, day, feature, model, requests, input_tokens, output_tokens, cost_micros)
			VALUES ($1, current_date, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (tenant_id, day, feature, model) DO UPDATE SET requests=ai_usage.requests+EXCLUDED.requests,
				input_tokens=ai_usage.input_tokens+EXCLUDED.input_tokens, output_tokens=ai_usage.output_tokens+EXCLUDED.output_tokens,
				cost_micros=ai_usage.cost_micros+EXCLUDED.cost_micros`, tenant, feature, model, requests, in, out, cost)
		return err
	})
}

// AIUsageSince lists usage from day on, newest first.
func (d *DB) AIUsageSince(ctx context.Context, tenant string, from time.Time) ([]AIUsage, error) {
	var out []AIUsage
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT to_char(day,'YYYY-MM-DD'), feature, model, requests, input_tokens, output_tokens, cost_micros
			FROM ai_usage WHERE tenant_id=$1 AND day >= $2 ORDER BY day DESC, feature, model`, tenant, from)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIUsage, error) {
			var u AIUsage
			err := r.Scan(&u.Day, &u.Feature, &u.Model, &u.Requests, &u.InputTokens, &u.OutputTokens, &u.CostMicros)
			return u, err
		})
		return err
	})
	return out, err
}

// AIMonthCost is the cost of this calendar month, in micro-dollars.
func (d *DB) AIMonthCost(ctx context.Context, tenant string) (int64, error) {
	var c int64
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT coalesce(sum(cost_micros),0) FROM ai_usage WHERE tenant_id=$1 AND day >= date_trunc('month', current_date)`, tenant).Scan(&c)
	})
	return c, err
}
