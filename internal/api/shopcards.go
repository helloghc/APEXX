package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// The signed-in customer's kept cards, and what a one-step purchase needs.
func (s *Server) routesShopCards(handle func(string, http.HandlerFunc)) {
	handle("GET /s/{tenant}/api/account/cards", s.shopSavedCards)
	handle("DELETE /s/{tenant}/api/account/cards/{id}", s.shopForgetCard)
	handle("GET /s/{tenant}/api/account/express", s.shopExpress)
}

func (s *Server) shopSavedCards(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	list, err := s.Orders.Cards(r.Context(), r.PathValue("tenant"), cid)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"items": nonNil(list)}
	// The checkout fills in who is signed in.
	if c, err := s.Customers.Get(r.Context(), r.PathValue("tenant"), cid, true); err == nil {
		out["email"], out["name"], out["phone"] = c.Email, c.Name, c.Phone
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) shopForgetCard(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	if err := s.Orders.ForgetCard(r.Context(), r.PathValue("tenant"), cid, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shopExpress gives the product page what a one-step purchase needs: the
// newest kept card and the default address (204 when either is missing).
func (s *Server) shopExpress(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	tenant := r.PathValue("tenant")
	cards, err := s.Orders.Cards(r.Context(), tenant, cid)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	c, err := s.Customers.Get(r.Context(), tenant, cid, true)
	if err != nil || len(cards) == 0 || len(c.Addresses) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	addr := c.Addresses[0]
	for _, a := range c.Addresses {
		if a.Default {
			addr = a
		}
	}
	line := addr.Line1
	if addr.Line2 != "" {
		line += ", " + addr.Line2
	}
	line += ", " + addr.City + ", " + addr.State
	httpx.JSON(w, r, http.StatusOK, map[string]any{"card": cards[0], "email": c.Email, "name": firstNonEmpty(addr.Name, c.Name), "phone": firstNonEmpty(addr.Phone, c.Phone),
		"address": line, "postalCode": addr.PostalCode, "cards": []orders.SavedCard(cards)})
}
