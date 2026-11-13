package api

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/crypto"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/reports"
	"github.com/helloghc/apexx/internal/store"
)

// Sandbox per store (docs/fases/plan-sandbox.md): the panel creates it,
// people enter it from production, preview links open its storefront, and
// what is ready is published to production with a revert.

// envInfo is what a node knows about a store: production or sandbox.
type envInfo struct {
	kind    string // production, sandbox
	parent  string // sandbox: its production store
	sandbox string // production: its sandbox, if any
	until   time.Time
}

type envCache struct {
	mu sync.Mutex
	m  map[string]envInfo
}

// envOf says whether a store is production or a sandbox (cached briefly;
// creating or deleting a sandbox clears it on every node).
func (s *Server) envOf(tenant string) envInfo {
	s.env.mu.Lock()
	e, ok := s.env.m[tenant]
	s.env.mu.Unlock()
	if ok && time.Now().Before(e.until) {
		return e
	}
	e = envInfo{kind: "production", until: time.Now().Add(30 * time.Second)}
	if s.DB != nil && tenant != "" {
		ctx := context.Background()
		if kind, parent, err := s.DB.TenantKind(ctx, tenant); err == nil {
			e.kind, e.parent = kind, parent
		}
		if e.kind == "production" {
			if sb, err := s.DB.SandboxOf(ctx, tenant); err == nil {
				e.sandbox = sb.ID
			}
		}
	}
	s.env.mu.Lock()
	if s.env.m == nil {
		s.env.m = map[string]envInfo{}
	}
	s.env.m[tenant] = e
	s.env.mu.Unlock()
	return e
}

func (s *Server) forgetEnv(tenants ...string) {
	s.env.mu.Lock()
	for _, t := range tenants {
		delete(s.env.m, t)
	}
	s.env.mu.Unlock()
}

// isSandbox says whether the store is a sandbox.
func (s *Server) isSandbox(tenant string) bool { return s.envOf(tenant).kind == "sandbox" }

// sandboxCaptured is what an outgoing call records when it comes from a
// sandbox: nothing leaves for a real service.
const sandboxCaptured = "Capturado en el sandbox: no se envía"

// refuseInSandbox answers 409 when a sandbox tries to connect something that
// would talk to the real world (webhooks, marketplaces, ad conversions).
func (s *Server) refuseInSandbox(w http.ResponseWriter, tenant, what string) bool {
	if !s.isSandbox(tenant) {
		return false
	}
	apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "sandbox_no_outbound",
		Detail: "En el sandbox " + what + " no se conecta: nada sale a servicios reales. Conéctalo en tu tienda de producción."})
	return true
}

// sandboxParent is the production store of a sandbox ("" otherwise).
func (s *Server) sandboxParent(tenant string) string { return s.envOf(tenant).parent }

func (s *Server) routesSandbox(handle func(string, http.HandlerFunc)) {
	// A sandbox reads the files it did not change from its production store.
	if s.Media != nil {
		if _, ok := s.Media.(media.Inherit); !ok {
			s.Media = media.Inherit{Storage: s.Media, Parent: s.sandboxParent}
		}
	}
	handle("GET /v1/sandbox", s.authed("settings", auth.Read, s.getSandbox))
	handle("POST /v1/sandbox", s.authed("settings", auth.Admin, s.createSandbox))
	handle("POST /v1/sandbox/refresh", s.authed("settings", auth.Admin, s.refreshSandbox))
	handle("DELETE /v1/sandbox", s.authed("settings", auth.Admin, s.deleteSandbox))
	handle("POST /v1/sandbox/enter", s.authed("", "", s.enterSandbox))
	handle("POST /v1/sandbox/exit", s.authed("", "", s.exitSandbox))
	handle("POST /v1/sandbox/preview", s.authed("settings", auth.Read, s.previewSandbox))
	handle("GET /v1/sandbox/diff", s.authed("settings", auth.Read, s.sandboxDiff))
	handle("POST /v1/sandbox/publish", s.authed("settings", auth.Admin, s.publishSandbox))
	handle("GET /v1/sandbox/publishes", s.authed("settings", auth.Read, s.listPublishes))
	handle("POST /v1/sandbox/publishes/{id}/revert", s.authed("settings", auth.Admin, s.revertPublish))
	handle("GET /v1/sandbox/mail", s.authed("settings", auth.Read, s.sandboxMail))
}

