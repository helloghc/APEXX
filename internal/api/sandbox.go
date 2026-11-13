package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
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
	name    string // sandbox: its name
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
		} else if sb, err := s.DB.SandboxByID(ctx, tenant); err == nil {
			e.name = sb.Name
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
	handle("GET /v1/sandbox/schedules", s.authed("settings", auth.Read, s.listSchedules))
	handle("PUT /v1/sandbox/password", s.authed("settings", auth.Admin, s.setSandboxPassword))
	handle("PUT /v1/sandboxes/{id}/password", s.authed("settings", auth.Admin, s.setSandboxPassword))
	handle("DELETE /v1/sandbox/schedules/{id}", s.authed("settings", auth.Admin, s.cancelSchedule))

	// Several sandboxes per store (as many as the plan includes): the same
	// actions for one sandbox by its id.
	handle("GET /v1/sandboxes", s.authed("settings", auth.Read, s.listSandboxes))
	handle("POST /v1/sandboxes", s.authed("settings", auth.Admin, s.createSandbox))
	handle("GET /v1/sandboxes/{id}", s.authed("settings", auth.Read, s.getSandbox))
	handle("PATCH /v1/sandboxes/{id}", s.authed("settings", auth.Admin, s.renameSandbox))
	handle("DELETE /v1/sandboxes/{id}", s.authed("settings", auth.Admin, s.deleteSandbox))
	handle("POST /v1/sandboxes/{id}/refresh", s.authed("settings", auth.Admin, s.refreshSandbox))
	handle("POST /v1/sandboxes/{id}/enter", s.authed("", "", s.enterSandbox))
	handle("POST /v1/sandboxes/{id}/preview", s.authed("settings", auth.Read, s.previewSandbox))
	handle("GET /v1/sandboxes/{id}/diff", s.authed("settings", auth.Read, s.sandboxDiff))
	handle("POST /v1/sandboxes/{id}/publish", s.authed("settings", auth.Admin, s.publishSandbox))
	handle("GET /v1/sandboxes/{id}/mail", s.authed("settings", auth.Read, s.sandboxMail))
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

// targetSandbox resolves the production store and the sandbox a request is
// about: the {id} in the path (a sandbox of the same store), or else the
// sandbox the person is in, or else the store's first sandbox.
func (s *Server) targetSandbox(r *http.Request) (parent string, sb store.Sandbox, err error) {
	tenant := principal(r).Tenant
	parent = tenant
	e := s.envOf(tenant)
	if e.kind == "sandbox" {
		parent = e.parent
	}
	switch id := r.PathValue("id"); {
	case id != "":
		sb, err = s.DB.SandboxByID(r.Context(), id)
		if err == nil && sb.ParentID != parent {
			return parent, store.Sandbox{}, store.ErrNotFound // another store's sandbox does not exist here
		}
	case e.kind == "sandbox":
		sb, err = s.DB.SandboxByID(r.Context(), tenant)
	default:
		sb, err = s.DB.SandboxOf(r.Context(), parent)
	}
	return parent, sb, err
}

// parentOf is the production store of a request (itself, or the sandbox's).
func (s *Server) parentOf(tenant string) string {
	if e := s.envOf(tenant); e.kind == "sandbox" {
		return e.parent
	}
	return tenant
}

const sandboxIdleDays = 30

func sandboxAreas() []map[string]any {
	var out []map[string]any
	for _, a := range store.SandboxAreas {
		out = append(out, map[string]any{"id": a.ID, "label": a.Label})
	}
	return append(out, map[string]any{"id": "tags", "label": "Etiquetas y píxeles"})
}

// fill completes a view with a sandbox's address, domains and expiry.
func (s *Server) fill(r *http.Request, v *sandboxView, sb store.Sandbox) {
	v.Sandbox = &sb
	v.URL = s.sandboxURL(r, sb.ID)
	hosts, _ := s.sandboxDomains(r.Context(), sb.ID)
	v.Domains = hosts
	if !s.hasOwnDomain(hosts) {
		exp := sb.LastUsedAt.Add(sandboxIdleDays * 24 * time.Hour)
		v.ExpiresAt = &exp
	}
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	e := s.envOf(pr.Tenant)
	v := sandboxView{Env: e.kind, Parent: e.parent, Domains: []string{}, Areas: sandboxAreas()}
	_, sb, err := s.targetSandbox(r)
	if errors.Is(err, store.ErrNotFound) && r.PathValue("id") != "" {
		apierr.NotFound(w, "Sandbox no encontrado")
		return
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		internalError(w, s.Log, err)
		return
	}
	if err == nil {
		s.fill(r, &v, sb)
	}
	httpx.JSON(w, r, http.StatusOK, v)
}

// listSandboxes lists the store's sandboxes with what its plan includes.
func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	e := s.envOf(pr.Tenant)
	parent := s.parentOf(pr.Tenant)
	list, err := s.DB.SandboxesOf(r.Context(), parent)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	plan, err := s.DB.TenantPlan(r.Context(), parent)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	items := make([]sandboxView, 0, len(list))
	for _, sb := range list {
		v := sandboxView{Env: e.kind, Parent: e.parent, Domains: []string{}}
		s.fill(r, &v, sb)
		items = append(items, v)
	}
	current := ""
	if e.kind == "sandbox" {
		current = pr.Tenant
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"env": e.kind, "parent": e.parent, "current": current, "items": items,
		"plan": plan, "used": len(list), "areas": sandboxAreas()})
}

