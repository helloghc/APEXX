package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/store"
)

// routesAccount registers account security (two-step verification, sessions),
// team invitations and custom roles.
func (s *Server) routesAccount(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/me/2fa/begin", s.authed("", "", s.totpBegin))
	handle("POST /v1/me/2fa/confirm", s.authed("", "", s.totpConfirm))
	handle("POST /v1/me/2fa/disable", s.authed("", "", s.totpDisable))
	handle("POST /v1/team/members/{id}/2fa/reset", s.authed("team", auth.Admin, s.totpReset))
	handle("GET /v1/me/sessions", s.authed("", "", s.mySessions))
	handle("DELETE /v1/me/sessions/{id}", s.authed("", "", s.revokeMySession))
	handle("POST /v1/me/sessions/revoke-others", s.authed("", "", s.revokeOtherSessions))

	handle("POST /v1/auth/stores", s.authStores)

	handle("GET /v1/team/invitations", s.authed("team", auth.Read, s.listInvitations))
	handle("POST /v1/team/invitations", s.authed("team", auth.Admin, s.createInvitation))
	handle("DELETE /v1/team/invitations/{id}", s.authed("team", auth.Admin, s.revokeInvitation))
	handle("GET /v1/invitations/{token}", s.invitationInfo)
	handle("POST /v1/invitations/{token}/accept", s.acceptInvitation)

	handle("GET /v1/team/roles", s.authed("team", auth.Read, s.listRoles))
	handle("PUT /v1/team/roles/{id}", s.authed("team", auth.Admin, s.putRole))
	handle("DELETE /v1/team/roles/{id}", s.authed("team", auth.Admin, s.deleteRole))
}

// memberOnly rejects API keys: these actions belong to a person.
func memberOnly(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p := principal(r)
	if p.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Esta acción es para una persona con sesión iniciada, no para una llave de API"})
		return p, false
	}
	return p, true
}

func (s *Server) authFail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrBadCode):
		apierr.Write(w, apierr.Problem{Status: http.StatusBadRequest, Code: "invalid_code", Detail: "El código no es correcto. Revisa la hora de tu teléfono e intenta con el código actual"})
	case errors.Is(err, auth.ErrTOTPState):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "invalid_state", Detail: "La verificación en dos pasos no está en ese estado"})
	case errors.Is(err, auth.ErrBadCredentials):
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "invalid_credentials", Detail: "La contraseña no es correcta"})
	case errors.Is(err, auth.ErrNotFound):
		apierr.NotFound(w, "No encontrado")
	case errors.Is(err, auth.ErrInvitation):
		apierr.Write(w, apierr.Problem{Status: http.StatusGone, Code: "invitation_invalid", Detail: "Esta invitación ya se usó, se canceló o venció. Pide a tu equipo que te envíe otra"})
	case errors.Is(err, auth.ErrExists):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "already_member", Detail: "Ese correo ya es parte del equipo"})
	default:
		if strings.HasPrefix(err.Error(), "auth:") {
			apierr.BadRequest(w, strings.TrimPrefix(err.Error(), "auth: "))
			return
		}
		internalError(w, s.Log, err)
	}
}

func (s *Server) totpBegin(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	issuer := "Apexx"
	if t, ok := s.Proj.Tenant(p.Tenant); ok && t.Site().Settings.Name != "" {
		issuer = "Apexx " + t.Site().Settings.Name
	}
	secret, uri, err := s.Auth.TOTPBegin(r.Context(), p.Tenant, p.ID, issuer)
	if err != nil {
		s.authFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"secret": secret, "uri": uri})
}

