package api

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Apps: third parties with their own token and permissions per module. Only
// someone who manages the team installs or revokes them, so an app can never
// give itself or another app more access.

func (s *Server) routesApps(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/apps", s.authed("team", auth.Read, s.listApps))
	handle("POST /v1/apps", s.authed("team", auth.Admin, s.createApp))
	handle("PATCH /v1/apps/{id}", s.authed("team", auth.Admin, s.patchApp))
	handle("DELETE /v1/apps/{id}", s.authed("team", auth.Admin, s.revokeApp))
	handle("GET /v1/apps/me", s.authed("", "", s.appMe))
}

func cleanAppPerms(in map[string]string) (map[string]string, []apierr.FieldError) {
	out := map[string]string{}
	var errs []apierr.FieldError
	for m, l := range in {
		switch {
		case m == "team":
			errs = append(errs, apierr.FieldError{Field: "permissions.team", Code: "forbidden", Message: "Una app no puede administrar el equipo"})
		case !slices.Contains(auth.Modules, m):
			errs = append(errs, apierr.FieldError{Field: "permissions." + m, Code: "unknown", Message: "Módulo desconocido: " + m})
		case l == auth.None || l == "":
		case l != auth.Read && l != auth.Edit && l != auth.Admin:
			errs = append(errs, apierr.FieldError{Field: "permissions." + m, Code: "invalid", Message: "Nivel: lectura, edición o administración"})
		default:
			out[m] = l
		}
	}
	if len(out) == 0 && len(errs) == 0 {
		errs = append(errs, apierr.FieldError{Field: "permissions", Code: "required", Message: "Dale a la app acceso al menos a un módulo"})
	}
	return out, errs
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	apps, err := s.DB.Apps(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if apps == nil {
		apps = []store.App{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": apps, "modules": auth.Modules})
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in store.App
	if !decode(w, r, &in) {
		return
	}
	in.Name, in.Developer, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.Developer), strings.TrimSpace(in.URL)
	perms, errs := cleanAppPerms(in.Permissions)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "name", Code: "invalid", Message: "Escribe el nombre de la app (hasta 80 caracteres)"})
	}
	if len([]rune(in.Developer)) > 80 {
		errs = append(errs, apierr.FieldError{Field: "developer", Code: "invalid", Message: "Hasta 80 caracteres"})
	}
	if in.URL != "" {
		if u, err := url.Parse(in.URL); err != nil || u.Scheme != "https" || u.Host == "" {
			errs = append(errs, apierr.FieldError{Field: "url", Code: "invalid", Message: "La dirección empieza con https://"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	in.Permissions = perms
	app, token, err := s.DB.CreateApp(r.Context(), pr.Tenant, pr.Actor(), in)
	if err != nil {
		if strings.Contains(err.Error(), "límite") {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "too_many_apps", Detail: "La tienda ya tiene 50 apps activas"})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"app": app, "token": token})
}

func (s *Server) patchApp(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		Permissions map[string]string `json:"permissions"`
	}
	if !decode(w, r, &in) {
		return
	}
	perms, errs := cleanAppPerms(in.Permissions)
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los permisos", errs...)
		return
	}
	if err := s.DB.UpdateAppPermissions(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), perms); errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "App no encontrada o ya revocada")
		return
	} else if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.listApps(w, r)
}

func (s *Server) revokeApp(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if err := s.DB.RevokeApp(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id")); errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "App no encontrada o ya revocada")
		return
	} else if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// appMe lets an app check its own access.
func (s *Server) appMe(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Storefront {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Una llave pública de tienda no tiene acceso de gestión"})
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"kind": pr.Kind, "id": pr.ID, "name": pr.Name, "store": pr.Tenant, "permissions": pr.Permissions})
}