// renameSandbox changes a sandbox's name.
func (s *Server) renameSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	name, ok := sandboxName(w, body.Name)
	if !ok {
		return
	}
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Sandbox no encontrado") {
		return
	}
	if err := s.DB.RenameSandbox(r.Context(), sb.ID, name); err != nil {
		if errors.Is(err, store.ErrSandboxExists) {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "sandbox_exists", Detail: "Ya tienes un sandbox con ese nombre"})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.rename", sb.ID, map[string]any{"from": sb.Name, "to": name})
	})
	sb.Name = name
	s.forgetEnv(sb.ID)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"sandbox": sb})
}

// sandboxName checks a sandbox's name (1 to 60 characters).
func sandboxName(w http.ResponseWriter, raw string) (string, bool) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" || len([]rune(name)) > 60 {
		apierr.BadRequest(w, "Revisa el nombre", apierr.FieldError{Field: "name", Code: "invalid", Message: "Escribe un nombre de hasta 60 caracteres"})
		return "", false
	}
	return name, true
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

// planRoom checks that a copy of production fits the plan's product limit.
func (s *Server) planRoom(ctx context.Context, parent string) error {
	plan, err := s.DB.TenantPlan(ctx, parent)
	if err != nil || plan.SandboxMaxProducts == 0 {
		return err
	}
	var n int
	if err := s.DB.System.QueryRow(ctx, `SELECT count(*) FROM products WHERE tenant_id=$1`, parent).Scan(&n); err != nil {
		return err
	}
	if n > plan.SandboxMaxProducts {
		return &store.ErrSandboxQuota{What: "products", Plan: plan, Count: n}
	}
	return nil
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Name     string `json:"name"`
		DataMode string `json:"dataMode"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	if body.Name == "" {
		body.Name = "Sandbox"
	}
	name, ok := sandboxName(w, body.Name)
	if !ok {
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
	if err := s.DB.StartSandbox(r.Context(), pr.Tenant, id, name, pr.Actor(), body.DataMode, wrapped); err != nil {
		if errors.Is(err, store.ErrSandboxExists) {
			apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "sandbox_exists", Detail: "Ya tienes un sandbox llamado «" + name + "». Usa otro nombre, o refréscalo o bórralo."})
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
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "sandbox.create", id, map[string]any{"dataMode": body.DataMode, "name": name})
	})
	s.forgetEnv(pr.Tenant)
	s.runSandboxCopy(id, pr.Tenant, body.DataMode, false)
	sb, _ := s.DB.SandboxByID(r.Context(), id)
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"sandbox": sb, "url": s.sandboxURL(r, id)})
}

// runSandboxCopy copies production into the sandbox in the background (a
// 50 000-product store takes seconds); the panel shows the progress.
func (s *Server) runSandboxCopy(id, parent, dataMode string, refresh bool) {
	if !s.sbxWork.start(id, "copy") {
		_ = s.DB.SetSandboxStatus(context.Background(), id, "failed", "El servidor se estaba reiniciando. Refresca el sandbox para copiarlo.", 0)
		return
	}
	s.sandboxCopies.Add(1)
	go func() {
		defer s.sandboxCopies.Done()
		defer s.sbxWork.done(id)
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
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	if err := s.planRoom(r.Context(), parent); err != nil {
		internalError(w, s.Log, err)
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
	_, sb, err := s.targetSandbox(r)
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
	s.sbxStats.forget(sb.ID)
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
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	if sb.ID == pr.Tenant {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "in_sandbox", Detail: "Ya estás en este sandbox"})
		return
	}
	// From a sandbox to another one of the same store: the person must
	// still be in production's team.
	if _, ok := s.memberIn(r.Context(), parent, pr); !ok {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_member", Detail: "No formas parte del equipo de esa tienda"})
		return
	}
	if sb.Status != "ready" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_ready", Detail: "El sandbox aún no está listo"})
		return
	}
	if err := s.DB.SyncSandboxMembers(r.Context(), parent, sb.ID); err != nil {
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
	_, sb, err := s.targetSandbox(r)
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
	return s.signSandbox(ctx, id, exp, "")
}

// signSandbox signs a token; pw (the password's fingerprint) ties a token
// given for the password to that password: changing it closes the door.
func (s *Server) signSandbox(ctx context.Context, id string, exp time.Time, pw string) (string, error) {
	if err := s.ensureKey(ctx, id); err != nil {
		return "", err
	}
	payload := id + "|" + strconv.FormatInt(exp.Unix(), 10)
	if pw != "" {
		payload += "|" + pw
	}
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
	ts, pw, withPassword := strings.Cut(ts, "|")
	if withPassword {
		h, err := s.DB.SandboxPasswordHash(ctx, id)
		if err != nil || h == "" || passwordPrint(h) != pw {
			return time.Time{}, false
		}
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
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			code := sw.code
			if code == 0 {
				code = http.StatusOK
			}
			s.sbxStats.add(tenant, code)
		}()
		w = sw
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
		page := privatePage{}
		if hash, err := s.DB.SandboxPasswordHash(r.Context(), tenant); err == nil && hash != "" {
			page.Password = true
			if r.Method == http.MethodPost {
				if s.sandboxPasswordLogin(w, r, tenant, hash) {
					return
				}
				page.Wrong = true
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_ = sandboxPrivatePage.Execute(w, page)
	})
}

type privatePage struct{ Password, Wrong bool }

var (
	passwordIPLimiter  = &shopLimiter{n: 10} // attempts per IP and sandbox per minute
	passwordAllLimiter = &shopLimiter{n: 60} // attempts per sandbox per minute, from anywhere
)

// passwordPrint is a short fingerprint of the password's hash (never the
// hash itself) that a token carries.
func passwordPrint(hash string) string {
	sum := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(sum[:6])
}

// sandboxPasswordLogin checks the storefront password; right, it leaves a
// cookie for 12 hours and sends the person on to the page they asked for.
func (s *Server) sandboxPasswordLogin(w http.ResponseWriter, r *http.Request, tenant, hash string) bool {
	if !passwordIPLimiter.allow(tenant+"|"+s.clientIP(r)) || !passwordAllLimiter.allow(tenant) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Demasiados intentos. Espera un minuto.", http.StatusTooManyRequests)
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !auth.CheckPassword(hash, r.PostFormValue("sbx_password")) {
		return false
	}
	exp := time.Now().Add(12 * time.Hour)
	tok, err := s.signSandbox(r.Context(), tenant, exp, passwordPrint(hash))
	if err != nil {
		internalError(w, s.Log, err)
		return true
	}
	http.SetCookie(w, &http.Cookie{Name: previewCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, Expires: exp})
	u := *r.URL
	if customHost(r) {
		u.Path = strings.TrimPrefix(u.Path, "/s/"+tenant+"/main")
	}
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
	return true
}

// setSandboxPassword sets or removes the storefront password.
func (s *Server) setSandboxPassword(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Password != "" && (len([]rune(body.Password)) < 8 || len(body.Password) > 128) {
		apierr.BadRequest(w, "Revisa la contraseña", apierr.FieldError{Field: "password", Code: "invalid", Message: "Usa de 8 a 128 caracteres, o déjala vacía para quitarla"})
		return
	}
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Tu tienda aún no tiene sandbox") {
		return
	}
	hash := ""
	if body.Password != "" {
		if hash, err = auth.HashPassword(body.Password); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	if err := s.DB.SetSandboxPassword(r.Context(), sb.ID, hash); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.password", sb.ID, map[string]any{"set": hash != ""})
	})
	httpx.JSON(w, r, http.StatusOK, map[string]any{"hasPassword": hash != ""})
}

var sandboxPrivatePage = template.Must(template.New("p").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Sandbox privado</title>
<style>body{font:16px/1.5 system-ui,sans-serif;margin:0;display:grid;place-items:center;min-height:100vh;background:#f6f6f2;color:#1d2026}main{max-width:32rem;padding:24px;box-sizing:border-box;width:100%}h1{font-size:1.5rem;margin:0 0 8px}
form{display:grid;gap:8px;margin-top:16px}label{font-weight:600}input{font:inherit;padding:10px 12px;border:1px solid #8a8f98;border-radius:8px}input:focus-visible,button:focus-visible{outline:3px solid #1d4ed8;outline-offset:2px}
button{font:inherit;font-weight:600;padding:10px 16px;border:0;border-radius:8px;background:#1d2026;color:#fff;cursor:pointer}.err{color:#b42318;margin:0}</style></head>
<body><main><h1>Este sandbox es privado</h1>{{if .Password}}<p>Escribe la contraseña que te dio el equipo de la tienda.</p>
<form method="post"><label for="sbx_password">Contraseña</label><input id="sbx_password" name="sbx_password" type="password" autocomplete="current-password" required autofocus{{if .Wrong}} aria-invalid="true" aria-describedby="sbx_err"{{end}}>
{{if .Wrong}}<p class="err" id="sbx_err" role="alert">La contraseña no es correcta.</p>{{end}}<button type="submit">Entrar</button></form>
{{else}}<p>Ábrelo desde el panel de tu tienda, en Configuración › Sandbox, o con un enlace de vista previa que te haya compartido el equipo de la tienda.</p>{{end}}</main></body></html>`))

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
	parent, sb, err := s.targetSandbox(r)
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
	ctx, cancel := longSandboxCtx(r)
	defer cancel()
	diffs, err := s.DB.SandboxDiff(ctx, sb.ID, tableAreas, splitList(r.URL.Query().Get("products")))
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
		Areas    []string   `json:"areas"`
		Products []string   `json:"products"`
		Force    bool       `json:"force"`
		RunAt    *time.Time `json:"runAt"` // set: publish then, not now
	}
	if !decode(w, r, &body) {
		return
	}
	tableAreas, tags, ok := validAreas(body.Areas)
	if !ok || len(body.Areas) == 0 {
		apierr.BadRequest(w, "Elige qué publicar", apierr.FieldError{Field: "areas", Code: "invalid", Message: "Elige al menos un área: catalog, prices, stock, promotions, design, settings o tags"})
		return
	}
	parent, sb, err := s.targetSandbox(r)
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
	if body.RunAt != nil {
		s.schedulePublish(w, r, parent, sb, body.Areas, body.Products, body.Force, *body.RunAt)
		return
	}
	ctx, cancel := longSandboxCtx(r)
	defer cancel()
	out, applied, err := s.doPublish(ctx, sb, parent, pr, tableAreas, tags, body.Products, body.Force)
	switch {
	case errors.Is(err, errUniqueConflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "unique_conflict", Detail: uniqueConflictText})
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	case applied == 0:
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "nothing_to_publish", Detail: "No hay cambios del sandbox para publicar en lo que elegiste"})
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// longSandboxCtx is the context of a publish, revert or diff: one
// transaction over a whole catalog (seconds at 50 000 products), so it has
// minutes instead of a request's 15 s, and ends whole even if the browser
// closes (the transaction commits or rolls back, never half).
func longSandboxCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
}

