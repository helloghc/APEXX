package api

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/helloghc/apexx/internal/orders"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/logistics"
)

// Connected carriers: live rates for the cart, labels and tracking for orders.

var liveQuoter = &carriers.Quoter{}

// carrierHTTPClient replaces the default client of carrier apps (tests).
var carrierHTTPClient *http.Client

// errCarrierDown: the carrier did not answer; nothing was bought.
var errCarrierDown = errors.New("la paquetería no respondió; no se compró ninguna guía, intenta de nuevo")

// carrierFail writes the error of a label purchase.
func (s *Server) carrierFail(w http.ResponseWriter, err error) {
	if errors.Is(err, errCarrierDown) {
		apierr.Write(w, apierr.Problem{Status: http.StatusBadGateway, Code: "carrier_unavailable", Detail: err.Error()})
		return
	}
	s.fail(w, err)
}

func (s *Server) routesCarriers(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/shipping/carriers/{id}/connection", s.authed("settings", auth.Admin, s.carrierConnection))
	handle("POST /v1/shipping/carriers/{id}/test", s.authed("settings", auth.Admin, s.carrierTest))
}

// carrierSecret is the signing secret of a carrier app: derived from the
// store's key, never stored, shown to the admin to configure the app.
func (s *Server) carrierSecret(tenant, carrierID string) ([]byte, error) {
	b, err := s.Keys.Blind(tenant, "carrier-signing", carrierID)
	if err != nil {
		return nil, err
	}
	return []byte(hex.EncodeToString(b)), nil
}

// carrierProvider builds the provider of a connected carrier.
func (s *Server) carrierProvider(tenant string, c logistics.Carrier) (carriers.Provider, bool) {
	switch c.Provider {
	case "sandbox":
		return carriers.Sandbox{}, true
	case "http":
		sec, err := s.carrierSecret(tenant, c.ID)
		if err != nil || c.Endpoint == "" {
			return nil, false
		}
		return carriers.HTTP{Endpoint: c.Endpoint, Secret: sec, Client: carrierHTTPClient}, true
	}
	return nil, false
}

// shipFrom is where the store's parcels leave: its first active location
// with an address (Mexico City's postal code when none is set).
func shipFrom(cfg *logistics.Config) carriers.Address {
	if cfg != nil {
		for _, l := range cfg.Locations {
			if l.Active && l.Kind != "store" && l.Address.PostalCode != "" {
				return carriers.Address{Name: l.Name, Line1: l.Address.Line1, City: l.Address.City, State: l.Address.State, PostalCode: l.Address.PostalCode, Country: firstNonEmpty(l.Address.Country, "MX")}
			}
		}
		for _, l := range cfg.Locations {
			if l.Active && l.Address.PostalCode != "" {
				return carriers.Address{Name: l.Name, Line1: l.Address.Line1, City: l.Address.City, State: l.Address.State, PostalCode: l.Address.PostalCode, Country: firstNonEmpty(l.Address.Country, "MX")}
			}
		}
	}
	return carriers.Address{PostalCode: "06600", Country: "MX"}
}

// LiveRate answers the cart of a store (installed in the projector).
func (s *Server) LiveRate(tenant, carrierID, country, postal string, weight, value int64) (int64, int, bool) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok || t.Cart == nil || t.Cart.Logistics == nil {
		return 0, 0, false
	}
	cfg := t.Cart.Logistics
	var car logistics.Carrier
	for _, c := range cfg.Carriers {
		if c.ID == carrierID {
			car = c
		}
	}
	if !car.Active || !car.Connected() {
		return 0, 0, false
	}
	p, ok := s.carrierProvider(tenant, car)
	if !ok {
		return 0, 0, false
	}
	r, ok := liveQuoter.Quote(context.Background(), tenant+"|"+car.ID+"|"+car.Endpoint, p,
		carriers.RateRequest{From: shipFrom(cfg), To: carriers.Address{PostalCode: postal, Country: firstNonEmpty(country, "MX")}, WeightGrams: weight, Value: value, Currency: "MXN"})
	return r.Amount, r.Days, ok
}

