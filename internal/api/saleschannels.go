package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/httpx"
	"github.com/helloghc/apexx/internal/orders"
	"github.com/helloghc/apexx/internal/saleschannels"
	"github.com/helloghc/apexx/internal/store"
)

// Sales channels: the store connects its Mercado Libre or Amazon account,
// picks products to list there, and Apexx keeps their price (with an
// optional markup) and stock (minus a safety buffer) in step, closes them on
// request and imports the paid orders into the store's own order list, once
// each, so stock is shared. Credentials are encrypted and never returned.

type channelSettings struct {
	MarkupBps    int64 `json:"markupBps"`    // added to the store price (0-10000)
	StockBuffer  int64 `json:"stockBuffer"`  // units held back from the channel
	ImportOrders bool  `json:"importOrders"` // bring paid orders into Apexx
}

type channelAccount struct {
	Provider    string          `json:"provider"`
	Name        string          `json:"name"`
	Connected   bool            `json:"connected"`
	SellerID    string          `json:"sellerId,omitempty"`
	Status      string          `json:"status,omitempty"`
	LastError   string          `json:"lastError,omitempty"`
	Settings    channelSettings `json:"settings"`
	ConnectedAt *time.Time      `json:"connectedAt,omitempty"`
	Counts      map[string]int  `json:"counts"`
	Orders      int             `json:"orders"`
}

type channelListing struct {
	SKU         string     `json:"sku"`
	Title       string     `json:"title"`
	ExternalID  string     `json:"externalId"`
	Status      string     `json:"status"`
	Category    string     `json:"category"`
	PushedPrice *int64     `json:"pushedPrice"`
	PushedQty   *int64     `json:"pushedQty"`
	Price       int64      `json:"price"` // what Apexx will send now
	Qty         int64      `json:"qty"`
	Error       string     `json:"error,omitempty"`
	SyncedAt    *time.Time `json:"syncedAt"`
	URL         string     `json:"url,omitempty"`
}

func (s *Server) routesSalesChannels(handle func(string, http.HandlerFunc)) {
	handle("GET /v1/sales-channels", s.authed("settings", auth.Read, noStorefront(s.listSalesChannels)))
	handle("PUT /v1/sales-channels/{provider}", s.authed("settings", auth.Admin, s.connectSalesChannel))
	handle("DELETE /v1/sales-channels/{provider}", s.authed("settings", auth.Admin, s.disconnectSalesChannel))
	handle("GET /v1/sales-channels/{provider}/listings", s.authed("catalog", auth.Read, noStorefront(s.listChannelListings)))
	handle("POST /v1/sales-channels/{provider}/listings", s.authed("catalog", auth.Edit, s.addChannelListings))
	handle("DELETE /v1/sales-channels/{provider}/listings/{sku}", s.authed("catalog", auth.Edit, s.closeChannelListing))
	handle("POST /v1/sales-channels/{provider}/sync", s.authed("catalog", auth.Edit, s.syncSalesChannel))
}

func (s *Server) channelAdapter(provider string) (saleschannels.Adapter, error) {
	ep := s.ChannelEndpoints
	if ep.MeliAPI == "" {
		ep = saleschannels.Default
	}
	return saleschannels.For(provider, ep, s.ChannelHTTP)
}

func knownProvider(w http.ResponseWriter, p string) bool {
	if _, ok := saleschannels.Names[p]; !ok {
		apierr.NotFound(w, "Canal no disponible")
		return false
	}
	return true
}