var errUniqueConflict = errors.New("api: production has an item with the same identifier")

const uniqueConflictText = "Producción ya tiene un elemento con el mismo identificador o dirección (por ejemplo, el mismo handle). Cámbialo en el sandbox y publica de nuevo. Nada se publicó."

// doPublish publishes what the sandbox changed in the chosen areas; pr is
// the person in production. applied counts what changed (0: nothing).
func (s *Server) doPublish(ctx context.Context, sb store.Sandbox, parent string, pr auth.Principal, tableAreas []string, tags bool, products []string, force bool) (map[string]any, int, error) {
	out := map[string]any{}
	applied := 0
	if len(tableAreas) > 0 {
		res, err := s.DB.PublishSandbox(ctx, sb.ID, pr.Actor(), tableAreas, products, force, func(res store.PublishResult) error {
			return s.copyPublishedFiles(ctx, sb.ID, parent, res)
		})
		switch {
		case errors.Is(err, store.ErrNothingToPublish):
			out["publish"] = nil
		case err != nil:
			if strings.Contains(err.Error(), "duplicate key") {
				if s.Log != nil {
					s.Log.Warn("sandbox publish: unique conflict", "sandbox", sb.ID, "err", err)
				}
				return nil, 0, errUniqueConflict
			}
			return nil, 0, err
		default:
			out["publish"] = res
			applied += res.Applied
		}
	}
	if tags {
		ch, err := s.tagsDiff(ctx, parent, sb.ID)
		if err != nil {
			return nil, 0, err
		}
		if len(ch) > 0 {
			cfg, err := s.loadTags(ctx, sb.ID)
			if err != nil {
				return nil, 0, err
			}
			c, err := s.saveTagsVersion(ctx, parent, pr, cfg, 0, "Publicado desde el sandbox")
			if err != nil {
				return nil, 0, err
			}
			out["tags"] = map[string]any{"version": c.Version, "changes": c.Changes}
			applied++
		}
	}
	return out, applied, nil
}

