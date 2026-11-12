package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
)

// Shopper account on the storefront: sign in with a one-time code sent by
// email (or an optional password), order history with tracking and invoice,
// addresses and profile. Pages are server-rendered forms that work without
// JavaScript; the same actions exist as JSON for headless storefronts.

var accountPages = localizedPages("account", "pay")

var (
	codeLimiter       = &shopLimiter{n: 8}   // code requests per IP per minute
	verifyLimiter     = &shopLimiter{n: 20}  // code / password tries per IP per minute
	newsletterLimiter = &shopLimiter{n: 10}  //
	trackLimiter      = &shopLimiter{n: 60}  //
	accountLimiter    = &shopLimiter{n: 120} // account page views and edits
)

const shopperCookie = "apexx_shopper"

func (s *Server) setShopperCookie(w http.ResponseWriter, tenant, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: shopperCookie, Value: token, Path: "/s/" + tenant, HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

// shopper returns the signed-in customer id ("" when there is no valid session).
func (s *Server) shopper(r *http.Request) (customerID, token string) {
	c, err := r.Cookie(shopperCookie)
	if err != nil {
		return "", ""
	}
	id, err := s.Customers.Session(r.Context(), r.PathValue("tenant"), c.Value)
	if err != nil {
		return "", ""
	}
	return id, c.Value
}

// sameOrigin rejects cross-site form posts (the cookie is also SameSite=Lax).
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (s *Server) limited(w http.ResponseWriter, r *http.Request, lim *shopLimiter) bool {
	if lim.allow(r.PathValue("tenant") + "|" + s.clientIP(r)) {
		return false
	}
	w.Header().Set("Retry-After", "60")
	apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "rate_limited", Detail: "Demasiadas solicitudes; espera un minuto"})
	return true
}

func (s *Server) knownTenant(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.Proj.Tenant(r.PathValue("tenant")); !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return false
	}
	return true
}

// ---------- JSON API ----------

