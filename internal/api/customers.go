package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
	"github.com/helloghc/apexx/internal/orders"
)

// fail maps domain errors of the customers, orders and notify packages to
// problem+json with stable codes.
func (s *Server) fail(w http.ResponseWriter, err error) {
	var cf customers.FieldError
	var of orders.FieldError
	var nf notify.FieldError
	var ce *orders.CreditError
	var te orders.ErrTemplate
	switch {
	case errors.As(err, &te):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "agreement_rule", Detail: te.Msg})
	case errors.As(err, &ce):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: ce.Code, Detail: ce.Message})
	case errors.As(err, &cf):
		apierr.BadRequest(w, cf.Message, apierr.FieldError{Field: cf.Field, Code: "invalid", Message: cf.Message})
	case errors.As(err, &of):
		apierr.BadRequest(w, of.Message, apierr.FieldError{Field: of.Field, Code: "invalid", Message: of.Message})
	case errors.As(err, &nf):
		apierr.BadRequest(w, nf.Message, apierr.FieldError{Field: nf.Field, Code: "invalid", Message: nf.Message})
	case errors.Is(err, customers.ErrNotFound), errors.Is(err, orders.ErrNotFound), errors.Is(err, notify.ErrNotFound):
		apierr.NotFound(w, "No encontrado")
	case errors.Is(err, customers.ErrConflict):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: apierr.CodeConflict, Detail: "Ya existe un registro con ese correo o nombre"})
	case errors.Is(err, customers.ErrVersion):
		apierr.Write(w, apierr.Problem{Status: http.StatusPreconditionFailed, Code: "version_mismatch", Detail: "Otra persona cambió este cliente; recarga y vuelve a intentar"})
	case errors.Is(err, customers.ErrTemplateOff):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "template_off", Detail: "La plantilla de carrito abandonado está apagada"})
	case errors.Is(err, orders.ErrInvalidState):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "invalid_state", Detail: strings.TrimPrefix(err.Error(), orders.ErrInvalidState.Error()+": ")})
	case errors.Is(err, orders.ErrRefundFailed):
		apierr.Write(w, apierr.Problem{Status: http.StatusBadGateway, Code: "refund_failed", Detail: "El proveedor de pagos no aceptó el reembolso; no se movió dinero. Intenta de nuevo en unos minutos"})
	case errors.Is(err, orders.ErrDeclined):
		apierr.Write(w, apierr.Problem{Status: http.StatusPaymentRequired, Code: "payment_declined", Detail: "El pago fue rechazado"})
	case errors.Is(err, orders.ErrNoPaymentAccount):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "payment_account_missing", Detail: orders.ErrNoPaymentAccount.Error()})
	case errors.Is(err, notify.ErrInvalid):
		apierr.BadRequest(w, "Solicitud inválida")
	default:
		internalError(w, s.Log, err)
	}
}

func limitOf(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// customerPII: readers of the customers module see masked names and emails.
func customerPII(r *http.Request) bool { return principal(r).Permissions.Allows("customers", "edit") }

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := customers.ListFilter{Q: q.Get("q"), Tag: q.Get("tag"), Group: q.Get("group"), Consent: q.Get("consent"), Account: q.Get("account"),
		Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: limitOf(r)}
	if len(f.Q) > 200 {
		apierr.BadRequest(w, "Búsqueda demasiado larga")
		return
	}
	items, next, total, err := s.Customers.List(r.Context(), principal(r).Tenant, f, customerPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "nextCursor": next, "total": total})
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request) {
	c, err := s.Customers.Get(r.Context(), principal(r).Tenant, r.PathValue("id"), customerPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("ETag", `"`+strconv.FormatInt(c.Version, 10)+`"`)
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) {
	var in customers.Input
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	c, err := s.Customers.Create(r.Context(), p.Tenant, p.Actor(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, c)
}

func (s *Server) updateCustomer(w http.ResponseWriter, r *http.Request) {
	var in customers.Input
	if !decode(w, r, &in) {
		return
	}
	var version int64
	if m := strings.Trim(r.Header.Get("If-Match"), `"`); m != "" {
		v, err := strconv.ParseInt(m, 10, 64)
		if err != nil {
			apierr.BadRequest(w, "If-Match inválido")
			return
		}
		version = v
	}
	p := principal(r)
	c, err := s.Customers.Update(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), in, version, "admin")
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, c)
}

func (s *Server) deleteCustomer(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.eraseCustomer(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.failErase(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) customerTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Customers.Tags(r.Context(), principal(r).Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) bulkTagCustomers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Tag    string   `json:"tag"`
		Remove bool     `json:"remove"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	n, err := s.Customers.BulkTag(r.Context(), p.Tenant, p.Actor(), body.IDs, body.Tag, body.Remove)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"changed": n})
}

func (s *Server) customerOrders(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	page, err := s.Orders.Query(r.Context(), p.Tenant, orders.ListFilter{CustomerID: r.PathValue("id"), Cursor: r.URL.Query().Get("cursor"), Limit: limitOf(r)}, false)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, page)
}

func (s *Server) saveCustomerAddress(w http.ResponseWriter, r *http.Request) {
	var a customers.Address
	if !decode(w, r, &a) {
		return
	}
	a.ID = r.PathValue("address")
	p := principal(r)
	out, err := s.Customers.SaveAddress(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), a)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) deleteCustomerAddress(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Customers.DeleteAddress(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), r.PathValue("address")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) addCustomerNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	n, err := s.Customers.AddNote(r.Context(), p.Tenant, p.Actor(), p.Name, r.PathValue("id"), body.Body)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, n)
}

func (s *Server) deleteCustomerNote(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Customers.DeleteNote(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), r.PathValue("note")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- groups ----------

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Customers.Groups(r.Context(), principal(r).Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": gs})
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.Customers.Group(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, g)
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request) {
	var g customers.Group
	if !decode(w, r, &g) {
		return
	}
	g.ID = r.PathValue("id")
	p := principal(r)
	out, err := s.Customers.SaveGroup(r.Context(), p.Tenant, p.Actor(), g)
	if err != nil {
		s.fail(w, err)
		return
	}
	status := http.StatusOK
	if g.ID == "" {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, out)
}

func (s *Server) previewGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules customers.Rules `json:"rules"`
	}
	if !decode(w, r, &body) {
		return
	}
	n, err := s.Customers.PreviewRules(r.Context(), principal(r).Tenant, body.Rules)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Customers.DeleteGroup(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setGroupMembers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Remove bool     `json:"remove"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	n, err := s.Customers.SetMembers(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), body.IDs, body.Remove)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"changed": n})
}

// ---------- abandoned carts ----------

func (s *Server) listCarts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", "open", "reminded", "recovered", "dismissed":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "status no válido"})
		return
	}
	items, next, stats, err := s.Customers.Carts(r.Context(), principal(r).Tenant, status, r.URL.Query().Get("cursor"), limitOf(r), customerPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "nextCursor": next, "stats": stats})
}

func (s *Server) remindCart(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Customers.Remind(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), true); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) dismissCart(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Customers.DismissCart(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