// sandboxHooks keep every node's copies right when a store is reloaded
// whole (a publish or revert) or a sandbox is deleted.
func (s *Server) sandboxHooks() {
	drop := func(tenant, _ string) {
		s.sitesMu.Lock()
		for k := range s.sites {
			if strings.HasPrefix(k, tenant+"/") {
				delete(s.sites, k)
			}
		}
		s.sitesMu.Unlock()
		s.tags.mu.Lock()
		delete(s.tags.m, tenant)
		s.tags.mu.Unlock()
		s.summaryMu.Lock()
		delete(s.summaries, tenant)
		s.summaryMu.Unlock()
		s.forgetEnv(tenant)
	}
	s.Proj.On(store.TopicTenantReload, drop)
	s.Proj.On(store.TopicTenantRemoved, func(tenant, key string) {
		drop(tenant, key)
		s.env.mu.Lock()
		for k, e := range s.env.m {
			if e.sandbox == tenant {
				delete(s.env.m, k)
			}
		}
		s.env.mu.Unlock()
	})
}

// sandboxHostSuffix is the domain of temporary sandbox addresses
// ({id}.<suffix>); empty: sandboxes are reached by path only.
func sandboxHostSuffix() string { return strings.TrimPrefix(os.Getenv("APEXX_SANDBOX_HOST"), ".") }

// sandboxURL is where a sandbox's storefront lives.
func (s *Server) sandboxURL(r *http.Request, id string) string {
	if suf := sandboxHostSuffix(); suf != "" {
		return "https://" + id + "." + suf + "/"
	}
	return "/s/" + id + "/main/index"
}

// ---------- panel ----------

type sandboxView struct {
	Env       string           `json:"env"` // where the request comes from: production or sandbox
	Parent    string           `json:"parent,omitempty"`
	Sandbox   *store.Sandbox   `json:"sandbox"`
	URL       string           `json:"url,omitempty"`
	Domains   []string         `json:"domains"`
	Areas     []map[string]any `json:"areas"`
	ExpiresAt *time.Time       `json:"expiresAt,omitempty"`
}

// pairOf returns the production store and its sandbox for any of the two.
func (s *Server) pairOf(ctx context.Context, tenant string) (parent string, sb store.Sandbox, err error) {
	parent = tenant
	if e := s.envOf(tenant); e.kind == "sandbox" {
		parent = e.parent
	}
	sb, err = s.DB.SandboxOf(ctx, parent)
	return parent, sb, err
}

const sandboxIdleDays = 30

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	e := s.envOf(pr.Tenant)
	v := sandboxView{Env: e.kind, Parent: e.parent, Domains: []string{}}
	for _, a := range store.SandboxAreas {
		v.Areas = append(v.Areas, map[string]any{"id": a.ID, "label": a.Label})
	}
	v.Areas = append(v.Areas, map[string]any{"id": "tags", "label": "Etiquetas y píxeles"})
	_, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, s.Log, err)
		return
	}
	if err == nil {
		v.Sandbox = &sb
		v.URL = s.sandboxURL(r, sb.ID)
		hosts, _ := s.sandboxDomains(r.Context(), sb.ID)
		v.Domains = hosts
		if !s.hasOwnDomain(hosts) {
			exp := sb.LastUsedAt.Add(sandboxIdleDays * 24 * time.Hour)
			v.ExpiresAt = &exp
		}
	}
	httpx.JSON(w, r, http.StatusOK, v)
}

