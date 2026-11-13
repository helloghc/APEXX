package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Superadmin: the platform's operators set what each store's plan includes
// (how many sandboxes, how big). They are not members of any store: their
// own accounts, made from the command line, always with two-step
// verification, in a session of their own.

const (
	operatorCookie  = "apexx_op"
	operatorKeyName = "platform:operators"
	operatorTTL     = 8 * time.Hour
)

var (
	operatorIPLimiter    = &shopLimiter{n: 10} // sign-in attempts per address per minute
	operatorEmailLimiter = &shopLimiter{n: 5}  // per account per minute
)

type operatorCtxKey struct{}

// operatorKey loads the key that encrypts the operators' two-step secrets.
func operatorKey(ctx context.Context, db *store.DB, keys *crypto.Keyring) error {
	if _, err := keys.Encrypt(operatorKeyName, nil, "probe"); !errors.Is(err, crypto.ErrNoKey) {
		return nil
	}
	w, err := db.PlatformKey(ctx, "operators", func() ([]byte, error) { return keys.NewWrappedKey(operatorKeyName) })
	if err != nil {
		return err
	}
	return keys.Load(operatorKeyName, w)
}

// CreateOperator makes an operator account (the command line calls it) and
// returns the two-step secret to add to an authenticator app, once.
func CreateOperator(ctx context.Context, db *store.DB, keys *crypto.Keyring, email, name, password string) (store.Operator, string, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || strings.TrimSpace(name) == "" || len(password) < 12 {
		return store.Operator{}, "", "", errors.New("se necesitan correo, nombre y una contraseña de al menos 12 caracteres")
	}
	if err := operatorKey(ctx, db, keys); err != nil {
		return store.Operator{}, "", "", err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.Operator{}, "", "", err
	}
	raw, encoded, err := auth.NewTOTPSecret()
	if err != nil {
		return store.Operator{}, "", "", err
	}
	enc, err := keys.Encrypt(operatorKeyName, raw, "operator-totp:"+email)
	if err != nil {
		return store.Operator{}, "", "", err
	}
	op, err := db.CreateOperator(ctx, email, strings.TrimSpace(name), hash, enc)
	if err != nil {
		return store.Operator{}, "", "", err
	}
	return op, encoded, auth.TOTPURI("Apexx superadmin", email, encoded), nil
}

func (s *Server) routesOperator(handle func(string, http.HandlerFunc)) {
	handle("POST /v1/operator/login", s.operatorLogin)
	handle("POST /v1/operator/logout", s.opAuthed(s.operatorLogout))
	handle("GET /v1/operator/me", s.opAuthed(s.operatorMe))
	handle("GET /v1/operator/stores", s.opAuthed(s.operatorStores))
	handle("PUT /v1/operator/stores/{id}/plan", s.opAuthed(s.operatorSetPlan))
	handle("GET /v1/operator/log", s.opAuthed(s.operatorLogList))
}

func operatorOf(r *http.Request) store.Operator {
	op, _ := r.Context().Value(operatorCtxKey{}).(store.Operator)
	return op
}

func operatorToken(r *http.Request) string {
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer apo_") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	if c, err := r.Cookie(operatorCookie); err == nil {
		return c.Value
	}
	return ""
}

// opAuthed lets through only a live operator session; a store's session or
// key, whatever its permissions, is not one.
func (s *Server) opAuthed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := operatorToken(r)
		if !strings.HasPrefix(tok, "apo_") {
			// A store's key or session is a credential, just not this one.
			if r.Header.Get("Authorization") != "" {
				apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "operators_only", Detail: "Esto es solo para los operadores de la plataforma"})
				return
			}
			apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "Inicia sesión como operador"})
			return
		}
		op, err := s.DB.OperatorSession(r.Context(), tok)
		if err != nil {
			apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "Tu sesión de operador terminó. Inicia sesión de nuevo."})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		h(w, r.WithContext(context.WithValue(r.Context(), operatorCtxKey{}, op)))
	}
}

