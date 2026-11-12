package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/cxml"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Punchout (cXML 1.2): a company's purchasing system opens the store for its
// buyer, gets the cart back to approve it there, and then sends the purchase
// order, which becomes an order on the company's credit (or a payment link)
// with the PO number. Every call is authenticated with the company's identity
// and shared secret; the start link works once; prices in the purchase order
// must be the ones Apexx would charge.

const punchoutMarker = "apexx_punchout"

type punchoutConfig struct {
	Enabled    bool   `json:"enabled"`
	Identity   string `json:"identity"`
	BuyerEmail string `json:"buyerEmail"`
	HasSecret  bool   `json:"hasSecret"`
	Secret     string `json:"secret,omitempty"` // only right after generating it
	SetupURL   string `json:"setupUrl"`
	OrderURL   string `json:"orderUrl"`
}

func (s *Server) routesPunchout(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/companies/{id}/punchout", s.authed("customers", auth.Read, noStorefront(s.getPunchout)))
	handle("PUT /v1/companies/{id}/punchout", s.authed("customers", auth.Admin, s.putPunchout))
	handle("POST /punchout/{tenant}/setup", s.punchoutSetup)
	handle("POST /punchout/{tenant}/order", s.punchoutOrder)
	handle("GET /s/{tenant}/{site}/punchout/{token}", s.punchoutStart)
	handle("POST /s/{tenant}/api/punchout/transfer", s.punchoutTransfer)
	handle("GET /s/{tenant}/punchout/return", s.punchoutReturn)
}

func secretHash(s string) []byte {
	h := sha256.Sum256([]byte("apexx-punchout|" + s))
	return h[:]
}

func (s *Server) publicOrigin(r *http.Request) string {
	if s.Orders != nil && s.Orders.PublicURL != "" {
		return strings.TrimRight(s.Orders.PublicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || (s.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) loadPunchout(ctx context.Context, tenant, company string) (punchoutConfig, []byte, error) {
	var c punchoutConfig
	var hash []byte
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT enabled, identity, buyer_email, secret_hash FROM company_punchout WHERE tenant_id=$1 AND company_id=$2`, tenant, company).Scan(&c.Enabled, &c.Identity, &c.BuyerEmail, &hash)
	})
	c.HasSecret = len(hash) > 0
	return c, hash, err
}

func (s *Server) getPunchout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.companyExists(w, r, p.Tenant, r.PathValue("id")) {
		return
	}
	c, _, err := s.loadPunchout(r.Context(), p.Tenant, r.PathValue("id"))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, s.Log, err)
		return
	}
	o := s.publicOrigin(r)
	c.SetupURL, c.OrderURL = o+"/punchout/"+p.Tenant+"/setup", o+"/punchout/"+p.Tenant+"/order"
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) putPunchout(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	company := r.PathValue("id")
	var in struct {
		Enabled    bool   `json:"enabled"`
		Identity   string `json:"identity"`
		BuyerEmail string `json:"buyerEmail"`
		Regenerate bool   `json:"regenerate"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Identity, in.BuyerEmail = strings.TrimSpace(in.Identity), strings.ToLower(strings.TrimSpace(in.BuyerEmail))
	if in.Identity == "" || len(in.Identity) > 120 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "identity", Code: "required", Message: "Escribe la identidad (NetworkID o DUNS) del sistema de compras de la empresa"})
		return
	}
	_, buyers, _, err := s.Orders.Company(r.Context(), p.Tenant, company, true)
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Empresa no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	isBuyer := false
	for _, b := range buyers {
		isBuyer = isBuyer || b == in.BuyerEmail
	}
	if !isBuyer {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "buyerEmail", Code: "not_buyer", Message: "Elige a una persona que compre para la empresa"})
		return
	}
	_, old, err := s.loadPunchout(r.Context(), p.Tenant, company)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		internalError(w, s.Log, err)
		return
	}
	secret := ""
	hash := old
	if in.Regenerate || len(old) == 0 {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		secret = hex.EncodeToString(b)
		hash = secretHash(secret)
	}
	err = s.DB.Tx(r.Context(), p.Tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO company_punchout (tenant_id, company_id, identity, secret_hash, buyer_email, enabled) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (tenant_id, company_id) DO UPDATE SET identity=EXCLUDED.identity, secret_hash=EXCLUDED.secret_hash, buyer_email=EXCLUDED.buyer_email, enabled=EXCLUDED.enabled, updated_at=now()`,
			p.Tenant, company, in.Identity, hash, in.BuyerEmail, in.Enabled)
		if isUnique(err) {
			return errIdentityTaken
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, p.Tenant, p.Actor(), "company.punchout", company, map[string]any{"identity": in.Identity, "enabled": in.Enabled, "newSecret": secret != ""})
	})
	if errors.Is(err, errIdentityTaken) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "identity", Code: "taken", Message: "Otra empresa ya usa esa identidad"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	o := s.publicOrigin(r)
	httpx.JSON(w, r, http.StatusOK, punchoutConfig{Enabled: in.Enabled, Identity: in.Identity, BuyerEmail: in.BuyerEmail, HasSecret: true, Secret: secret,
		SetupURL: o + "/punchout/" + p.Tenant + "/setup", OrderURL: o + "/punchout/" + p.Tenant + "/order"})
}

var errIdentityTaken = errors.New("identity taken")

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505") }

func writeCXML(w http.ResponseWriter, status int, b []byte) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// punchoutAuth finds the company of a cXML document and checks its secret.
func (s *Server) punchoutAuth(r *http.Request, d *cxml.Document) (string, punchoutConfig, bool) {
	tenant := r.PathValue("tenant")
	var company string
	var c punchoutConfig
	var hash []byte
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT company_id, enabled, identity, buyer_email, secret_hash FROM company_punchout WHERE tenant_id=$1 AND identity=$2`, tenant, d.FromIdentity()).
			Scan(&company, &c.Enabled, &c.Identity, &c.BuyerEmail, &hash)
	})
	given := secretHash(d.SenderSecret())
	if err != nil || !c.Enabled || subtle.ConstantTimeCompare(given, hash) != 1 {
		return "", c, false
	}
	return company, c, true
}

