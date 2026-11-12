package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
)

// Single sign-on (OpenID Connect) for the store's team. The identity
// provider proves the email; Apexx only lets in people already invited and
// active in the store. The login state travels in a signed, short-lived
// cookie with PKCE, so it cannot be forged or replayed.

// SSOConfig is a store's identity provider.
type SSOConfig struct {
	Enabled      bool     `json:"enabled"`
	Issuer       string   `json:"issuer"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret,omitempty"` // encrypted at rest; never returned
	Domains      []string `json:"domains"`                // allowed email domains (empty = any)
	Enforce      bool     `json:"enforce"`                // the team cannot use passwords (owners can)
	Name         string   `json:"name"`
}

var ssoHTTPClient *http.Client // tests

type ssoProviders struct {
	m sync.Map // issuer -> *oidc.Provider
}

func (s *Server) routesSSO(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/sso", s.authed("team", auth.Read, s.getSSO))
	handle("PUT /v1/settings/sso", s.authed("team", auth.Admin, s.putSSO))
	handle("GET /v1/auth/sso/available", s.ssoAvailable)
	handle("GET /v1/auth/sso/start", s.ssoStart)
	handle("GET /v1/auth/sso/callback", s.ssoCallback)
}

func (s *Server) ssoConfig(ctx context.Context, tenant string) (SSOConfig, error) {
	var c SSOConfig
	_, err := s.DB.GetDoc(ctx, tenant, "sso", "config", &c)
	if err == nil && c.ClientSecret != "" {
		raw, derr := base64.StdEncoding.DecodeString(c.ClientSecret)
		plain, perr := s.Keys.Decrypt(tenant, raw, "sso-client-secret")
		if derr != nil || perr != nil {
			return c, errors.New("sso: secret unreadable")
		}
		c.ClientSecret = string(plain)
	}
	return c, err
}

// SSORequired is installed in the auth service.
func (s *Server) SSORequired(ctx context.Context, tenant string) bool {
	c, err := s.ssoConfig(ctx, tenant)
	return err == nil && c.Enabled && c.Enforce
}