func (s *Server) shopNewsletter(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, newsletterLimiter) {
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.Customers.Subscribe(r.Context(), r.PathValue("tenant"), body.Email, "newsletter"); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopCartTrack(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, trackLimiter) {
		return
	}
	var body struct {
		Email   string               `json:"email"`
		Name    string               `json:"name"`
		Channel string               `json:"channel"`
		Site    string               `json:"site"`
		Items   []customers.CartItem `json:"items"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Site == "" || len(body.Site) > 60 || strings.ContainsAny(body.Site, "/?#") {
		body.Site = "main"
	}
	if body.Channel == "" {
		body.Channel = "mx"
	}
	if err := s.Customers.Track(r.Context(), r.PathValue("tenant"), body.Site, body.Channel, body.Email, body.Name, body.Items); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopCartRestore(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, trackLimiter) {
		return
	}
	lines, err := s.Customers.Restore(r.Context(), r.PathValue("tenant"), r.URL.Query().Get("token"))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": lines})
}

func (s *Server) shopAccountCode(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, codeLimiter) {
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &body) {
		return
	}
	err := s.Customers.RequestCode(r.Context(), r.PathValue("tenant"), body.Email)
	if errors.Is(err, customers.ErrRateLimited) {
		w.Header().Set("Retry-After", "600")
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "too_many_codes", Detail: "Ya enviamos varios códigos a ese correo. Espera 10 minutos."})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"sent": true, "expiresInSeconds": int(customers.OTPTTL.Seconds())})
}

func (s *Server) signInResult(w http.ResponseWriter, r *http.Request, token, id string, err error) {
	tenant := r.PathValue("tenant")
	switch {
	case errors.Is(err, customers.ErrBadCode):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_code", Detail: "El código no es correcto o ya venció"})
	case errors.Is(err, customers.ErrBadCredentials):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "invalid_credentials", Detail: "Correo o contraseña incorrectos"})
	case errors.Is(err, customers.ErrDisabled):
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "account_disabled", Detail: "Esta cuenta está desactivada; escribe a la tienda"})
	case err != nil:
		s.fail(w, err)
	default:
		s.setShopperCookie(w, tenant, token, int(customers.SessionTTL.Seconds()))
		c, err := s.Customers.Get(r.Context(), tenant, id, true)
		if err != nil {
			s.fail(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "customer": shopperView(c)})
	}
}

func shopperView(c customers.Customer) map[string]any {
	return map[string]any{"id": c.ID, "email": c.Email, "name": c.Name, "phone": c.Phone, "marketingConsent": c.MarketingConsent, "hasPassword": c.HasPassword, "ordersCount": c.OrdersCount}
}

func (s *Server) shopAccountVerify(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, verifyLimiter) {
		return
	}
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, id, err := s.Customers.VerifyCode(r.Context(), r.PathValue("tenant"), body.Email, body.Code)
	s.signInResult(w, r, token, id, err)
}

func (s *Server) shopAccountLogin(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, verifyLimiter) {
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	token, id, err := s.Customers.LoginPassword(r.Context(), r.PathValue("tenant"), body.Email, body.Password)
	s.signInResult(w, r, token, id, err)
}

// shopperToken reads the session from the cookie or a bearer token (headless clients).
func (s *Server) shopperAPI(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) {
		return "", false
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(shopperCookie); err == nil && token == "" {
		token = c.Value
	}
	id, err := s.Customers.Session(r.Context(), r.PathValue("tenant"), token)
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnauthorized, Code: "unauthenticated", Detail: "Inicia sesión en tu cuenta"})
		return "", false
	}
	return id, true
}

func (s *Server) shopAccountLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	tenant := r.PathValue("tenant")
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if c, err := r.Cookie(shopperCookie); err == nil && token == "" {
		token = c.Value
	}
	if token != "" {
		_ = s.Customers.Logout(r.Context(), tenant, token)
	}
	s.setShopperCookie(w, tenant, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) shopAccountMe(w http.ResponseWriter, r *http.Request) {
	id, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	c, err := s.Customers.Get(r.Context(), r.PathValue("tenant"), id, true)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	out := shopperView(c)
	out["addresses"] = c.Addresses
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) shopAccountOrders(w http.ResponseWriter, r *http.Request) {
	id, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	tenant := r.PathValue("tenant")
	w.Header().Set("Cache-Control", "no-store")
	if oid := r.URL.Query().Get("id"); oid != "" {
		o, err := s.shopperOrder(r, tenant, id, oid)
		if err != nil {
			s.fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, o)
		return
	}
	page, err := s.Orders.Query(r.Context(), tenant, orders.ListFilter{CustomerID: id, Cursor: r.URL.Query().Get("cursor"), Limit: 20}, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// shopperOrder loads an order only if it belongs to the customer, trimmed to
// what a shopper may see (no internal notes, no staff identities).
func (s *Server) shopperOrder(r *http.Request, tenant, customerID, orderID string) (orders.Order, error) {
	o, err := s.Orders.Get(r.Context(), tenant, orderID, true)
	if err != nil {
		return o, err
	}
	if o.CustomerID != customerID {
		return orders.Order{}, orders.ErrNotFound
	}
	notes := o.Notes[:0]
	for _, n := range o.Notes {
		if n.Visibility == "customer" {
			n.Author, n.AuthorName = "", ""
			notes = append(notes, n)
		}
	}
	o.Notes, o.Events, o.Edits, o.Payments = notes, nil, nil, nil
	for i := range o.Refunds {
		o.Refunds[i].CreatedBy = ""
	}
	for i := range o.Returns {
		o.Returns[i].CreatedBy = ""
	}
	return o, nil
}

func (s *Server) shopPay(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, checkoutLimiter) {
		return
	}
	var body struct {
		Token         string `json:"token"`
		PaymentMethod string `json:"paymentMethod"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !strings.HasPrefix(body.PaymentMethod, "pm_") {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "paymentMethod", Code: "invalid", Message: "Método de pago inválido"})
		return
	}
	info, err := s.Orders.PayWithLink(r.Context(), r.PathValue("tenant"), body.Token, body.PaymentMethod)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// ---------- pages ----------

type shopOrderRow struct {
	ID, Date, Status, Total string
	Number                  int64
	Items                   int64
}

type shopShipment struct {
	Number                               int
	Status, Carrier, Tracking, URL, Date string
	Items                                []orders.FulfillmentItem
}

