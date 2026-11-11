package api

import (
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/logistics"
	"github.com/helloghc/apexx/internal/store"
)

var locationIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

type locationView struct {
	logistics.Location
	Usage store.LocationUsage `json:"usage"`
	// Registered is false for an id that has stock but no location data yet.
	Registered    bool     `json:"registered"`
	PickupOptions []string `json:"pickupOptions"`
}

func (s *Server) listLocations(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	c, err := s.DB.LoadLogistics(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	usage, err := s.DB.LocationUsages(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	out := make([]locationView, 0, len(c.Locations))
	seen := map[string]bool{}
	for _, l := range c.Locations {
		seen[l.ID] = true
		v := locationView{Location: l, Usage: usage[l.ID], Registered: true, PickupOptions: []string{}}
		for _, o := range c.Options {
			if o.Kind == logistics.KindPickup && o.LocationID == l.ID {
				v.PickupOptions = append(v.PickupOptions, o.Name)
			}
		}
		out = append(out, v)
	}
	for id, u := range usage {
		if !seen[id] {
			out = append(out, locationView{Location: logistics.Location{ID: id, Name: id, Kind: "warehouse", Active: true}, Usage: u, PickupOptions: []string{}})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	st, _, err := s.DB.Settings(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	alerts, err := s.DB.LowStockCount(r.Context(), tenant, st.LowStockThreshold, "")
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out, "states": logistics.States, "lowStock": alerts, "defaultThreshold": st.LowStockThreshold})
}

func (s *Server) putLocation(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	var l logistics.Location
	if !decode(w, r, &l) {
		return
	}
	l.ID = id
	errs := l.Validate()
	if !locationIDRe.MatchString(id) {
		errs = append(errs, apierr.FieldError{Field: "id", Code: "invalid", Message: "El identificador usa minúsculas, números y guiones (por ejemplo cdmx-1)"})
	}
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	if !l.PickupEnabled || !l.Active {
		for _, o := range c.Options {
			if o.Kind == logistics.KindPickup && o.LocationID == id && o.Active {
				errs = append(errs, apierr.FieldError{Field: "pickupEnabled", Code: "in_use",
					Message: "La opción de entrega “" + o.Name + "” recoge aquí. Desactívala en Envíos antes de quitar la recolección."})
				break
			}
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa los campos marcados", errs...)
		return
	}
	if err := s.DB.PutDoc(r.Context(), pr.Tenant, pr.Actor(), "locations", id, withoutID(l)); err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, l)
}

func (s *Server) deleteLocation(w http.ResponseWriter, r *http.Request) {
	pr := principal(r)
	id := r.PathValue("id")
	c, err := s.DB.LoadLogistics(r.Context(), pr.Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for _, o := range c.Options {
		if o.Kind == logistics.KindPickup && o.LocationID == id {
			inUse(w, "La opción de entrega “"+o.Name+"” recoge en esta ubicación. Elimínala primero en Envíos.")
			return
		}
	}
	err = s.DB.DeleteDoc(r.Context(), pr.Tenant, pr.Actor(), "locations", id, func(tx pgx.Tx) error {
		return store.LocationEmptyTx(r.Context(), tx, pr.Tenant, id)
	})
	if errors.Is(err, store.ErrInUse) {
		inUse(w, "La ubicación todavía tiene existencias. Transfiérelas a otra ubicación antes de eliminarla.")
		return
	}
	if s.storeError(w, err, "Ubicación no encontrada") {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func queryLimit(r *http.Request, def, maxN int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 || n > maxN {
		return def
	}
	return n
}

type stockView struct {
	store.StockRow
	Title string `json:"title"`
}

func (s *Server) titled(r *http.Request, rows []store.StockRow) []stockView {
	t, _ := s.Proj.Tenant(principal(r).Tenant)
	out := make([]stockView, 0, len(rows))
	for _, x := range rows {
		v := stockView{StockRow: x}
		if t != nil {
			if va, p, err := t.Catalog.VariantBySKU(x.SKU); err == nil {
				v.Title = strings.TrimSpace(p.Title + " · " + va.Title)
			}
		}
		out = append(out, v)
	}
	return out
}

// locationStock lists what a location holds (for the transfer form).
func (s *Server) locationStock(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50, 200)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	rows, err := s.DB.LocationStock(r.Context(), principal(r).Tenant, r.PathValue("id"), q, r.URL.Query().Get("cursor"), limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].SKU
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": s.titled(r, rows), "nextCursor": next})
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 25, 100)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	ts, err := s.DB.Transfers(r.Context(), principal(r).Tenant, before, limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := int64(0)
	if len(ts) > limit {
		ts = ts[:limit]
		next = ts[limit-1].Number
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(ts), "nextBefore": next})
}

func (s *Server) getTransfer(w http.ResponseWriter, r *http.Request) {
	t, err := s.DB.GetTransfer(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if s.storeError(w, err, "Transferencia no encontrada") {
		return
	}
	httpx.JSON(w, r, http.StatusOK, t)
}

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From  string               `json:"from"`
		To    string               `json:"to"`
		Note  string               `json:"note"`
		Lines []store.TransferLine `json:"lines"`
	}
	if !decode(w, r, &body) {
		return
	}
	var errs []apierr.FieldError
	if body.From == "" {
		errs = append(errs, apierr.FieldError{Field: "from", Code: "required", Message: "Elige la ubicación de origen"})
	}
	if body.To == "" {
		errs = append(errs, apierr.FieldError{Field: "to", Code: "required", Message: "Elige la ubicación de destino"})
	} else if body.To == body.From {
		errs = append(errs, apierr.FieldError{Field: "to", Code: "invalid", Message: "El destino debe ser distinto del origen"})
	}
	if len(body.Note) > 300 {
		errs = append(errs, apierr.FieldError{Field: "note", Code: "too_long", Message: "La nota admite hasta 300 caracteres"})
	}
	if len(body.Lines) == 0 || len(body.Lines) > 200 {
		errs = append(errs, apierr.FieldError{Field: "lines", Code: "out_of_range", Message: "Agrega entre 1 y 200 productos"})
	}
	seen := map[string]bool{}
	for i, l := range body.Lines {
		f := "lines[" + strconv.Itoa(i) + "]"
		switch {
		case strings.TrimSpace(l.SKU) == "" || seen[l.SKU]:
			errs = append(errs, apierr.FieldError{Field: f + ".sku", Code: "invalid", Message: "SKU vacío o repetido"})
		case l.Qty < 1 || l.Qty > 1_000_000:
			errs = append(errs, apierr.FieldError{Field: f + ".qty", Code: "out_of_range", Message: "La cantidad de " + l.SKU + " debe ser 1 o más"})
		}
		seen[l.SKU] = true
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "Revisa la transferencia", errs...)
		return
	}
	pr := principal(r)
	t, err := s.DB.CreateTransfer(r.Context(), pr.Tenant, pr.Actor(), body.From, body.To, strings.TrimSpace(body.Note), body.Lines)
	switch {
	case errors.Is(err, store.ErrInsufficient):
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "insufficient_stock",
			Detail: "No hay unidades suficientes en el origen: " + strings.TrimPrefix(err.Error(), store.ErrInsufficient.Error()+": ")})
	case errors.Is(err, store.ErrNotFound):
		apierr.BadRequest(w, "", apierr.FieldError{Field: "from", Code: "unknown", Message: "Registra primero la ubicación (" + strings.TrimPrefix(err.Error(), store.ErrNotFound.Error()+": ") + ")"})
	case err != nil:
		internalError(w, s.Log, err)
	default:
		httpx.JSON(w, r, http.StatusCreated, t)
	}
}