// allowedReturn accepts https addresses (http only on this machine, for tests).
func allowedReturn(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return false
	}
	if p.Scheme == "https" {
		return true
	}
	h := p.Hostname()
	return p.Scheme == "http" && (h == "localhost" || h == "127.0.0.1")
}

func (s *Server) punchoutSetup(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if _, ok := s.Proj.Tenant(r.PathValue("tenant")); !ok || !accountLimiter.allow(r.PathValue("tenant")+"|po|"+s.clientIP(r)) {
		writeCXML(w, http.StatusNotFound, cxml.Response(host, 404, "Not Found"))
		return
	}
	d, err := cxml.Parse(r.Body)
	if err != nil || d.Request.Setup == nil {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "Bad Request"))
		return
	}
	company, cfg, ok := s.punchoutAuth(r, d)
	if !ok {
		writeCXML(w, http.StatusUnauthorized, cxml.Response(host, 401, "Unauthorized"))
		return
	}
	setup := d.Request.Setup
	if !allowedReturn(setup.BrowserPost) || setup.BuyerCookie == "" || len(setup.BuyerCookie) > 500 {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "BrowserFormPost URL must be https"))
		return
	}
	tenant := r.PathValue("tenant")
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token := "pos_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(token))
	id := store.NewID("pun_")
	err = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO punchout_sessions (tenant_id, id, company_id, start_hash, buyer_cookie, return_url, expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			tenant, id, company, sum[:], setup.BuyerCookie, setup.BrowserPost, time.Now().Add(time.Hour)); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, tenant, "punchout:"+cfg.Identity, "punchout.setup", company, map[string]any{"session": id, "operation": setup.Operation})
	})
	if err != nil {
		writeCXML(w, http.StatusInternalServerError, cxml.Response(host, 500, "Internal Server Error"))
		return
	}
	writeCXML(w, http.StatusOK, cxml.SetupResponse(host, s.publicOrigin(r)+"/s/"+tenant+"/main/punchout/"+token))
}

func (s *Server) punchoutStart(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	w.Header().Set("Cache-Control", "no-store")
	sum := sha256.Sum256([]byte(r.PathValue("token")))
	var id, company string
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `UPDATE punchout_sessions SET started_at=now() WHERE tenant_id=$1 AND start_hash=$2 AND started_at IS NULL AND expires_at > now() RETURNING id, company_id`,
			tenant, sum[:]).Scan(&id, &company)
	})
	if err != nil {
		http.Error(w, "El enlace ya se usó o venció. Vuelve a entrar desde tu sistema de compras.", http.StatusGone)
		return
	}
	cfg, _, err := s.loadPunchout(r.Context(), tenant, company)
	if err != nil {
		http.Error(w, "Punchout no disponible", http.StatusGone)
		return
	}
	session, err := s.Customers.PunchoutSession(r.Context(), tenant, cfg.BuyerEmail, id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.setShopperCookie(w, tenant, session, 3600)
	http.SetCookie(w, &http.Cookie{Name: punchoutMarker, Value: "1", Path: "/s/" + tenant, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 3600})
	http.Redirect(w, r, "/s/"+tenant+"/"+r.PathValue("site")+"/index", http.StatusSeeOther)
}