// sandboxDomains lists the verified addresses of a sandbox.
func (s *Server) sandboxDomains(ctx context.Context, id string) ([]string, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT hostname FROM domains WHERE tenant_id=$1 AND status='verified' ORDER BY hostname`, id)
	if err != nil {
		return nil, err
	}
	hosts, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if hosts == nil {
		hosts = []string{}
	}
	return hosts, err
}

// hasOwnDomain: a sandbox with the store's own subdomain does not expire.
func (s *Server) hasOwnDomain(hosts []string) bool {
	suf := sandboxHostSuffix()
	for _, h := range hosts {
		if suf == "" || !strings.HasSuffix(h, "."+suf) {
			return true
		}
	}
	return false
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		DataMode string `json:"dataMode"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	if body.DataMode == "" {
		body.DataMode = "none"
	}
	if body.DataMode != "none" && body.DataMode != "demo" {
		apierr.BadRequest(w, "Revisa los datos", apierr.FieldError{Field: "dataMode", Code: "invalid", Message: "Elige: sin clientes ni pedidos, o con datos de prueba"})
		return
	}
	if s.isSandbox(pr.Tenant) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_sandbox", Detail: "Ya estás en el sandbox. Créalo o refréscalo desde producción."})
		return
	}
	id := store.NewSandboxID()
	wrapped, err := s.Keys.NewWrappedKey(id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if err := s.DB.StartSandbox(r.Context(), pr.Tenant, id, pr.Actor(), body.DataMode, wrapped); err != nil {
		if errors.Is(err, store.ErrSandboxExists) {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "sandbox_exists", Detail: "Tu tienda ya tiene su sandbox. Puedes refrescarlo o borrarlo."})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	if err := s.Keys.Load(id, wrapped); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "sandbox.create", id, map[string]any{"dataMode": body.DataMode})
	})
	s.forgetEnv(pr.Tenant)
	s.runSandboxCopy(id, pr.Tenant, body.DataMode, false)
	sb, _ := s.DB.SandboxByID(r.Context(), id)
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"sandbox": sb, "url": s.sandboxURL(r, id)})
}

// runSandboxCopy copies production into the sandbox in the background (a
// 50 000-product store takes seconds); the panel shows the progress.
func (s *Server) runSandboxCopy(id, parent, dataMode string, refresh bool) {
	s.sandboxCopies.Add(1)
	go func() {
		defer s.sandboxCopies.Done()
		ctx := context.Background()
		start := time.Now()
		fail := func(err error) {
			s.Log.Error("sandbox copy", "sandbox", id, "err", err)
			_ = s.DB.SetSandboxStatus(ctx, id, "failed", "La copia no terminó: "+err.Error(), 0)
		}
		if refresh {
			if err := s.DB.WipeSandbox(ctx, id, true); err != nil {
				fail(err)
				return
			}
		}
		if _, err := s.DB.CopySandbox(ctx, id); err != nil {
			fail(err)
			return
		}
		if err := s.copyEncrypted(ctx, parent, id); err != nil {
			fail(err)
			return
		}
		if err := s.ensureSandboxHost(ctx, id); err != nil {
			fail(err)
			return
		}
		if dataMode == "demo" {
			if err := s.seedSandboxDemo(ctx, id); err != nil {
				s.Log.Warn("sandbox demo data", "sandbox", id, "err", err)
			}
		}
		// Ready means usable: the store is loaded in memory (50 000
		// products take a few seconds after the copy).
		if s.Proj != nil {
			// Apply the reload now (drain is serialized with the projection
			// loop), then wait in case another node's loop holds it.
			if err := s.Proj.Drain(ctx); err != nil {
				s.Log.Warn("sandbox projection", "sandbox", id, "err", err)
			}
			for i := 0; i < 1200; i++ {
				if _, ok := s.Proj.Tenant(id); ok {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
		_ = s.DB.SetSandboxStatus(ctx, id, "ready", "", int(time.Since(start).Milliseconds()))
		s.forgetEnv(id, parent)
		s.Log.Info("sandbox ready", "sandbox", id, "parent", parent, "ms", time.Since(start).Milliseconds())
	}()
}

// copyEncrypted copies the rows encrypted with the store's key: decrypted
// with production's key, encrypted with the sandbox's (the record id and
// store are bound into the ciphertext, so a plain copy could not be read).
func (s *Server) copyEncrypted(ctx context.Context, parent, id string) error {
	if err := s.ensureKey(ctx, parent); err != nil {
		return err
	}
	rows, err := s.DB.System.Query(ctx, `SELECT entity, id, data, source, created_at FROM entity_records WHERE tenant_id=$1`, parent)
	if err != nil {
		return err
	}
	type rec struct {
		entity, id, source string
		data               []byte
		created            time.Time
	}
	var recs []rec
	for rows.Next() {
		var x rec
		if err := rows.Scan(&x.entity, &x.id, &x.data, &x.source, &x.created); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, x := range recs {
		aad := "entity:" + x.entity + ":" + x.id
		plain, err := s.Keys.Decrypt(parent, x.data, aad)
		if err != nil {
			return err
		}
		enc, err := s.Keys.Encrypt(id, plain, aad)
		if err != nil {
			return err
		}
		if _, err := s.DB.System.Exec(ctx, `INSERT INTO entity_records (tenant_id, entity, id, data, source, created_at) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT DO NOTHING`, id, x.entity, x.id, enc, x.source, x.created); err != nil {
			return err
		}
	}
	return nil
}

// ensureKey loads a store's data key on this node if it is not yet.
func (s *Server) ensureKey(ctx context.Context, tenant string) error {
	if _, err := s.Keys.Encrypt(tenant, nil, "probe"); !errors.Is(err, crypto.ErrNoKey) {
		return nil
	}
	w, err := s.DB.WrappedKey(ctx, tenant)
	if err != nil {
		return err
	}
	return s.Keys.Load(tenant, w)
}

// ensureSandboxHost registers the temporary address {id}.<suffix> as the
// sandbox's verified domain: the same routing and certificates as any
// domain, and production can never take it.
func (s *Server) ensureSandboxHost(ctx context.Context, id string) error {
	suf := sandboxHostSuffix()
	if suf == "" {
		return nil
	}
	_, err := s.DB.System.Exec(ctx, `INSERT INTO domains (tenant_id, id, hostname, site, status, token, verified_at)
		VALUES ($1, 'dom_sandbox', $2, 'main', 'verified', 'sandbox', now()) ON CONFLICT (tenant_id, hostname) DO NOTHING`, id, id+"."+suf)
	if err == nil {
		_ = s.DB.EmitAI(ctx, id, store.TopicSettings, store.KeyDomains)
	}
	return err
}

// seedSandboxDemo fills the sandbox with test customers and orders.
func (s *Server) seedSandboxDemo(ctx context.Context, id string) error {
	if err := s.ensureKey(ctx, id); err != nil {
		return err
	}
	rep := &reports.Service{DB: s.DB, Keys: s.Keys, Log: s.Log}
	if err := rep.SeedDemo(ctx, id, "system:sandbox", reports.DemoOptions{Orders: 300, Days: 90, Sessions: 0, Seed: time.Now().UnixNano()}); err != nil {
		return err
	}
	_, err := rep.SyncOrders(ctx)
	return err
}

func (s *Server) refreshSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	parent, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	if sb.Status == "copying" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "copying", Detail: "El sandbox se está copiando. Espera a que termine."})
		return
	}
	if err := s.DB.SetSandboxStatusOnly(r.Context(), sb.ID, "copying"); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.refresh", sb.ID, map[string]any{})
	})
	s.runSandboxCopy(sb.ID, parent, sb.DataMode, true)
	sb.Status = "copying"
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"sandbox": sb})
}

