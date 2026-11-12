package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
)

func orderPII(r *http.Request) bool { return principal(r).Permissions.Allows("orders", "edit") }

func multi(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseDay accepts YYYY-MM-DD (store time, America/Mexico_City) or RFC 3339.
func parseDay(v string, endOfDay bool) (*time.Time, bool) {
	if v == "" {
		return nil, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, true
	}
	loc, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		loc = time.FixedZone("CST", -6*3600)
	}
	t, err := time.ParseInLocation("2006-01-02", v, loc)
	if err != nil {
		return nil, false
	}
	if endOfDay {
		t = t.AddDate(0, 0, 1)
	}
	return &t, true
}

func orderFilter(w http.ResponseWriter, r *http.Request) (orders.ListFilter, bool) {
	q := r.URL.Query()
	f := orders.ListFilter{Q: q.Get("q"), Status: multi(q.Get("status")), Payment: multi(q.Get("payment")), Fulfillment: multi(q.Get("fulfillment")),
		Channel: q.Get("channel"), CustomerID: q.Get("customer"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: limitOf(r)}
	f.Review = q.Get("review") == "1"
	if b := q.Get("before"); b != "" && f.Cursor == "" {
		f.Cursor = b // older clients page with ?before=<number>
	}
	var ok1, ok2 bool
	f.From, ok1 = parseDay(q.Get("from"), false)
	f.To, ok2 = parseDay(q.Get("to"), true)
	if !ok1 || !ok2 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "from", Code: "invalid", Message: "Usa fechas con formato AAAA-MM-DD"})
		return f, false
	}
	return f, true
}

func (s *Server) queryOrders(w http.ResponseWriter, r *http.Request) {
	f, ok := orderFilter(w, r)
	if !ok {
		return
	}
	page, err := s.Orders.Query(r.Context(), principal(r).Tenant, f, orderPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	next := int64(0)
	if !strings.HasPrefix(f.Sort, "total") {
		next, _ = strconv.ParseInt(page.Next, 10, 64)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": page.Items, "nextCursor": page.Next, "nextBefore": next, "total": page.Total, "channels": page.Channels})
}

func (s *Server) exportOrders(w http.ResponseWriter, r *http.Request) {
	f, ok := orderFilter(w, r)
	if !ok {
		return
	}
	if err := f.Validate(); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pedidos-`+time.Now().Format("2006-01-02")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xEF\xBB\xBF")) // BOM so Excel reads UTF-8
	if _, err := s.Orders.Export(r.Context(), principal(r).Tenant, f, orderPII(r), w); err != nil {
		s.Log.Error("export orders", "err", err)
	}
}

func (s *Server) listOrderViews(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Orders.Views(r.Context(), principal(r).Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": vs})
}

func (s *Server) saveOrderView(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string          `json:"name"`
		Filters json.RawMessage `json:"filters"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	v, err := s.Orders.SaveView(r.Context(), p.Tenant, p.Actor(), body.Name, body.Filters)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, v)
}

func (s *Server) deleteOrderView(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.DeleteView(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// orderResult answers an order action with the fresh order.
func (s *Server) orderResult(w http.ResponseWriter, r *http.Request, status int, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}
	p := principal(r)
	o, err := s.Orders.Get(r.Context(), p.Tenant, r.PathValue("id"), orderPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, status, o)
}

func (s *Server) createFulfillment(w http.ResponseWriter, r *http.Request) {
	var in orders.FulfillmentInput
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	in.Label = nil // only set here, from the carrier
	if in.BuyLabel {
		o, err := s.Orders.Get(r.Context(), p.Tenant, r.PathValue("id"), true)
		if err != nil {
			s.fail(w, err)
			return
		}
		to, weight, value := s.parcelOf(p.Tenant, o, in.Items)
		label, err := s.buyLabel(r.Context(), p.Tenant, in.CarrierID, fmt.Sprintf("%s-%d", o.ID, len(o.Fulfillments)+1), carriers.Address{}, to, weight, value, false)
		if err != nil {
			s.carrierFail(w, err)
			return
		}
		in.Label = label
	}
	_, err := s.Orders.CreateFulfillment(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in)
	if err != nil && in.Label != nil {
		s.cancelLabel(p.Tenant, in.Label.CarrierID, in.Label.TrackingNumber) // reversal: the label is not paid for nothing
	}
	s.orderResult(w, r, http.StatusCreated, err)
}

func (s *Server) updateFulfillment(w http.ResponseWriter, r *http.Request) {
	var in orders.FulfillmentUpdate
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	s.orderResult(w, r, http.StatusOK, s.Orders.UpdateFulfillment(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), r.PathValue("fulfillment"), in))
}

func (s *Server) quoteRefund(w http.ResponseWriter, r *http.Request) {
	var in orders.RefundInput
	if !decode(w, r, &in) {
		return
	}
	q, err := s.Orders.RefundQuote(r.Context(), principal(r).Tenant, r.PathValue("id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, q)
}

func (s *Server) createRefund(w http.ResponseWriter, r *http.Request) {
	var in orders.RefundInput
	if !decode(w, r, &in) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 200 {
		apierr.BadRequest(w, "Idempotency-Key demasiado larga")
		return
	}
	p := principal(r)
	_, err := s.Orders.Refund(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), key, in)
	s.orderResult(w, r, http.StatusCreated, err)
}

func (s *Server) createReturn(w http.ResponseWriter, r *http.Request) {
	var in orders.ReturnInput
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	in.RequestedBy = "store"
	ret, err := s.openReturn(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in)
	if err != nil {
		s.carrierFail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, ret)
}

func (s *Server) listReturns(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "requested", "received", "completed", "cancelled":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "status no válido"})
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, next, err := s.Orders.Returns(r.Context(), principal(r).Tenant, status, before, limitOf(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "nextBefore": next, "reasons": orders.ReasonCodes})
}