func (s *Server) findCarrier(w http.ResponseWriter, r *http.Request) (logistics.Carrier, *logistics.Config, bool) {
	cfg, err := s.DB.LoadLogistics(r.Context(), principal(r).Tenant)
	if err != nil {
		internalError(w, s.Log, err)
		return logistics.Carrier{}, nil, false
	}
	for _, c := range cfg.Carriers {
		if c.ID == r.PathValue("id") {
			return c, cfg, true
		}
	}
	apierr.NotFound(w, "Paquetería no encontrada")
	return logistics.Carrier{}, nil, false
}

// carrierConnection shows how to connect a carrier app: the contract and the secret.
func (s *Server) carrierConnection(w http.ResponseWriter, r *http.Request) {
	c, _, ok := s.findCarrier(w, r)
	if !ok {
		return
	}
	sec, err := s.carrierSecret(principal(r).Tenant, c.ID)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"provider": c.Provider, "endpoint": c.Endpoint, "secret": string(sec),
		"contract": "POST {endpoint}/rates, /labels, /track y /cancel con JSON; encabezados X-Apexx-Timestamp y X-Apexx-Signature = hex(HMAC-SHA256(secreto, timestamp + \".\" + cuerpo))."})
}

// carrierTest asks a connected carrier for a rate, to check the connection.
func (s *Server) carrierTest(w http.ResponseWriter, r *http.Request) {
	c, cfg, ok := s.findCarrier(w, r)
	if !ok {
		return
	}
	var body struct {
		PostalCode  string `json:"postalCode"`
		WeightGrams int64  `json:"weightGrams"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, ok := s.carrierProvider(principal(r).Tenant, c)
	if !ok {
		apierr.Write(w, apierr.Problem{Status: http.StatusConflict, Code: "not_connected", Detail: "Esta paquetería no está conectada; elige «De prueba» o «App» y guarda"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	rates, err := p.Rates(ctx, carriers.RateRequest{From: shipFrom(cfg), To: carriers.Address{PostalCode: body.PostalCode, Country: "MX"}, WeightGrams: max(body.WeightGrams, 1), Currency: "MXN"})
	ms := time.Since(start).Milliseconds()
	switch {
	case errors.Is(err, carriers.ErrRejected):
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "carrier_rejected", Detail: err.Error()})
	case err != nil:
		apierr.Write(w, apierr.Problem{Status: http.StatusBadGateway, Code: "carrier_unavailable", Detail: "La paquetería no respondió: " + err.Error()})
	default:
		httpx.JSON(w, r, http.StatusOK, map[string]any{"rates": rates, "ms": ms})
	}
}

// parcelOf is the destination, weight and value of the given lines of an
// order (all the lines left to ship when items is empty).
func (s *Server) parcelOf(tenant string, o orders.Order, items []orders.RefundLine) (carriers.Address, int64, int64) {
	to := carriers.Address{PostalCode: o.PostalCode, Country: "MX"}
	if o.Customer != nil {
		to.Name, to.Line1, to.Phone = o.Customer.Name, o.Customer.Address, o.Customer.Phone
	}
	want := map[int]int64{}
	for _, it := range items {
		want[it.Line] = it.Qty
	}
	var weight, value int64
	t, _ := s.Proj.Tenant(tenant)
	for _, l := range o.Lines {
		q := want[l.Line]
		if len(items) == 0 {
			q = l.Qty - l.FulfilledQty - l.CancelledQty
		}
		if q <= 0 {
			continue
		}
		value += l.Unit * q
		if t != nil {
			if v, _, err := t.Catalog.VariantBySKU(l.SKU); err == nil {
				weight += v.WeightGrams * q
			}
		}
	}
	return to, max(weight, 100), value
}

// buyLabel buys a label from a connected carrier of the store.
func (s *Server) buyLabel(ctx context.Context, tenant, carrierID, reference string, from, to carriers.Address, weight, value int64, isReturn bool) (*orders.Label, error) {
	cfg, err := s.DB.LoadLogistics(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var car logistics.Carrier
	for _, c := range cfg.Carriers {
		if c.ID == carrierID {
			car = c
		}
	}
	p, ok := s.carrierProvider(tenant, car)
	if !ok || !car.Active {
		return nil, orders.FieldError{Field: "carrierId", Message: "Elige una paquetería activa y conectada para comprar la guía"}
	}
	if from.PostalCode == "" {
		from = shipFrom(cfg)
	}
	if to.PostalCode == "" {
		to = shipFrom(cfg)
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	l, err := p.BuyLabel(cctx, carriers.LabelRequest{RateRequest: carriers.RateRequest{From: from, To: to, WeightGrams: weight, Value: value, Currency: "MXN"}, Reference: reference, Return: isReturn})
	if errors.Is(err, carriers.ErrRejected) {
		return nil, orders.FieldError{Field: "carrierId", Message: "La paquetería no generó la guía: " + err.Error()}
	}
	if err != nil {
		return nil, errCarrierDown
	}
	return &orders.Label{CarrierID: car.ID, Carrier: firstNonEmpty(car.Name, l.Carrier), TrackingNumber: l.TrackingNumber, TrackingURL: car.TrackingLink(l.TrackingNumber), URL: l.LabelURL, Cost: l.Cost}, nil
}

// cancelLabel undoes a label that ended up not used (best effort).
func (s *Server) cancelLabel(tenant, carrierID, tracking string) {
	cfg, err := s.DB.LoadLogistics(context.Background(), tenant)
	if err != nil {
		return
	}
	for _, c := range cfg.Carriers {
		if c.ID == carrierID {
			if p, ok := s.carrierProvider(tenant, c); ok {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = p.Cancel(ctx, tracking)
				cancel()
			}
		}
	}
}

// RunTracking asks the carriers where the shipped parcels with a bought
// label are, at most once an hour each, and marks the delivered ones (the
// customer gets the usual delivery email). Background round.
func (s *Server) RunTracking(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT f.tenant_id, f.order_id, f.id, f.carrier_id, f.tracking_number FROM fulfillments f
		WHERE f.status='shipped' AND f.carrier_id <> '' AND f.tracking_number <> '' AND (f.tracked_at IS NULL OR f.tracked_at < now() - interval '1 hour')
		ORDER BY f.tracked_at NULLS FIRST LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type parcel struct{ tenant, order, id, carrier, tracking string }
	var list []parcel
	for rows.Next() {
		var x parcel
		if err := rows.Scan(&x.tenant, &x.order, &x.id, &x.carrier, &x.tracking); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	delivered := 0
	for _, x := range list {
		cfg, err := s.DB.LoadLogistics(ctx, x.tenant)
		if err != nil {
			continue
		}
		var p carriers.Provider
		for _, c := range cfg.Carriers {
			if c.ID == x.carrier {
				p, _ = s.carrierProvider(x.tenant, c)
			}
		}
		status := "unavailable"
		if p != nil {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			t, err := p.Track(cctx, x.tracking)
			cancel()
			if err == nil {
				status = t.Status
			}
		}
		_ = s.DB.Tx(ctx, x.tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE fulfillments SET tracking_status=$3, tracked_at=now() WHERE tenant_id=$1 AND id=$2`, x.tenant, x.id, status)
			return err
		})
		if status == "delivered" {
			if err := s.Orders.UpdateFulfillment(ctx, x.tenant, "carrier:"+x.carrier, x.order, x.id, orders.FulfillmentUpdate{Action: "deliver"}); err == nil {
				delivered++
			}
		}
	}
	return delivered, nil
}
