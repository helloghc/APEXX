package orders

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/notify"
)

// FulfillmentInput creates a shipment. No items = everything still pending.
type FulfillmentInput struct {
	Items          []RefundLine `json:"items"` // line + qty
	Carrier        string       `json:"carrier"`
	TrackingNumber string       `json:"trackingNumber"`
	TrackingURL    string       `json:"trackingUrl"`
	Note           string       `json:"note"`
	Pending        bool         `json:"pending"` // prepare the package now, mark it shipped later
	Notify         *bool        `json:"notify"`
	// CarrierID with BuyLabel buys the label from that connected carrier
	// (the API does it and fills Label); a client never sets Label itself.
	CarrierID string `json:"carrierId"`
	BuyLabel  bool   `json:"buyLabel"`
	Label     *Label `json:"-"`
}

// Label is a label bought from a connected carrier.
type Label struct {
	CarrierID, Carrier, TrackingNumber, TrackingURL, URL string
	Cost                                                 int64
}

// Carriers with a known tracking page; the URL is filled in when missing.
var carrierTracking = map[string]string{
	"estafeta":      "https://rastreositecorecms.azurewebsites.net/Tracking/searchByGet/?wayBillType=1&wayBill=%s",
	"dhl":           "https://www.dhl.com/mx-es/home/rastreo.html?tracking-id=%s",
	"fedex":         "https://www.fedex.com/fedextrack/?trknbr=%s",
	"ups":           "https://www.ups.com/track?tracknum=%s",
	"paquetexpress": "https://www.paquetexpress.com.mx/rastreo/%s",
	"redpack":       "https://www.redpack.com.mx/es/rastreo/?guias=%s",
	"99minutos":     "https://tracking.99minutos.com/search/%s",
}

func (in *FulfillmentInput) clean() error {
	in.Carrier, in.TrackingNumber, in.TrackingURL, in.Note = strings.TrimSpace(in.Carrier), strings.TrimSpace(in.TrackingNumber), strings.TrimSpace(in.TrackingURL), strings.TrimSpace(in.Note)
	if len(in.Carrier) > 60 {
		return FieldError{"carrier", "Paquetería de hasta 60 caracteres"}
	}
	if len(in.TrackingNumber) > 80 || strings.ContainsAny(in.TrackingNumber, " \n\r\t<>\"") {
		return FieldError{"trackingNumber", "Número de guía inválido: hasta 80 caracteres, sin espacios"}
	}
	if len(in.Note) > 300 {
		return FieldError{"note", "Nota de hasta 300 caracteres"}
	}
	if in.TrackingURL == "" && in.TrackingNumber != "" {
		if f, ok := carrierTracking[strings.ToLower(strings.ReplaceAll(in.Carrier, " ", ""))]; ok {
			in.TrackingURL = fmt.Sprintf(f, url.QueryEscape(in.TrackingNumber))
		}
	}
	if in.TrackingURL != "" {
		u, err := url.Parse(in.TrackingURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(in.TrackingURL) > 500 {
			return FieldError{"trackingUrl", "El enlace de rastreo debe empezar con https://"}
		}
	}
	return nil
}

func fulfillmentsTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) ([]Fulfillment, error) {
	rows, err := tx.Query(ctx, `SELECT id, number, status, carrier, tracking_number, tracking_url, note, created_at, shipped_at, delivered_at, cancelled_at,
			carrier_id, label_url, label_cost, tracking_status
		FROM fulfillments WHERE tenant_id=$1 AND order_id=$2 ORDER BY number`, tenant, orderID)
	if err != nil {
		return nil, err
	}
	fs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Fulfillment, error) {
		var f Fulfillment
		return f, r.Scan(&f.ID, &f.Number, &f.Status, &f.Carrier, &f.TrackingNumber, &f.TrackingURL, &f.Note, &f.CreatedAt, &f.ShippedAt, &f.DeliveredAt, &f.CancelledAt,
			&f.CarrierID, &f.LabelURL, &f.LabelCost, &f.TrackingStatus)
	})
	if err != nil {
		return nil, err
	}
	for i := range fs {
		rows, err := tx.Query(ctx, `SELECT line, sku, title, qty FROM fulfillment_items WHERE tenant_id=$1 AND fulfillment_id=$2 ORDER BY line`, tenant, fs[i].ID)
		if err != nil {
			return nil, err
		}
		fs[i].Items, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FulfillmentItem, error) {
			var it FulfillmentItem
			return it, r.Scan(&it.Line, &it.SKU, &it.Title, &it.Qty)
		})
		if err != nil {
			return nil, err
		}
	}
	return fs, nil
}

