package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/store"
)

// Gifts: wrapping and card messages at checkout, and gift registries. A
// registry belongs to a signed-in shopper, lists what they wish for and is
// shared with a link; guests buy from it and may send the gift to the
// registry's address without ever seeing it. What shows as bought is derived
// from paid orders, so cancellations and returns put gifts back on the list.

type registryItem struct {
	SKU       string `json:"sku"`
	Wanted    int    `json:"wanted"`
	Bought    int    `json:"bought"`
	Title     string `json:"title,omitempty"`
	Price     string `json:"price,omitempty"`
	Handle    string `json:"handle,omitempty"`
	Available bool   `json:"available"`
}

type registry struct {
	ID         string         `json:"id"`
	Handle     string         `json:"handle"`
	Title      string         `json:"title"`
	Kind       string         `json:"kind"`
	EventDate  string         `json:"eventDate"`
	Message    string         `json:"message"`
	Listed     bool           `json:"listed"`
	Status     string         `json:"status"`
	HasAddress bool           `json:"hasAddress"`
	CustomerID string         `json:"customerId,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
	Wanted     int            `json:"wanted"`
	Bought     int            `json:"bought"`
	Items      []registryItem `json:"items,omitempty"`
	ShipTo     *registryShip  `json:"shipTo,omitempty"` // only for its owner
}

type registryShip struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	PostalCode string `json:"postalCode"`
}

var registryKinds = map[string]string{"boda": "Boda", "bebe": "Bebé", "cumple": "Cumpleaños", "otro": "Celebración"}

const registryCols = `r.id, r.handle, r.title, r.kind, COALESCE(to_char(r.event_date,'YYYY-MM-DD'),''), r.message, r.listed, r.status, r.ship_to IS NOT NULL, r.customer_id, r.created_at,
	COALESCE((SELECT sum(wanted) FROM gift_registry_items i WHERE i.tenant_id=r.tenant_id AND i.registry_id=r.id),0),
	COALESCE((SELECT sum(LEAST(bought,wanted)) FROM gift_registry_items i WHERE i.tenant_id=r.tenant_id AND i.registry_id=r.id),0)`

func scanRegistry(row pgx.CollectableRow) (registry, error) {
	var g registry
	return g, row.Scan(&g.ID, &g.Handle, &g.Title, &g.Kind, &g.EventDate, &g.Message, &g.Listed, &g.Status, &g.HasAddress, &g.CustomerID, &g.CreatedAt, &g.Wanted, &g.Bought)
}

func (s *Server) routesGifts(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/gift-options", s.authed("settings", auth.Read, noStorefront(s.getGiftOptions)))
	handle("PUT /v1/gift-options", s.authed("settings", auth.Admin, s.putGiftOptions))
	handle("GET /v1/gift-registries", s.authed("customers", auth.Read, noStorefront(s.listRegistries)))
	handle("GET /v1/gift-registries/{id}", s.authed("customers", auth.Read, noStorefront(s.getRegistry)))
	handle("PUT /v1/gift-registries/{id}/status", s.authed("customers", auth.Edit, s.setRegistryStatus))
	handle("GET /s/{tenant}/api/registries", s.shopFindRegistries)
	handle("GET /s/{tenant}/api/account/registries", s.shopMyRegistries)
	handle("POST /s/{tenant}/api/account/registries", s.shopSaveRegistry)
	handle("PUT /s/{tenant}/api/account/registries/{id}", s.shopSaveRegistry)
	handle("PUT /s/{tenant}/api/account/registries/{id}/items", s.shopRegistryItem)
	handle("GET /s/{tenant}/{site}/mesa", s.shopRegistryPage)
	handle("GET /s/{tenant}/{site}/mesa/{handle}", s.shopRegistryPage)
}

func (s *Server) giftOptions(ctx context.Context, tenant string) cart.GiftOptions {
	if t, ok := s.Proj.Tenant(tenant); ok {
		return t.GiftOptions() // kept in memory by the projector
	}
	var g cart.GiftOptions
	_, _ = s.DB.GetDoc(ctx, tenant, "gift_options", "config", &g)
	return g
}

func (s *Server) getGiftOptions(w http.ResponseWriter, r *http.Request) {
	g := s.giftOptions(r.Context(), principal(r).Tenant)
	if g.MessageMax == 0 {
		g.MessageMax = 250
	}
	httpx.JSON(w, r, http.StatusOK, g)
}

func (s *Server) putGiftOptions(w http.ResponseWriter, r *http.Request) {
	var in cart.GiftOptions
	if !decode(w, r, &in) {
		return
	}
	g, err := in.Clean()
	if err != nil {
		apierr.BadRequest(w, err.Error())
		return
	}
	pr := principal(r)
	if err := s.DB.SetDoc(r.Context(), pr.Tenant, pr.Actor(), "gift_options", "config", store.KeyGiftOptions, g); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, g)
}

func (s *Server) listRegistries(w http.ResponseWriter, r *http.Request) {
	t := principal(r).Tenant
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var out []registry
	err := s.DB.Tx(r.Context(), t, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT `+registryCols+` FROM gift_registries r WHERE r.tenant_id=$1 AND ($2='' OR r.title ILIKE '%'||$2||'%' OR r.handle=$2)
			ORDER BY r.created_at DESC LIMIT 200`, t, q)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanRegistry)
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if out == nil {
		out = []registry{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// loadRegistry reads one registry by id or handle with its items.
func (s *Server) loadRegistry(ctx context.Context, tenant, where string, arg string) (registry, []byte, error) {
	var g registry
	var ship []byte
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+registryCols+`, r.ship_to FROM gift_registries r WHERE r.tenant_id=$1 AND r.`+where+`=$2`, tenant, arg)
		if err != nil {
			return err
		}
		g, err = pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (registry, error) {
			var x registry
			return x, row.Scan(&x.ID, &x.Handle, &x.Title, &x.Kind, &x.EventDate, &x.Message, &x.Listed, &x.Status, &x.HasAddress, &x.CustomerID, &x.CreatedAt, &x.Wanted, &x.Bought, &ship)
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, `SELECT sku, wanted, bought FROM gift_registry_items WHERE tenant_id=$1 AND registry_id=$2 ORDER BY added_at`, tenant, g.ID)
		if err != nil {
			return err
		}
		g.Items, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (registryItem, error) {
			var it registryItem
			return it, row.Scan(&it.SKU, &it.Wanted, &it.Bought)
		})
		return err
	})
	if g.Items == nil {
		g.Items = []registryItem{}
	}
	return g, ship, err
}