type shopOrderView struct {
	ID, Date, Status, Items, Discounts, Shipping, Total, Refunded, Balance, InvoiceURL, Address string
	Number                                                                                      int64
	Lines                                                                                       []struct {
		Title, Total string
		Qty          int64
	}
	Shipments []shopShipment
	Notes     []struct{ Body, Date string }
	Returns   []struct{ Number, Status, LabelURL, LabelTracking string }
	Fees      []struct{ Name, Amount string }
	// Self-service return: lines that can go back, or why not.
	Returnable []returnableLine
	ReturnNote string
}

type returnableLine struct {
	Line  int
	Title string
	Max   int64
}

type accountData struct {
	pageData
	View      string // login | code | home | order | addresses | profile
	Email     string
	Error     string
	Notice    string
	Customer  customers.Customer
	Orders    []shopOrderRow
	NextPage  string
	Order     *shopOrderView
	Addresses []customers.Address
	Lists     []reorderList
	Company   *orders.CompanyAccess // the company the shopper buys for, if any
	Portal    *companyPortal
}

// companyPortal is the company view of "Mi cuenta": the buyer's month and
// requests; the members and branches for the company's administrators.
type companyPortal struct {
	Available, Spent, Cap, BranchSpent, BranchCap string
	Requests                                      []portalRequest
	Members                                       []orders.Member
	Locations                                     []orders.Location
	Autopay                                       *orders.Autopay
	Credit                                        bool // the company buys on credit (autopay applies)
}

type portalRequest struct {
	ID, Date, Status, Total, Reason, Requester, Level string
	Pending                                           bool
}

func shopperStatus(o orders.Order) string {
	switch {
	case o.Status == orders.StatusCancelled:
		return "Cancelado"
	case o.Status == orders.StatusRefunded:
		return "Reembolsado"
	case o.Status == orders.StatusPendingPayment:
		return "Pago pendiente"
	case o.FulfillmentStatus == "delivered":
		return "Entregado"
	case o.FulfillmentStatus == "fulfilled":
		return "Enviado"
	case o.FulfillmentStatus == "partial":
		return "Enviado en parte"
	}
	return "En preparación"
}

var mx = func() *time.Location {
	if l, err := time.LoadLocation("America/Mexico_City"); err == nil {
		return l
	}
	return time.FixedZone("CST", -6*3600)
}()

var months = []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

func shortDate(t time.Time) string {
	t = t.In(mx)
	return strconv.Itoa(t.Day()) + " " + months[t.Month()-1] + " " + strconv.Itoa(t.Year())
}

func mon(n int64, cur string) string { return pesos(money.New(n, money.Currency(cur))) }

func (s *Server) renderAccount(w http.ResponseWriter, page string, status int, data any) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", shopCSP)
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "same-origin") // payment and reminder links carry tokens in the URL
	w.WriteHeader(status)
	if err := accountPages[pageLang(w)][page].ExecuteTemplate(w, "layout", data); err != nil {
		s.Log.Error("render account page", "page", page, "err", err)
	}
}

