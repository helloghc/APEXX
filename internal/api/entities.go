package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Custom data: the store defines its own entities (wholesale requests,
// warranties, contact messages…) and collects records from the admin, the
// API or a public form. Records are encrypted: forms carry personal data.

// EntityField is one field of an entity.
type EntityField struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // text | textarea | email | phone | number | select | checkbox
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
}

// Entity is a kind of record defined by the store.
type Entity struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Public      bool          `json:"public"`  // accepts submissions from the storefront form
	Success     string        `json:"success"` // message after a public submission
	Fields      []EntityField `json:"fields"`
	Records     int           `json:"records"`
}

// EntityRecord is one stored record.
type EntityRecord struct {
	ID        string            `json:"id"`
	Data      map[string]string `json:"data"`
	Source    string            `json:"source"`
	CreatedAt time.Time         `json:"createdAt"`
}

var (
	entityID    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	fieldKey    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	fieldTypes  = map[string]bool{"text": true, "textarea": true, "email": true, "phone": true, "number": true, "select": true, "checkbox": true}
	formLimiter = &shopLimiter{n: 5}
)

func (s *Server) routesEntities(handle func(string, http.HandlerFunc)) {
	const mod = "settings"
	handle("GET /v1/entities", s.authed(mod, auth.Read, s.listEntities))
	handle("PUT /v1/entities/{id}", s.authed(mod, auth.Admin, s.putEntity))
	handle("DELETE /v1/entities/{id}", s.authed(mod, auth.Admin, s.deleteEntity))
	handle("GET /v1/entities/{id}/records", s.authed(mod, auth.Edit, s.listRecords)) // records hold personal data
	handle("POST /v1/entities/{id}/records", s.authed(mod, auth.Edit, s.createRecord))
	handle("DELETE /v1/entities/{id}/records/{record}", s.authed(mod, auth.Edit, s.deleteRecord))
	handle("GET /v1/entities/{id}/export", s.authed(mod, auth.Edit, s.exportRecords))

	handle("GET /s/{tenant}/{site}/formulario/{id}", s.shopForm)
	handle("POST /s/{tenant}/api/forms/{id}", s.shopFormSubmit)
}

func (e *Entity) normalize() string {
	e.Name, e.Description, e.Success = strings.TrimSpace(e.Name), strings.TrimSpace(e.Description), strings.TrimSpace(e.Success)
	switch {
	case !entityID.MatchString(e.ID) || len(e.ID) > 40:
		return "El identificador solo lleva minúsculas, números y guiones (por ejemplo solicitudes-mayoreo)"
	case e.Name == "" || len(e.Name) > 80:
		return "Escribe el nombre (hasta 80 caracteres)"
	case len(e.Description) > 300 || len(e.Success) > 300:
		return "La descripción y el mensaje de confirmación admiten hasta 300 caracteres"
	case len(e.Fields) == 0 || len(e.Fields) > 30:
		return "Agrega entre 1 y 30 campos"
	}
	seen := map[string]bool{}
	for i := range e.Fields {
		f := &e.Fields[i]
		f.Key, f.Label = strings.TrimSpace(f.Key), strings.TrimSpace(f.Label)
		switch {
		case !fieldKey.MatchString(f.Key):
			return "La clave “" + f.Key + "” debe empezar con letra y llevar solo minúsculas, números y guion bajo"
		case seen[f.Key]:
			return "La clave “" + f.Key + "” está repetida"
		case f.Label == "" || len(f.Label) > 80:
			return "Escribe la etiqueta del campo “" + f.Key + "”"
		case !fieldTypes[f.Type]:
			return "Tipo de campo desconocido en “" + f.Key + "”"
		}
		seen[f.Key] = true
		if f.Type == "select" {
			var opts []string
			for _, o := range f.Options {
				if o = strings.TrimSpace(o); o != "" && len(o) <= 80 {
					opts = append(opts, o)
				}
			}
			if len(opts) < 2 || len(opts) > 50 {
				return "El campo “" + f.Label + "” necesita entre 2 y 50 opciones"
			}
			f.Options = opts
		} else {
			f.Options = nil
		}
	}
	return ""
}