// describeItems fills titles, prices and availability from the catalog.
func (s *Server) describeItems(tenant string, items []registryItem) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return
	}
	cur := money.Currency("MXN")
	if ch, ok := t.Channel(mainChannel); ok && ch.Currency != "" {
		cur = ch.Currency
	}
	for i := range items {
		v, p, err := t.Catalog.VariantBySKU(items[i].SKU)
		if err != nil {
			items[i].Title = items[i].SKU
			continue
		}
		items[i].Title, items[i].Handle = p.Title, p.Handle
		if v.Title != "" && len(p.Variants) > 1 {
			items[i].Title += " · " + v.Title
		}
		if q, ok := t.Pricing.Resolve(mainChannel, v.SKU, 1); ok {
			items[i].Price = pesos(money.New(q.Unit.Amount, cur))
		}
		items[i].Available = p.Status == "active" && t.Inventory.Available(v.SKU) > 0
	}
}

func (s *Server) getRegistry(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	g, _, err := s.loadRegistry(r.Context(), pr.Tenant, "id", r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Mesa de regalos no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.describeItems(pr.Tenant, g.Items)
	var orderIDs []string
	_ = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id FROM orders WHERE tenant_id=$1 AND registry_id=$2 ORDER BY created_at DESC LIMIT 100`, pr.Tenant, g.ID)
		if err != nil {
			return err
		}
		orderIDs, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if orderIDs == nil {
		orderIDs = []string{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"registry": g, "orders": orderIDs, "url": s.registryURL(pr.Tenant, g.Handle)})
}

func (s *Server) registryURL(tenant, handle string) string {
	return "/s/" + tenant + "/main/mesa/" + handle
}

func (s *Server) setRegistryStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Status != "active" && in.Status != "closed" {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "status", Code: "invalid", Message: "La mesa está activa o cerrada"})
		return
	}
	pr := principal(r)
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `UPDATE gift_registries SET status=$3, updated_at=now() WHERE tenant_id=$1 AND id=$2`, pr.Tenant, r.PathValue("id"), in.Status)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "registry.status", r.PathValue("id"), map[string]any{"status": in.Status})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Mesa de regalos no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.getRegistry(w, r)
}

// --- shopper side ---

func (s *Server) registriesOn(w http.ResponseWriter, r *http.Request) bool {
	if g := s.giftOptions(r.Context(), r.PathValue("tenant")); !g.Registries {
		apierr.NotFound(w, "Esta tienda no ofrece mesas de regalos")
		return false
	}
	return true
}

func (s *Server) shopFindRegistries(w http.ResponseWriter, r *http.Request) {
	if !s.knownTenant(w, r) || s.limited(w, r, accountLimiter) || !s.registriesOn(w, r) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []registry{}
	if utf8.RuneCountInString(q) >= 3 && len(q) <= 80 {
		var err error
		out, err = s.findRegistries(r.Context(), r.PathValue("tenant"), q)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

// findRegistries searches the listed, active registries by name (no addresses, no owners).
func (s *Server) findRegistries(ctx context.Context, tenant, q string) ([]registry, error) {
	var out []registry
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+registryCols+` FROM gift_registries r WHERE r.tenant_id=$1 AND r.status='active' AND r.listed
			AND (r.title ILIKE '%'||$2||'%' OR r.handle=lower($2)) ORDER BY r.event_date NULLS LAST LIMIT 20`, tenant, q)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, scanRegistry)
		return err
	})
	for i := range out {
		out[i].CustomerID, out[i].HasAddress = "", false
	}
	if out == nil {
		out = []registry{}
	}
	return out, err
}

func (s *Server) shopMyRegistries(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok {
		return
	}
	tenant := r.PathValue("tenant")
	w.Header().Set("Cache-Control", "no-store")
	opts := s.giftOptions(r.Context(), tenant)
	var ids []string
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(r.Context(), `SELECT id FROM gift_registries WHERE tenant_id=$1 AND customer_id=$2 ORDER BY created_at DESC LIMIT 20`, tenant, cid)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := []registry{}
	for _, id := range ids {
		g, ship, err := s.loadRegistry(r.Context(), tenant, "id", id)
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		s.describeItems(tenant, g.Items)
		g.ShipTo = s.openShip(tenant, g.ID, ship)
		g.CustomerID = ""
		out = append(out, g)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "enabled": opts.Registries})
}

func (s *Server) openShip(tenant, id string, ct []byte) *registryShip {
	if len(ct) == 0 {
		return nil
	}
	plain, err := s.Keys.Decrypt(tenant, ct, "registry:"+id)
	if err != nil {
		return nil
	}
	var sh registryShip
	if json.Unmarshal(plain, &sh) != nil {
		return nil
	}
	return &sh
}

func (s *Server) shopSaveRegistry(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok || !s.registriesOn(w, r) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "cross_site", Detail: "Solicitud de otro sitio"})
		return
	}
	var in struct {
		Title     string        `json:"title"`
		Kind      string        `json:"kind"`
		EventDate string        `json:"eventDate"`
		Message   string        `json:"message"`
		Listed    bool          `json:"listed"`
		Status    string        `json:"status"`
		ShipTo    *registryShip `json:"shipTo"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Title, in.Message = strings.TrimSpace(in.Title), strings.TrimSpace(in.Message)
	var errs []apierr.FieldError
	if n := utf8.RuneCountInString(in.Title); n < 3 || n > 80 {
		errs = append(errs, apierr.FieldError{Field: "title", Code: "invalid", Message: "Escribe un nombre de 3 a 80 caracteres"})
	}
	if _, ok := registryKinds[in.Kind]; !ok {
		in.Kind = "otro"
	}
	if in.EventDate != "" {
		if _, err := time.Parse("2006-01-02", in.EventDate); err != nil {
			errs = append(errs, apierr.FieldError{Field: "eventDate", Code: "invalid", Message: "Fecha inválida"})
		}
	}
	if utf8.RuneCountInString(in.Message) > 500 {
		errs = append(errs, apierr.FieldError{Field: "message", Code: "too_long", Message: "El mensaje admite hasta 500 caracteres"})
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "closed" {
		errs = append(errs, apierr.FieldError{Field: "status", Code: "invalid", Message: "La mesa está activa o cerrada"})
	}
	if sh := in.ShipTo; sh != nil {
		sh.Name, sh.Address, sh.PostalCode = strings.TrimSpace(sh.Name), strings.TrimSpace(sh.Address), strings.TrimSpace(sh.PostalCode)
		switch {
		case sh.Address == "" && sh.PostalCode == "":
			in.ShipTo = nil
		case sh.Name == "" || len(sh.Name) > 120 || sh.Address == "" || len(sh.Address) > 400 || !postalRe.MatchString(sh.PostalCode):
			errs = append(errs, apierr.FieldError{Field: "shipTo", Code: "invalid", Message: "Escribe nombre, dirección y código postal para recibir los regalos"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "La mesa tiene errores", errs...)
		return
	}
	tenant := r.PathValue("tenant")
	id := r.PathValue("id")
	created := id == ""
	if created {
		id = newRandomID("reg_")
	}
	var ship []byte
	if in.ShipTo != nil {
		b, _ := json.Marshal(in.ShipTo)
		var err error
		if ship, err = s.Keys.Encrypt(tenant, b, "registry:"+id); err != nil {
			internalError(w, s.Log, err)
			return
		}
	}
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		if created {
			var n int
			if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM gift_registries WHERE tenant_id=$1 AND customer_id=$2`, tenant, cid).Scan(&n); err != nil {
				return err
			}
			if n >= 10 {
				return errRegistryLimit
			}
			handle := slugify(in.Title)
			if len(handle) > 50 {
				handle = strings.Trim(handle[:50], "-")
			}
			handle += "-" + strings.ToLower(newRandomID("")[:5])
			_, err := tx.Exec(r.Context(), `INSERT INTO gift_registries (tenant_id, id, handle, customer_id, title, kind, event_date, message, ship_to, listed, status)
				VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::date,$8,$9,$10,$11)`, tenant, id, handle, cid, in.Title, in.Kind, in.EventDate, in.Message, ship, in.Listed, in.Status)
			return err
		}
		ct, err := tx.Exec(r.Context(), `UPDATE gift_registries SET title=$4, kind=$5, event_date=NULLIF($6,'')::date, message=$7, ship_to=$8, listed=$9, status=$10, updated_at=now()
			WHERE tenant_id=$1 AND id=$2 AND customer_id=$3`, tenant, id, cid, in.Title, in.Kind, in.EventDate, in.Message, ship, in.Listed, in.Status)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		return err
	})
	switch {
	case errors.Is(err, errRegistryLimit):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "limit", Detail: "Puedes tener hasta 10 mesas de regalos"})
		return
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Mesa de regalos no encontrada")
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	g, ship2, err := s.loadRegistry(r.Context(), tenant, "id", id)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.describeItems(tenant, g.Items)
	g.ShipTo, g.CustomerID = s.openShip(tenant, id, ship2), ""
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.JSON(w, r, status, g)
}

var errRegistryLimit = errors.New("registry limit")

var postalRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{1,9}$`)

// giftFieldError turns a gift the store cannot take into a field error.
func giftFieldError(err error) (apierr.FieldError, bool) {
	var ge cart.GiftError
	if errors.As(err, &ge) {
		return apierr.FieldError{Field: "gift.message", Code: "invalid", Message: ge.Error()}, true
	}
	return apierr.FieldError{}, false
}

func (s *Server) shopRegistryItem(w http.ResponseWriter, r *http.Request) {
	cid, ok := s.shopperAPI(w, r)
	if !ok || !s.registriesOn(w, r) {
		return
	}
	if !sameOrigin(r) {
		apierr.Write(w, apierr.Problem{Status: http.StatusForbidden, Code: "cross_site", Detail: "Solicitud de otro sitio"})
		return
	}
	var in struct {
		SKU    string `json:"sku"`
		Wanted int    `json:"wanted"`
	}
	if !decode(w, r, &in) {
		return
	}
	tenant := r.PathValue("tenant")
	t, _ := s.Proj.Tenant(tenant)
	if in.Wanted < 0 || in.Wanted > 999 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "wanted", Code: "invalid", Message: "Pide de 1 a 999 piezas"})
		return
	}
	if _, p, err := t.Catalog.VariantBySKU(in.SKU); in.Wanted > 0 && (err != nil || p.Status != "active") {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "sku", Code: "unknown", Message: "El producto no existe o no está a la venta"})
		return
	}
	err := s.DB.Tx(r.Context(), tenant, func(tx pgx.Tx) error {
		var mine bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM gift_registries WHERE tenant_id=$1 AND id=$2 AND customer_id=$3)`, tenant, r.PathValue("id"), cid).Scan(&mine); err != nil {
			return err
		}
		if !mine {
			return store.ErrNotFound
		}
		if in.Wanted == 0 {
			_, err := tx.Exec(r.Context(), `DELETE FROM gift_registry_items WHERE tenant_id=$1 AND registry_id=$2 AND sku=$3`, tenant, r.PathValue("id"), in.SKU)
			return err
		}
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM gift_registry_items WHERE tenant_id=$1 AND registry_id=$2 AND sku<>$3`, tenant, r.PathValue("id"), in.SKU).Scan(&n); err != nil {
			return err
		}
		if n >= 200 {
			return errRegistryLimit
		}
		// What guests already bought stays counted when the item comes back.
		_, err := tx.Exec(r.Context(), `INSERT INTO gift_registry_items (tenant_id, registry_id, sku, wanted, bought)
			VALUES ($1,$2,$3,$4, COALESCE((SELECT sum(l.qty - l.cancelled_qty - l.returned_qty) FROM order_lines l JOIN orders o ON o.tenant_id=l.tenant_id AND o.id=l.order_id
				WHERE o.tenant_id=$1 AND o.registry_id=$2 AND o.status IN ('paid','fulfilled') AND l.sku=$3),0))
			ON CONFLICT (tenant_id, registry_id, sku) DO UPDATE SET wanted=EXCLUDED.wanted`, tenant, r.PathValue("id"), in.SKU, in.Wanted)
		return err
	})
	switch {
	case errors.Is(err, errRegistryLimit):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "limit", Detail: "Una mesa admite hasta 200 productos"})
		return
	case errors.Is(err, store.ErrNotFound):
		apierr.NotFound(w, "Mesa de regalos no encontrada")
		return
	case err != nil:
		internalError(w, s.Log, err)
		return
	}
	g, _, err := s.loadRegistry(r.Context(), tenant, "id", r.PathValue("id"))
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.describeItems(tenant, g.Items)
	g.CustomerID = ""
	httpx.JSON(w, r, http.StatusOK, g)
}

