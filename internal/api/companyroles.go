package api

import (
	"errors"
	"net/http"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// Company roles, managed by the store from the panel and by the company's
// own administrators from "Mi empresa".

func (s *Server) routesCompanyRoles(handle func(string, http.HandlerFunc)) {
	const mod = "customers"
	handle("GET /v1/companies/{id}/roles", s.authed(mod, auth.Read, noStorefront(s.companyRoles)))
	handle("POST /v1/companies/{id}/roles", s.authed(mod, auth.Admin, s.saveCompanyRole))
	handle("PUT /v1/companies/{id}/roles/{role}", s.authed(mod, auth.Admin, s.saveCompanyRole))
	handle("DELETE /v1/companies/{id}/roles/{role}", s.authed(mod, auth.Admin, s.deleteCompanyRole))
	handle("GET /s/{tenant}/api/account/company/roles", s.shopCompanyRoles)
	handle("POST /s/{tenant}/api/account/company/roles", s.shopSaveCompanyRole)
	handle("PUT /s/{tenant}/api/account/company/roles/{role}", s.shopSaveCompanyRole)
	handle("DELETE /s/{tenant}/api/account/company/roles/{role}", s.shopDeleteCompanyRole)
}

func (s *Server) companyExists(w http.ResponseWriter, r *http.Request, tenant, id string) bool {
	if _, _, _, err := s.Orders.Company(r.Context(), tenant, id, false); err != nil {
		if errors.Is(err, orders.ErrNotFound) {
			apierr.NotFound(w, "Empresa no encontrada")
		} else {
			internalError(w, s.Log, err)
		}
		return false
	}
	return true
}

func (s *Server) companyRoles(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	list, err := s.Orders.CompanyRoles(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) saveCompanyRole(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	var in orders.CompanyRole
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("role")
	out, err := s.Orders.SaveCompanyRole(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "", in)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, out)
}

func (s *Server) deleteCompanyRole(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.DeleteCompanyRole(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), r.PathValue("role")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// portalAdmin is the company of a signed-in shopper who administers it.
func (s *Server) roleAdmin(w http.ResponseWriter, r *http.Request, write bool) (*orders.CompanyAccess, string, bool) {
	if write && !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return nil, "", false
	}
	c, email, ok := s.portalCompany(w, r)
	if !ok {
		return nil, "", false
	}
	if !c.Admin {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_company_admin", Detail: "Solo quien administra la empresa cambia sus roles"})
		return nil, "", false
	}
	return c, email, true
}

func (s *Server) shopCompanyRoles(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.roleAdmin(w, r, false)
	if !ok {
		return
	}
	list, err := s.Orders.CompanyRoles(r.Context(), r.PathValue("tenant"), c.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) shopSaveCompanyRole(w http.ResponseWriter, r *http.Request) {
	c, email, ok := s.roleAdmin(w, r, true)
	if !ok {
		return
	}
	var in orders.CompanyRole
	if !decode(w, r, &in) {
		return
	}
	in.ID = r.PathValue("role")
	out, err := s.Orders.SaveCompanyRole(r.Context(), r.PathValue("tenant"), "company-admin:"+c.ID, c.ID, email, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, out)
}

func (s *Server) shopDeleteCompanyRole(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.roleAdmin(w, r, true)
	if !ok {
		return
	}
	if err := s.Orders.DeleteCompanyRole(r.Context(), r.PathValue("tenant"), "company-admin:"+c.ID, c.ID, r.PathValue("role")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
