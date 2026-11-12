package api

import (
	"context"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Scheduled design: the current draft is frozen as a version that goes live
// by itself at its start and leaves at its end (the regular published site
// comes back). Before that, the store previews any moment with a signed link.

func (s *Server) routesDesignSchedule(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/sites/{site}/schedules", s.authed("builder", auth.Read, noStorefront(s.listDesignSchedules)))
	handle("POST /v1/sites/{site}/schedules", s.authed("builder", auth.Admin, s.createDesignSchedule))
	handle("DELETE /v1/sites/{site}/schedules/{id}", s.authed("builder", auth.Admin, s.deleteDesignSchedule))
	handle("GET /v1/sites/{site}/schedules/preview", s.authed("builder", auth.Read, noStorefront(s.designPreviewLink)))
}

func pendingSchedule(list []builder.SiteSchedule) bool {
	now := time.Now()
	return slices.ContainsFunc(list, func(sc builder.SiteSchedule) bool { return sc.EndsAt.After(now) })
}

func loadSchedulesTx(ctx context.Context, tx pgx.Tx, tenant, site string) ([]builder.SiteSchedule, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT data FROM tenant_docs WHERE tenant_id=$1 AND kind='site_schedules' AND id=$2`, tenant, site).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []builder.SiteSchedule
	return out, json.Unmarshal(raw, &out)
}

// saveSchedules stores the list and tells every node to reload the site.
func (s *Server) saveSchedules(ctx context.Context, tenant, actor, site string, list []builder.SiteSchedule, action string, data any) error {
	b, _ := json.Marshal(list)
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO tenant_docs (tenant_id, kind, id, data) VALUES ($1,'site_schedules',$2,$3)
			ON CONFLICT (tenant_id, kind, id) DO UPDATE SET data=EXCLUDED.data, updated_at=now()`, tenant, site, b); err != nil {
			return err
		}
		if err := store.Emit(ctx, tx, tenant, "site.released", site, map[string]any{"schedules": len(list)}); err != nil {
			return err
		}
		return store.Audit(ctx, tx, tenant, actor, action, site, data)
	})
}

func (s *Server) listDesignSchedules(w http.ResponseWriter, r *http.Request) {
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	list := st.Schedules()
	if list == nil {
		list = []builder.SiteSchedule{}
	}
	now := time.Now()
	type view struct {
		builder.SiteSchedule
		State string `json:"state"`
	}
	out := make([]view, len(list))
	for i, sc := range list {
		state := "programada"
		switch {
		case !now.Before(sc.EndsAt):
			state = "terminada"
		case !now.Before(sc.StartsAt):
			state = "en vivo"
		}
		out[i] = view{sc, state}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) createDesignSchedule(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	var in struct {
		Name     string    `json:"name"`
		StartsAt time.Time `json:"startsAt"`
		EndsAt   time.Time `json:"endsAt"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	var errs []apierr.FieldError
	switch {
	case in.Name == "" || len([]rune(in.Name)) > 80:
		errs = append(errs, apierr.FieldError{Field: "name", Code: "invalid", Message: "Escribe un nombre de hasta 80 caracteres"})
	case !in.EndsAt.After(in.StartsAt):
		errs = append(errs, apierr.FieldError{Field: "endsAt", Code: "invalid", Message: "El fin va después del inicio"})
	case !in.EndsAt.After(time.Now()):
		errs = append(errs, apierr.FieldError{Field: "endsAt", Code: "past", Message: "El fin ya pasó"})
	case in.EndsAt.Sub(in.StartsAt) > 120*24*time.Hour:
		errs = append(errs, apierr.FieldError{Field: "endsAt", Code: "too_long", Message: "Una versión programada dura hasta 120 días"})
	}
	list := st.Schedules()
	active := slices.DeleteFunc(slices.Clone(list), func(sc builder.SiteSchedule) bool { return !sc.EndsAt.After(time.Now()) })
	if len(active) >= 20 {
		errs = append(errs, apierr.FieldError{Field: "name", Code: "too_many", Message: "Hasta 20 versiones programadas pendientes"})
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa la programación", errs...)
		return
	}
	// The draft as it is now becomes the scheduled version.
	revID, reports, err := st.Freeze(pr.Actor(), "Programada: "+in.Name)
	if errors.Is(err, builder.ErrBlocked) {
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "blocked", Detail: "El borrador tiene errores que impiden publicarlo; corrígelos antes de programarlo"})
		_ = reports
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	sc := builder.SiteSchedule{ID: newRandomID("sch_"), Name: in.Name, RevisionID: revID, StartsAt: in.StartsAt.UTC(), EndsAt: in.EndsAt.UTC(), CreatedBy: pr.Actor()}
	list = append(active, sc)
	if err := s.saveSchedules(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("site"), list, "site.scheduled", sc); err != nil {
		internalError(w, s.Log, err)
		return
	}
	st.SetSchedules(list)
	httpx.JSON(w, r, http.StatusCreated, sc)
}

func (s *Server) deleteDesignSchedule(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	st, ok := s.withSite(w, r)
	if !ok {
		return
	}
	list := st.Schedules()
	n := len(list)
	list = slices.DeleteFunc(list, func(sc builder.SiteSchedule) bool { return sc.ID == r.PathValue("id") })
	if len(list) == n {
		apierr.NotFound(w, "Programación no encontrada")
		return
	}
	if err := s.saveSchedules(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("site"), list, "site.unscheduled", map[string]any{"id": r.PathValue("id")}); err != nil {
		internalError(w, s.Log, err)
		return
	}
	st.SetSchedules(list)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) designPreviewSig(tenant, at string) string {
	b, err := s.Keys.Blind(tenant, "design-preview", at)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(b[:12])
}

// designPreviewLink signs a link that shows the site as it will be at a moment.
func (s *Server) designPreviewLink(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	at, err := time.Parse(time.RFC3339, r.URL.Query().Get("at"))
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "at", Code: "invalid", Message: "Fecha y hora en formato ISO"})
		return
	}
	stamp := at.UTC().Format(time.RFC3339)
	httpx.JSON(w, r, http.StatusOK, map[string]any{"url": "/s/" + pr.Tenant + "/" + r.PathValue("site") + "/index?fecha=" + url.QueryEscape(stamp) + "&vista=" + s.designPreviewSig(pr.Tenant, stamp)})
}

// designPreviewAt reads a signed "as of" moment from a page request.
func (s *Server) designPreviewAt(r *http.Request, tenant string) (time.Time, bool) {
	q := r.URL.Query()
	stamp, sig := q.Get("fecha"), q.Get("vista")
	if stamp == "" || sig == "" || !hmac.Equal([]byte(sig), []byte(s.designPreviewSig(tenant, stamp))) {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, stamp)
	return at, err == nil
}