func trackingVars(f Fulfillment) map[string]string {
	carrier := f.Carrier
	if carrier == "" {
		carrier = "nuestra paquetería"
	}
	num := f.TrackingNumber
	if num == "" {
		num = "sin número de guía"
	}
	return map[string]string{"guia.paqueteria": carrier, "guia.numero": num, "guia.url": f.TrackingURL}
}

// CreateFulfillment ships some or all pending units of a paid order.
func (s *Service) CreateFulfillment(ctx context.Context, tenant, actor, orderID string, in FulfillmentInput) (Fulfillment, error) {
	if err := in.clean(); err != nil {
		return Fulfillment{}, err
	}
	f := Fulfillment{ID: newID("ful_"), Status: "shipped", Carrier: in.Carrier, TrackingNumber: in.TrackingNumber, TrackingURL: in.TrackingURL, Note: in.Note}
	if l := in.Label; l != nil {
		f.Carrier, f.TrackingNumber, f.TrackingURL, f.CarrierID, f.LabelURL, f.LabelCost = l.Carrier, l.TrackingNumber, l.TrackingURL, l.CarrierID, l.URL, l.Cost
	}
	if in.Pending {
		f.Status = "pending"
	}
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		o, err := lockOrder(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		if o.Status != StatusPaid {
			return fmt.Errorf("%w: %s → %s", ErrInvalidState, o.Status, StatusFulfilled)
		}
		lines, err := linesTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		want := map[int]int64{}
		if len(in.Items) == 0 {
			for _, l := range lines {
				if n := l.Qty - l.FulfilledQty - l.CancelledQty; n > 0 {
					want[l.Line] = n
				}
			}
		}
		for i, it := range in.Items {
			if _, dup := want[it.Line]; dup || it.Qty < 1 {
				return FieldError{fmt.Sprintf("items[%d]", i), "Línea repetida o cantidad inválida"}
			}
			want[it.Line] = it.Qty
		}
		if len(want) == 0 {
			return fmt.Errorf("%w: no quedan piezas por enviar", ErrInvalidState)
		}
		for _, l := range lines {
			n, ok := want[l.Line]
			if !ok {
				continue
			}
			if left := l.Qty - l.FulfilledQty - l.CancelledQty; n > left {
				return FieldError{"items", fmt.Sprintf("De %s solo quedan %d piezas por enviar", l.Title, left)}
			}
			f.Items = append(f.Items, FulfillmentItem{Line: l.Line, SKU: l.SKU, Title: l.Title, Qty: n})
			delete(want, l.Line)
		}
		if len(want) > 0 {
			return FieldError{"items", "Hay líneas que no pertenecen a este pedido"}
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(number),0)+1 FROM fulfillments WHERE tenant_id=$1 AND order_id=$2`, tenant, orderID).Scan(&f.Number); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO fulfillments (tenant_id, id, order_id, number, status, carrier, tracking_number, tracking_url, note, created_by, shipped_at, carrier_id, label_url, label_cost)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10, CASE WHEN $5='shipped' THEN now() END, $11, $12, $13) RETURNING created_at, shipped_at`,
			tenant, f.ID, orderID, f.Number, f.Status, f.Carrier, f.TrackingNumber, f.TrackingURL, f.Note, actor, f.CarrierID, f.LabelURL, f.LabelCost).Scan(&f.CreatedAt, &f.ShippedAt); err != nil {
			return err
		}
		for _, it := range f.Items {
			if _, err := tx.Exec(ctx, `INSERT INTO fulfillment_items (tenant_id, fulfillment_id, line, sku, title, qty) VALUES ($1,$2,$3,$4,$5,$6)`, tenant, f.ID, it.Line, it.SKU, it.Title, it.Qty); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE order_lines SET fulfilled_qty=fulfilled_qty+$4 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, it.Line, it.Qty); err != nil {
				return err
			}
		}
		if f.Status == "shipped" && (in.Notify == nil || *in.Notify) {
			if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplOrderShipped, trackingVars(f), f.TrackingURL, "shipped:"+f.ID); err != nil {
				return err
			}
		}
		typ := "fulfillment.shipped"
		if f.Status == "pending" {
			typ = "fulfillment.created"
		}
		return s.syncStatusTx(ctx, tx, tenant, actor, orderID, typ, map[string]any{"fulfillment": f.ID, "number": f.Number, "carrier": f.Carrier, "tracking": f.TrackingNumber, "units": units(f.Items)})
	})
	return f, err
}

