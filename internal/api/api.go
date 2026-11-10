// Package api exposes the commerce modules over HTTP (v1).
package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/builder"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/inventory"
)

// Server holds the modules the routes need.
type Server struct {
	Catalog   *catalog.Store
	Inventory *inventory.Service
	Cart      *cart.Service
	Library   *builder.Library
	Sites     map[string]*builder.Site
}

// Routes returns the HTTP handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /v1/products", s.listProducts)
	mux.HandleFunc("GET /v1/products/{id}", s.getProduct)
	mux.HandleFunc("GET /v1/inventory/{sku}", s.getInventory)
	mux.HandleFunc("GET /v1/channels/{id}", s.getChannel)
	mux.HandleFunc("POST /v1/carts/simulate", s.simulate)
	if s.Library != nil {
		s.builderRoutes(mux)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		apierr.NotFound(w, "No existe la ruta "+r.Method+" "+r.URL.Path)
	})
	return httpx.Middleware(mux)
}

// productView adds live availability to a product.
type productView struct {
	catalog.Product
	Available map[string]int64 `json:"available"`
}

func (s *Server) view(p catalog.Product) productView {
	av := make(map[string]int64, len(p.Variants))
	for _, v := range p.Variants {
		av[v.SKU] = s.Inventory.Available(v.SKU)
	}
	return productView{Product: p, Available: av}
}

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 250 {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "limit", Code: "out_of_range", Message: "limit debe estar entre 1 y 250"})
			return
		}
		limit = n
	}
	after, err := httpx.DecodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "cursor", Code: "invalid", Message: "cursor inválido"})
		return
	}
	items, next := s.Catalog.List(after, limit)
	fields := httpx.Fields(r)
	out := make([]any, len(items))
	for i, p := range items {
		out[i] = httpx.Project(s.view(p), fields)
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{
		"items":      out,
		"nextCursor": httpx.EncodeCursor(next),
		"total":      s.Catalog.Count(),
	})
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	p, err := s.Catalog.Get(r.PathValue("id"))
	if errors.Is(err, catalog.ErrNotFound) {
		apierr.NotFound(w, "Producto "+r.PathValue("id")+" no encontrado")
		return
	}
	httpx.JSON(w, r, http.StatusOK, httpx.Project(s.view(p), httpx.Fields(r)))
}

func (s *Server) getInventory(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.Inventory.Get(r.PathValue("sku"))
	if !ok {
		apierr.NotFound(w, "SKU "+r.PathValue("sku")+" sin registros de inventario")
		return
	}
	httpx.JSON(w, r, http.StatusOK, snap)
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.Cart.Channels[r.PathValue("id")]
	if !ok {
		apierr.NotFound(w, "Canal "+r.PathValue("id")+" no encontrado")
		return
	}
	httpx.JSON(w, r, http.StatusOK, ch)
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	var req cart.Request
	if err := httpx.Decode(r, &req); err != nil {
		apierr.BadRequest(w, "JSON inválido: "+err.Error())
		return
	}
	var errs []apierr.FieldError
	if req.Channel == "" {
		errs = append(errs, apierr.FieldError{Field: "channel", Code: "required", Message: "channel es obligatorio"})
	}
	if len(req.Items) == 0 || len(req.Items) > 200 {
		errs = append(errs, apierr.FieldError{Field: "items", Code: "out_of_range", Message: "items debe tener entre 1 y 200 líneas"})
	}
	for i, it := range req.Items {
		if it.Qty < 1 {
			errs = append(errs, apierr.FieldError{Field: "items[" + strconv.Itoa(i) + "].qty", Code: "out_of_range", Message: "qty debe ser al menos 1"})
		}
	}
	if len(errs) > 0 {
		apierr.BadRequest(w, "La solicitud tiene errores", errs...)
		return
	}
	res, err := s.Cart.Simulate(req)
	var uc cart.ErrUnknownChannel
	if errors.As(err, &uc) {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "channel", Code: "unknown", Message: err.Error()})
		return
	}
	if err != nil {
		apierr.Write(w, apierr.Problem{Status: http.StatusInternalServerError, Code: apierr.CodeInternal})
		return
	}
	httpx.JSON(w, r, http.StatusOK, res)
}