// ---------- scheduled publishes ----------

// schedulePublish sets a publish for later. What travels is the sandbox as
// it is then; permissions are checked now and again when it runs.
func (s *Server) schedulePublish(w http.ResponseWriter, r *http.Request, parent string, sb store.Sandbox, areas, products []string, force bool, at time.Time) {
	pr := principal(r)
	if pr.Kind != "member" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "members_only", Detail: "Solo las personas del equipo programan publicaciones"})
		return
	}
	if at.Before(time.Now().Add(time.Minute)) || at.After(time.Now().Add(90*24*time.Hour)) {
		apierr.BadRequest(w, "Revisa la fecha", apierr.FieldError{Field: "runAt", Code: "invalid", Message: "Elige una fecha entre un minuto y 90 días a partir de ahora"})
		return
	}
	x, err := s.DB.CreateSchedule(r.Context(), store.Schedule{ParentID: parent, SandboxID: sb.ID, Areas: areas, Products: products, Force: force, RunAt: at.UTC(), CreatedBy: pr.Actor()})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.schedule", x.ID, map[string]any{"sandbox": sb.ID, "areas": areas, "runAt": x.RunAt})
	})
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"schedule": x})
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	list, err := s.DB.Schedules(r.Context(), s.parentOf(principal(r).Tenant), 50)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) cancelSchedule(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	parent := s.parentOf(pr.Tenant)
	if pp, ok := s.memberIn(r.Context(), parent, pr); !ok || !pp.Permissions.Allows("settings", auth.Admin) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Cancelar requiere permiso de administrador en la tienda de producción"})
		return
	}
	err := s.DB.CancelSchedule(r.Context(), parent, r.PathValue("id"))
	if errors.Is(err, store.ErrScheduleDone) {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "already_ran", Detail: "Esa publicación ya se ejecutó o se canceló"})
		return
	}
	if s.storeError(w, err, "Publicación programada no encontrada") {
		return
	}
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.schedule_cancel", r.PathValue("id"), map[string]any{})
	})
	w.WriteHeader(http.StatusNoContent)
}