func (s *Server) accountHome(w http.ResponseWriter, r *http.Request, d accountData, customerID string) {
	tenant := r.PathValue("tenant")
	c, err := s.Customers.Get(r.Context(), tenant, customerID, true)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	d.Customer, d.Addresses = c, c.Addresses
	if c.Email != "" && s.Orders != nil {
		d.Company, _ = s.Orders.CompanyForEmail(r.Context(), tenant, c.Email)
	}
	q := r.URL.Query()
	switch {
	case q.Get("ver") == "empresa" && d.Company != nil:
		d.View, d.Title = "company", d.Company.Name
		a := d.Company
		cur := "MXN"
		p := &companyPortal{Spent: mon(a.MonthSpent, cur), BranchSpent: mon(a.LocationSpent, cur)}
		if a.Terms != "prepaid" {
			p.Available = mon(a.Available, cur)
		}
		if a.MonthlyLimit > 0 {
			p.Cap = mon(a.MonthlyLimit, cur)
		}
		if a.LocationLimit > 0 {
			p.BranchCap = mon(a.LocationLimit, cur)
		}
		var mine []byte
		if !a.Admin {
			mine = a.BuyerHash
		}
		reqs, err := s.Orders.PurchaseRequests(r.Context(), tenant, a.ID, mine)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		label := map[string]string{"pending": "En espera", "approved": "Aprobada", "rejected": "Rechazada", "expired": "Vencida", "cancelled": "Cancelada"}
		for _, x := range reqs {
			p.Requests = append(p.Requests, portalRequest{ID: x.ID, Date: shortDate(x.CreatedAt), Status: label[x.Status], Total: mon(x.Total, x.Currency), Reason: x.Reason,
				Requester: x.Requester, Level: strconv.Itoa(x.Level) + "/" + strconv.Itoa(x.RequiredLevel), Pending: x.Status == "pending" && bytes.Equal(x.BuyerHash, a.BuyerHash)})
		}
		if a.Admin {
			if p.Members, err = s.Orders.Members(r.Context(), tenant, a.ID); err == nil {
				p.Locations, err = s.Orders.Locations(r.Context(), tenant, a.ID)
			}
			if err == nil {
				var ap orders.Autopay
				ap, err = s.Orders.CompanyAutopay(r.Context(), tenant, a.ID)
				p.Autopay, p.Credit = &ap, a.Terms != "prepaid"
			}
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
		}
		d.Portal = p
	case q.Get("pedido") != "":
		o, err := s.shopperOrder(r, tenant, customerID, q.Get("pedido"))
		if err != nil {
			d.View, d.Title, d.Error = "home", "Mi cuenta", "No encontramos ese pedido en tu cuenta."
			break
		}
		v := &shopOrderView{ID: o.ID, Number: o.Number, Date: shortDate(o.CreatedAt), Status: shopperStatus(o), Items: mon(o.ItemsTotal, o.Currency),
			Shipping: mon(o.Shipping, o.Currency), Total: mon(o.GrandTotal, o.Currency)}
		for _, f := range o.FeeLines {
			v.Fees = append(v.Fees, struct{ Name, Amount string }{f.Name, mon(f.Amount, o.Currency)})
		}
		if o.Discounts > 0 {
			v.Discounts = mon(o.Discounts, o.Currency)
		}
		if o.RefundedTotal > 0 && o.Status != orders.StatusCancelled {
			v.Refunded = mon(o.RefundedTotal, o.Currency)
		}
		if o.Balance > 0 {
			v.Balance = mon(o.Balance, o.Currency)
		}
		if o.Invoice != nil && o.Invoice.Status == "issued" && strings.HasPrefix(o.Invoice.URL, "https://") {
			v.InvoiceURL = o.Invoice.URL
		}
		if o.Customer != nil {
			v.Address = o.Customer.Address
		}
		for _, l := range o.Lines {
			if l.Qty-l.CancelledQty <= 0 {
				continue
			}
			v.Lines = append(v.Lines, struct {
				Title, Total string
				Qty          int64
			}{l.Title, mon(l.Unit*(l.Qty-l.CancelledQty), o.Currency), l.Qty - l.CancelledQty})
		}
		for _, f := range o.Fulfillments {
			if f.Status == "cancelled" {
				continue
			}
			sh := shopShipment{Number: f.Number, Carrier: f.Carrier, Tracking: f.TrackingNumber, Items: f.Items,
				Status: map[string]string{"pending": "En preparación", "shipped": "En camino", "delivered": "Entregado"}[f.Status]}
			if strings.HasPrefix(f.TrackingURL, "https://") {
				sh.URL = f.TrackingURL
			}
			if f.DeliveredAt != nil {
				sh.Date = shortDate(*f.DeliveredAt)
			} else if f.ShippedAt != nil {
				sh.Date = shortDate(*f.ShippedAt)
			}
			v.Shipments = append(v.Shipments, sh)
		}
		for _, n := range o.Notes {
			v.Notes = append(v.Notes, struct{ Body, Date string }{n.Body, shortDate(n.CreatedAt)})
		}
		for _, ret := range o.Returns {
			lu := ""
			if strings.HasPrefix(ret.LabelURL, "https://") {
				lu = ret.LabelURL
			}
			v.Returns = append(v.Returns, struct{ Number, Status, LabelURL, LabelTracking string }{orders.RMA(ret.Number),
				map[string]string{"requested": "Esperando tus productos", "received": "Recibida", "completed": "Completada", "cancelled": "Cancelada"}[ret.Status], lu, ret.LabelTracking})
		}
		if pol := s.returnsPolicy(r.Context(), tenant); pol.SelfService {
			var last *time.Time
			for _, f := range o.Fulfillments {
				if f.ShippedAt != nil && (last == nil || f.ShippedAt.After(*last)) {
					last = f.ShippedAt
				}
			}
			switch {
			case last == nil:
				v.ReturnNote = "Podrás pedir una devolución cuando tu pedido se envíe."
			case time.Since(*last) > time.Duration(pol.WindowDays)*24*time.Hour:
				v.ReturnNote = "El plazo para devoluciones de este pedido terminó."
			default:
				for _, l := range o.Lines {
					if n := l.FulfilledQty - l.ReturnedQty; n > 0 {
						v.Returnable = append(v.Returnable, returnableLine{l.Line, l.Title, n})
					}
				}
				if pol.CarrierID != "" {
					v.ReturnNote = "Te damos la guía para enviarlo sin costo."
				}
			}
		}
		d.View, d.Order, d.Title = "order", v, "Pedido #"+strconv.FormatInt(o.Number, 10)
	case q.Get("ver") == "direcciones":
		d.View, d.Title = "addresses", "Mis direcciones"
	case q.Get("ver") == "datos":
		d.View, d.Title = "profile", "Mis datos"
	case q.Get("ver") == "listas":
		d.View, d.Title = "lists", "Mis listas"
		company := ""
		if c, _ := s.shopperCompany(r); c != nil {
			company = c.ID
		}
		lists, err := s.reorderLists(r, tenant, customerID, company)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		if t, ok := s.Proj.Tenant(tenant); ok {
			for i := range lists {
				for _, it := range lists[i].Items {
					ln := listLine{SKU: it.SKU, Qty: it.Qty}
					if v, p, err := t.Catalog.VariantBySKU(it.SKU); err == nil && p.Status == "active" {
						ln.Title, ln.Available = strings.TrimSpace(p.Title+" "+v.Title), t.Inventory.Available(it.SKU) > 0
					} else {
						ln.Title = it.SKU
					}
					lists[i].Lines = append(lists[i].Lines, ln)
				}
			}
		}
		d.Lists = lists
	default:
		d.View, d.Title = "home", "Mi cuenta"
	}
	if d.View == "home" {
		page, err := s.Orders.Query(r.Context(), tenant, orders.ListFilter{CustomerID: customerID, Cursor: q.Get("cursor"), Limit: 20}, false)
		if err != nil && q.Get("cursor") != "" {
			page, err = s.Orders.Query(r.Context(), tenant, orders.ListFilter{CustomerID: customerID, Limit: 20}, false)
		}
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		for _, o := range page.Items {
			d.Orders = append(d.Orders, shopOrderRow{ID: o.ID, Number: o.Number, Date: shortDate(o.CreatedAt), Status: shopperStatus(o), Total: mon(o.GrandTotal, o.Currency), Items: o.ItemsCount})
		}
		d.NextPage = page.Next
	}
	s.renderAccount(w, "account", http.StatusOK, d)
}