func (s *Server) deleteSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if s.isSandbox(pr.Tenant) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_sandbox", Detail: "El sandbox se borra desde producción."})
		return
	}
	sb, err := s.DB.SandboxOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda no tiene sandbox") {
		return
	}
	if sb.Status == "copying" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "copying", Detail: "El sandbox se está copiando. Espera a que termine."})
		return
	}
	if err := s.removeSandbox(r.Context(), sb, pr.Actor()); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeSandbox deletes a sandbox, its files, and checks nothing is left.
func (s *Server) removeSandbox(ctx context.Context, sb store.Sandbox, actor string) error {
	if err := s.DB.SetSandboxStatusOnly(ctx, sb.ID, "deleting"); err != nil {
		return err
	}
	if err := s.DB.DeleteSandbox(ctx, sb.ID); err != nil {
		return err
	}
	if rm, ok := s.Media.(media.TenantRemover); ok {
		_ = rm.RemoveTenant(ctx, sb.ID)
	}
	left, err := s.DB.SandboxRowCounts(ctx, sb.ID)
	if err != nil {
		return err
	}
	delete(left, "audit_log") // immutable by design
	delete(left, "outbox")
	if len(left) > 0 {
		b, _ := json.Marshal(left)
		return errors.New("rows left after deleting the sandbox: " + string(b))
	}
	s.forgetEnv(sb.ID, sb.ParentID)
	return s.DB.Tx(ctx, sb.ParentID, func(tx pgx.Tx) error {
		return store.Audit(ctx, tx, sb.ParentID, actor, "sandbox.delete", sb.ID, map[string]any{})
	})
}

// enterSandbox moves a person from production into the sandbox: same
// person and permissions, a session of the sandbox.
func (s *Server) enterSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Solo las personas del equipo entran al sandbox"})
		return
	}
	if s.isSandbox(pr.Tenant) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_sandbox", Detail: "Ya estás en el sandbox"})
		return
	}
	sb, err := s.DB.SandboxOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	if sb.Status != "ready" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_ready", Detail: "El sandbox aún no está listo"})
		return
	}
	if err := s.DB.SyncSandboxMembers(r.Context(), pr.Tenant, sb.ID); err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.switchSession(w, r, sb.ID)
	_ = s.DB.TouchSandbox(r.Context(), sb.ID)
}

