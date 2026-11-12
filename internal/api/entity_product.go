package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Records of an entity marked "show on product" appear on the page of the
// product they point to. They are read once per store and kept in memory for
// a short time, so a product page never decrypts records on every visit.

type extraSection struct {
	Title string
	Rows  []specView
}

type extrasCache struct {
	at   time.Time
	byPr map[string][]extraSection // product id or handle -> sections
}

var productExtrasMu sync.Mutex
var productExtras = map[string]*extrasCache{}

const extrasTTL = time.Minute

// dropProductExtras forgets the cached records of a store after a change.
func dropProductExtras(tenant string) {
	productExtrasMu.Lock()
	delete(productExtras, tenant)
	productExtrasMu.Unlock()
}

// extrasFor returns the sections to show on a product page.
func (s *Server) extrasFor(ctx context.Context, tenant string, ids ...string) []extraSection {
	productExtrasMu.Lock()
	c := productExtras[tenant]
	productExtrasMu.Unlock()
	if c == nil || time.Since(c.at) > extrasTTL {
		c = s.loadExtras(ctx, tenant)
		productExtrasMu.Lock()
		productExtras[tenant] = c
		productExtrasMu.Unlock()
	}
	var out []extraSection
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, c.byPr[strings.ToLower(id)]...)
	}
	return out
}

func (s *Server) loadExtras(ctx context.Context, tenant string) *extrasCache {
	c := &extrasCache{at: time.Now(), byPr: map[string][]extraSection{}}
	docs, err := s.DB.ListDocs(ctx, tenant, "entities")
	if err != nil {
		c.at = time.Now().Add(-extrasTTL + 5*time.Second) // retry soon
		return c
	}
	for _, d := range docs {
		var e Entity
		if json.Unmarshal(d.Data, &e) != nil || !e.ShowOnProduct {
			continue
		}
		_ = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id, data FROM entity_records WHERE tenant_id=$1 AND entity=$2 ORDER BY created_at LIMIT 5000`, tenant, e.ID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				var enc []byte
				if rows.Scan(&id, &enc) != nil {
					continue
				}
				plain, err := s.Keys.Decrypt(tenant, enc, "entity:"+e.ID+":"+id)
				if err != nil {
					continue
				}
				var data map[string]string
				if json.Unmarshal(plain, &data) != nil {
					continue
				}
				sec := extraSection{Title: e.Name}
				var targets []string
				for _, f := range e.Fields {
					v := strings.TrimSpace(data[f.Key])
					switch f.Type {
					case "product":
						if v != "" {
							targets = append(targets, strings.ToLower(v))
						}
					case "email", "phone":
						// never on a public page
					default:
						if v != "" {
							sec.Rows = append(sec.Rows, specView{f.Label, v})
						}
					}
				}
				if len(sec.Rows) == 0 {
					continue
				}
				for _, t := range targets {
					c.byPr[t] = append(c.byPr[t], sec)
				}
			}
			return rows.Err()
		})
	}
	for k := range c.byPr {
		sort.SliceStable(c.byPr[k], func(i, j int) bool { return c.byPr[k][i].Title < c.byPr[k][j].Title })
	}
	return c
}