func (s *Server) getSSO(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	c, err := s.ssoConfig(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	has := c.ClientSecret != ""
	c.ClientSecret = ""
	if c.Domains == nil {
		c.Domains = []string{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": c, "hasSecret": has, "redirectUri": s.ssoRedirect(r)})
}

func (s *Server) putSSO(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var in SSOConfig
	if !decode(w, r, &in) {
		return
	}
	old, _ := s.ssoConfig(r.Context(), pr.Tenant)
	in.Issuer, in.ClientID, in.Name = strings.TrimRight(strings.TrimSpace(in.Issuer), "/"), strings.TrimSpace(in.ClientID), strings.TrimSpace(in.Name)
	if in.ClientSecret == "" {
		in.ClientSecret = old.ClientSecret // left empty: keep the stored one
	}
	var errs []apierr.FieldError
	u, err := url.Parse(in.Issuer)
	if in.Issuer != "" && (err != nil || u.Host == "" || (u.Scheme != "https" && !(s.Webhooks != nil && s.Webhooks.AllowPrivate))) {
		errs = append(errs, apierr.FieldError{Field: "issuer", Code: "invalid", Message: "La dirección del proveedor empieza con https://"})
	}
	if in.Enabled && (in.Issuer == "" || in.ClientID == "" || in.ClientSecret == "") {
		errs = append(errs, apierr.FieldError{Field: "clientId", Code: "required", Message: "Para activarlo llena proveedor, ID de cliente y secreto"})
	}
	for i, d := range in.Domains {
		in.Domains[i] = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
	}
	in.Domains = slices.DeleteFunc(in.Domains, func(d string) bool { return d == "" })
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if in.Enabled {
		// Check the provider answers before saving, so a typo does not lock anyone out.
		if _, err := s.ssoProvider(r.Context(), in.Issuer); err != nil {
			apierr.BadRequest(w, "No pudimos leer la configuración del proveedor", apierr.FieldError{Field: "issuer", Code: "unreachable", Message: "El proveedor no respondió su configuración OpenID (.well-known/openid-configuration)"})
			return
		}
	}
	stored := in
	if in.ClientSecret != "" {
		enc, err := s.Keys.Encrypt(pr.Tenant, []byte(in.ClientSecret), "sso-client-secret")
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		stored.ClientSecret = base64.StdEncoding.EncodeToString(enc)
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "sso", "config", "", stored); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.getSSO(w, r)
}

func (s *Server) ssoProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	if p, ok := s.sso.m.Load(issuer); ok {
		return p.(*oidc.Provider), nil
	}
	c := ssoHTTPClient
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	// The provider keeps this context to fetch its signing keys later: it must
	// not be cancelled. The client's own timeout bounds every call.
	p, err := oidc.NewProvider(oidc.ClientContext(context.Background(), c), issuer)
	if err != nil {
		return nil, err
	}
	s.sso.m.Store(issuer, p)
	return p, nil
}

func (s *Server) ssoRedirect(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && !s.SecureCookies {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/v1/auth/sso/callback"
}

func (s *Server) ssoAvailable(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("store")
	c, err := s.ssoConfig(r.Context(), tenant)
	ok := err == nil && c.Enabled
	httpx.JSON(w, r, http.StatusOK, map[string]any{"enabled": ok, "enforce": ok && c.Enforce, "name": map[bool]string{true: c.Name}[ok]})
}

type ssoState struct {
	Tenant   string `json:"t"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
}

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) signState(st ssoState) (string, error) {
	b, _ := json.Marshal(st)
	mac, err := s.Keys.Blind(st.Tenant, "sso-state", string(b))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b) + "." + hex.EncodeToString(mac), nil
}

func (s *Server) readState(v string) (ssoState, bool) {
	var st ssoState
	body, sig, ok := strings.Cut(v, ".")
	b, err := base64.RawURLEncoding.DecodeString(body)
	if !ok || err != nil || json.Unmarshal(b, &st) != nil || time.Now().Unix() > st.Expires {
		return st, false
	}
	mac, err := s.Keys.Blind(st.Tenant, "sso-state", string(b))
	return st, err == nil && hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac)))
}

func (s *Server) ssoStart(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("store")
	c, err := s.ssoConfig(r.Context(), tenant)
	if err != nil || !c.Enabled {
		apierr.NotFound(w, "Esta tienda no tiene inicio de sesión con su empresa")
		return
	}
	p, err := s.ssoProvider(r.Context(), c.Issuer)
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusBadGateway, Code: "sso_unavailable", Detail: "El proveedor de inicio de sesión no responde; intenta en un momento"})
		return
	}
	st := ssoState{Tenant: tenant, State: randToken(), Nonce: randToken(), Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(10 * time.Minute).Unix()}
	cookie, err := s.signState(st)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_sso", Value: cookie, Path: "/v1/auth/sso/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	conf := oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: s.ssoRedirect(r), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	http.Redirect(w, r, conf.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

var ssoDone = template.Must(template.New("sso").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="referrer" content="no-referrer">
<meta http-equiv="refresh" content="0;url={{.}}"><title>Entrando…</title></head><body style="font-family:system-ui;padding:24px">Entrando… <a href="{{.}}">Continuar</a></body></html>`))

func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	fail := func(status int, msg string) {
		http.SetCookie(w, &http.Cookie{Name: "apexx_sso", Value: "", Path: "/v1/auth/sso/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.WriteHeader(status)
		_ = ssoDone.Execute(w, "/admin/#sso="+url.QueryEscape(msg))
	}
	ck, err := r.Cookie("apexx_sso")
	if err != nil {
		fail(http.StatusBadRequest, "El inicio de sesión venció; vuelve a intentarlo")
		return
	}
	st, ok := s.readState(ck.Value)
	if !ok || !hmac.Equal([]byte(st.State), []byte(r.URL.Query().Get("state"))) {
		fail(http.StatusBadRequest, "El inicio de sesión no es válido; vuelve a intentarlo")
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		fail(http.StatusUnauthorized, "Tu empresa no autorizó el acceso")
		return
	}
	c, err := s.ssoConfig(r.Context(), st.Tenant)
	if err != nil || !c.Enabled {
		fail(http.StatusForbidden, "La tienda ya no usa este inicio de sesión")
		return
	}
	p, err := s.ssoProvider(r.Context(), c.Issuer)
	if err != nil {
		fail(http.StatusBadGateway, "El proveedor no responde")
		return
	}
	hc := ssoHTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	ctx := oidc.ClientContext(r.Context(), hc)
	conf := oauth2.Config{ClientID: c.ClientID, ClientSecret: c.ClientSecret, Endpoint: p.Endpoint(), RedirectURL: s.ssoRedirect(r), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}
	tok, err := conf.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		fail(http.StatusUnauthorized, "No pudimos confirmar tu identidad")
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := p.Verifier(&oidc.Config{ClientID: c.ClientID}).Verify(ctx, raw)
	if err != nil || !hmac.Equal([]byte(idt.Nonce), []byte(st.Nonce)) {
		fail(http.StatusUnauthorized, "No pudimos confirmar tu identidad")
		return
	}
	var claims struct {
		Email    string `json:"email"`
		Verified *bool  `json:"email_verified"`
	}
	_ = idt.Claims(&claims)
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	_, domain, _ := strings.Cut(email, "@")
	switch {
	case email == "" || (claims.Verified != nil && !*claims.Verified):
		fail(http.StatusForbidden, "Tu proveedor no confirmó tu correo")
		return
	case len(c.Domains) > 0 && !slices.Contains(c.Domains, domain):
		fail(http.StatusForbidden, "Tu correo no es de un dominio permitido por la tienda")
		return
	}
	token, _, err := s.Auth.LoginVerifiedEmail(r.Context(), st.Tenant, email, s.clientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrBadCredentials) {
		fail(http.StatusForbidden, "No eres parte del equipo de esta tienda; pide una invitación")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_sso", Value: "", Path: "/v1/auth/sso/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookies})
	http.SetCookie(w, &http.Cookie{Name: "apexx_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	// A page on our own site moves to the panel, so the strict session cookie is sent.
	_ = ssoDone.Execute(w, "/admin/")
}