// exitSandbox moves a person back to production.
func (s *Server) exitSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Solo las personas del equipo entran y salen del sandbox"})
		return
	}
	e := s.envOf(pr.Tenant)
	if e.kind != "sandbox" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_in_sandbox", Detail: "No estás en el sandbox"})
		return
	}
	s.switchSession(w, r, e.parent)
}

// switchSession ends the current session and starts one in the other store
// for the same person (who must be active there).
func (s *Server) switchSession(w http.ResponseWriter, r *http.Request, to string) {
	pr := principal(r)
	token, err := s.Auth.StartSessionFor(r.Context(), to, pr.ID, s.clientIP(r), r.UserAgent())
	if errors.Is(err, auth.ErrBadCredentials) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_member", Detail: "No formas parte del equipo de esa tienda"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// The session of the store being left ends, however it was sent.
	if c, err := r.Cookie("apexx_session"); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer aps_") {
		_ = s.Auth.Logout(r.Context(), strings.TrimPrefix(a, "Bearer "))
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	_ = s.DB.Tx(r.Context(), to, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, to, pr.Actor(), "session.switch", pr.Tenant, map[string]any{"from": pr.Tenant})
	})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"tenant": to, "token": token})
}

// ---------- preview links ----------

// previewSandbox makes a signed link to the sandbox's storefront, for the
// team or for someone outside (an agency, a client trying a catalog).
func (s *Server) previewSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Hours int `json:"hours"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	if body.Hours <= 0 {
		body.Hours = 12
	}
	if body.Hours > 24*14 {
		apierr.BadRequest(w, "Revisa el vencimiento", apierr.FieldError{Field: "hours", Code: "too_long", Message: "Un enlace dura hasta 14 días"})
		return
	}
	_, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	exp := time.Now().Add(time.Duration(body.Hours) * time.Hour)
	tok, err := s.sandboxToken(r.Context(), sb.ID, exp)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	u := s.sandboxURL(r, sb.ID)
	_ = s.DB.TouchSandbox(r.Context(), sb.ID)
	_ = s.DB.Tx(r.Context(), sb.ParentID, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, sb.ParentID, pr.Actor(), "sandbox.preview_link", sb.ID, map[string]any{"hours": body.Hours})
	})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"url": u + "?sbx=" + url.QueryEscape(tok), "expiresAt": exp})
}

// sandboxToken signs "sandbox|expiry" with the sandbox's own key: a link
// opens only that sandbox, until it expires; deleting the sandbox voids it.
func (s *Server) sandboxToken(ctx context.Context, id string, exp time.Time) (string, error) {
	if err := s.ensureKey(ctx, id); err != nil {
		return "", err
	}
	payload := id + "|" + strconv.FormatInt(exp.Unix(), 10)
	mac, err := s.Keys.Blind(id, "sandbox-preview", payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac[:16]), nil
}

// validPreview checks a token for a sandbox.
func (s *Server) validPreview(ctx context.Context, id, tok string) (time.Time, bool) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return time.Time{}, false
	}
	sid, ts, ok := strings.Cut(string(raw), "|")
	if !ok || sid != id {
		return time.Time{}, false
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Now().Unix() > sec {
		return time.Time{}, false
	}
	if s.ensureKey(ctx, id) != nil {
		return time.Time{}, false
	}
	mac, err := s.Keys.Blind(id, "sandbox-preview", string(raw))
	if err != nil {
		return time.Time{}, false
	}
	want, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(want, mac[:16]) {
		return time.Time{}, false
	}
	return time.Unix(sec, 0), true
}

const previewCookie = "apexx_sbx"

// sandboxGate keeps a sandbox's storefront private: only with a valid
// preview link (which leaves a cookie for that sandbox) or a session of its
// team. Every answer carries noindex.
func (s *Server) sandboxGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := ""
		for _, pre := range []string{"/s/", "/media/"} {
			if strings.HasPrefix(r.URL.Path, pre) {
				tenant, _, _ = strings.Cut(r.URL.Path[len(pre):], "/")
			}
		}
		if tenant == "" || tenant == "assets" || !strings.HasPrefix(tenant, store.SandboxPrefix) || !s.isSandbox(tenant) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		if tok := r.URL.Query().Get("sbx"); tok != "" {
			if exp, ok := s.validPreview(r.Context(), tenant, tok); ok {
				http.SetCookie(w, &http.Cookie{Name: previewCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
					SameSite: http.SameSiteLaxMode, Expires: exp})
				q := r.URL.Query()
				q.Del("sbx")
				u := *r.URL
				u.RawQuery = q.Encode()
				if customHost(r) {
					u.Path = strings.TrimPrefix(u.Path, "/s/"+tenant+"/main")
				}
				http.Redirect(w, r, u.String(), http.StatusSeeOther)
				return
			}
		}
		if c, err := r.Cookie(previewCookie); err == nil {
			if _, ok := s.validPreview(r.Context(), tenant, c.Value); ok {
				next.ServeHTTP(w, r)
				return
			}
		}
		// The team, signed in to the sandbox's panel, sees it too.
		if c, err := r.Cookie("apexx_session"); err == nil && s.Auth != nil {
			if p, err := s.Auth.Authenticate(r.Context(), c.Value); err == nil && p.Tenant == tenant {
				next.ServeHTTP(w, r)
				return
			}
		}
		if strings.Contains(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/media/") {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "sandbox_private", Detail: "Este sandbox es privado"})
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_ = sandboxPrivatePage.Execute(w, nil)
	})
}

var sandboxPrivatePage = template.Must(template.New("p").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Sandbox privado</title>
<style>body{font:16px/1.5 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f6f6f2;color:#1d2026}main{max-width:32rem;padding:24px}h1{font-size:1.5rem;margin:0 0 8px}</style></head>
<body><main><h1>Este sandbox es privado</h1><p>Ábrelo desde el panel de tu tienda, en Configuración › Sandbox, o con un enlace de vista previa que te haya compartido el equipo de la tienda.</p></main></body></html>`))