// validate checks a submission against the fields and returns the clean values.
func (e *Entity) validate(in map[string]string) (map[string]string, []apierr.FieldError) {
	out := map[string]string{}
	var errs []apierr.FieldError
	bad := func(f EntityField, m string) {
		errs = append(errs, apierr.FieldError{Field: f.Key, Code: "invalid", Message: m})
	}
	for _, f := range e.Fields {
		v := strings.TrimSpace(in[f.Key])
		if f.Type == "checkbox" {
			if v == "true" || v == "on" || v == "1" || v == "sí" {
				out[f.Key] = "sí"
			} else if f.Required {
				bad(f, "Marca “"+f.Label+"” para continuar")
			} else {
				out[f.Key] = "no"
			}
			continue
		}
		if v == "" {
			if f.Required {
				bad(f, "Falta “"+f.Label+"”")
			}
			continue
		}
		limit := 300
		if f.Type == "textarea" {
			limit = 4000
		}
		switch {
		case utf8.RuneCountInString(v) > limit:
			bad(f, "“"+f.Label+"” es demasiado largo")
		case f.Type == "email":
			if a, err := mail.ParseAddress(v); err != nil || a.Address != v {
				bad(f, "Escribe un correo válido en “"+f.Label+"”")
			}
			v = strings.ToLower(v)
		case f.Type == "phone":
			d := 0
			for _, r := range v {
				if r >= '0' && r <= '9' {
					d++
				}
			}
			if d < 7 || d > 15 {
				bad(f, "Escribe un teléfono válido en “"+f.Label+"”")
			}
		case f.Type == "number":
			if _, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64); err != nil {
				bad(f, "“"+f.Label+"” debe ser un número")
			}
		case f.Type == "select":
			ok := false
			for _, o := range f.Options {
				ok = ok || o == v
			}
			if !ok {
				bad(f, "Elige una opción de “"+f.Label+"”")
			}
		}
		out[f.Key] = v
	}
	return out, errs
}

func (s *Server) entity(r *http.Request, tenant, id string) (Entity, bool, error) {
	var e Entity
	ok, err := s.DB.GetDoc(r.Context(), tenant, "entities", id, &e)
	e.ID = id
	return e, ok, err
}

func (s *Server) listEntities(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	docs, err := s.DB.ListDocs(r.Context(), tenant, "entities")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	counts := map[string]int{}
	err = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT entity, count(*)::int FROM entity_records WHERE tenant_id=$1 GROUP BY entity`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			counts[k] = n
		}
		return rows.Err()
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]Entity, 0, len(docs))
	for _, d := range docs {
		var e Entity
		if json.Unmarshal(d.Data, &e) == nil {
			e.ID, e.Records = d.ID, counts[d.ID]
			out = append(out, e)
		}
	}
	base := ""
	if t, ok := s.Proj.Tenant(tenant); ok {
		origin, _ := s.origin(r, t.Site())
		base = origin + "/s/" + tenant + "/main/formulario/"
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "formBase": base})
}

func (s *Server) putEntity(w http.ResponseWriter, r *http.Request) {
	var e Entity
	if !decode(w, r, &e) {
		return
	}
	e.ID, e.Records = r.PathValue("id"), 0
	if msg := e.normalize(); msg != "" {
		apierr.BadRequest(w, msg)
		return
	}
	p := principal(r)
	if err := s.DB.SetDoc(r.Context(), p.Tenant, p.Actor(), "entities", e.ID, "", e); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, e)
}

func (s *Server) deleteEntity(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := r.PathValue("id")
	err := s.DB.RemoveDoc(r.Context(), p.Tenant, p.Actor(), "entities", id, "")
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Entidad no encontrada")
		return
	}
	if err == nil {
		err = s.DB.Tx(r.Context(), p.Tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(r.Context(), `DELETE FROM entity_records WHERE tenant_id=$1 AND entity=$2`, p.Tenant, id)
			return err
		})
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) insertRecord(r *http.Request, tenant, entity, source string, data map[string]string) (EntityRecord, error) {
	rec := EntityRecord{ID: store.NewID("rec_"), Data: data, Source: source}
	plain, _ := json.Marshal(data)
	enc, err := s.Keys.Encrypt(tenant, plain, "entity:"+entity+":"+rec.ID)
	if err != nil {
		return rec, err
	}
	err = s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM entity_records WHERE tenant_id=$1 AND entity=$2`, tenant, entity).Scan(&n); err != nil {
			return err
		}
		if n >= 100000 {
			return store.ErrConflict
		}
		return tx.QueryRow(r.Context(), `INSERT INTO entity_records (tenant_id, entity, id, data, source) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`,
			tenant, entity, rec.ID, enc, source).Scan(&rec.CreatedAt)
	})
	return rec, err
}