func units(items []FulfillmentItem) int64 {
	var n int64
	for _, it := range items {
		n += it.Qty
	}
	return n
}

// FulfillmentUpdate changes a shipment: tracking data and/or its status
// (action: "ship", "deliver" or "cancel").
type FulfillmentUpdate struct {
	Action         string  `json:"action"`
	Carrier        *string `json:"carrier"`
	TrackingNumber *string `json:"trackingNumber"`
	TrackingURL    *string `json:"trackingUrl"`
	Notify         *bool   `json:"notify"`
}

// UpdateFulfillment edits tracking or moves a shipment forward.
func (s *Service) UpdateFulfillment(ctx context.Context, tenant, actor, orderID, fulfillmentID string, in FulfillmentUpdate) error {
	return s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		if _, err := lockOrder(ctx, tx, tenant, orderID); err != nil {
			return err
		}
		fs, err := fulfillmentsTx(ctx, tx, tenant, orderID)
		if err != nil {
			return err
		}
		var f *Fulfillment
		for i := range fs {
			if fs[i].ID == fulfillmentID {
				f = &fs[i]
			}
		}
		if f == nil {
			return ErrNotFound
		}
		if f.Status == "cancelled" {
			return fmt.Errorf("%w: el envío está cancelado", ErrInvalidState)
		}
		fi := FulfillmentInput{Carrier: f.Carrier, TrackingNumber: f.TrackingNumber, TrackingURL: f.TrackingURL}
		changed := false
		for _, p := range []struct {
			src *string
			dst *string
		}{{in.Carrier, &fi.Carrier}, {in.TrackingNumber, &fi.TrackingNumber}, {in.TrackingURL, &fi.TrackingURL}} {
			if p.src != nil && *p.src != *p.dst {
				*p.dst, changed = *p.src, true
			}
		}
		if changed {
			if in.TrackingURL == nil && (in.TrackingNumber != nil || in.Carrier != nil) {
				fi.TrackingURL = "" // recompute from the carrier when the guide changed
			}
			if err := fi.clean(); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE fulfillments SET carrier=$3, tracking_number=$4, tracking_url=$5 WHERE tenant_id=$1 AND id=$2`, tenant, f.ID, fi.Carrier, fi.TrackingNumber, fi.TrackingURL); err != nil {
				return err
			}
			f.Carrier, f.TrackingNumber, f.TrackingURL = fi.Carrier, fi.TrackingNumber, fi.TrackingURL
		}
		doNotify := in.Notify == nil || *in.Notify
		data := map[string]any{"fulfillment": f.ID, "number": f.Number, "carrier": f.Carrier, "tracking": f.TrackingNumber}
		switch in.Action {
		case "":
			if !changed {
				return FieldError{"action", "No hay cambios"}
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "fulfillment.tracking_updated", data)
		case "ship":
			if f.Status != "pending" {
				return fmt.Errorf("%w: el envío ya salió", ErrInvalidState)
			}
			if _, err := tx.Exec(ctx, `UPDATE fulfillments SET status='shipped', shipped_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, f.ID); err != nil {
				return err
			}
			if doNotify {
				if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplOrderShipped, trackingVars(*f), f.TrackingURL, "shipped:"+f.ID); err != nil {
					return err
				}
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "fulfillment.shipped", data)
		case "deliver":
			if f.Status != "shipped" {
				return fmt.Errorf("%w: solo un envío en camino se puede marcar como entregado", ErrInvalidState)
			}
			if _, err := tx.Exec(ctx, `UPDATE fulfillments SET status='delivered', delivered_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, f.ID); err != nil {
				return err
			}
			if doNotify {
				if err := s.notifyTx(ctx, tx, tenant, orderID, notify.TplOrderDelivered, nil, "", "delivered:"+f.ID); err != nil {
					return err
				}
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "fulfillment.delivered", data)
		case "cancel":
			if f.Status == "delivered" {
				return fmt.Errorf("%w: un envío entregado no se cancela; crea una devolución", ErrInvalidState)
			}
			for _, it := range f.Items {
				var returned, fulfilled int64
				if err := tx.QueryRow(ctx, `SELECT returned_qty, fulfilled_qty FROM order_lines WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, it.Line).Scan(&returned, &fulfilled); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						continue
					}
					return err
				}
				if fulfilled-it.Qty < returned {
					return fmt.Errorf("%w: hay devoluciones sobre las piezas de este envío", ErrInvalidState)
				}
				if _, err := tx.Exec(ctx, `UPDATE order_lines SET fulfilled_qty=fulfilled_qty-$4 WHERE tenant_id=$1 AND order_id=$2 AND line=$3`, tenant, orderID, it.Line, it.Qty); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE fulfillments SET status='cancelled', cancelled_at=now() WHERE tenant_id=$1 AND id=$2`, tenant, f.ID); err != nil {
				return err
			}
			return s.syncStatusTx(ctx, tx, tenant, actor, orderID, "fulfillment.cancelled", data)
		}
		return FieldError{"action", "action: ship, deliver o cancel"}
	})
}

// cancelFulfillmentRequestsTx tells third-party warehouses an order is
// cancelled: a request not yet delivered is dropped; one they accepted gets
// a cancel request.
func cancelFulfillmentRequestsTx(ctx context.Context, tx pgx.Tx, tenant, orderID string) error {
	if _, err := tx.Exec(ctx, `UPDATE fulfillment_requests SET status='rejected', last_error='Pedido cancelado antes de enviarse al almacén', updated_at=now()
		WHERE tenant_id=$1 AND order_id=$2 AND kind='fulfill' AND status IN ('pending','failed')`, tenant, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO fulfillment_requests (tenant_id, id, order_id, location_id, kind)
		SELECT tenant_id, 'ffr_' || substr(md5(random()::text || location_id), 1, 20), order_id, location_id, 'cancel'
		FROM fulfillment_requests WHERE tenant_id=$1 AND order_id=$2 AND kind='fulfill' AND status='accepted'
		ON CONFLICT DO NOTHING`, tenant, orderID)
	return err
}

// FulfillmentRequest is an order sent to a third-party warehouse.
type FulfillmentRequest struct {
	ID         string    `json:"id"`
	LocationID string    `json:"locationId"`
	Kind       string    `json:"kind"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	Reference  string    `json:"reference,omitempty"`
	LastError  string    `json:"lastError,omitempty"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// FulfillmentRequests lists what an order sent to warehouses.
func (s *Service) FulfillmentRequests(ctx context.Context, tenant, orderID string) ([]FulfillmentRequest, error) {
	var out []FulfillmentRequest
	err := s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, location_id, kind, status, attempts, reference, last_error, updated_at FROM fulfillment_requests WHERE tenant_id=$1 AND order_id=$2 ORDER BY created_at`, tenant, orderID)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (FulfillmentRequest, error) {
			var f FulfillmentRequest
			return f, r.Scan(&f.ID, &f.LocationID, &f.Kind, &f.Status, &f.Attempts, &f.Reference, &f.LastError, &f.UpdatedAt)
		})
		return err
	})
	return out, err
}