// ---------- publish ----------

func splitList(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func validAreas(areas []string) ([]string, bool, bool) {
	tags := false
	var out []string
	for _, a := range areas {
		if a == "tags" {
			tags = true
			continue
		}
		ok := false
		for _, x := range store.SandboxAreas {
			ok = ok || x.ID == a
		}
		if !ok {
			return nil, false, false
		}
		out = append(out, a)
	}
	return out, tags, true
}

func (s *Server) sandboxDiff(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	parent, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	areas := splitList(r.URL.Query().Get("areas"))
	if len(areas) == 0 {
		for _, a := range store.SandboxAreas {
			areas = append(areas, a.ID)
		}
		areas = append(areas, "tags")
	}
	tableAreas, tags, ok := validAreas(areas)
	if !ok {
		apierr.BadRequest(w, "Área desconocida", apierr.FieldError{Field: "areas", Code: "invalid", Message: "Áreas: catalog, prices, stock, promotions, design, settings, tags"})
		return
	}
	diffs, err := s.DB.SandboxDiff(r.Context(), sb.ID, tableAreas, splitList(r.URL.Query().Get("products")))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := map[string]any{"areas": diffs, "sandbox": sb.ID, "parent": parent}
	if tags {
		ch, err := s.tagsDiff(r.Context(), parent, sb.ID)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		out["tags"] = ch
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// tagsDiff says, in words, what publishing the sandbox's tags would change.
func (s *Server) tagsDiff(ctx context.Context, parent, sandbox string) ([]string, error) {
	prod, err := s.loadTags(ctx, parent)
	if err != nil {
		return nil, err
	}
	sbx, err := s.loadTags(ctx, sandbox)
	if err != nil {
		return nil, err
	}
	ch := tagsChanges(prod, sbx)
	if len(ch) == 1 && ch[0] == "Sin cambios" {
		return []string{}, nil
	}
	return ch, nil
}

func (s *Server) publishSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Areas    []string `json:"areas"`
		Products []string `json:"products"`
		Force    bool     `json:"force"`
	}
	if !decode(w, r, &body) {
		return
	}
	tableAreas, tags, ok := validAreas(body.Areas)
	if !ok || len(body.Areas) == 0 {
		apierr.BadRequest(w, "Elige qué publicar", apierr.FieldError{Field: "areas", Code: "invalid", Message: "Elige al menos un área: catalog, prices, stock, promotions, design, settings o tags"})
		return
	}
	parent, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	if sb.Status != "ready" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_ready", Detail: "El sandbox aún no está listo"})
		return
	}
	// The person publishes in production with the permissions they have there.
	if pp, ok := s.memberIn(r.Context(), parent, pr); !ok || !pp.Permissions.Allows("settings", auth.Admin) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Publicar a producción requiere permiso de administrador en la tienda de producción"})
		return
	}
	out := map[string]any{}
	applied := 0
	if len(tableAreas) > 0 {
		res, err := s.DB.PublishSandbox(r.Context(), sb.ID, pr.Actor(), tableAreas, body.Products, body.Force, func(res store.PublishResult) error {
			return s.copyPublishedFiles(r.Context(), sb.ID, parent, res)
		})
		switch {
		case errors.Is(err, store.ErrNothingToPublish):
			out["publish"] = nil
		case err != nil:
			if strings.Contains(err.Error(), "duplicate key") {
				apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "unique_conflict",
					Detail: "Producción ya tiene un elemento con el mismo identificador o dirección (por ejemplo, el mismo handle). Cámbialo en el sandbox y publica de nuevo. Nada se publicó."})
				return
			}
			internalError(w, s.Log, err)
			return
		default:
			out["publish"] = res
			applied += res.Applied
		}
	}
	if tags {
		ch, err := s.tagsDiff(r.Context(), parent, sb.ID)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		if len(ch) > 0 {
			cfg, err := s.loadTags(r.Context(), sb.ID)
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
			c, err := s.saveTagsVersion(r.Context(), parent, pr, cfg, 0, "Publicado desde el sandbox")
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
			out["tags"] = map[string]any{"version": c.Version, "changes": c.Changes}
			applied++
		}
	}
	if applied == 0 {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "nothing_to_publish", Detail: "No hay cambios del sandbox para publicar en lo que elegiste"})
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// memberIn returns the person's principal in another store of the pair.
func (s *Server) memberIn(ctx context.Context, tenant string, pr auth.Principal) (auth.Principal, bool) {
	if pr.Tenant == tenant {
		return pr, true
	}
	if pr.Kind != "member" {
		return auth.Principal{}, false
	}
	var perms []byte
	out := auth.Principal{Tenant: tenant, Kind: "member", ID: pr.ID}
	var status string
	if err := s.DB.System.QueryRow(ctx, `SELECT is_owner, permissions, status FROM members WHERE tenant_id=$1 AND id=$2`, tenant, pr.ID).Scan(&out.Owner, &perms, &status); err != nil || status != "active" {
		return auth.Principal{}, false
	}
	_ = json.Unmarshal(perms, &out.Permissions)
	if out.Owner {
		out.Permissions = auth.All(auth.Admin)
	}
	return out, true
}

