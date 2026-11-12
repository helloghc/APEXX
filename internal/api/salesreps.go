package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Sales representatives shop as a company's buyer: only the representative
// the store assigned (or a customers administrator), only while the company
// allows it, and only for its own buyers. The store shows a banner with both
// names, and orders record the representative.

const repMarker = "apexx_rep" // readable by the store script: shows the banner

func (s *Server) routesSalesReps(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/companies/{id}/act-as", s.authed("customers", auth.Edit, noStorefront(s.actAsBuyer)))
	handle("PUT /s/{tenant}/api/account/company/rep-access", s.shopRepAccess)
	handle("GET /s/{tenant}/{site}/representante/{token}", s.shopRepHandoff)
	handle("GET /s/{tenant}/api/account/acting", s.shopActing)
	handle("POST /s/{tenant}/api/account/acting/exit", s.shopActingExit)
}

func (s *Server) actAsBuyer(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "member_only", Detail: "Solo una persona del equipo puede comprar como un cliente"})
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	c, buyers, _, err := s.Orders.Company(r.Context(), pr.Tenant, r.PathValue("id"), true)
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Empresa no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	switch {
	case !c.RepAccess:
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "no_consent", Detail: "La empresa no ha permitido que su representante compre por ella"})
		return
	case c.SalesRep != pr.ID && !pr.Permissions.Allows("customers", auth.Admin):
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_rep", Detail: "Solo el representante asignado a la empresa puede comprar por ella"})
		return
	case c.Status != "active":
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "company_suspended", Detail: "La empresa está suspendida"})
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	found := false
	for _, b := range buyers {
		found = found || b == email
	}
	if !found {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "email", Code: "not_buyer", Message: "Esa persona no compra para la empresa"})
		return
	}
	name := pr.Name
	if name == "" {
		name = pr.Email
	}
	token, _, err := s.Customers.RepHandoff(r.Context(), pr.Tenant, email, c.ID, pr.ID, name)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"url": "/s/" + pr.Tenant + "/main/representante/" + token, "expiresIn": int(customers.HandoffTTL.Seconds())})
}

func (s *Server) shopRepHandoff(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if !accountLimiter.allow(tenant + "|" + s.clientIP(r)) {
		http.Error(w, "Demasiadas solicitudes; espera un minuto", http.StatusTooManyRequests)
		return
	}
	session, _, _, err := s.Customers.ConsumeHandoff(r.Context(), tenant, r.PathValue("token"))
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "El enlace ya se usó o venció. Pide uno nuevo desde el panel.", http.StatusGone)
		return
	}
	s.setShopperCookie(w, tenant, session, int(customers.RepSessionTTL.Seconds()))
	http.SetCookie(w, &http.Cookie{Name: repMarker, Value: "1", Path: "/s/" + tenant, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(customers.RepSessionTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/s/"+tenant+"/"+r.PathValue("site")+"/cuenta?ver=empresa", http.StatusSeeOther)
}

// shopperActing returns the representative behind the shopper's session.
func (s *Server) shopperActing(r *http.Request) *customers.Acting {
	c, err := r.Cookie(shopperCookie)
	if err != nil {
		return nil
	}
	_, a, err := s.Customers.SessionActing(r.Context(), r.PathValue("tenant"), c.Value)
	if err != nil {
		return nil
	}
	return a
}

func (s *Server) shopActing(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	a := s.shopperActing(r)
	if a == nil {
		httpx.JSON(w, r, http.StatusOK, map[string]any{"acting": false})
		return
	}
	cu, err := s.Customers.Get(r.Context(), r.PathValue("tenant"), cid, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"acting": true, "representative": a.Name, "buyer": cu.Email})
}

func (s *Server) shopActingExit(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	if c, err := r.Cookie(shopperCookie); err == nil {
		_ = s.Customers.Logout(r.Context(), tenant, c.Value)
	}
	s.setShopperCookie(w, tenant, "", -1)
	http.SetCookie(w, &http.Cookie{Name: repMarker, Value: "", Path: "/s/" + tenant, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopRepAccess(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.roleAdmin(w, r, true)
	if !ok {
		return
	}
	if s.shopperActing(r) != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "rep_session", Detail: "El permiso lo da la empresa, no su representante"})
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Orders.SetRepAccess(r.Context(), r.PathValue("tenant"), "company-admin:"+c.ID, c.ID, in.On); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// repOrderAudit records that an order was placed by a representative.
func (s *Server) repOrderAudit(r *http.Request, a *customers.Acting, orderID, customerID string) {
	tenant := r.PathValue("tenant")
	_ = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, tenant, "member:"+a.MemberID, "order.by_representative", orderID, map[string]any{"representative": a.Name, "customer": customerID})
	})
}