// punchoutTransfer prices the cart for the company and returns the
// PunchOutOrderMessage the browser posts back to the purchasing system.
func (s *Server) punchoutTransfer(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	ck, err := r.Cookie(shopperCookie)
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "La sesión de compra terminó"})
		return
	}
	pid, err := s.Customers.SessionPunchout(r.Context(), tenant, ck.Value)
	if err != nil || pid == "" {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "not_punchout", Detail: "Esta sesión no viene de un sistema de compras"})
		return
	}
	var in struct {
		Channel string      `json:"channel"`
		Items   []cart.Item `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	if errs := validateItems("mx", in.Items); len(errs) > 0 || len(in.Items) == 0 {
		apierr.BadRequest(w, "El carrito está vacío o tiene errores", errs...)
		return
	}
	var cookie, company string
	if err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT buyer_cookie, company_id FROM punchout_sessions WHERE tenant_id=$1 AND id=$2 AND expires_at > now()`, tenant, pid).Scan(&cookie, &company)
	}); err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusGone, Code: "expired", Detail: "La sesión de compra venció; vuelve a entrar desde tu sistema"})
		return
	}
	lines, cur, err := s.companyPrices(r.Context(), tenant, company, in.Items)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg, _, _ := s.loadPunchout(r.Context(), tenant, company)
	msg := cxml.OrderMessage(r.Host, cookie, cur, lines, tenant, cfg.Identity)
	if err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE punchout_sessions SET message=$3, transferred_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, pid, string(msg))
		return err
	}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"next": "/s/" + tenant + "/punchout/return", "lines": len(lines)})
}

// The return page posts the cart to the purchasing system. The store's
// pages only post to the store itself, so this page carries its own policy
// that allows exactly the purchasing system's address and nothing else.
const punchoutReturnJS = `document.getElementById("po").submit();`

func (s *Server) punchoutReturn(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	ck, err := r.Cookie(shopperCookie)
	var pid string
	if err == nil {
		pid, err = s.Customers.SessionPunchout(r.Context(), tenant, ck.Value)
	}
	var ret, msg string
	if err == nil && pid != "" {
		err = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(r.Context(), `SELECT return_url, message FROM punchout_sessions WHERE tenant_id=$1 AND id=$2 AND expires_at > now() AND message <> ''`, tenant, pid).Scan(&ret, &msg)
		})
	}
	if err != nil || pid == "" || !allowedReturn(ret) {
		http.Error(w, "No hay un carrito para regresar a tu sistema de compras.", http.StatusNotFound)
		return
	}
	u, _ := url.Parse(ret)
	sum := sha256.Sum256([]byte(punchoutReturnJS))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'; form-action "+u.Scheme+"://"+u.Host+"; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="es"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Regresando a tu sistema de compras</title>