// copyPublishedFiles puts in production the files of media and digital
// files the sandbox added or changed (the ones it inherited are there).
func (s *Server) copyPublishedFiles(ctx context.Context, sandbox, parent string, res store.PublishResult) error {
	cp, ok := s.Media.(media.Copier)
	if !ok {
		if len(res.MediaIDs)+len(res.Digital) == 0 {
			return nil
		}
		return errors.New("the media storage cannot copy files between stores")
	}
	for _, id := range res.MediaIDs {
		if err := cp.CopyPrefix(ctx, sandbox, parent, id); err != nil {
			return err
		}
	}
	for _, id := range res.Digital {
		var key string
		if err := s.DB.System.QueryRow(ctx, `SELECT storage_key FROM digital_files WHERE tenant_id=$1 AND id=$2`, sandbox, id).Scan(&key); err != nil {
			return err
		}
		prefix, _, _ := strings.Cut(key, "/")
		if err := cp.CopyPrefix(ctx, sandbox, parent, prefix); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) listPublishes(w http.ResponseWriter, r *http.Request) {
	parent, _, err := s.pairOf(r.Context(), principal(r).Tenant)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, s.Log, err)
		return
	}
	ps, err := s.DB.Publishes(r.Context(), parent, 50)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if ps == nil {
		ps = []store.Publish{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ps})
}

