package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/store"
)

// Reorder lists of a signed-in buyer (shared with their company when they
// buy for one). The list keeps only SKUs and quantities: prices are always
// today's, from the cart.

type reorderItem struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

type reorderList struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Items     []reorderItem `json:"items"`
	Shared    bool          `json:"shared"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Lines     []listLine    `json:"lines,omitempty"` // for the account page
}

type listLine struct {
	SKU, Title string
	Qty        int64
	Available  bool
}

var listLimiter = &shopLimiter{n: 30}

// reorderLists returns the lists a buyer can see: theirs and their company's.
func (s *Server) reorderLists(r *http.Request, tenant, customerID, companyID string) ([]reorderList, error) {
	var out []reorderList
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id, name, items, company_id IS NOT NULL, updated_at FROM reorder_lists
			WHERE tenant_id=$1 AND (customer_id=$2 OR ($3 <> '' AND company_id=$3)) ORDER BY updated_at DESC LIMIT 50`, tenant, customerID, companyID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (reorderList, error) {
			var l reorderList
			var items []byte
			err := row.Scan(&l.ID, &l.Name, &items, &l.Shared, &l.UpdatedAt)
			_ = json.Unmarshal(items, &l.Items)
			return l, err
		})
		return err
	})
	return out, err
}

func (s *Server) shopListSave(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, listLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	customerID, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	var body struct {
		Name  string        `json:"name"`
		Items []reorderItem `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	tenant := r.PathValue("tenant")
	t, _ := s.Proj.Tenant(tenant)
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 80 {
		apierr.BadRequest(w, "", fe("name", "invalid", "Ponle un nombre a la lista (hasta 80 caracteres)"))
		return
	}
	if len(body.Items) == 0 || len(body.Items) > 200 {
		apierr.BadRequest(w, "", fe("items", "invalid", "La lista lleva entre 1 y 200 productos"))
		return
	}
	for i, it := range body.Items {
		if _, _, err := t.Catalog.VariantBySKU(it.SKU); err != nil || it.Qty < 1 || it.Qty > 100000 {
			apierr.BadRequest(w, "", fe("items", "invalid", "Producto o cantidad inválidos en la línea "+itoaInt(int64(i+1))))
			return
		}
	}
	var company *string
	if c, _ := s.shopperCompany(r); c != nil {
		company = &c.ID
	}
	items, _ := json.Marshal(body.Items)
	id := r.PathValue("id")
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		if id == "" {
			var n int
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM reorder_lists WHERE tenant_id=$1 AND customer_id=$2`, tenant, customerID).Scan(&n); err != nil {
				return err
			}
			if n >= 50 {
				return store.ErrConflict
			}
			id = store.NewID("rl_")
			_, err := tx.Exec(r.Context(), `INSERT INTO reorder_lists (tenant_id, id, customer_id, company_id, name, items) VALUES ($1,$2,$3,$4,$5,$6)`, tenant, id, customerID, company, body.Name, items)
			return err
		}
		// Anyone of the company may update a shared list; a personal list only its owner.
		ct, err := tx.Exec(r.Context(), `UPDATE reorder_lists SET name=$5, items=$6, updated_at=now() WHERE tenant_id=$1 AND id=$2 AND (customer_id=$3 OR ($4::text IS NOT NULL AND company_id=$4))`,
			tenant, id, customerID, company, body.Name, items)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return err
	})
	switch {
	case errors.Is(err, store.ErrConflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "too_many_lists", Detail: "Tienes 50 listas; borra alguna para crear otra"})
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Lista no encontrada")
	case err != nil:
		internalError(w, s.Log, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	}
}

func (s *Server) shopListDelete(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, listLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	customerID, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	tenant := r.PathValue("tenant")
	var company string
	if c, _ := s.shopperCompany(r); c != nil {
		company = c.ID
	}
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM reorder_lists WHERE tenant_id=$1 AND id=$2 AND (customer_id=$3 OR ($4 <> '' AND company_id=$4))`, tenant, r.PathValue("id"), customerID, company)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Lista no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
