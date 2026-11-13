package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/media"
	"github.com/helloghc/apexx/internal/store"
)

// Products in and out of a sandbox. A light sandbox starts without
// products: the team brings the ones it wants to try from production, and
// can send them, as they are, to another sandbox of the store.

const maxMoveProducts = 500

// productList reads the products of a bring or send request.
func productList(w http.ResponseWriter, ids []string) ([]string, bool) {
	var out []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 || len(out) > maxMoveProducts {
		apierr.BadRequest(w, "Elige los productos", apierr.FieldError{Field: "products", Code: "invalid", Message: "Elige de 1 a 500 productos"})
		return nil, false
	}
	return out, true
}

// readyIn checks the sandbox is ready and the person may read production's
// catalog (they bring from it, or compare with it).
func (s *Server) readyIn(w http.ResponseWriter, r *http.Request, parent string, sb store.Sandbox) bool {
	if sb.Status != "ready" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_ready", Detail: "El sandbox «" + sb.Name + "» aún no está listo"})
		return false
	}
	if pp, ok := s.memberIn(r.Context(), parent, principal(r)); !ok || !pp.Permissions.Allows("catalog", auth.Read) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "forbidden", Detail: "Necesitas ver el catálogo de la tienda de producción"})
		return false
	}
	return true
}

// pickProducts lists products to choose from: production's (to bring) or
// the sandbox's own (to send), saying which are already in the sandbox.
func (s *Server) pickProducts(w http.ResponseWriter, r *http.Request) {
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Sandbox no encontrado") {
		return
	}
	if !s.readyIn(w, r, parent, sb) {
		return
	}
	tenant := parent
	if r.URL.Query().Get("from") == "sandbox" {
		tenant = sb.ID
	}
	items, total, err := s.DB.PickProducts(r.Context(), tenant, sb.ID, r.URL.Query().Get("q"), 50)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items, "total": total})
}

// bringProducts copies products of production into the sandbox. reset puts
// back as production has them the ones the sandbox already had.
func (s *Server) bringProducts(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		Products []string `json:"products"`
		Reset    bool     `json:"reset"`
	}
	if !decode(w, r, &body) {
		return
	}
	ids, ok := productList(w, body.Products)
	if !ok {
		return
	}
	parent, sb, err := s.targetSandbox(r)
	if s.storeError(w, err, "Sandbox no encontrado") {
		return
	}
	if !s.readyIn(w, r, parent, sb) {
		return
	}
	res, err := s.DB.BringProducts(r.Context(), sb.ID, ids, body.Reset)
	if s.moveError(w, err) {
		return
	}
	_ = s.DB.TouchSandbox(r.Context(), sb.ID)
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.bring", sb.ID, map[string]any{"products": ids, "reset": body.Reset, "brought": res.Products})
	})
	httpx.JSON(w, r, http.StatusOK, res)
}

// sendProducts copies products of this sandbox, as they are, to another
// sandbox of the store (replacing its version of them).
func (s *Server) sendProducts(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	var body struct {
		To       string   `json:"to"`
		Products []string `json:"products"`
	}
	if !decode(w, r, &body) {
		return
	}
	ids, ok := productList(w, body.Products)
	if !ok {
		return
	}
	parent, from, err := s.targetSandbox(r)
	if s.storeError(w, err, "Sandbox no encontrado") {
		return
	}
	if !s.readyIn(w, r, parent, from) {
		return
	}
	to, err := s.DB.SandboxByID(r.Context(), body.To)
	if err != nil || to.ParentID != parent || to.ID == from.ID {
		apierr.NotFound(w, "Elige otro sandbox de tu tienda")
		return
	}
	if to.Status != "ready" {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_ready", Detail: "El sandbox «" + to.Name + "» aún no está listo"})
		return
	}
	res, err := s.DB.SendProducts(r.Context(), from.ID, to.ID, ids)
	if s.moveError(w, err) {
		return
	}
	// Images uploaded in the source sandbox live with it: copied over. The
	// ones from production are read from production by both.
	if err := s.copySandboxMedia(r.Context(), parent, from.ID, to.ID, res.MediaIDs); err != nil {
		internalError(w, s.Log, err)
		return
	}
	_ = s.DB.TouchSandbox(r.Context(), to.ID)
	_ = s.DB.Tx(r.Context(), parent, func(tx pgx.Tx) error {
		return store.Audit(r.Context(), tx, parent, pr.Actor(), "sandbox.send", from.ID, map[string]any{"to": to.ID, "products": ids})
	})
	httpx.JSON(w, r, http.StatusOK, res)
}

// copySandboxMedia copies the files of media the source sandbox added (not
// in production) to the destination sandbox.
func (s *Server) copySandboxMedia(ctx context.Context, parent, from, to string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.DB.System.Query(ctx, `SELECT id FROM media WHERE tenant_id = $1 AND id = ANY($2)
		AND NOT EXISTS (SELECT 1 FROM media p WHERE p.tenant_id = $3 AND p.id = media.id)`, from, ids, parent)
	if err != nil {
		return err
	}
	own, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(own) == 0 {
		return err
	}
	cp, ok := s.Media.(media.Copier)
	if !ok {
		return errors.New("the media storage cannot copy files between stores")
	}
	for _, id := range own {
		if err := cp.CopyPrefix(ctx, from, to, id); err != nil {
			return err
		}
	}
	return nil
}

// moveError answers the errors of bringing or sending products.
func (s *Server) moveError(w http.ResponseWriter, err error) bool {
	var missing *store.ErrProductsNotFound
	switch {
	case err == nil:
		return false
	case errors.As(err, &missing):
		apierr.Write(w, apierr.Problem{Status: http.StatusNotFound, Code: "products_not_found", Detail: "No se encontraron estos productos: " + strings.Join(firstN(missing.IDs, 10), ", ")})
	case errors.Is(err, store.ErrSameHandle):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "same_handle",
			Detail: "Ya hay otro producto con la misma dirección (handle) en el sandbox. Cámbiale el handle a uno de los dos e inténtalo de nuevo. No se movió nada."})
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Sandbox no encontrado")
	default:
		internalError(w, s.Log, err)
	}
	return true
}
