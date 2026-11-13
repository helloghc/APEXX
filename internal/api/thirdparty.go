package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/carriers"
	"github.com/helloghc/apexx/internal/httpx"
)

// Third-party warehouses (3PL): paid orders with stock in their location are
// delivered to them signed; they answer with their reference and later ship
// the order through POST /v1/orders/{id}/fulfillments with their API key.

var warehouseHTTPClient *http.Client // tests

func (s *Server) routesWarehouses(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/orders/{id}/warehouse-requests", s.authed("orders", auth.Read, s.listWarehouseRequests))
	handle("GET /v1/locations/{id}/warehouse-secret", s.authed("settings", auth.Admin, s.warehouseSecret))
}

func (s *Server) warehouseKey(tenant, location string) []byte {
	b, err := s.Keys.Blind(tenant, "warehouse-signing", location)
	if err != nil {
		return nil
	}
	return []byte(hex.EncodeToString(b))
}

func (s *Server) warehouseSecret(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, r, http.StatusOK, map[string]any{"secret": string(s.warehouseKey(principal(r).Tenant, r.PathValue("id"))),
		"contract": "POST {dirección}/fulfillment-orders (o /cancellations) con JSON {orderId, orderNumber, items:[{sku, title, qty}], shipTo:{name, address, postalCode, phone, email}}; responde 2xx con {reference}. Firma: X-Apexx-Signature = hex(HMAC-SHA256(secreto, timestamp + \".\" + cuerpo)). Para avisar el envío usa POST /v1/orders/{orderId}/fulfillments con tu llave de API."})
}

func (s *Server) listWarehouseRequests(w http.ResponseWriter, r *http.Request) {
	list, err := s.Orders.FulfillmentRequests(r.Context(), principal(r).Tenant, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": nonNil(list)})
}

// RunWarehouseRequests delivers the due requests. A refusal (4xx) is final;
// anything else is retried with backoff (1 min, 2, 4… up to 10 tries).
func (s *Server) RunWarehouseRequests(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `WITH due AS (
			SELECT tenant_id, id FROM fulfillment_requests WHERE status='pending' AND next_attempt <= now() ORDER BY next_attempt LIMIT 50 FOR UPDATE SKIP LOCKED)
		UPDATE fulfillment_requests f SET next_attempt = now() + interval '2 minutes' FROM due
		WHERE f.tenant_id=due.tenant_id AND f.id=due.id RETURNING f.tenant_id, f.id, f.order_id, f.location_id, f.kind, f.attempts`)
	if err != nil {
		return 0, err
	}
	type req struct {
		tenant, id, order, location, kind string
		attempts                          int
	}
	var list []req
	for rows.Next() {
		var x req
		if err := rows.Scan(&x.tenant, &x.id, &x.order, &x.location, &x.kind, &x.attempts); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, x)
	}
	rows.Close()
	done := 0
	for _, x := range list {
		status, ref, msg := s.deliverWarehouse(ctx, x.tenant, x.order, x.location, x.kind)
		attempts := x.attempts + 1
		if status == "pending" && attempts >= 10 {
			status = "failed"
		}
		next := time.Now().Add(time.Duration(1<<min(attempts, 9)) * time.Minute)
		_ = s.DB.Tx(ctx, x.tenant, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE fulfillment_requests SET status=$3, reference=$4, last_error=$5, attempts=$6, next_attempt=$7, updated_at=now() WHERE tenant_id=$1 AND id=$2`,
				x.tenant, x.id, status, ref, msg, attempts, next)
			return err
		})
		if status == "accepted" {
			done++
		}
	}
	return done, nil
}

func (s *Server) deliverWarehouse(ctx context.Context, tenant, orderID, location, kind string) (status, ref, msg string) {
	if s.isSandbox(tenant) {
		return "rejected", "", sandboxCaptured + " al almacén externo"
	}
	cfg, err := s.DB.LoadLogistics(ctx, tenant)
	if err != nil {
		return "pending", "", err.Error()
	}
	endpoint := ""
	for _, l := range cfg.Locations {
		if l.ID == location {
			endpoint = l.FulfillerEndpoint
		}
	}
	if endpoint == "" {
		return "rejected", "", "La ubicación ya no tiene almacén externo"
	}
	o, err := s.Orders.Get(ctx, tenant, orderID, true)
	if err != nil {
		return "pending", "", err.Error()
	}
	var alloc []struct {
		SKU string
		Qty int64
	}
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT sku, qty FROM order_allocations WHERE tenant_id=$1 AND order_id=$2 AND location_id=$3 ORDER BY sku`, tenant, orderID, location)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a struct {
				SKU string
				Qty int64
			}
			if err := rows.Scan(&a.SKU, &a.Qty); err != nil {
				return err
			}
			alloc = append(alloc, a)
		}
		return rows.Err()
	})
	if err != nil {
		return "pending", "", err.Error()
	}
	titles := map[string]string{}
	for _, l := range o.Lines {
		titles[l.SKU] = l.Title
	}
	items := []map[string]any{}
	t, _ := s.Proj.Tenant(tenant)
	for _, a := range alloc {
		// A kit is picked as its components: name them from the catalog.
		if titles[a.SKU] == "" && t != nil {
			if v, p, err := t.Catalog.VariantBySKU(a.SKU); err == nil {
				titles[a.SKU] = variantTitle(p.Title, v.Title)
			}
		}
		items = append(items, map[string]any{"sku": a.SKU, "title": titles[a.SKU], "qty": a.Qty})
	}
	ship := map[string]any{"postalCode": o.PostalCode}
	if o.Customer != nil {
		ship["name"], ship["address"], ship["phone"], ship["email"] = o.Customer.Name, o.Customer.Address, o.Customer.Phone, o.Customer.Email
	}
	body, _ := json.Marshal(map[string]any{"orderId": o.ID, "orderNumber": firstNonEmpty(o.DisplayNumber, strconv.FormatInt(o.Number, 10)), "items": items, "shipTo": ship})
	path := "/fulfillment-orders"
	if kind == "cancel" {
		path = "/cancellations"
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Apexx-Timestamp", ts)
	req.Header.Set("X-Apexx-Signature", carriers.Sign(s.warehouseKey(tenant, location), ts, body))
	c := warehouseHTTPClient
	if c == nil {
		c = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := c.Do(req)
	if err != nil {
		return "pending", "", "El almacén no respondió: " + err.Error()
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	var out struct {
		Reference string `json:"reference"`
		Detail    string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &out)
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return "accepted", out.Reference, ""
	case res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != 408 && res.StatusCode != 429:
		return "rejected", "", firstNonEmpty(out.Detail, fmt.Sprintf("El almacén rechazó el pedido (%d)", res.StatusCode))
	}
	return "pending", "", fmt.Sprintf("El almacén respondió %d; se reintenta", res.StatusCode)
}