func (s *Server) getReturn(w http.ResponseWriter, r *http.Request) {
	ret, err := s.Orders.GetReturn(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, ret)
}

func (s *Server) receiveReturn(w http.ResponseWriter, r *http.Request) {
	var in orders.ReceiveInput
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	ret, err := s.Orders.ReceiveReturn(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in)
	if err != nil && ret.ID != "" && ret.Status == "received" {
		// Items are in but the refund did not go through: say so without losing the reception.
		httpx.JSON(w, r, http.StatusAccepted, map[string]any{"return": ret, "refundError": "La devolución quedó recibida, pero el reembolso no se pudo hacer. Reintenta el reembolso."})
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"return": ret})
}

func (s *Server) refundReturn(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.RefundReturn(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.getReturn(w, r)
}

func (s *Server) cancelReturn(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.CancelReturn(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.getReturn(w, r)
}

func (s *Server) editOrder(w http.ResponseWriter, r *http.Request) {
	var in orders.EditInput
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	res, err := s.Orders.EditOrder(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, res)
}

func (s *Server) addOrderNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body       string `json:"body"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Visibility == "" {
		body.Visibility = "internal"
	}
	p := principal(r)
	_, err := s.Orders.AddNote(r.Context(), p.Tenant, p.Actor(), p.Name, r.PathValue("id"), body.Visibility, body.Body)
	s.orderResult(w, r, http.StatusCreated, err)
}

func (s *Server) deleteOrderNote(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	s.orderResult(w, r, http.StatusOK, s.Orders.DeleteNote(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), r.PathValue("note")))
}

func (s *Server) manualPayment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Amount int64  `json:"amount"`
		Note   string `json:"note"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	s.orderResult(w, r, http.StatusCreated, s.Orders.RecordManualPayment(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.Amount, body.Note))
}

func (s *Server) sendBalanceLink(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	url, err := s.Orders.SendBalanceLink(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"url": url})
}

func (s *Server) settleCredit(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	_, err := s.Orders.SettleCredit(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"))
	s.orderResult(w, r, http.StatusCreated, err)
}

// ---------- draft orders ----------

func (s *Server) listDrafts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "open", "sent", "completed", "cancelled":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "status no válido"})
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	items, next, err := s.Orders.Drafts(r.Context(), principal(r).Tenant, status, before, limitOf(r), orderPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "nextBefore": next})
}

func (s *Server) getDraftOrder(w http.ResponseWriter, r *http.Request) {
	d, err := s.Orders.GetDraft(r.Context(), principal(r).Tenant, r.PathValue("id"), orderPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request) {
	var in orders.DraftInput
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	id := r.PathValue("id")
	d, err := s.Orders.SaveDraft(r.Context(), p.Tenant, p.Actor(), id, in)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, d)
}

func (s *Server) sendDraft(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	d, err := s.Orders.SendDraftLink(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) completeDraft(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	o, err := s.Orders.CompleteDraft(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), "manual")
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, o)
}

func (s *Server) cancelDraft(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Orders.CancelDraft(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	s.getDraftOrder(w, r)
}