// RunSandboxSchedules publishes what is due (every minute; each publish is
// claimed by one node). The person who scheduled it must still be an
// administrator of production; the sandbox must be ready.
func (s *Server) RunSandboxSchedules(ctx context.Context) error {
	if err := s.DB.FailStuckSchedules(ctx); err != nil {
		return err
	}
	s.sbxWork.mu.Lock()
	closed := s.sbxWork.closed
	s.sbxWork.mu.Unlock()
	if closed {
		return nil // shutting down: another node takes them
	}
	due, err := s.DB.ClaimDueSchedules(ctx, 20)
	if err != nil {
		return err
	}
	// Claimed: finish even if the node starts shutting down meanwhile (the
	// node waits for it before exiting).
	ctx = context.WithoutCancel(ctx)
	for _, x := range due {
		if !s.sbxWork.start(x.ID, "schedule") {
			_ = s.DB.FinishSchedule(ctx, x.ID, "pending", "", "")
			continue
		}
		status, publishID, result := s.runSchedule(ctx, x)
		err := s.DB.FinishSchedule(ctx, x.ID, status, publishID, result)
		s.sbxWork.done(x.ID)
		if err != nil {
			return err
		}
		_ = s.DB.Tx(ctx, x.ParentID, func(tx pgx.Tx) error {
			return store.Audit(ctx, tx, x.ParentID, x.CreatedBy, "sandbox.schedule_run", x.ID, map[string]any{"status": status, "publish": publishID, "result": result})
		})
	}
	return nil
}

