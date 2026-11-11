package api

import (
	"net/http"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/customers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/notify"
)

func (s *Server) getNotifySettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.Notify.GetSettings(r.Context(), principal(r).Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, st)
}

func (s *Server) putNotifySettings(w http.ResponseWriter, r *http.Request) {
	var in notify.Settings
	if !decode(w, r, &in) {
		return
	}
	p := principal(r)
	st, err := s.Notify.SaveSettings(r.Context(), p.Tenant, p.Actor(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, st)
}

func (s *Server) listNotifyTemplates(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Notify.Templates(r.Context(), principal(r).Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ts})
}

type templateBody struct {
	Subject *string        `json:"subject"`
	Blocks  *notify.Blocks `json:"blocks"`
	Enabled *bool          `json:"enabled"`
	Reset   bool           `json:"reset"`
	Brand   *notify.Brand  `json:"brand"`
	To      string         `json:"to"`
}

func (s *Server) putNotifyTemplate(w http.ResponseWriter, r *http.Request) {
	var b templateBody
	if !decode(w, r, &b) {
		return
	}
	p := principal(r)
	key := r.PathValue("key")
	cur, err := s.Notify.Templates(r.Context(), p.Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	var t *notify.Template
	for i := range cur {
		if cur[i].Key == key {
			t = &cur[i]
		}
	}
	if t == nil {
		apierr.NotFound(w, "Plantilla desconocida")
		return
	}
	subject, blocks, enabled := t.Subject, t.Blocks, t.Enabled
	if b.Subject != nil {
		subject = *b.Subject
	}
	if b.Blocks != nil {
		blocks = *b.Blocks
	}
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	out, err := s.Notify.SaveTemplate(r.Context(), p.Tenant, p.Actor(), key, subject, blocks, enabled, b.Reset)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

func (s *Server) previewNotifyTemplate(w http.ResponseWriter, r *http.Request) {
	var b templateBody
	if !decode(w, r, &b) {
		return
	}
	out, err := s.Notify.Preview(r.Context(), principal(r).Tenant, r.PathValue("key"), b.Subject, b.Blocks, b.Brand)
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, out)
}

// testNotifyTemplate sends the saved template with sample data to one address.
func (s *Server) testNotifyTemplate(w http.ResponseWriter, r *http.Request) {
	var b templateBody
	if !decode(w, r, &b) {
		return
	}
	to, ok := customers.NormalizeEmail(b.To)
	if !ok {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "to", Code: "invalid", Message: "Escribe un correo válido para la prueba"})
		return
	}
	d, ok := notify.DefinitionOf(r.PathValue("key"))
	if !ok {
		apierr.NotFound(w, "Plantilla desconocida")
		return
	}
	id, err := s.Notify.Enqueue(r.Context(), principal(r).Tenant, notify.Message{Template: d.Key, To: to, Name: "Prueba", Data: notify.Sample(d), Force: true})
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"id": id})
}

func notifyPII(r *http.Request) bool { return principal(r).Permissions.Allows("settings", "edit") }

func (s *Server) listNotifyMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := notify.ListFilter{Status: q.Get("status"), Template: q.Get("template"), Email: strings.TrimSpace(q.Get("email")), OrderID: q.Get("order"),
		CustomerID: q.Get("customer"), Cursor: q.Get("cursor"), Limit: limitOf(r)}
	switch f.Status {
	case "", "queued", "sent", "failed", "dead", "skipped":
	default:
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "status no válido"})
		return
	}
	p := principal(r)
	items, next, err := s.Notify.List(r.Context(), p.Tenant, f, notifyPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	stats, err := s.Notify.Stats(r.Context(), p.Tenant)
	if err != nil {
		s.fail(w, err)
		return
	}
	if items == nil {
		items = []notify.Entry{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "nextCursor": next, "stats": stats})
}

func (s *Server) getNotifyMessage(w http.ResponseWriter, r *http.Request) {
	e, err := s.Notify.Get(r.Context(), principal(r).Tenant, r.PathValue("id"), notifyPII(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, e)
}

func (s *Server) retryNotifyMessage(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := s.Notify.Retry(r.Context(), p.Tenant, p.Actor(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