func (s *Server) totpConfirm(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	codes, err := s.Auth.TOTPConfirm(r.Context(), p.Tenant, p.ID, body.Code)
	if err != nil {
		s.authFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Auth.TOTPDisable(r.Context(), p.Tenant, p.Actor(), p.ID, body.Password, true); err != nil {
		s.authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) totpReset(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Auth.TOTPDisable(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "", false); err != nil {
		s.authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	list, err := s.Auth.Sessions(r.Context(), p.Tenant, p.ID, p.SessionID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) revokeMySession(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	n, err := s.Auth.RevokeSession(r.Context(), p.Tenant, p.ID, r.PathValue("id"), "")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Sesión no encontrada")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p, ok := memberOnly(w, r)
	if !ok {
		return
	}
	n, err := s.Auth.RevokeSession(r.Context(), p.Tenant, p.ID, "", p.SessionID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"revoked": n})
}

// ---------- invitations ----------

func (s *Server) adminURL(r *http.Request) string {
	if s.Orders != nil && s.Orders.PublicURL != "" {
		return strings.TrimRight(s.Orders.PublicURL, "/") + "/admin/"
	}
	scheme := "https"
	if r.TLS == nil && !s.SecureCookies {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/admin/"
}

func (s *Server) listInvitations(w http.ResponseWriter, r *http.Request) {
	list, err := s.Auth.Invitations(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

func (s *Server) createInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email       string           `json:"email"`
		Name        string           `json:"name"`
		Permissions auth.Permissions `json:"permissions"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	inv, token, err := s.Auth.Invite(r.Context(), p.Tenant, p.Actor(), body.Email, body.Name, body.Permissions, "")
	if err != nil {
		s.authFail(w, err)
		return
	}
	link := s.adminURL(r) + "#invitacion/" + token
	emailed := false
	if s.Notify != nil {
		_, err := s.Notify.Enqueue(r.Context(), p.Tenant, notify.Message{Template: notify.TplTeamInvite, To: inv.Email, Name: inv.Name, DedupeKey: "invite-" + inv.ID, Force: true,
			Data: notify.Data{ActionURL: link, Vars: map[string]string{"cliente.nombre": firstName(inv.Name), "equipo.invita": p.Name}}})
		emailed = err == nil
		if err != nil {
			s.Log.Warn("invitation email", "tenant", p.Tenant, "err", err)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	// The link is returned once so the inviter can also share it by hand.
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"invitation": inv, "link": link, "emailed": emailed})
}

func firstName(n string) string {
	if f := strings.Fields(n); len(f) > 0 {
		return f[0]
	}
	return "Hola"
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Auth.RevokeInvitation(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.authFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) invitationInfo(w http.ResponseWriter, r *http.Request) {
	if s.loginBlocked("invite|" + s.clientIP(r)) {
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Detail: "Demasiados intentos; espera un minuto"})
		return
	}
	tenant, storeName, email, name, err := s.Auth.InvitationInfo(r.Context(), r.PathValue("token"))
	if err != nil {
		s.loginFailed("invite|" + s.clientIP(r))
		s.authFail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"store": tenant, "storeName": storeName, "email": email, "name": name})
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	if s.loginBlocked("invite|" + s.clientIP(r)) {
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Detail: "Demasiados intentos; espera un minuto"})
		return
	}
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "password", Code: "weak", Message: strings.ToUpper(err.Error()[:1]) + err.Error()[1:]})
		return
	}
	tenant, m, err := s.Auth.AcceptInvitation(r.Context(), r.PathValue("token"), body.Name, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvitation) {
			s.loginFailed("invite|" + s.clientIP(r))
		}
		s.authFail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"store": tenant, "member": m})
}

// ---------- custom roles ----------

// Role is a named set of permissions the store reuses when adding people.
type Role struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Permissions auth.Permissions `json:"permissions"`
}

var roleID = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	docs, err := s.DB.ListDocs(r.Context(), principal(r).Tenant, "roles")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]Role, 0, len(docs))
	for _, d := range docs {
		var x Role
		if jsonUnmarshal(d.Data, &x) == nil {
			x.ID = d.ID
			out = append(out, x)
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) putRole(w http.ResponseWriter, r *http.Request) {
	var x Role
	if !decode(w, r, &x) {
		return
	}
	x.ID, x.Name, x.Description = r.PathValue("id"), strings.TrimSpace(x.Name), strings.TrimSpace(x.Description)
	switch {
	case !roleID.MatchString(x.ID) || len(x.ID) > 40:
		apierr.BadRequest(w, "El identificador del rol solo lleva minúsculas, números y guiones")
		return
	case x.Name == "" || len(x.Name) > 60 || len(x.Description) > 200:
		apierr.BadRequest(w, "Escribe un nombre para el rol (hasta 60 caracteres)")
		return
	}
	if err := x.Permissions.Validate(); err != nil {
		apierr.BadRequest(w, strings.TrimPrefix(err.Error(), "auth: "))
		return
	}
	p := principal(r)
	if err := s.DB.SetDoc(r.Context(), p.Tenant, p.Actor(), "roles", x.ID, "", x); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, x)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	err := s.DB.RemoveDoc(r.Context(), p.Tenant, p.Actor(), "roles", r.PathValue("id"), "")
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Rol no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authStores answers "which stores can I enter with this email and password?".
// It is how one person manages several stores: the sign-in screen offers the
// list, and the panel uses it to switch.
func (s *Server) authStores(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	key := s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(body.Email))
	if s.loginBlocked(key) {
		w.Header().Set("Retry-After", "60")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Detail: "Demasiados intentos; espera un minuto"})
		return
	}
	list, err := s.Auth.StoresFor(r.Context(), body.Email, body.Password)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if len(list) == 0 {
		s.loginFailed(key)
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_credentials", Detail: "Correo o contraseña incorrectos"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"stores": list})
}