var accountNotices = map[string]string{
	"direccion": "Dirección guardada.", "direccion-borrada": "Dirección eliminada.", "datos": "Tus datos se guardaron.",
	"contrasena": "Contraseña guardada. Ya puedes entrar con ella.", "sin-contrasena": "Quitamos tu contraseña; entra con un código por correo.",
	"salir": "Cerraste sesión.", "bienvenida": "¡Listo! Ya estás en tu cuenta.",
	"cuenta-borrada": "Borramos tu cuenta y tus datos personales. Tus pedidos se conservan sin tu nombre ni tu dirección, como exige la contabilidad.",
}

func (s *Server) shopAccountPage(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	if !accountLimiter.allow(pd.Tenant + "|" + s.clientIP(r)) {
		http.Error(w, "Demasiadas solicitudes; espera un minuto", http.StatusTooManyRequests)
		return
	}
	d := accountData{pageData: pd, Notice: accountNotices[r.URL.Query().Get("ok")]}
	if id, _ := s.shopper(r); id != "" {
		s.accountHome(w, r, d, id)
		return
	}
	d.View, d.Title = "login", "Entrar a mi cuenta"
	s.renderAccount(w, "account", http.StatusOK, d)
}

func (s *Server) shopAccountForm(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "Origen no permitido", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulario inválido", http.StatusBadRequest)
		return
	}
	tenant, ip := pd.Tenant, s.clientIP(r)
	f := func(k string) string { return strings.TrimSpace(r.PostForm.Get(k)) }
	d := accountData{pageData: pd, Email: f("email")}
	back := func(q string) { http.Redirect(w, r, pd.Base+"/cuenta"+q, http.StatusSeeOther) }
	loginError := func(view, msg string, status int) {
		d.View, d.Error, d.Title = view, msg, "Entrar a mi cuenta"
		s.renderAccount(w, "account", status, d)
	}
	action := f("accion")
	switch action {
	case "codigo":
		if !codeLimiter.allow(tenant + "|" + ip) {
			loginError("login", "Demasiados intentos. Espera un minuto.", http.StatusTooManyRequests)
			return
		}
		err := s.Customers.RequestCode(r.Context(), tenant, d.Email)
		var fe customers.FieldError
		switch {
		case errors.As(err, &fe):
			loginError("login", fe.Message, http.StatusBadRequest)
		case errors.Is(err, customers.ErrRateLimited):
			loginError("code", "Ya te enviamos varios códigos. Usa el más reciente o espera 10 minutos para pedir otro.", http.StatusTooManyRequests)
		case err != nil:
			internalError(w, s.Log, err)
		default:
			d.View, d.Title, d.Notice = "code", "Escribe tu código", "Te enviamos un código de 6 dígitos. Vence en 10 minutos."
			s.renderAccount(w, "account", http.StatusOK, d)
		}
		return
	case "verificar", "entrar":
		if !verifyLimiter.allow(tenant + "|" + ip) {
			loginError("login", "Demasiados intentos. Espera un minuto.", http.StatusTooManyRequests)
			return
		}
		var token string
		var err error
		view := "code"
		if action == "verificar" {
			token, _, err = s.Customers.VerifyCode(r.Context(), tenant, d.Email, f("codigo"))
		} else {
			view = "login"
			token, _, err = s.Customers.LoginPassword(r.Context(), tenant, d.Email, r.PostForm.Get("contrasena"))
		}
		switch {
		case errors.Is(err, customers.ErrBadCode):
			loginError(view, "El código no es correcto o ya venció. Revisa tu correo o pide uno nuevo.", http.StatusUnauthorized)
		case errors.Is(err, customers.ErrBadCredentials):
			loginError(view, "Correo o contraseña incorrectos. También puedes entrar con un código por correo.", http.StatusUnauthorized)
		case errors.Is(err, customers.ErrDisabled):
			loginError("login", "Esta cuenta está desactivada. Escribe a la tienda.", http.StatusForbidden)
		case err != nil:
			internalError(w, s.Log, err)
		default:
			s.setShopperCookie(w, tenant, token, int(customers.SessionTTL.Seconds()))
			back("?ok=bienvenida")
		}
		return
	}
	// Everything below needs a session.
	id, token := s.shopper(r)
	if id == "" {
		back("")
		return
	}
	if !accountLimiter.allow(tenant + "|" + ip) {
		http.Error(w, "Demasiadas solicitudes; espera un minuto", http.StatusTooManyRequests)
		return
	}
	actor := "shopper:" + id
	formError := func(view, title string, err error) {
		var fe customers.FieldError
		if !errors.As(err, &fe) {
			if errors.Is(err, customers.ErrNotFound) {
				back("")
				return
			}
			internalError(w, s.Log, err)
			return
		}
		c, gerr := s.Customers.Get(r.Context(), tenant, id, true)
		if gerr != nil {
			internalError(w, s.Log, gerr)
			return
		}
		d.Customer, d.Addresses, d.View, d.Title, d.Error = c, c.Addresses, view, title, fe.Message
		s.renderAccount(w, "account", http.StatusBadRequest, d)
	}
	switch action {
	case "salir":
		_ = s.Customers.Logout(r.Context(), tenant, token)
		s.setShopperCookie(w, tenant, "", -1)
		back("?ok=salir")
	case "direccion":
		a := customers.Address{ID: f("id"), Name: f("nombre"), Phone: f("telefono"), Line1: f("calle"), Line2: f("interior"), City: f("ciudad"), State: f("estado"),
			PostalCode: f("cp"), Country: "MX", Default: f("principal") != ""}
		if _, err := s.Customers.SaveAddress(r.Context(), tenant, actor, id, a); err != nil {
			formError("addresses", "Mis direcciones", err)
			return
		}
		back("?ver=direcciones&ok=direccion")
	case "borrar-direccion":
		if err := s.Customers.DeleteAddress(r.Context(), tenant, actor, id, f("id")); err != nil && !errors.Is(err, customers.ErrNotFound) {
			internalError(w, s.Log, err)
			return
		}
		back("?ver=direcciones&ok=direccion-borrada")
	case "datos":
		name, phone, consent := f("nombre"), f("telefono"), f("novedades") != ""
		if _, err := s.Customers.Update(r.Context(), tenant, actor, id, customers.Input{Name: &name, Phone: &phone, MarketingConsent: &consent}, 0, "account"); err != nil {
			formError("profile", "Mis datos", err)
			return
		}
		back("?ver=datos&ok=datos")
	case "borrar-cuenta":
		if strings.TrimSpace(strings.ToUpper(f("confirmar"))) != "BORRAR" {
			formError("profile", "Mis datos", customers.FieldError{Field: "confirmar", Message: "Escribe BORRAR para confirmar"})
			return
		}
		if err := s.eraseCustomer(r.Context(), tenant, actor, id); err != nil {
			if errors.Is(err, errActiveSubscription) {
				formError("profile", "Mis datos", customers.FieldError{Field: "confirmar", Message: "Tienes suscripciones activas. Cancélalas primero desde el correo de tu suscripción"})
				return
			}
			internalError(w, s.Log, err)
			return
		}
		s.setShopperCookie(w, tenant, "", -1)
		back("?ok=cuenta-borrada")
	case "contrasena":
		pw := r.PostForm.Get("contrasena")
		if err := s.Customers.SetPassword(r.Context(), tenant, id, pw, token); err != nil {
			formError("profile", "Mis datos", err)
			return
		}
		if pw == "" {
			back("?ver=datos&ok=sin-contrasena")
			return
		}
		back("?ver=datos&ok=contrasena")
	default:
		back("")
	}
}

