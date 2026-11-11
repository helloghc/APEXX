package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Condition is one rule of a segment.
//
//	field: total_spent | orders_count | days_since_last_order | marketing_consent | has_account | tag
//	op:    gte | lte | eq (numbers), is (booleans), has | has_not (tag)
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// Rules is the definition of a rule-based segment.
type Rules struct {
	Match      string      `json:"match"` // all | any
	Conditions []Condition `json:"conditions"`
}

// Group is a customer group or segment.
type Group struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Kind        string    `json:"kind"` // manual | rules
	Rules       Rules     `json:"rules"`
	Count       int       `json:"count"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func num(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		if x < 0 || x > 1e15 || x != float64(int64(x)) {
			return 0, false
		}
		return int64(x), true
	case int64:
		return x, x >= 0
	case int:
		return int64(x), x >= 0
	case json.Number:
		n, err := x.Int64()
		return n, err == nil && n >= 0
	}
	return 0, false
}

// ruleSQL turns rules into a SQL condition over the customers row aliased "c".
// Values only travel as bind parameters (arg).
func ruleSQL(r Rules, arg func(any) string) (string, error) {
	if len(r.Conditions) == 0 {
		return "", FieldError{"rules.conditions", "Agrega al menos una condición"}
	}
	if len(r.Conditions) > 10 {
		return "", FieldError{"rules.conditions", "Máximo 10 condiciones"}
	}
	join := " AND "
	switch r.Match {
	case "all", "":
	case "any":
		join = " OR "
	default:
		return "", FieldError{"rules.match", "match: all o any"}
	}
	ops := map[string]string{"gte": ">=", "lte": "<=", "eq": "="}
	var parts []string
	for i, c := range r.Conditions {
		field := fmt.Sprintf("rules.conditions[%d]", i)
		switch c.Field {
		case "total_spent", "orders_count":
			op, ok := ops[c.Op]
			n, ok2 := num(c.Value)
			if !ok || !ok2 {
				return "", FieldError{field, "Usa gte, lte o eq con un número entero ≥ 0"}
			}
			parts = append(parts, "c."+c.Field+" "+op+" "+arg(n))
		case "days_since_last_order":
			n, ok := num(c.Value)
			if !ok || n > 36500 || (c.Op != "gte" && c.Op != "lte") {
				return "", FieldError{field, "Usa gte o lte con un número de días"}
			}
			// gte N days: last order is at least N days old. lte: within the last N days.
			cmp := "<="
			if c.Op == "lte" {
				cmp = ">="
			}
			parts = append(parts, "(c.last_order_at IS NOT NULL AND c.last_order_at "+cmp+" now() - make_interval(days => "+arg(int(n))+"))")
		case "marketing_consent", "has_account":
			b, ok := c.Value.(bool)
			if !ok || c.Op != "is" {
				return "", FieldError{field, "Usa is con true o false"}
			}
			col := "c.marketing_consent"
			if c.Field == "has_account" {
				col = "(c.verified_at IS NOT NULL)"
			}
			parts = append(parts, col+" = "+arg(b))
		case "tag":
			t, ok := c.Value.(string)
			tags, err := CleanTags([]string{t})
			if !ok || err != nil || len(tags) != 1 || (c.Op != "has" && c.Op != "has_not") {
				return "", FieldError{field, "Usa has o has_not con una etiqueta"}
			}
			cond := "c.tags @> " + arg(tags)
			if c.Op == "has_not" {
				cond = "NOT " + cond
			}
			parts = append(parts, cond)
		default:
			return "", FieldError{field, "Campo desconocido: " + c.Field}
		}
	}
	return strings.Join(parts, join), nil
}

func (s *Service) countTx(ctx context.Context, tx pgx.Tx, tenant string, g Group) (int, error) {
	var n int
	if g.Kind == "manual" {
		return n, tx.QueryRow(ctx, `SELECT count(*) FROM customer_group_members WHERE tenant_id=$1 AND group_id=$2`, tenant, g.ID).Scan(&n)
	}
	args := []any{tenant}
	cond, err := ruleSQL(g.Rules, func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	})
	if err != nil {
		return 0, nil // a stored rule that no longer validates matches nobody
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FROM customers c WHERE c.tenant_id=$1 AND (`+cond+`)`, args...).Scan(&n)
	return n, err
}

func (s *Service) groupTx(ctx context.Context, tx pgx.Tx, tenant, id string) (Group, error) {
	var g Group
	var rules []byte
	err := tx.QueryRow(ctx, `SELECT id, name, description, kind, rules, created_at, updated_at FROM customer_groups WHERE tenant_id=$1 AND id=$2`, tenant, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.Kind, &rules, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return g, ErrNotFound
	}
	if err != nil {
		return g, err
	}
	_ = json.Unmarshal(rules, &g.Rules)
	if g.Rules.Conditions == nil {
		g.Rules.Conditions = []Condition{}
	}
	return g, nil
}

