package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/subs"
)

// routesSubscriptions registers recurring purchases.
func (s *Server) routesSubscriptions(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/subscriptions", s.authed("orders", auth.Read, s.listSubscriptions))
	handle("GET /v1/subscriptions/{id}", s.authed("orders", auth.Read, s.getSubscription))
	handle("POST /v1/subscriptions/{id}/{action}", s.authed("orders", auth.Edit, s.actSubscription))
	handle("GET /v1/subscription-config", s.authed("orders", auth.Read, s.getSubConfig))
	handle("PUT /v1/subscription-config", s.authed("orders", auth.Admin, s.putSubConfig))
	handle("GET /v1/settings/languages", s.staff("settings", auth.Read, s.getLanguages))
	handle("PUT /v1/settings/languages", s.authed("settings", auth.Admin, s.putLanguages))

	handle("GET /s/{tenant}/{site}/suscripcion/{token}", s.shopSubscription)
	handle("POST /s/{tenant}/api/subscription/{token}/{action}", s.shopSubscriptionAct)
}

var subLimiter = &shopLimiter{n: 30}

func (s *Server) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := queryLimit(r, 50, 100)
	var before time.Time
	if c := q.Get("cursor"); c != "" {
		before, _ = time.Parse(time.RFC3339Nano, c)
	}
	list, counts, err := s.Orders.Subscriptions(r.Context(), principal(r).Tenant, q.Get("status"), before, limit+1, orderPII(r))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list), "nextCursor": next, "counts": counts})
}

func (s *Server) getSubscription(w http.ResponseWriter, r *http.Request) {
	v, err := s.Orders.Subscription(r.Context(), principal(r).Tenant, r.PathValue("id"), orderPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, v)
}

func (s *Server) actSubscription(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	v, err := s.Orders.SubscriptionAction(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("id"), r.PathValue("action"), false)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, v)
}

func (s *Server) getSubConfig(w http.ResponseWriter, r *http.Request) {
	t, ok := s.Proj.Tenant(principal(r).Tenant)
	if !ok {
		apierr.NotFound(w, "Tienda no encontrada")
		return
	}
	cfg := *t.Subscriptions()
	_ = cfg.Normalize()
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": cfg, "products": refsOf(t, cfg.ProductIDs), "plans": planList(subs.Plans)})
}

func planList(ids []string) []map[string]string {
	out := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]string{"id": id, "label": subs.Label(id)})
	}
	return out
}

func (s *Server) putSubConfig(w http.ResponseWriter, r *http.Request) {
	var cfg subs.Config
	if !decode(w, r, &cfg) {
		return
	}
	if err := cfg.Normalize(); err != nil {
		apierr.BadRequest(w, err.Error())
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "subscriptions", "config", store.KeySubscriptions, cfg); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"config": cfg})
}

type subPage struct {
	pageData
	Sub     *orders.Subscription
	Token   string
	Next    string
	Percent string
}

// shopSubscription is the page where the customer manages a subscription. The
// link in the email is the key: no password is needed.
func (s *Server) shopSubscription(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pd.Title, pd.NoIndex = "Mi suscripción", true
	pg := subPage{pageData: pd, Token: r.PathValue("token")}
	if !subLimiter.allow(pd.Tenant + "|" + s.clientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
	} else if v, err := s.Orders.SubscriptionByToken(r.Context(), pd.Tenant, pg.Token); err == nil {
		pg.Sub, pg.Next = &v, shortDate(v.NextAt)
		if v.DiscountBps > 0 {
			pg.Percent = strconv.FormatFloat(float64(v.DiscountBps)/100, 'f', -1, 64)
		}
	} else if !errors.Is(err, orders.ErrNotFound) {
		internalError(w, s.Log, err)
		return
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.renderShop(w, "subscription", pg, false)
}

func (s *Server) shopSubscriptionAct(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, subLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	v, err := s.Orders.SubscriptionAction(r.Context(), r.PathValue("tenant"), "customer", r.PathValue("token"), r.PathValue("action"), true)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"status": v.Status, "nextAt": v.NextAt})
}