func (s *Server) runSchedule(ctx context.Context, x store.Schedule) (status, publishID, result string) {
	sb, err := s.DB.SandboxByID(ctx, x.SandboxID)
	if err != nil {
		return "failed", "", "El sandbox ya no existe"
	}
	if sb.Status != "ready" {
		return "failed", "", "El sandbox no estaba listo a la hora programada (se estaba copiando o falló)"
	}
	kind, id, _ := strings.Cut(x.CreatedBy, ":")
	pp, ok := s.memberIn(ctx, x.ParentID, auth.Principal{Tenant: sb.ID, Kind: kind, ID: id})
	if !ok || !pp.Permissions.Allows("settings", auth.Admin) {
		return "failed", "", "Quien la programó ya no es administrador de la tienda de producción"
	}
	tableAreas, tags, ok := validAreas(x.Areas)
	if !ok {
		return "failed", "", "Área desconocida"
	}
	out, applied, err := s.doPublish(ctx, sb, x.ParentID, pp, tableAreas, tags, x.Products, x.Force)
	switch {
	case errors.Is(err, errUniqueConflict):
		return "failed", "", uniqueConflictText
	case err != nil:
		s.Log.Error("scheduled publish", "schedule", x.ID, "err", err)
		return "failed", "", "La publicación no terminó; nada se publicó. Inténtalo de nuevo."
	case applied == 0:
		return "done", "", "No había cambios que publicar"
	}
	if res, ok := out["publish"].(store.PublishResult); ok {
		publishID = res.ID
		return "done", publishID, strconv.Itoa(res.Applied) + " cambios publicados"
	}
	return "done", "", "Etiquetas publicadas"
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
	parent := s.parentOf(principal(r).Tenant)
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
	parent := s.parentOf(pr.Tenant)
	if pp, ok := s.memberIn(r.Context(), parent, pr); !ok || !pp.Permissions.Allows("settings", auth.Admin) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Revertir requiere permiso de administrador en la tienda de producción"})
		return
	}
	ctx, cancel := longSandboxCtx(r)
	defer cancel()
	n, changed, err := s.DB.RevertPublish(ctx, parent, r.PathValue("id"), pr.Actor(), body.Force)
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
	_, sb, err := s.targetSandbox(r)
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
