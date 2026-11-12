package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

// ReturnsPolicy is how the store takes returns: whether customers open them
// from their account, within how many days of shipping, and which connected
// carrier makes their prepaid label ("" = no label, they send it themselves).
type ReturnsPolicy struct {
	SelfService bool   `json:"selfService"`
	WindowDays  int    `json:"windowDays"`
	CarrierID   string `json:"carrierId"`
}

func (s *Server) returnsPolicy(ctx context.Context, tenant string) ReturnsPolicy {
	p := ReturnsPolicy{WindowDays: 30}
	_, _ = s.DB.GetDoc(ctx, tenant, "returns_policy", "config", &p)
	if p.WindowDays <= 0 {
		p.WindowDays = 30
	}
	return p
}

func (s *Server) routesReturnsPolicy(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/settings/returns", s.authed("settings", auth.Read, s.getReturnsPolicy))
	handle("PUT /v1/settings/returns", s.authed("settings", auth.Admin, s.putReturnsPolicy))
	handle("POST /s/{tenant}/api/account/orders/{id}/returns", s.shopReturn)
}

func (s *Server) getReturnsPolicy(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, s.returnsPolicy(r.Context(), principal(r).Tenant))
}

func (s *Server) putReturnsPolicy(w http.ResponseWriter, r *http.Request) {
	var in ReturnsPolicy
	if !decode(w, r, &in) {
		return
	}
	if in.WindowDays < 1 || in.WindowDays > 365 {
		apierr.BadRequest(w, "Revisa los campos marcados", apierr.FieldError{Field: "windowDays", Code: "out_of_range", Message: "El plazo va de 1 a 365 días"})
		return
	}
	pr := principal(r)
	if in.CarrierID != "" {
		cfg, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		ok := false
		for _, c := range cfg.Carriers {
			ok = ok || (c.ID == in.CarrierID && c.Active && c.Connected())
		}
		if !ok {
			apierr.BadRequest(w, "Revisa los campos marcados", apierr.FieldError{Field: "carrierId", Code: "invalid", Message: "Elige una paquetería activa y conectada"})
			return
		}
	}
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "returns_policy", "config", "", in); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, in)
}

// openReturn opens a return, buying its prepaid label first when asked. A
// return that cannot be saved cancels the label it bought.
func (s *Server) openReturn(ctx context.Context, tenant, actor, orderID string, in orders.ReturnInput) (orders.Return, error) {
	in.Label = nil
	if in.ReturnLabel && in.CarrierID != "" {
		o, err := s.Orders.Get(ctx, tenant, orderID, true)
		if err != nil {
			return orders.Return{}, err
		}
		lines := make([]orders.RefundLine, 0, len(in.Items))
		for _, it := range in.Items {
			lines = append(lines, orders.RefundLine{Line: it.Line, Qty: it.Qty})
		}
		from, weight, value := s.parcelOf(tenant, o, lines)
		label, err := s.buyLabel(ctx, tenant, in.CarrierID, fmt.Sprintf("%s-dev-%d", o.ID, len(o.Returns)+1), from, carriers.Address{}, weight, value, true)
		if err != nil {
			return orders.Return{}, err
		}
		in.Label = label
	}
	ret, err := s.Orders.CreateReturn(ctx, tenant, actor, orderID, in)
	if err != nil && in.Label != nil {
		s.cancelLabel(tenant, in.Label.CarrierID, in.Label.TrackingNumber)
	}
	return ret, err
}

// shopReturn lets a customer return items of their own order from their
// account, within the store's window, with a prepaid label when the store
// has a returns carrier.
func (s *Server) shopReturn(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	tenant := r.PathValue("tenant")
	pol := s.returnsPolicy(r.Context(), tenant)
	if !pol.SelfService {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "returns_by_store", Detail: "Esta tienda recibe las devoluciones por mensaje; escríbenos y te ayudamos"})
		return
	}
	o, err := s.Orders.Get(r.Context(), tenant, r.PathValue("id"), false)
	if err != nil || o.CustomerID != cid {
		apierr.NotFound(w, "No encontramos ese pedido en tu cuenta")
		return
	}
	var last *time.Time
	for _, f := range o.Fulfillments {
		if f.ShippedAt != nil && (last == nil || f.ShippedAt.After(*last)) {
			last = f.ShippedAt
		}
	}
	if last == nil || time.Since(*last) > time.Duration(pol.WindowDays)*24*time.Hour {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "return_window", Detail: fmt.Sprintf("Las devoluciones se piden dentro de %d días desde el envío", pol.WindowDays)})
		return
	}
	var body struct {
		Items []orders.ReturnItemInput `json:"items"`
		Note  string                   `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	ret, err := s.openReturn(r.Context(), tenant, "customer:"+cid, o.ID, orders.ReturnInput{Items: body.Items, Note: body.Note, RequestedBy: "customer",
		CarrierID: pol.CarrierID, ReturnLabel: pol.CarrierID != ""})
	if err != nil {
		s.carrierFail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"number": ret.Number, "labelUrl": ret.LabelURL, "labelTracking": ret.LabelTracking})
}