<body style="font:16px system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 16px;text-align:center">
<form id="po" method="POST" action="%s"><input type="hidden" name="cxml-urlencoded" value="%s">
<p>Enviando tu carrito a tu sistema de compras…</p><button type="submit" style="font:inherit;padding:.6em 1.2em">Continuar</button></form>
<script>%s</script></body></html>`, html.EscapeString(ret), html.EscapeString(msg), punchoutReturnJS)
}

// companyPrices prices items as the company buys them (its list and discount).
func (s *Server) companyPrices(ctx context.Context, tenant, company string, items []cart.Item) ([]cxml.CartLine, string, error) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return nil, "", orders.ErrNotFound
	}
	acc, err := s.Orders.CompanyAccessByID(ctx, tenant, company)
	if err != nil {
		return nil, "", err
	}
	res, err := t.Simulate(cart.Request{Channel: mainChannel, Items: items, Extra: orders.CompanyPromo(acc), Group: orders.CompanyGroup(acc), RestrictGroup: acc.CatalogRestricted, TaxExempt: acc.TaxExempt})
	if err != nil {
		return nil, "", err
	}
	if len(res.Lines) != len(items) {
		return nil, "", orders.FieldError{Field: "items", Message: "Algún producto no está disponible para la empresa"}
	}
	out := make([]cxml.CartLine, 0, len(res.Lines))
	for _, l := range res.Lines {
		net := l.Total.Amount
		if l.Discount != nil {
			net -= l.Discount.Amount
		}
		unit := (net + max(l.Qty, 1)/2) / max(l.Qty, 1)
		out = append(out, cxml.CartLine{SKU: l.SKU, Description: l.Title, Qty: l.Qty, Unit: unit})
	}
	return out, string(res.Totals.Grand.Currency), nil
}

// punchoutOrder turns the purchase order of the purchasing system into an
// order, once per PO number.
func (s *Server) punchoutOrder(w http.ResponseWriter, r *http.Request) {
	host, tenant := r.Host, r.PathValue("tenant")
	if _, ok := s.Proj.Tenant(tenant); !ok {
		writeCXML(w, http.StatusNotFound, cxml.Response(host, 404, "Not Found"))
		return
	}
	d, err := cxml.Parse(r.Body)
	if err != nil || d.Request.Order == nil {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "Bad Request"))
		return
	}
	company, cfg, ok := s.punchoutAuth(r, d)
	if !ok {
		writeCXML(w, http.StatusUnauthorized, cxml.Response(host, 401, "Unauthorized"))
		return
	}
	po := strings.TrimSpace(d.Request.Order.Header.OrderID)
	if po == "" || len(po) > 60 || len(d.Request.Order.Items) == 0 || len(d.Request.Order.Items) > 500 {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "OrderID and items are required"))
		return
	}
	var dup bool
	_ = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM punchout_orders WHERE tenant_id=$1 AND company_id=$2 AND po=$3)`, tenant, company, po).Scan(&dup)
	})
	if dup {
		writeCXML(w, http.StatusOK, cxml.Response(host, 200, "OK (already received)"))
		return
	}
	var items []cart.Item
	asked := map[string]int64{}
	for _, it := range d.Request.Order.Items {
		q, err := strconv.ParseInt(strings.TrimSpace(it.Quantity), 10, 64)
		unit, ok := cxml.Minor(it.UnitPrice.Value)
		if err != nil || q <= 0 || q > 100000 || !ok || it.SupplierPart == "" {
			writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "Invalid item "+it.SupplierPart))
			return
		}
		items = append(items, cart.Item{SKU: it.SupplierPart, Qty: q})
		asked[it.SupplierPart] = unit
	}
	lines, _, err := s.companyPrices(r.Context(), tenant, company, items)
	if err != nil {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "Items not available"))
		return
	}
	var draftItems []orders.ItemInput
	for _, l := range lines {
		if diff := asked[l.SKU] - l.Unit; diff > 1 || diff < -1 {
			writeCXML(w, http.StatusConflict, cxml.Response(host, 409, "Price changed for "+l.SKU+"; punch out again to refresh the cart"))
			return
		}
		unit := l.Unit
		draftItems = append(draftItems, orders.ItemInput{SKU: l.SKU, Qty: l.Qty, Unit: &unit})
	}
	acc, err := s.Orders.CompanyAccessByID(r.Context(), tenant, company)
	if err != nil {
		writeCXML(w, http.StatusConflict, cxml.Response(host, 409, "Company not available"))
		return
	}
	hdr := d.Request.Order.Header
	addr := strings.Trim(strings.Join([]string{hdr.ShipTo, hdr.Street, hdr.City}, ", "), ", ")
	actor := "punchout:" + cfg.Identity
	dr, err := s.Orders.SaveDraft(r.Context(), tenant, actor, "", orders.DraftInput{Channel: mainChannel, Items: draftItems, PostalCode: hdr.Postal,
		Customer: orders.Customer{Email: cfg.BuyerEmail, Name: acc.Name, Address: addr}, Note: "Orden de compra " + po + " (punchout)"})
	if err != nil {
		writeCXML(w, http.StatusBadRequest, cxml.Response(host, 400, "Order could not be prepared"))
		return
	}
	if err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `INSERT INTO punchout_orders (tenant_id, company_id, po, draft_id) VALUES ($1,$2,$3,$4)`, tenant, company, po, dr.ID)
		return err
	}); err != nil {
		_ = s.Orders.CancelDraft(context.Background(), tenant, actor, dr.ID)
		writeCXML(w, http.StatusOK, cxml.Response(host, 200, "OK (already received)"))
		return
	}
	if acc.Terms != "prepaid" && acc.AllowsPayment("credit") {
		o, err := s.Orders.CompleteDraftOnCredit(r.Context(), tenant, actor, dr.ID, acc)
		if err != nil {
			writeCXML(w, http.StatusConflict, cxml.Response(host, 409, "Order on credit refused: "+err.Error()))
			return
		}
		_ = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(r.Context(), `UPDATE orders SET po_number=$3 WHERE tenant_id=$1 AND id=$2`, tenant, o.ID, po)
			return err
		})
	} else if _, err := s.Orders.SendDraftLink(r.Context(), tenant, actor, dr.ID); err != nil {
		writeCXML(w, http.StatusInternalServerError, cxml.Response(host, 500, "Internal Server Error"))
		return
	}
	writeCXML(w, http.StatusOK, cxml.Response(host, 200, "OK"))
}