// dummyHash makes a sign-in with an unknown email take as long as one with
// a wrong password.
var dummyHash, _ = auth.HashPassword("apexx-operator-dummy-password")

func (s *Server) operatorLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	ip := s.clientIP(r)
	if !operatorIPLimiter.allow(ip) || !operatorEmailLimiter.allow(email) {
		w.Header().Set("Retry-After", "60")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_attempts", Detail: "Demasiados intentos. Espera un minuto."})
		return
	}
	bad := func() {
		_ = s.DB.LogOperator(r.Context(), "", "login.failed", email, ip, map[string]any{})
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "bad_credentials", Detail: "Correo, contraseña o código incorrectos"})
	}
	op, err := s.DB.OperatorByEmail(r.Context(), email)
	if err != nil || op.DisabledAt != nil {
		auth.CheckPassword(dummyHash, body.Password)
		bad()
		return
	}
	if !auth.CheckPassword(op.PasswordHash, body.Password) {
		bad()
		return
	}
	if err := operatorKey(r.Context(), s.DB, s.Keys); err != nil {
		internalError(w, s.Log, err)
		return
	}
	secret, err := s.Keys.Decrypt(operatorKeyName, op.TOTPSecret, "operator-totp:"+op.Email)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	step := auth.MatchTOTP(secret, body.Code, time.Now())
	if step == 0 {
		bad()
		return
	}
	if ok, err := s.DB.UseOperatorStep(r.Context(), op.ID, step); err != nil || !ok {
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		bad() // the same code twice
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		internalError(w, s.Log, err)
		return
	}
	token := "apo_" + hex.EncodeToString(raw)
	if err := s.DB.StartOperatorSession(r.Context(), op.ID, token, ip, r.UserAgent(), operatorTTL); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.LogOperator(r.Context(), op.ID, "login", "", ip, map[string]any{})
	http.SetCookie(w, &http.Cookie{Name: operatorCookie, Value: token, Path: "/v1/operator", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int(operatorTTL.Seconds())})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"operator": op, "token": token})
}

func (s *Server) operatorLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.DB.EndOperatorSession(r.Context(), operatorToken(r))
	http.SetCookie(w, &http.Cookie{Name: operatorCookie, Value: "", Path: "/v1/operator", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) operatorMe(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, map[string]any{"operator": operatorOf(r)})
}

func (s *Server) operatorStores(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.StoreSummaries(r.Context(), strings.TrimSpace(r.URL.Query().Get("q")), 200)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if list == nil {
		list = []store.StoreSummary{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list, "defaultPlan": store.DefaultPlan})
}

// operatorSetPlan changes what a store's plan includes. Sandboxes it already
// has stay; the new numbers apply to what it creates or brings next.
func (s *Server) operatorSetPlan(w http.ResponseWriter, r *http.Request) {
	op := operatorOf(r)
	var p store.Plan
	if !decode(w, r, &p) {
		return
	}
	p.Plan = strings.TrimSpace(p.Plan)
	if p.Sandboxes < 0 || p.Sandboxes > 50 || p.SandboxMaxProducts < 0 || len(p.Plan) > 40 {
		apierr.BadRequest(w, "Revisa el plan", apierr.FieldError{Field: "sandboxes", Code: "invalid", Message: "Sandboxes de 0 a 50; productos 0 (sin límite) o más; nombre del plan de hasta 40 caracteres"})
		return
	}
	tenant := r.PathValue("id")
	kind, _, err := s.DB.TenantKind(r.Context(), tenant)
	if err != nil || kind != "production" {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	old, err := s.DB.TenantPlan(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	actor := "operator:" + op.Email
	if err := s.DB.SetTenantPlan(r.Context(), tenant, actor, p); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.LogOperator(r.Context(), op.ID, "plan.change", tenant, s.clientIP(r), map[string]any{"from": old, "to": p})
	// The store sees who changed its plan, in its own Activity.
	_ = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, tenant, actor, "plan.change", tenant, map[string]any{"from": old, "to": p})
	})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"plan": p})
}

func (s *Server) operatorLogList(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.OperatorLog(r.Context(), 100)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}