type payData struct {
	pageData
	Token, Total, Error string
	Number              int64
	Kind                string
	Paid                bool
	Missing             bool
	Lines               []struct {
		Title, Total string
		Qty          int64
	}
}

func (s *Server) shopPayPage(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	if !accountLimiter.allow(pd.Tenant + "|" + s.clientIP(r)) {
		http.Error(w, "Demasiadas solicitudes; espera un minuto", http.StatusTooManyRequests)
		return
	}
	pd.Title = "Pagar"
	d := payData{pageData: pd, Token: r.URL.Query().Get("t")}
	info, err := s.Orders.PayLinkInfo(r.Context(), pd.Tenant, d.Token)
	status := http.StatusOK
	switch {
	case errors.Is(err, orders.ErrNotFound):
		d.Missing, status = true, http.StatusNotFound
	case err != nil:
		internalError(w, s.Log, err)
		return
	default:
		d.Number, d.Kind, d.Paid, d.Total = info.Number, info.Kind, info.Paid, mon(info.Amount, info.Currency)
		for _, l := range info.Lines {
			d.Lines = append(d.Lines, struct {
				Title, Total string
				Qty          int64
			}{l.Title, mon(l.Total, info.Currency), l.Qty})
		}
	}
	s.renderAccount(w, "pay", status, d)
}

// shopAccountExport hands the signed-in shopper a copy of their data.
func (s *Server) shopAccountExport(w http.ResponseWriter, r *http.Request) {
	id, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	out, err := s.exportCustomer(r.Context(), r.PathValue("tenant"), id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	writeExport(w, out, id)
}
