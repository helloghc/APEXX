package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

func (s *Server) routesQuotes(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/quotes", s.authed("orders", auth.Read, s.listQuotes))
	handle("GET /v1/quotes/{id}", s.authed("orders", auth.Read, s.getQuote))
	handle("POST /v1/quotes/{id}/offer", s.authed("orders", auth.Edit, s.offerQuote))
	handle("POST /v1/quotes/{id}/cancel", s.authed("orders", auth.Edit, s.cancelQuote))
	handle("POST /s/{tenant}/api/quotes", s.shopRequestQuote)
	handle("POST /s/{tenant}/api/quotes/{token}/{action}", s.shopAnswerQuote)
	handle("GET /s/{tenant}/{site}/cotizacion/{token}", s.shopQuotePage)
	handle("POST /s/{tenant}/api/lists", s.shopListSave)
	handle("PUT /s/{tenant}/api/lists/{id}", s.shopListSave)
	handle("DELETE /s/{tenant}/api/lists/{id}", s.shopListDelete)
}

var quoteLimiter = &shopLimiter{n: 5}

func ordersPII(r *http.Request) bool { return principal(r).Permissions.Allows("orders", auth.Edit) }

func (s *Server) listQuotes(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.Quotes(r.Context(), principal(r).Tenant, r.URL.Query().Get("status"), 100, ordersPII(r))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) getQuote(w http.ResponseWriter, r *http.Request) {
	q, err := s.Orders.GetQuote(r.Context(), principal(r).Tenant, r.PathValue("id"), ordersPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, q)
}

func (s *Server) offerQuote(w http.ResponseWriter, r *http.Request) {
	var in orders.QuoteOffer
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	q, err := s.Orders.OfferQuote(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, q)
}

func (s *Server) cancelQuote(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.CancelQuote(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// shopRequestQuote: a buyer asks for a quote from the cart.
func (s *Server) shopRequestQuote(w http.ResponseWriter, r *http.Request) {
	t, release, ok := s.publicTenant(w, r, quoteLimiter)
	if !ok {
		return
	}
	defer release()
	var body struct {
		orders.QuoteRequest
		Website string `json:"website"` // honeypot: people leave it empty
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Website != "" {
		writeJSON(w, http.StatusCreated, map[string]any{"number": 0})
		return
	}
	if !shopChannel(t, body.Channel) {
		apierr.BadRequest(w, "Canal de venta no disponible")
		return
	}
	in := body.QuoteRequest
	if c, email := s.shopperCompany(r); c != nil {
		if !c.CanQuote {
			apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "role_forbidden", Detail: "Tu rol en la empresa no permite pedir cotizaciones"})
			return
		}
		in.Company, in.Customer.Email = c, email
	}
	q, err := s.Orders.RequestQuote(r.Context(), t.ID, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"number": q.Number, "url": q.URL})
}

func (s *Server) shopAnswerQuote(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, subLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	company, _ := s.shopperCompany(r)
	q, payURL, err := s.Orders.AnswerQuote(r.Context(), r.PathValue("tenant"), r.PathValue("token"), r.PathValue("action"), body.Message, company)
	if errors.Is(err, orders.ErrNotFound) {
		apierr.NotFound(w, "Cotización no encontrada")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": q.Status, "orderId": q.OrderID, "payUrl": payURL})
}

type quotePage struct {
	pageData
	Token  string
	Q      *orders.PriceQuote
	Titles []string
	Lines  []quoteLine // products of the quote, for line comments
}

type quoteLine struct{ SKU, Title string }

func (s *Server) shopQuotePage(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pd.Title, pd.NoIndex = "Cotización", true
	pg := quotePage{pageData: pd, Token: r.PathValue("token")}
	if !subLimiter.allow(pd.Tenant + "|" + s.clientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
	} else if q, err := s.Orders.QuoteByToken(r.Context(), pd.Tenant, pg.Token); err == nil {
		q.Customer = nil
		pg.Q = &q
		for _, it := range q.Items {
			if v, p, err := t.Catalog.VariantBySKU(it.SKU); err == nil {
				pg.Titles = append(pg.Titles, strings.TrimSpace(p.Title+" "+v.Title)+" × "+itoaInt(it.Qty))
				pg.Lines = append(pg.Lines, quoteLine{it.SKU, strings.TrimSpace(p.Title + " " + v.Title)})
			}
		}
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.renderShop(w, "quote", pg, false)
}

func itoaInt(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