// lowStock lists SKUs at or under their threshold.
func (s *Server) lowStock(w http.ResponseWriter, r *http.Request) {
	tenant := principal(r).Tenant
	limit := queryLimit(r, 50, 200)
	st, _, err := s.DB.Settings(r.Context(), tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	loc := r.URL.Query().Get("location")
	rows, err := s.DB.LowStock(r.Context(), tenant, st.LowStockThreshold, loc, r.URL.Query().Get("cursor"), limit+1)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[limit-1].SKU + "|" + rows[limit-1].Location
	}
	total, err := s.DB.LowStockCount(r.Context(), tenant, st.LowStockThreshold, loc)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": s.titled(r, rows), "nextCursor": next, "total": total, "defaultThreshold": st.LowStockThreshold})
}

// putThreshold sets the low-stock threshold of a SKU; null goes back to the store default.
func (s *Server) putThreshold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Threshold *int64 `json:"threshold"`
		Location  string `json:"location"`
	}
	if !decode(w, r, &body) {
		return
	}
	th := int64(-1)
	if body.Threshold != nil {
		th = *body.Threshold
		if th < 0 || th > 1_000_000 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "threshold", Code: "out_of_range", Message: "El umbral debe ser 0 o más"})
			return
		}
	}
	pr := principal(r)
	if err := s.DB.SetThreshold(r.Context(), pr.Tenant, pr.Actor(), r.PathValue("sku"), body.Location, th); err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