func (s *Server) listSalesChannels(w http.ResponseWriter, r *http.Request) {
	t := principal(r).Tenant
	out := []channelAccount{}
	err := s.DB.Tx(r.Context(), t, func(tx pgx.Tx) error {
		for _, p := range []string{"mercadolibre", "amazon"} {
			a := channelAccount{Provider: p, Name: saleschannels.Names[p], Counts: map[string]int{}, Settings: channelSettings{ImportOrders: true}}
			var settings []byte
			var at time.Time
			err := tx.QueryRow(r.Context(), `SELECT seller_id, settings, status, last_error, connected_at FROM channel_accounts WHERE tenant_id=$1 AND provider=$2`, t, p).
				Scan(&a.SellerID, &settings, &a.Status, &a.LastError, &at)
			if err == nil {
				a.Connected, a.ConnectedAt = true, &at
				_ = json.Unmarshal(settings, &a.Settings)
				rows, err := tx.Query(r.Context(), `SELECT status, count(*) FROM channel_listings WHERE tenant_id=$1 AND provider=$2 GROUP BY status`, t, p)
				if err != nil {
					return err
				}
				for rows.Next() {
					var st string
					var n int
					if err := rows.Scan(&st, &n); err != nil {
						rows.Close()
						return err
					}
					a.Counts[st] = n
				}
				rows.Close()
				_ = tx.QueryRow(r.Context(), `SELECT count(*) FROM channel_orders WHERE tenant_id=$1 AND provider=$2`, t, p).Scan(&a.Orders)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			out = append(out, a)
		}
		return nil
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) connectSalesChannel(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	var in struct {
		ClientID     string          `json:"clientId"`
		ClientSecret string          `json:"clientSecret"`
		RefreshToken string          `json:"refreshToken"`
		SellerID     string          `json:"sellerId"`
		Settings     channelSettings `json:"settings"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Settings.MarkupBps < 0 || in.Settings.MarkupBps > 10000 || in.Settings.StockBuffer < 0 || in.Settings.StockBuffer > 1000 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "settings", Code: "invalid", Message: "El aumento va de 0 a 100% y la reserva de 0 a 1000 piezas"})
		return
	}
	pr := principal(r)
	settings, _ := json.Marshal(in.Settings)
	in.ClientID, in.ClientSecret, in.RefreshToken, in.SellerID = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.ClientSecret), strings.TrimSpace(in.RefreshToken), strings.TrimSpace(in.SellerID)
	if in.ClientID == "" && in.ClientSecret == "" && in.RefreshToken == "" {
		// Only the settings change.
		err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
			ct, err := tx.Exec(r.Context(), `UPDATE channel_accounts SET settings=$3, updated_at=now() WHERE tenant_id=$1 AND provider=$2`, pr.Tenant, p, settings)
			if err == nil && ct.RowsAffected() == 0 {
				return store.ErrNotFound
			}
			if err != nil {
				return err
			}
			// New price rules apply to every listing at the next pass.
			if _, err := tx.Exec(r.Context(), `UPDATE channel_listings SET next_try=now() WHERE tenant_id=$1 AND provider=$2 AND status='active'`, pr.Tenant, p); err != nil {
				return err
			}
			return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.settings", p, map[string]any{"settings": in.Settings})
		})
		if errors.Is(err, store.ErrNotFound) {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "clientId", Code: "required", Message: "Primero conecta tu cuenta con sus credenciales"})
			return
		}
		if err != nil {
			internalError(w, s.Log, err)
			return
		}
		s.listSalesChannels(w, r)
		return
	}
	if in.ClientID == "" || in.ClientSecret == "" || in.RefreshToken == "" || len(in.ClientID)+len(in.ClientSecret)+len(in.RefreshToken)+len(in.SellerID) > 4000 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "clientId", Code: "required", Message: "Escribe el ID de la app, su secreto y el token de actualización"})
		return
	}
	ad, err := s.channelAdapter(p)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	c := saleschannels.Creds{ClientID: in.ClientID, ClientSecret: in.ClientSecret, RefreshToken: in.RefreshToken, SellerID: in.SellerID}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	name, err := ad.Check(ctx, &c)
	if err != nil {
		msg := "No pudimos conectar con " + saleschannels.Names[p] + "; intenta de nuevo"
		if saleschannels.IsRejected(err) {
			msg = err.Error()
		}
		apierr.Write(w, apierr.Problem{Status: http.StatusUnprocessableEntity, Code: "channel_rejected", Detail: msg})
		return
	}
	enc, err := s.sealCreds(pr.Tenant, p, c)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	err = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `INSERT INTO channel_accounts (tenant_id, provider, seller_id, creds, settings) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id, provider) DO UPDATE SET seller_id=EXCLUDED.seller_id, creds=EXCLUDED.creds, settings=EXCLUDED.settings, status='connected', last_error='', updated_at=now()`,
			pr.Tenant, p, c.SellerID, enc, settings); err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.connect", p, map[string]any{"seller": c.SellerID, "account": name})
	})
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	s.listSalesChannels(w, r)
}

func (s *Server) sealCreds(tenant, provider string, c saleschannels.Creds) ([]byte, error) {
	b, _ := json.Marshal(c)
	return s.Keys.Encrypt(tenant, b, "channel:"+provider)
}

func (s *Server) openCreds(tenant, provider string, ct []byte) (saleschannels.Creds, error) {
	var c saleschannels.Creds
	b, err := s.Keys.Decrypt(tenant, ct, "channel:"+provider)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func (s *Server) disconnectSalesChannel(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	pr := principal(r)
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `DELETE FROM channel_accounts WHERE tenant_id=$1 AND provider=$2`, pr.Tenant, p)
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.disconnect", p, map[string]any{})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "El canal no está conectado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// desired is what a listing should show now: the store price plus markup and
// the available stock minus the buffer. A product no longer for sale shows 0.
func (s *Server) desired(tenant, sku string, set channelSettings) (saleschannels.Listing, bool) {
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		return saleschannels.Listing{}, false
	}
	v, p, err := t.Catalog.VariantBySKU(sku)
	if err != nil {
		return saleschannels.Listing{SKU: sku}, false
	}
	l := saleschannels.Listing{SKU: sku, Title: p.Title, Description: plainText(p.Description), Currency: "MXN", GTIN: v.Barcode}
	if v.Title != "" && len(p.Variants) > 1 {
		l.Title += " " + v.Title
	}
	_, l.Brand = t.Taxo.BrandKey(&p)
	if ch, ok := t.Channel(mainChannel); ok && ch.Currency != "" {
		l.Currency = string(ch.Currency)
	}
	if q, ok := t.Pricing.Resolve(mainChannel, sku, 1); ok {
		l.Price = q.Unit.Amount + q.Unit.Amount*set.MarkupBps/10000
	}
	if p.Status == "active" {
		l.Qty = max(t.Inventory.Available(sku)-set.StockBuffer, 0)
	}
	base := strings.TrimRight(s.Orders.PublicURL, "/")
	ids := append([]string{}, p.Media...)
	if v.MediaID != "" {
		ids = append([]string{v.MediaID}, ids...)
	}
	for _, id := range ids {
		if m, ok := t.Taxo.Media(id); ok && base != "" {
			if u := viewMedia(tenant, m).URL; u != "" && len(l.Images) < 10 {
				l.Images = append(l.Images, base+u)
			}
		}
	}
	return l, l.Price > 0
}

func plainText(html string) string {
	var b strings.Builder
	in := false
	for _, r := range html {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
			b.WriteRune(' ')
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func (s *Server) listChannelListings(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	t := principal(r).Tenant
	var set channelSettings
	var out []channelListing
	err := s.DB.Tx(r.Context(), t, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(r.Context(), `SELECT settings FROM channel_accounts WHERE tenant_id=$1 AND provider=$2`, t, p).Scan(&raw); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return store.ErrNotFound
			}
			return err
		}
		_ = json.Unmarshal(raw, &set)
		rows, err := tx.Query(r.Context(), `SELECT sku, external_id, status, category, pushed_price, pushed_qty, error, synced_at FROM channel_listings
			WHERE tenant_id=$1 AND provider=$2 AND status <> 'closed' ORDER BY created_at DESC LIMIT 1000`, t, p)
		if err != nil {
			return err
		}
		out, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (channelListing, error) {
			var l channelListing
			return l, row.Scan(&l.SKU, &l.ExternalID, &l.Status, &l.Category, &l.PushedPrice, &l.PushedQty, &l.Error, &l.SyncedAt)
		})
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "El canal no está conectado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	for i := range out {
		d, _ := s.desired(t, out[i].SKU, set)
		out[i].Title, out[i].Price, out[i].Qty = d.Title, d.Price, d.Qty
		if out[i].Title == "" {
			out[i].Title = out[i].SKU
		}
		if p == "mercadolibre" && out[i].ExternalID != "" {
			out[i].URL = "https://articulo.mercadolibre.com.mx/" + strings.Replace(out[i].ExternalID, "MLM", "MLM-", 1)
		}
	}
	if out == nil {
		out = []channelListing{}
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) addChannelListings(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	var in struct {
		SKUs     []string `json:"skus"`
		Category string   `json:"category"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.SKUs) == 0 || len(in.SKUs) > 500 || len(in.Category) > 100 {
		apierr.BadRequest(w, "", apierr.FieldError{Field: "skus", Code: "invalid", Message: "Elige de 1 a 500 productos"})
		return
	}
	pr := principal(r)
	t, _ := s.Proj.Tenant(pr.Tenant)
	for _, sku := range in.SKUs {
		if _, prod, err := t.Catalog.VariantBySKU(sku); err != nil || prod.Status != "active" {
			apierr.BadRequest(w, "", apierr.FieldError{Field: "skus", Code: "unknown", Message: "El SKU " + sku + " no existe o no está a la venta"})
			return
		}
	}
	added := 0
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		var ok bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM channel_accounts WHERE tenant_id=$1 AND provider=$2)`, pr.Tenant, p).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return store.ErrNotFound
		}
		for _, sku := range in.SKUs {
			// A closed listing comes back; an existing one keeps its id.
			ct, err := tx.Exec(r.Context(), `INSERT INTO channel_listings (tenant_id, provider, sku, category) VALUES ($1,$2,$3,$4)
				ON CONFLICT (tenant_id, provider, sku) DO UPDATE SET status=CASE WHEN channel_listings.status IN ('closed','closing','error') THEN 'pending' ELSE channel_listings.status END,
					category=CASE WHEN EXCLUDED.category<>'' THEN EXCLUDED.category ELSE channel_listings.category END,
					external_id=CASE WHEN channel_listings.status='closed' THEN '' ELSE channel_listings.external_id END, attempts=0, error='', next_try=now()`, pr.Tenant, p, sku, in.Category)
			if err != nil {
				return err
			}
			added += int(ct.RowsAffected())
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.list", p, map[string]any{"skus": in.SKUs})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "El canal no está conectado")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	httpx.JSON(w, r, http.StatusAccepted, map[string]any{"queued": added})
}

func (s *Server) closeChannelListing(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	pr := principal(r)
	err := s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		ct, err := tx.Exec(r.Context(), `UPDATE channel_listings SET status=CASE WHEN external_id='' THEN 'closed' ELSE 'closing' END, attempts=0, error='', next_try=now()
			WHERE tenant_id=$1 AND provider=$2 AND sku=$3 AND status <> 'closed'`, pr.Tenant, p, r.PathValue("sku"))
		if err == nil && ct.RowsAffected() == 0 {
			return store.ErrNotFound
		}
		if err != nil {
			return err
		}
		return store.Audit(r.Context(), tx, pr.Tenant, pr.Actor(), "channel.unlist", p, map[string]any{"sku": r.PathValue("sku")})
	})
	if errors.Is(err, store.ErrNotFound) {
		apierr.NotFound(w, "Publicación no encontrada")
		return
	}
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) syncSalesChannel(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("provider")
	if !knownProvider(w, p) {
		return
	}
	pr := principal(r)
	_ = s.DB.Tx(r.Context(), pr.Tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), `UPDATE channel_listings SET next_try=now() WHERE tenant_id=$1 AND provider=$2 AND status IN ('pending','active','error','closing')`, pr.Tenant, p)
		return err
	})
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	n, err := s.syncAccount(ctx, pr.Tenant, p, true)
	if err != nil && !saleschannels.IsRejected(err) {
		s.Log.Warn("channel sync", "tenant", pr.Tenant, "provider", p, "err", err)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"listings": n.listings, "orders": n.orders, "error": msg})
}

// --- worker ---

type syncCount struct{ listings, orders int }

// RunSalesChannels keeps every connected account in step: listings due for a
// change and, every few minutes, the new orders.
func (s *Server) RunSalesChannels(ctx context.Context) (int, error) {
	rows, err := s.DB.System.Query(ctx, `SELECT a.tenant_id, a.provider, (a.settings->>'importOrders')::boolean IS TRUE AND a.updated_at < now() - interval '2 minutes'
		FROM channel_accounts a WHERE a.status <> 'paused' AND (a.settings->>'importOrders' = 'true' OR EXISTS (
			SELECT 1 FROM channel_listings l WHERE l.tenant_id=a.tenant_id AND l.provider=a.provider AND l.status IN ('pending','active','error','closing') AND l.next_try <= now()))
		LIMIT 200`)
	if err != nil {
		return 0, err
	}
	type acct struct {
		tenant, provider string
	}
	var list []acct
	for rows.Next() {
		var a acct
		var due bool
		if err := rows.Scan(&a.tenant, &a.provider, &due); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, a)
	}
	rows.Close()
	total := 0
	for _, a := range list {
		n, err := s.syncAccount(ctx, a.tenant, a.provider, false)
		total += n.listings + n.orders
		if err != nil && s.Log != nil && !saleschannels.IsRejected(err) {
			s.Log.Warn("sales channel", "tenant", a.tenant, "provider", a.provider, "err", err)
		}
	}
	return total, nil
}

// syncAccount pushes the due listings and imports new orders of one account.
// One sync per account runs at a time (row lock), so two nodes never push
// the same change twice.
func (s *Server) syncAccount(ctx context.Context, tenant, provider string, force bool) (syncCount, error) {
	var n syncCount
	ad, err := s.channelAdapter(provider)
	if err != nil {
		return n, err
	}
	var creds saleschannels.Creds
	var set channelSettings
	var since, updated time.Time
	type due struct {
		sku, ext, status, category string
		pPrice, pQty               *int64
		attempts                   int
	}
	var list []due
	var syncErr error
	err = s.DB.Tx(ctx, tenant, func(tx pgx.Tx) error {
		var ct, raw []byte
		if err := tx.QueryRow(ctx, `SELECT creds, settings, orders_since, updated_at FROM channel_accounts WHERE tenant_id=$1 AND provider=$2 FOR UPDATE SKIP LOCKED`, tenant, provider).
			Scan(&ct, &raw, &since, &updated); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil // busy on another node, or gone
			}
			return err
		}
		_ = json.Unmarshal(raw, &set)
		c, err := s.openCreds(tenant, provider, ct)
		if err != nil {
			return err
		}
		creds = c
		rows, err := tx.Query(ctx, `SELECT sku, external_id, status, category, pushed_price, pushed_qty, attempts FROM channel_listings
			WHERE tenant_id=$1 AND provider=$2 AND status IN ('pending','active','error','closing') AND next_try <= now() ORDER BY next_try LIMIT 100`, tenant, provider)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.sku, &d.ext, &d.status, &d.category, &d.pPrice, &d.pQty, &d.attempts); err != nil {
				rows.Close()
				return err
			}
			list = append(list, d)
		}
		rows.Close()
		before := creds
		for _, d := range list {
			want, ok := s.desired(tenant, d.sku, set)
			want.Category = d.category
			status, ext, errMsg := d.status, d.ext, ""
			var opErr error
			changed := false
			switch {
			case d.status == "closing":
				opErr = ad.Close(ctx, &creds, d.ext, d.sku)
				if opErr == nil {
					status = "closed"
				}
				changed = true
			case d.ext == "" || d.status == "pending":
				if !ok {
					opErr = saleschannels.Rejected{Msg: "El producto no tiene precio o ya no existe"}
				} else {
					ext, opErr = ad.Publish(ctx, &creds, d.ext, want)
				}
				changed = true
			case d.pPrice == nil || d.pQty == nil || *d.pPrice != want.Price || *d.pQty != want.Qty || d.status == "error":
				opErr = ad.Update(ctx, &creds, d.ext, want)
				changed = true
			}
			if opErr != nil {
				status, errMsg = "error", opErr.Error()
				if !saleschannels.IsRejected(opErr) {
					errMsg = "No pudimos comunicarnos con el canal; lo reintentamos solos"
				}
				if d.status == "closing" {
					status = "closing"
				}
				syncErr = opErr
			} else if status != "closed" {
				status = "active"
			}
			wait := 30 * time.Second
			if opErr != nil {
				wait = time.Duration(1<<min(d.attempts, 8)) * time.Minute
			}
			if changed {
				n.listings++
			}
			attempts := 0
			if opErr != nil {
				attempts = d.attempts + 1
			}
			if _, err := tx.Exec(ctx, `UPDATE channel_listings SET status=$4, external_id=$5, error=$6, attempts=$7, next_try=now()+$8::interval,
					pushed_price=CASE WHEN $9 THEN $10::bigint ELSE pushed_price END, pushed_qty=CASE WHEN $9 THEN $11::bigint ELSE pushed_qty END,
					synced_at=CASE WHEN $9 THEN now() ELSE synced_at END
				WHERE tenant_id=$1 AND provider=$2 AND sku=$3`, tenant, provider, d.sku, status, ext, errMsg, attempts, wait.String(),
				opErr == nil && changed && status != "closed", want.Price, want.Qty); err != nil {
				return err
			}
		}
		// Orders, every two minutes (or now, when asked).
		if set.ImportOrders && (force || time.Since(updated) > 2*time.Minute) {
			got, err := s.importChannelOrders(ctx, tx, tenant, provider, ad, &creds, since)
			n.orders = got
			if err != nil {
				syncErr = err
			}
		}
		lastErr := ""
		accStatus := "connected"
		if syncErr != nil && saleschannels.IsRejected(syncErr) && strings.Contains(syncErr.Error(), "credenciales") {
			lastErr, accStatus = syncErr.Error(), "error"
		}
		sealed := ct
		if creds != before {
			if sealed, err = s.sealCreds(tenant, provider, creds); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE channel_accounts SET creds=$3, status=$4, last_error=$5, updated_at=CASE WHEN $6 THEN now() ELSE updated_at END WHERE tenant_id=$1 AND provider=$2`,
			tenant, provider, sealed, accStatus, lastErr, set.ImportOrders && (force || time.Since(updated) > 2*time.Minute))
		return err
	})
	if err != nil {
		return n, err
	}
	return n, syncErr
}

