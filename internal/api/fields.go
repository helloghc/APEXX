package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/fields"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/store"
)

// Custom fields on customers, orders and companies. Definitions are store
// settings; values belong to each record and follow the permissions of its
// module. Personal values are encrypted and only shown in full to people who
// can edit that module.

// fieldModule is the permission module that guards the values of a resource.
var fieldModule = map[string]string{"customer": "customers", "order": "orders", "company": "customers"}

// ownerTable is where a record of each resource lives.
var ownerTable = map[string]string{"customer": "customers", "order": "orders", "company": "companies"}

func (s *Server) fieldDefs(ctx context.Context, tenant, resource string) (fields.Defs, error) {
	var d fields.Defs
	_, err := s.DB.GetDoc(ctx, tenant, "fields", resource, &d)
	return d, err
}

func resourceOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	res := r.PathValue("resource")
	if _, ok := fields.Resources[res]; !ok {
		apierr.NotFound(w, "Solo hay campos propios en customer, order y company")
		return "", false
	}
	return res, true
}

func (s *Server) getFieldDefs(w http.ResponseWriter, r *http.Request) {
	res, ok := resourceOf(w, r)
	if !ok {
		return
	}
	d, err := s.fieldDefs(r.Context(), principal(r).Tenant, res)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if d.Fields == nil {
		d.Fields = []fields.Def{}
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

func (s *Server) putFieldDefs(w http.ResponseWriter, r *http.Request) {
	res, ok := resourceOf(w, r)
	if !ok {
		return
	}
	var d fields.Defs
	if !decode(w, r, &d) {
		return
	}
	if err := d.Clean(); err != nil {
		var fe2 fields.FieldError
		if errors.As(err, &fe2) {
			apierr.BadRequest(w, "", fe(fe2.Field, "invalid", fe2.Message))
			return
		}
		internalError(w, s.Log, err)
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "fields", res, "", d); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, d)
}

// fieldOwner checks that the record exists in this store (RLS keeps other
// stores out; this keeps typos out).
func fieldOwner(ctx context.Context, tx pgx.Tx, tenant, resource, id string) error {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM `+ownerTable[resource]+` WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

func (s *Server) getFieldValues(w http.ResponseWriter, r *http.Request) {
	res, ok := resourceOf(w, r)
	if !ok {
		return
	}
	pr := principal(r)
	if !pr.Permissions.Allows(fieldModule[res], auth.Read) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "No tienes acceso a este módulo"})
		return
	}
	id := r.PathValue("id")
	var data []byte
	var secret []byte
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if err := fieldOwner(r.Context(), tx, pr.Tenant, res, id); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `SELECT data, secret FROM custom_values WHERE tenant_id=$1 AND resource=$2 AND owner_id=$3`, pr.Tenant, res, id).Scan(&data, &secret)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "No existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	values := map[string]string{}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &values)
	}
	if len(secret) > 0 {
		plain, err := s.Keys.Decrypt(pr.Tenant, secret, "fields:"+res+":"+id)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		var p map[string]string
		_ = json.Unmarshal(plain, &p)
		full := pr.Permissions.Allows(fieldModule[res], auth.Edit)
		for k, v := range p {
			if full {
				values[k] = v
			} else {
				values[k] = mask(v)
			}
		}
	}
	defs, _ := s.fieldDefs(r.Context(), pr.Tenant, res)
	if defs.Fields == nil {
		defs.Fields = []fields.Def{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"fields": defs.Fields, "values": values})
}

func (s *Server) putFieldValues(w http.ResponseWriter, r *http.Request) {
	res, ok := resourceOf(w, r)
	if !ok {
		return
	}
	pr := principal(r)
	if !pr.Permissions.Allows(fieldModule[res], auth.Edit) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Tu acceso no permite cambiar estos datos"})
		return
	}
	var values map[string]string
	if !decode(w, r, &values) {
		return
	}
	defs, err := s.fieldDefs(r.Context(), pr.Tenant, res)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	plain, personal, err := defs.Split(values)
	var fe2 fields.FieldError
	if errors.As(err, &fe2) {
		apierr.BadRequest(w, "", fe(fe2.Field, "invalid", fe2.Message))
		return
	}
	id := r.PathValue("id")
	pj, _ := json.Marshal(plain)
	var secret []byte
	if len(personal) > 0 {
		sj, _ := json.Marshal(personal)
		if secret, err = s.Keys.Encrypt(pr.Tenant, sj, "fields:"+res+":"+id); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	err = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if err := fieldOwner(r.Context(), tx, pr.Tenant, res, id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO custom_values (tenant_id, resource, owner_id, data, secret) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, resource, owner_id) DO UPDATE SET data=EXCLUDED.data, secret=EXCLUDED.secret, updated_at=now()`, pr.Tenant, res, id, pj, secret); err != nil {
			return err
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "fields."+res+".update", id, map[string]any{"fields": keys})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "No existe")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mask hides most of a personal value for people who may only read.
func mask(v string) string {
	r := []rune(v)
	if len(r) <= 2 {
		return "••"
	}
	return string(r[0]) + "•••" + string(r[len(r)-1])
}