func (s *Server) records(r *http.Request, tenant, entity string, before time.Time, limit int) ([]EntityRecord, error) {
	var out []EntityRecord
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		var b *time.Time
		if !before.IsZero() {
			b = &before
		}
		rows, err := tx.Query(r.Context(), `SELECT id, data, source, created_at FROM entity_records WHERE tenant_id=$1 AND entity=$2 AND ($3::timestamptz IS NULL OR created_at < $3)
			ORDER BY created_at DESC, id DESC LIMIT $4`, tenant, entity, b, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var rec EntityRecord
			var enc []byte
			if err := rows.Scan(&rec.ID, &enc, &rec.Source, &rec.CreatedAt); err != nil {
				return err
			}
			rec.Data = map[string]string{}
			if plain, err := s.Keys.Decrypt(tenant, enc, "entity:"+entity+":"+rec.ID); err == nil {
				_ = json.Unmarshal(plain, &rec.Data)
			}
			out = append(out, rec)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	tenant, id := principal(r).Tenant, r.PathValue("id")
	e, ok, err := s.entity(r, tenant, id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !ok {
		apierr.NotFound(w, "Entidad no encontrada")
		return
	}
	limit := queryLimit(r, 50, 200)
	var before time.Time
	if c := r.URL.Query().Get("cursor"); c != "" {
		before, _ = time.Parse(time.RFC3339Nano, c)
	}
	list, err := s.records(r, tenant, id, before, limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, r, http.StatusOK, map[string]any{"entity": e, "items": nonNil(list), "nextCursor": next})
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) {
	tenant, id := principal(r).Tenant, r.PathValue("id")
	e, ok, err := s.entity(r, tenant, id)
	if err != nil || !ok {
		apierr.NotFound(w, "Entidad no encontrada")
		return
	}
	var body struct {
		Data map[string]string `json:"data"`
	}
	if !decode(w, r, &body) {
		return
	}
	data, errs := e.validate(body.Data)
	if len(errs) > 0 {
		apierr.BadRequest(w, errs[0].Message, errs...)
		return
	}
	rec, err := s.insertRecord(r, tenant, id, "admin", data)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, rec)
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var n int64
	err := s.DB.Tx(r.Context(), p.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM entity_records WHERE tenant_id=$1 AND entity=$2 AND id=$3`, p.Tenant, r.PathValue("id"), r.PathValue("record"))
		if err != nil {
			return err
		}
		n = ct.RowsAffected()
		return store.Audit(r.Context(), tx, p.Tenant, p.Actor(), "entity.record_deleted", r.PathValue("record"), map[string]any{"entity": r.PathValue("id")})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if n == 0 {
		apierr.NotFound(w, "Registro no encontrado")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) exportRecords(w http.ResponseWriter, r *http.Request) {
	tenant, id := principal(r).Tenant, r.PathValue("id")
	e, ok, err := s.entity(r, tenant, id)
	if err != nil || !ok {
		apierr.NotFound(w, "Entidad no encontrada")
		return
	}
	list, err := s.records(r, tenant, id, time.Time{}, 50000)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("\xEF\xBB\xBF"))
	cw := csv.NewWriter(w)
	head := []string{"fecha", "origen"}
	for _, f := range e.Fields {
		head = append(head, f.Label)
	}
	_ = cw.Write(head)
	for _, rec := range list {
		row := []string{rec.CreatedAt.Format("2006-01-02 15:04"), rec.Source}
		for _, f := range e.Fields {
			row = append(row, csvSafeCell(rec.Data[f.Key]))
		}
		_ = cw.Write(row)
	}
	cw.Flush()
}

type formPage struct {
	pageData
	Entity *Entity
}

// shopForm renders the public form of an entity.
func (s *Server) shopForm(w http.ResponseWriter, r *http.Request) {
	_, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	pg := formPage{pageData: pd}
	e, found, err := s.entity(r, pd.Tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if found && e.Public {
		pg.Entity, pg.Title, pg.Description = &e, e.Name, e.Description
	} else {
		pg.Title, pg.NoIndex = "Formulario no encontrado", true
		w.WriteHeader(http.StatusNotFound)
	}
	s.renderShop(w, "form", pg, false)
}

func (s *Server) shopFormSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, formLimiter) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Origen no permitido"})
		return
	}
	tenant := r.PathValue("tenant")
	e, ok, err := s.entity(r, tenant, r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !ok || !e.Public {
		apierr.NotFound(w, "Formulario no encontrado")
		return
	}
	var body struct {
		Data    map[string]string `json:"data"`
		Website string            `json:"website"` // hidden field: only bots fill it
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Website != "" {
		w.WriteHeader(http.StatusNoContent) // pretend it worked
		return
	}
	data, errs := e.validate(body.Data)
	if len(errs) > 0 {
		apierr.BadRequest(w, errs[0].Message, errs...)
		return
	}
	if _, err := s.insertRecord(r, tenant, e.ID, "form", data); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apierr.Write(w, apierr.Problem{Status: http.StatusServiceUnavailable, Code: "form_full", Detail: "Por ahora no podemos recibir más respuestas. Escríbenos directamente"})
			return
		}
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"message": firstNonEmpty(e.Success, "¡Gracias! Recibimos tu información.")})
}