// applyRegistry resolves the registry a purchase is for. Sending to the
// registry's address fills the address on the server: the guest never sees it.
func (s *Server) applyRegistry(ctx context.Context, tenant, handle string, ship bool, postal *string, address *string) (string, *apierr.FieldError) {
	if handle == "" {
		if ship {
			return "", &apierr.FieldError{Field: "registry", Code: "required", Message: "Elige la mesa de regalos"}
		}
		return "", nil
	}
	g, ct, err := s.loadRegistry(ctx, tenant, "handle", handle)
	if err != nil || g.Status != "active" {
		return "", &apierr.FieldError{Field: "registry", Code: "unknown", Message: "La mesa de regalos ya no está disponible"}
	}
	if ship {
		sh := s.openShip(tenant, g.ID, ct)
		if sh == nil {
			return "", &apierr.FieldError{Field: "shipToRegistry", Code: "unavailable", Message: "La mesa no tiene dirección de entrega"}
		}
		*postal = sh.PostalCode
		if address != nil {
			*address = "Para " + sh.Name + " (mesa de regalos): " + sh.Address
		}
	}
	return g.ID, nil
}

// registryCheckout prepares a shopper's purchase for a registry.
func (s *Server) registryCheckout(ctx context.Context, tenant string, req *orders.CheckoutRequest) *apierr.FieldError {
	req.RegistryID = ""
	id, fe := s.applyRegistry(ctx, tenant, req.Registry, req.ShipToRegistry, &req.PostalCode, &req.Customer.Address)
	req.RegistryID = id
	return fe
}

