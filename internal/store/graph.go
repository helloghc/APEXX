package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// TopicAIGraph announces approved or removed edges: every node reloads the
// approved graph of the store.
const TopicAIGraph = "ai.graph"

// Edge is one relation of the knowledge graph (kg_edges).
type Edge struct {
	ID         string     `json:"id"`
	SrcKind    string     `json:"srcKind"`
	SrcID      string     `json:"srcId"`
	Rel        string     `json:"rel"` // compatible_with, accessory_of, substitute_of
	DstKind    string     `json:"dstKind"`
	DstID      string     `json:"dstId"`
	Confidence float64    `json:"confidence"`
	Source     string     `json:"source"` // why it was proposed, in words the merchant can check
	Status     string     `json:"status"` // proposed, approved, rejected
	CreatedAt  time.Time  `json:"createdAt"`
	DecidedBy  string     `json:"decidedBy,omitempty"`
	DecidedAt  *time.Time `json:"decidedAt,omitempty"`
}

// ProposeEdges stores new proposals. An edge that already exists in any
// state is left alone: a rejected edge is never proposed again, an approved
// one stays approved. Returns how many were new.
func (d *DB) ProposeEdges(ctx context.Context, tenant string, es []Edge) (int, error) {
	if len(es) == 0 {
		return 0, nil
	}
	var n int
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		// The products at both ends are locked (key share) before the
		// insert: a deletion running now finishes first and its products
		// are then left out, so no edge outlives its product.
		var pids []string
		for _, e := range es {
			if e.SrcKind == "product" {
				pids = append(pids, e.SrcID)
			}
			if e.DstKind == "product" {
				pids = append(pids, e.DstID)
			}
		}
		if len(pids) > 0 {
			rows, err := tx.Query(ctx, `SELECT id FROM products WHERE tenant_id=$1 AND id = ANY($2) ORDER BY id FOR KEY SHARE`, tenant, pids)
			if err != nil {
				return err
			}
			alive, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			live := make(map[string]bool, len(alive))
			for _, id := range alive {
				live[id] = true
			}
			kept := es[:0:0]
			for _, e := range es {
				if (e.SrcKind != "product" || live[e.SrcID]) && (e.DstKind != "product" || live[e.DstID]) {
					kept = append(kept, e)
				}
			}
			es = kept
			if len(es) == 0 {
				return nil
			}
		}
		k := len(es)
		ids, sk, si, rel, dk, di, src := make([]string, k), make([]string, k), make([]string, k), make([]string, k), make([]string, k), make([]string, k), make([]string, k)
		conf := make([]float64, k)
		for i, e := range es {
			ids[i], sk[i], si[i], rel[i], dk[i], di[i], src[i], conf[i] = NewID("edge_"), e.SrcKind, e.SrcID, e.Rel, e.DstKind, e.DstID, e.Source, e.Confidence
		}
		tag, err := tx.Exec(ctx, `INSERT INTO kg_edges (tenant_id, id, src_kind, src_id, rel, dst_kind, dst_id, confidence, source)
			SELECT $1, id, sk, si, r, dk, di, c, s FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::text[], $7::text[], $8::float8[], $9::text[]) AS x(id, sk, si, r, dk, di, c, s)
			ON CONFLICT (tenant_id, src_kind, src_id, rel, dst_kind, dst_id) DO NOTHING`, tenant, ids, sk, si, rel, dk, di, conf, src)
		n = int(tag.RowsAffected())
		return err
	})
	return n, err
}

const edgeCols = `id, src_kind, src_id, rel, dst_kind, dst_id, confidence, source, status, created_at, decided_by, decided_at`

func scanEdge(r pgx.CollectableRow) (Edge, error) {
	var e Edge
	var c float32
	err := r.Scan(&e.ID, &e.SrcKind, &e.SrcID, &e.Rel, &e.DstKind, &e.DstID, &c, &e.Source, &e.Status, &e.CreatedAt, &e.DecidedBy, &e.DecidedAt)
	e.Confidence = float64(c)
	return e, err
}

// Edges lists edges of a status (empty: all), most confident first.
func (d *DB) Edges(ctx context.Context, tenant, status, rel string, limit, offset int) ([]Edge, map[string]int, error) {
	var out []Edge
	counts := map[string]int{}
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT status, count(*)::int FROM kg_edges WHERE tenant_id=$1 GROUP BY status`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var s string
			var n int
			if err := rows.Scan(&s, &n); err != nil {
				rows.Close()
				return err
			}
			counts[s] = n
		}
		rows.Close()
		rows, err = tx.Query(ctx, `SELECT `+edgeCols+` FROM kg_edges WHERE tenant_id=$1 AND ($2='' OR status=$2) AND ($3='' OR rel=$3)
			ORDER BY confidence DESC, created_at, id LIMIT $4 OFFSET $5`, tenant, status, rel, limit, offset)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanEdge)
		return err
	})
	return out, counts, err
}

// ApprovedEdges returns every approved edge (for the in-memory graph).
func (d *DB) ApprovedEdges(ctx context.Context, tenant string) ([]Edge, error) {
	var out []Edge
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+edgeCols+` FROM kg_edges WHERE tenant_id=$1 AND status='approved'`, tenant)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanEdge)
		return err
	})
	return out, err
}

// DecideEdges approves, rejects or (status "proposed") reopens edges, with
// one audit entry each, and announces the change to every node.
func (d *DB) DecideEdges(ctx context.Context, tenant, actor, status string, ids []string) (int, error) {
	if status != "approved" && status != "rejected" && status != "proposed" {
		return 0, errors.New("store: invalid edge status")
	}
	var n int
	err := d.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE kg_edges SET status=$3, decided_by=$4, decided_at=now()
			WHERE tenant_id=$1 AND id = ANY($2) AND status <> $3 RETURNING id, src_id, rel, dst_id`, tenant, ids, status, actor)
		if err != nil {
			return err
		}
		var entries []auditEntry
		for rows.Next() {
			var id, src, rel, dst string
			if err := rows.Scan(&id, &src, &rel, &dst); err != nil {
				rows.Close()
				return err
			}
			entries = append(entries, auditEntry{action: "ai.edge." + status, target: id, data: map[string]any{"src": src, "rel": rel, "dst": dst}})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		n = len(entries)
		if n == 0 {
			return nil
		}
		if err := auditMany(ctx, tx, tenant, actor, entries); err != nil {
			return err
		}
		return Emit(ctx, tx, tenant, TopicAIGraph, "", nil)
	})
	return n, err
}