// importChannelOrders brings paid marketplace orders into Apexx, once each.
// The order is placed like a counter sale (already paid on the channel), so
// it reserves the shared stock; a channel order cancelled later is cancelled
// here too.
func (s *Server) importChannelOrders(ctx context.Context, tx pgx.Tx, tenant, provider string, ad saleschannels.Adapter, creds *saleschannels.Creds, since time.Time) (int, error) {
	list, err := ad.Orders(ctx, creds, since.Add(-10*time.Minute))
	if err != nil {
		return 0, err
	}
	n := 0
	newest := since
	var firstErr error
	for _, o := range list {
		if o.Created.After(newest) {
			newest = o.Created
		}
		var have string
		_ = tx.QueryRow(ctx, `SELECT order_id FROM channel_orders WHERE tenant_id=$1 AND provider=$2 AND external_id=$3`, tenant, provider, o.ExternalID).Scan(&have)
		if have != "" {
			if o.Cancelled {
				// Cancelled on the channel after import: cancel here too (the channel returned the money).
				if err := s.Orders.Cancel(ctx, tenant, "channel:"+provider, have, "Cancelado en "+saleschannels.Names[provider]); err != nil && !errors.Is(err, orders.ErrInvalidState) && s.Log != nil {
					s.Log.Warn("channel cancel", "order", have, "err", err)
				}
			}
			continue
		}
		if !o.Paid || len(o.Lines) == 0 {
			continue
		}
		items := make([]orders.ItemInput, 0, len(o.Lines))
		for _, l := range o.Lines {
			unit := l.Unit
			items = append(items, orders.ItemInput{SKU: l.SKU, Qty: l.Qty, Unit: &unit})
		}
		name := saleschannels.Names[provider]
		d, err := s.Orders.SaveDraft(ctx, tenant, "channel:"+provider, "", orders.DraftInput{Channel: mainChannel, Items: items,
			Customer: orders.Customer{Name: o.Buyer + " (" + name + ")"}, Note: "Pedido " + o.ExternalID + " de " + name, WalkIn: true})
		if err != nil {
			if firstErr == nil {
				firstErr = saleschannels.Rejected{Msg: "No pudimos importar el pedido " + o.ExternalID + ": " + err.Error()}
			}
			continue
		}
		ord, err := s.Orders.CompleteDraft(ctx, tenant, "channel:"+provider, d.ID, "manual")
		if err != nil {
			_ = s.Orders.CancelDraft(context.Background(), tenant, "channel:"+provider, d.ID)
			if firstErr == nil {
				firstErr = saleschannels.Rejected{Msg: "No pudimos importar el pedido " + o.ExternalID + " (" + err.Error() + "); revisa sus productos y existencias"}
			}
			continue
		}
		// Recorded in its own transaction: the order exists now, whatever happens next.
		if err := s.DB.Tx(ctx, tenant, func(tx2 pgx.Tx) error {
			if _, err := tx2.Exec(ctx, `INSERT INTO channel_orders (tenant_id, provider, external_id, order_id) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, provider, o.ExternalID, ord.ID); err != nil {
				return err
			}
			_, err := tx2.Exec(ctx, `UPDATE orders SET source='marketplace' WHERE tenant_id=$1 AND id=$2`, tenant, ord.ID)
			return err
		}); err != nil {
			return n, err
		}
		n++
	}
	if _, err := tx.Exec(ctx, `UPDATE channel_accounts SET orders_since=$3 WHERE tenant_id=$1 AND provider=$2`, tenant, provider, newest); err != nil {
		return n, err
	}
	return n, firstErr
}