// --- registry pages ---

type registryPage struct {
	pageData
	Q        string
	Found    []registry
	Searched bool
	Reg      *registry
	KindName string
	Date     string
}

func (s *Server) shopRegistryPage(w http.ResponseWriter, r *http.Request) {
	t, pd, ok := s.shopContext(w, r)
	if !ok {
		return
	}
	if g := s.giftOptions(r.Context(), t.ID); !g.Registries {
		w.WriteHeader(http.StatusNotFound)
		pd.Title, pd.NoIndex = "Página no encontrada", true
		s.renderShop(w, "registry", registryPage{pageData: pd}, false)
		return
	}
	pg := registryPage{pageData: pd}
	pg.NoIndex = true // registries are personal: never in search engines
	h := r.PathValue("handle")
	if h == "" {
		pg.Title = "Mesa de regalos"
		pg.Q = strings.TrimSpace(r.URL.Query().Get("q"))
		if utf8.RuneCountInString(pg.Q) >= 3 && len(pg.Q) <= 80 && !s.limited(w, r, accountLimiter) {
			found, err := s.findRegistries(r.Context(), t.ID, pg.Q)
			if err != nil {
				internalError(w, s.Log, err)
				return
			}
			pg.Found, pg.Searched = found, true
		}
		s.renderShop(w, "registry", pg, false)
		return
	}
	g, _, err := s.loadRegistry(r.Context(), t.ID, "handle", h)
	if err != nil || g.Status != "active" {
		w.WriteHeader(http.StatusNotFound)
		pg.Title = "Mesa no disponible"
		s.renderShop(w, "registry", pg, false)
		return
	}
	s.describeItems(t.ID, g.Items)
	g.CustomerID = ""
	pg.Reg, pg.Title, pg.KindName = &g, g.Title, registryKinds[g.Kind]
	if d, err := time.Parse("2006-01-02", g.EventDate); err == nil {
		pg.Date = humanDay(d)
	}
	s.renderShop(w, "registry", pg, false)
}