func (s *Server) revertPublish(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Force bool `json:"force"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	parent := pr.Tenant
	if e := s.envOf(pr.Tenant); e.kind == "sandbox" {
		parent = e.parent
	}
	if pp, ok := s.memberIn(r.Context(), parent, pr); !ok || !pp.Permissions.Allows("settings", auth.Admin) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Revertir requiere permiso de administrador en la tienda de producción"})
		return
	}
	n, changed, err := s.DB.RevertPublish(r.Context(), parent, r.PathValue("id"), pr.Actor(), body.Force)
	var conflict *store.ErrRevertConflict
	switch {
	case errors.As(err, &conflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "changed_since",
			Detail: strconv.Itoa(len(changed)) + " elementos publicados cambiaron en producción después: " + strings.Join(firstN(changed, 10), ", ") + ". Confirma para regresarlos también."})
		return
	case errors.Is(err, store.ErrAlreadyReverted):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "already_reverted", Detail: "Esa publicación ya se revirtió"})
		return
	case s.storeError(w, err, "Publicación no encontrada"):
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"rows": n})
}

func firstN(v []string, n int) []string {
	if len(v) > n {
		return v[:n]
	}
	return v
}

// sandboxMail lists the emails the sandbox captured instead of sending.
func (s *Server) sandboxMail(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	_, sb, err := s.pairOf(r.Context(), pr.Tenant)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	rows, err := s.DB.System.Query(r.Context(), `SELECT id, template, status, created_at FROM notification_messages WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 100`, sb.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	type msg struct {
		ID       string    `json:"id"`
		Template string    `json:"template"`
		Status   string    `json:"status"`
		At       time.Time `json:"createdAt"`
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (msg, error) {
		var m msg
		return m, r.Scan(&m.ID, &m.Template, &m.Status, &m.At)
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if items == nil {
		items = []msg{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

// ---------- lifecycle ----------

const sandboxWarnDays = 25

// RunSandboxLifecycle runs on one node (leased job): a copy interrupted by a
// restart is marked failed, a sandbox nobody used in 25 days warns the
// owners by email, and one unused for 30 days is deleted. A sandbox with
// the store's own subdomain does not expire.
func (s *Server) RunSandboxLifecycle(ctx context.Context) error {
	list, err := s.DB.Sandboxes(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	for _, sb := range list {
		if sb.Status == "copying" {
			// Copies run in the background of one node; a node that stopped
			// mid-copy leaves the status behind. Refreshing starts it again.
			at, _ := sb.Progress["at"].(float64)
			last := sb.CreatedAt
			if at > 0 {
				last = time.Unix(int64(at), 0)
			}
			if now.Sub(last) > 30*time.Minute {
				_ = s.DB.SetSandboxStatus(ctx, sb.ID, "failed", "La copia se interrumpió. Refresca el sandbox para copiarlo de nuevo.", 0)
			}
			continue
		}
		hosts, err := s.sandboxDomains(ctx, sb.ID)
		if err != nil {
			return err
		}
		if s.hasOwnDomain(hosts) {
			continue
		}
		idle := now.Sub(sb.LastUsedAt)
		switch {
		case idle >= sandboxIdleDays*24*time.Hour:
			if err := s.removeSandbox(ctx, sb, "system:expiry"); err != nil {
				s.Log.Error("sandbox expiry", "sandbox", sb.ID, "err", err)
			}
		case idle >= sandboxWarnDays*24*time.Hour && sb.WarnedAt == nil && s.Notify != nil:
			when := sb.LastUsedAt.Add(sandboxIdleDays * 24 * time.Hour)
			rows, err := s.DB.System.Query(ctx, `SELECT email, name FROM members WHERE tenant_id=$1 AND is_owner AND status='active'`, sb.ParentID)
			if err != nil {
				return err
			}
			type owner struct{ email, name string }
			owners, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (owner, error) {
				var o owner
				return o, r.Scan(&o.email, &o.name)
			})
			if err != nil {
				return err
			}
			for _, o := range owners {
				_, err := s.Notify.Enqueue(ctx, sb.ParentID, notify.Message{Template: notify.TplSandboxExpiring, To: o.email, Name: o.name, Force: true,
					DedupeKey: "sandbox-expiring-" + sb.ID + "-" + when.Format("20060102"),
					Data:      notify.Data{Vars: map[string]string{"sandbox.dias": strconv.Itoa(int(idle.Hours() / 24)), "sandbox.vence": shortDate(when)}}})
				if err != nil {
					s.Log.Warn("sandbox expiry notice", "sandbox", sb.ID, "err", err)
				}
			}
			_ = s.DB.MarkSandboxWarned(ctx, sb.ID)
		}
	}
	return nil
}

// WaitSandboxCopies waits for the copies running in the background (tests,
// and a node that is shutting down).
func (s *Server) WaitSandboxCopies() { s.sandboxCopies.Wait() }
