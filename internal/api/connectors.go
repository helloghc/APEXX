package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
	"github.com/helloghc/apexx/internal/webhooks"
)

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 100
	}
	var types []string
	if t := r.URL.Query().Get("types"); t != "" {
		types = strings.Split(t, ",")
	}
	evs, err := s.Webhooks.Feed(r.Context(), principal(r).Tenant, after, limit, types)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := after
	if len(evs) > 0 {
		next = evs[len(evs)-1].Sequence
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": evs, "next": next})
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	subs, err := s.Webhooks.List(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": subs})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string   `json:"url"`
		Types []string `json:"types"`
	}
	if !decode(w, r, &body) {
		return
	}
	p := principal(r)
	sub, secret, err := s.Webhooks.Subscribe(r.Context(), p.Tenant, p.Actor(), body.URL, body.Types)
	if errors.Is(err, webhooks.ErrUnsafeURL) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "url", Code: "unsafe_url", Message: "La URL debe ser https y pública"})
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"subscription": sub, "secret": secret, "notice": "Guarda el secreto ahora; sirve para verificar Apexx-Signature y no se volverá a mostrar"})
}

func (s *Server) disableWebhook(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	err := s.Webhooks.Disable(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Webhook no encontrado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) webhookDeliveries(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Webhooks.Deliveries(r.Context(), principal(r).Tenant, r.PathValue("id"), 200)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": ds})
}

func (s *Server) replayWebhook(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	p := principal(r)
	n, err := s.Webhooks.Replay(r.Context(), p.Tenant, p.Actor(), r.PathValue("id"), after)
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Webhook no encontrado o inactivo")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"requeued": n})
}
