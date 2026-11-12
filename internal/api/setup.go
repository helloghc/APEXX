package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/httpx"
)

// setupStatus says which first steps a store has done, so the panel's home
// can guide a new merchant from an empty store to the first sale. One query,
// each step an EXISTS over an indexed tenant key: it stays fast however big
// the store grows.
type setupStatus struct {
	Products  bool `json:"products"`
	Payments  bool `json:"payments"`
	Shipping  bool `json:"shipping"`
	Published bool `json:"published"`
	Team      bool `json:"team"`
	Domain    bool `json:"domain"`
	FirstSale bool `json:"firstSale"`
}

func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	var st setupStatus
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT
			EXISTS (SELECT 1 FROM products WHERE tenant_id=$1 AND status='active'),
			EXISTS (SELECT 1 FROM payment_accounts WHERE tenant_id=$1),
			EXISTS (SELECT 1 FROM shipping_options WHERE tenant_id=$1),
			EXISTS (SELECT 1 FROM site_releases WHERE tenant_id=$1),
			(SELECT count(*) FROM members WHERE tenant_id=$1) > 1,
			EXISTS (SELECT 1 FROM domains WHERE tenant_id=$1 AND status='verified'),
			EXISTS (SELECT 1 FROM orders WHERE tenant_id=$1)`, tenant).
			Scan(&st.Products, &st.Payments, &st.Shipping, &st.Published, &st.Team, &st.Domain, &st.FirstSale)
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, st)
}