// Groups lists groups with their current size.
func (s *Service) Groups(ctx context.Context, tenant string) ([]Group, error) {
	out := []Group{}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM customer_groups WHERE tenant_id=$1 ORDER BY name`, tenant)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range ids {
			g, err := s.groupTx(ctx, tx, tenant, id)
			if err != nil {
				return err
			}
			if g.Count, err = s.countTx(ctx, tx, tenant, g); err != nil {
				return err
			}
			out = append(out, g)
		}
		return nil
	})
	return out, err
}

// Group returns one group with its size.
func (s *Service) Group(ctx context.Context, tenant, id string) (Group, error) {
	var g Group
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if g, err = s.groupTx(ctx, tx, tenant, id); err != nil {
			return err
		}
		g.Count, err = s.countTx(ctx, tx, tenant, g)
		return err
	})
	return g, err
}

func checkGroup(g *Group) error {
	g.Name, g.Description = strings.TrimSpace(g.Name), strings.TrimSpace(g.Description)
	if g.Name == "" || len(g.Name) > 80 {
		return FieldError{"name", "Escribe un nombre de hasta 80 caracteres"}
	}
	if len(g.Description) > 300 {
		return FieldError{"description", "Descripción de hasta 300 caracteres"}
	}
	switch g.Kind {
	case "manual":
		g.Rules = Rules{Conditions: []Condition{}}
	case "rules":
		if g.Rules.Match == "" {
			g.Rules.Match = "all"
		}
		if _, err := ruleSQL(g.Rules, func(any) string { return "$1" }); err != nil {
			return err
		}
	default:
		return FieldError{"kind", "kind: manual o rules"}
	}
	return nil
}

// PreviewRules counts how many customers match rules without saving them.
func (s *Service) PreviewRules(ctx context.Context, tenant string, r Rules) (int, error) {
	g := Group{Name: "x", Kind: "rules", Rules: r}
	if err := checkGroup(&g); err != nil {
		return 0, err
	}
	var n int
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var e error
		n, e = s.countTx(ctx, tx, tenant, g)
		return e
	})
	return n, err
}

// SaveGroup creates (g.ID empty) or updates a group. The kind of an existing group cannot change.
func (s *Service) SaveGroup(ctx context.Context, tenant, actor string, g Group) (Group, error) {
	if err := checkGroup(&g); err != nil {
		return g, err
	}
	rules, _ := json.Marshal(g.Rules)
	create := g.ID == ""
	if create {
		g.ID = newID("grp_")
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var err error
		if create {
			_, err = tx.Exec(ctx, `INSERT INTO customer_groups (tenant_id, id, name, description, kind, rules) VALUES ($1,$2,$3,$4,$5,$6)`, tenant, g.ID, g.Name, g.Description, g.Kind, rules)
		} else {
			var ct interface{ RowsAffected() int64 }
			ct, err = tx.Exec(ctx, `UPDATE customer_groups SET name=$3, description=$4, rules=$5, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND kind=$6`, tenant, g.ID, g.Name, g.Description, rules, g.Kind)
			if err == nil && ct.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if err != nil {
			if strings.Contains(err.Error(), "duplicate key") {
				return ErrConflict
			}
			return err
		}
		if err := store.Emit(ctx, tx, tenant, Topic, g.ID, map[string]any{"type": "customer.group_saved"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.group_saved", g.ID, map[string]any{"name": g.Name, "kind": g.Kind})
	})
	if err != nil {
		return g, err
	}
	return s.Group(ctx, tenant, g.ID)
}

// DeleteGroup removes a group (never its customers).
func (s *Service) DeleteGroup(ctx context.Context, tenant, actor, id string) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `DELETE FROM customer_groups WHERE tenant_id=$1 AND id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if ct.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := store.Emit(ctx, tx, tenant, Topic, id, map[string]any{"type": "customer.group_deleted"}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.group_deleted", id, map[string]any{})
	})
}

// SetMembers adds or removes customers of a manual group.
func (s *Service) SetMembers(ctx context.Context, tenant, actor, groupID string, ids []string, remove bool) (int, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return 0, FieldError{"ids", "Selecciona entre 1 y 500 clientes"}
	}
	n := 0
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		g, err := s.groupTx(ctx, tx, tenant, groupID)
		if err != nil {
			return err
		}
		if g.Kind != "manual" {
			return FieldError{"group", "Un segmento por reglas se llena solo; edita sus reglas"}
		}
		var ct interface{ RowsAffected() int64 }
		if remove {
			ct, err = tx.Exec(ctx, `DELETE FROM customer_group_members WHERE tenant_id=$1 AND group_id=$2 AND customer_id = ANY($3)`, tenant, groupID, ids)
		} else {
			ct, err = tx.Exec(ctx, `INSERT INTO customer_group_members (tenant_id, group_id, customer_id)
				SELECT $1, $2, id FROM customers WHERE tenant_id=$1 AND id = ANY($3) ON CONFLICT DO NOTHING`, tenant, groupID, ids)
		}
		if err != nil {
			return err
		}
		n = int(ct.RowsAffected())
		if err := store.Emit(ctx, tx, tenant, Topic, groupID, map[string]any{"type": "customer.group_members", "count": n, "remove": remove}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, "customer.group_members", groupID, map[string]any{"count": n, "remove": remove})
	})
	return n, err
}

// groupsOfTx lists the groups a customer belongs to (manual membership and matching rules).
func (s *Service) groupsOfTx(ctx context.Context, tx pgx.Tx, tenant, customerID string) ([]GroupRef, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM customer_groups WHERE tenant_id=$1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := []GroupRef{}
	for _, id := range ids {
		g, err := s.groupTx(ctx, tx, tenant, id)
		if err != nil {
			return nil, err
		}
		var in bool
		if g.Kind == "manual" {
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customer_group_members WHERE tenant_id=$1 AND group_id=$2 AND customer_id=$3)`, tenant, id, customerID).Scan(&in)
		} else {
			args := []any{tenant, customerID}
			cond, rerr := ruleSQL(g.Rules, func(v any) string {
				args = append(args, v)
				return fmt.Sprintf("$%d", len(args))
			})
			if rerr != nil {
				continue
			}
			err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM customers c WHERE c.tenant_id=$1 AND c.id=$2 AND (`+cond+`))`, args...).Scan(&in)
		}
		if err != nil {
			return nil, err
		}
		if in {
			out = append(out, GroupRef{ID: g.ID, Name: g.Name, Kind: g.Kind})
		}
	}
	return out, nil
}
