package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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
	handle("PUT /v1/apps/{id}/extensions", s.authed("team", auth.Admin, s.putAppExtensions))
	handle("GET /v1/apps/{id}/extension-secret", s.authed("team", auth.Admin, s.appExtensionSecret))
	handle("GET /v1/app-extensions", s.authed("", "", s.listAppExtensions))
	handle("POST /v1/app-extensions/{id}/session", s.authed("", "", s.appExtensionSession))
}

func validHTTPS(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && len(raw) <= 300
}

func (s *Server) putAppExtensions(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in struct {
		AdminPage  string `json:"adminPage"`
		OrderBlock string `json:"orderBlock"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.AdminPage, in.OrderBlock = strings.TrimSpace(in.AdminPage), strings.TrimSpace(in.OrderBlock)
	var errs []apierr.FieldError
	if !validHTTPS(in.AdminPage) {
		errs = append(errs, apierr.FieldError{Field: "adminPage", Code: "invalid", Message: "La página empieza con https://"})
	}
	if !validHTTPS(in.OrderBlock) {
		errs = append(errs, apierr.FieldError{Field: "orderBlock", Code: "invalid", Message: "El bloque empieza con https://"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa las direcciones", errs...)
		return
	}
	if err := s.DB.SetAppExtensions(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), in.AdminPage, in.OrderBlock); errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "App no encontrada o ya revocada")
		return
	} else if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.listApps(w, r)
}

func (s *Server) extensionKey(tenant, appID string) []byte {
	b, err := s.Keys.Blind(tenant, "app-extension", appID)
	if err != nil {
		return nil
	}
	return []byte(hex.EncodeToString(b))
}

// appExtensionSecret is what the app uses to check the sessions it receives.
func (s *Server) appExtensionSecret(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	apps, err := s.DB.Apps(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !slices.ContainsFunc(apps, func(a store.App) bool { return a.ID == r.PathValue("id") && a.RevokedAt == nil }) {
		apierr.NotFound(w, "App no encontrada o ya revocada")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"secret": string(s.extensionKey(pr.Tenant, r.PathValue("id"))),
		"contract": "La página recibe ?session=<cuerpo>.<firma>. cuerpo = base64url(JSON {store, app, member, order, exp}); firma = hex(HMAC-SHA256(secreto, cuerpo)). Rechaza la sesión si la firma no coincide o exp (segundos Unix) ya pasó."})
}

type appExtension struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AdminPage  bool   `json:"adminPage"`
	OrderBlock bool   `json:"orderBlock"`
}

// listAppExtensions tells the panel which apps show a page or an order block.
func (s *Server) listAppExtensions(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Solo para personas con sesión en el panel"})
		return
	}
	apps, err := s.DB.Apps(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := []appExtension{}
	for _, a := range apps {
		if a.RevokedAt == nil && (a.AdminPage != "" || a.OrderBlock != "") {
			out = append(out, appExtension{a.ID, a.Name, a.AdminPage != "", a.OrderBlock != ""})
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// appExtensionSession signs a 5-minute session for an app's frame: which
// store, which person and (for an order block) which order. Seeing an order
// block needs read access to orders.
func (s *Server) appExtensionSession(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Solo para personas con sesión en el panel"})
		return
	}
	var in struct {
		Place string `json:"place"` // page | order
		Order string `json:"order"`
	}
	if !decode(w, r, &in) {
		return
	}
	apps, err := s.DB.Apps(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	i := slices.IndexFunc(apps, func(a store.App) bool { return a.ID == r.PathValue("id") && a.RevokedAt == nil })
	if i < 0 {
		apierr.NotFound(w, "App no encontrada o ya revocada")
		return
	}
	a := apps[i]
	target := a.AdminPage
	if in.Place == "order" {
		target = a.OrderBlock
		if !pr.Permissions.Allows("orders", auth.Read) || in.Order == "" {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Necesitas ver pedidos"})
			return
		}
		if _, err := s.Orders.Get(r.Context(), pr.Tenant, in.Order, false); err != nil {
			apierr.NotFound(w, "Pedido no encontrado")
			return
		}
	}
	if target == "" {
		apierr.NotFound(w, "La app no tiene esa vista")
		return
	}
	body, _ := json.Marshal(map[string]any{"store": pr.Tenant, "app": a.ID, "member": pr.ID, "order": in.Order, "exp": time.Now().Add(5 * time.Minute).Unix()})
	b64 := base64.RawURLEncoding.EncodeToString(body)
	m := hmac.New(sha256.New, s.extensionKey(pr.Tenant, a.ID))
	m.Write([]byte(b64))
	session := b64 + "." + hex.EncodeToString(m.Sum(nil))
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"url": target + sep + "session=" + url.QueryEscape(session), "session": session})
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
	in.AdminPage, in.OrderBlock = strings.TrimSpace(in.AdminPage), strings.TrimSpace(in.OrderBlock)
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
	if !validHTTPS(in.AdminPage) || !validHTTPS(in.OrderBlock) {
		errs = append(errs, apierr.FieldError{Field: "adminPage", Code: "invalid", Message: "Las vistas de la app empiezan con https://"})
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
