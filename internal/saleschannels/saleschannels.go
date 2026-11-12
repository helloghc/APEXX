// Package saleschannels talks to external marketplaces (Mercado Libre and
// Amazon) for the stores that sell there: it publishes listings, keeps their
// price and stock, closes them and reads the orders placed there.
//
// Each adapter is built from the marketplace's public API documentation; the
// store brings its own app credentials. Errors the marketplace rejects (a
// missing attribute, a wrong category) come back as Rejected with the
// marketplace's own message, so the store can fix them; anything else is
// transient and retried.
package saleschannels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Creds are a store's credentials for one marketplace.
type Creds struct {
	ClientID     string    `json:"clientId"`
	ClientSecret string    `json:"clientSecret"`
	RefreshToken string    `json:"refreshToken"`
	SellerID     string    `json:"sellerId"`
	AccessToken  string    `json:"accessToken,omitempty"`
	Expires      time.Time `json:"expires,omitempty"`
}

// Listing is what is published for one SKU.
type Listing struct {
	SKU         string
	Title       string
	Description string
	Brand       string
	Category    string // the marketplace's category id or product type; "" lets it suggest one
	GTIN        string
	Price       int64 // minor units
	Currency    string
	Qty         int64
	Images      []string // absolute URLs
}

// Order is an order placed on the marketplace.
type Order struct {
	ExternalID string
	Created    time.Time
	Paid       bool // false: pending or cancelled, not to import
	Cancelled  bool
	Buyer      string
	Currency   string
	Total      int64
	Lines      []OrderLine
}

// OrderLine is one product of a marketplace order.
type OrderLine struct {
	SKU  string
	Qty  int64
	Unit int64
}

// Adapter is one marketplace.
type Adapter interface {
	// Refresh gets a fresh access token when the current one is near expiry.
	Refresh(ctx context.Context, c *Creds) error
	// Check confirms the credentials work and returns the seller's name.
	Check(ctx context.Context, c *Creds) (string, error)
	// Publish creates or replaces a listing and returns its id on the marketplace.
	Publish(ctx context.Context, c *Creds, externalID string, l Listing) (string, error)
	// Update sends the price and stock of an existing listing.
	Update(ctx context.Context, c *Creds, externalID string, l Listing) error
	// Close ends a listing.
	Close(ctx context.Context, c *Creds, externalID, sku string) error
	// Orders lists orders created from since.
	Orders(ctx context.Context, c *Creds, since time.Time) ([]Order, error)
}

// Rejected is a request the marketplace refused for a reason the store can fix.
type Rejected struct{ Msg string }

func (e Rejected) Error() string { return e.Msg }

// Endpoints are the hosts of each marketplace (overridden in tests).
type Endpoints struct {
	MeliAPI, MeliAuth     string
	AmazonAPI, AmazonAuth string
	AmazonMarketplace     string
}

// Default are the production hosts for Mexico.
var Default = Endpoints{
	MeliAPI: "https://api.mercadolibre.com", MeliAuth: "https://api.mercadolibre.com",
	AmazonAPI: "https://sellingpartnerapi-na.amazon.com", AmazonAuth: "https://api.amazon.com",
	AmazonMarketplace: "A1AM78C64UM0Y8",
}

// For returns the adapter of a provider.
func For(provider string, ep Endpoints, client *http.Client) (Adapter, error) {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	switch provider {
	case "mercadolibre":
		return &meli{ep: ep, http: client}, nil
	case "amazon":
		return &amazon{ep: ep, http: client}, nil
	}
	return nil, fmt.Errorf("canal desconocido: %s", provider)
}

// Names are the providers' display names.
var Names = map[string]string{"mercadolibre": "Mercado Libre", "amazon": "Amazon"}

// --- shared helpers ---

func decimal(minor int64) float64 { return math.Round(float64(minor)) / 100 }

func minor(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * 100))
}

func tokenForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, c *Creds) error {
	if c.AccessToken != "" && time.Until(c.Expires) > 2*time.Minute {
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Message      string `json:"message"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		msg := strings.TrimSpace(out.ErrorDesc + " " + out.Message)
		if msg == "" {
			msg = out.Error
		}
		return Rejected{"Las credenciales no son válidas o el permiso fue revocado: " + msg}
	}
	if resp.StatusCode >= 300 || out.AccessToken == "" {
		return fmt.Errorf("token: http %d", resp.StatusCode)
	}
	c.AccessToken = out.AccessToken
	if out.RefreshToken != "" {
		c.RefreshToken = out.RefreshToken // rotated
	}
	c.Expires = time.Now().Add(time.Duration(max(out.ExpiresIn, 60)) * time.Second)
	return nil
}

// call sends JSON and decodes JSON; 4xx is Rejected with the marketplace's message.
func call(ctx context.Context, client *http.Client, method, u string, hdr map[string]string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("%s %s: http %d", method, u, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return Rejected{rejectMessage(raw, resp.StatusCode)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("respuesta no válida: %w", err)
		}
	}
	return nil
}

// rejectMessage extracts the human message of a marketplace error.
func rejectMessage(raw []byte, status int) string {
	var e struct {
		Message string `json:"message"`
		Error   string `json:"error"`
		Cause   []struct {
			Message string `json:"message"`
		} `json:"cause"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(raw, &e)
	var parts []string
	for _, c := range e.Cause {
		parts = append(parts, c.Message)
	}
	for _, c := range e.Errors {
		parts = append(parts, c.Message)
	}
	if len(parts) == 0 && e.Message != "" {
		parts = append(parts, e.Message)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("El canal rechazó la solicitud (http %d)", status)
	}
	msg := strings.Join(parts, "; ")
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}

// IsRejected reports whether err is a refusal the store must fix.
func IsRejected(err error) bool {
	var r Rejected
	return errors.As(err, &r)
}

// ---------- Mercado Libre ----------

type meli struct {
	ep   Endpoints
	http *http.Client
}

func (m *meli) Refresh(ctx context.Context, c *Creds) error {
	return tokenForm(ctx, m.http, m.ep.MeliAuth+"/oauth/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID},
		"client_secret": {c.ClientSecret}, "refresh_token": {c.RefreshToken}}, c)
}

func (m *meli) auth(c *Creds) map[string]string {
	return map[string]string{"Authorization": "Bearer " + c.AccessToken}
}

func (m *meli) Check(ctx context.Context, c *Creds) (string, error) {
	if err := m.Refresh(ctx, c); err != nil {
		return "", err
	}
	var me struct {
		ID       int64  `json:"id"`
		Nickname string `json:"nickname"`
	}
	if err := call(ctx, m.http, "GET", m.ep.MeliAPI+"/users/me", m.auth(c), nil, &me); err != nil {
		return "", err
	}
	if c.SellerID == "" {
		c.SellerID = strconv.FormatInt(me.ID, 10)
	}
	if strconv.FormatInt(me.ID, 10) != c.SellerID {
		return "", Rejected{"El token es de otra cuenta de vendedor"}
	}
	return me.Nickname, nil
}

func (m *meli) Publish(ctx context.Context, c *Creds, externalID string, l Listing) (string, error) {
	if err := m.Refresh(ctx, c); err != nil {
		return "", err
	}
	if externalID != "" {
		return externalID, m.Update(ctx, c, externalID, l)
	}
	cat := l.Category
	if cat == "" {
		var pred []struct {
			CategoryID string `json:"category_id"`
		}
		if err := call(ctx, m.http, "GET", m.ep.MeliAPI+"/sites/MLM/domain_discovery/search?limit=1&q="+url.QueryEscape(l.Title), m.auth(c), nil, &pred); err != nil {
			return "", err
		}
		if len(pred) == 0 {
			return "", Rejected{"Mercado Libre no sugirió una categoría; elige una para este producto"}
		}
		cat = pred[0].CategoryID
	}
	pics := []map[string]string{}
	for _, u := range l.Images {
		pics = append(pics, map[string]string{"source": u})
	}
	attrs := []map[string]string{{"id": "SELLER_SKU", "value_name": l.SKU}}
	if l.Brand != "" {
		attrs = append(attrs, map[string]string{"id": "BRAND", "value_name": l.Brand})
	}
	if l.GTIN != "" {
		attrs = append(attrs, map[string]string{"id": "GTIN", "value_name": l.GTIN})
	}
	title := l.Title
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60])
	}
	body := map[string]any{"title": title, "category_id": cat, "price": decimal(l.Price), "currency_id": l.Currency, "available_quantity": l.Qty,
		"buying_mode": "buy_it_now", "listing_type_id": "gold_special", "condition": "new", "pictures": pics, "attributes": attrs}
	var out struct {
		ID string `json:"id"`
	}
	if err := call(ctx, m.http, "POST", m.ep.MeliAPI+"/items", m.auth(c), body, &out); err != nil {
		return "", err
	}
	if l.Description != "" {
		// The description is separate; a failure here does not undo the listing.
		_ = call(ctx, m.http, "POST", m.ep.MeliAPI+"/items/"+url.PathEscape(out.ID)+"/description", m.auth(c), map[string]string{"plain_text": l.Description}, nil)
	}
	return out.ID, nil
}

func (m *meli) Update(ctx context.Context, c *Creds, externalID string, l Listing) error {
	if err := m.Refresh(ctx, c); err != nil {
		return err
	}
	return call(ctx, m.http, "PUT", m.ep.MeliAPI+"/items/"+url.PathEscape(externalID), m.auth(c), map[string]any{"price": decimal(l.Price), "available_quantity": l.Qty}, nil)
}

func (m *meli) Close(ctx context.Context, c *Creds, externalID, _ string) error {
	if err := m.Refresh(ctx, c); err != nil {
		return err
	}
	return call(ctx, m.http, "PUT", m.ep.MeliAPI+"/items/"+url.PathEscape(externalID), m.auth(c), map[string]string{"status": "closed"}, nil)
}

func (m *meli) Orders(ctx context.Context, c *Creds, since time.Time) ([]Order, error) {
	if err := m.Refresh(ctx, c); err != nil {
		return nil, err
	}
	var out []Order
	for offset := 0; offset < 1000; offset += 50 {
		var page struct {
			Results []struct {
				ID          int64   `json:"id"`
				Status      string  `json:"status"`
				DateCreated string  `json:"date_created"`
				TotalAmount float64 `json:"total_amount"`
				CurrencyID  string  `json:"currency_id"`
				Buyer       struct {
					Nickname string `json:"nickname"`
				} `json:"buyer"`
				Items []struct {
					Item struct {
						SellerSKU         string `json:"seller_sku"`
						SellerCustomField string `json:"seller_custom_field"`
					} `json:"item"`
					Quantity  int64   `json:"quantity"`
					UnitPrice float64 `json:"unit_price"`
				} `json:"order_items"`
			} `json:"results"`
			Paging struct {
				Total int `json:"total"`
			} `json:"paging"`
		}
		q := url.Values{"seller": {c.SellerID}, "order.date_created.from": {since.UTC().Format("2006-01-02T15:04:05.000-00:00")}, "sort": {"date_asc"},
			"offset": {strconv.Itoa(offset)}, "limit": {"50"}}
		if err := call(ctx, m.http, "GET", m.ep.MeliAPI+"/orders/search?"+q.Encode(), m.auth(c), nil, &page); err != nil {
			return nil, err
		}
		for _, r := range page.Results {
			o := Order{ExternalID: strconv.FormatInt(r.ID, 10), Paid: r.Status == "paid", Cancelled: r.Status == "cancelled", Buyer: r.Buyer.Nickname,
				Currency: r.CurrencyID, Total: int64(math.Round(r.TotalAmount * 100))}
			o.Created, _ = time.Parse(time.RFC3339, r.DateCreated)
			for _, it := range r.Items {
				sku := it.Item.SellerSKU
				if sku == "" {
					sku = it.Item.SellerCustomField
				}
				o.Lines = append(o.Lines, OrderLine{SKU: sku, Qty: it.Quantity, Unit: int64(math.Round(it.UnitPrice * 100))})
			}
			out = append(out, o)
		}
		if offset+50 >= page.Paging.Total {
			break
		}
	}
	return out, nil
}

// ---------- Amazon (Selling Partner API) ----------

type amazon struct {
	ep   Endpoints
	http *http.Client
}

func (a *amazon) Refresh(ctx context.Context, c *Creds) error {
	return tokenForm(ctx, a.http, a.ep.AmazonAuth+"/auth/o2/token", url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID},
		"client_secret": {c.ClientSecret}, "refresh_token": {c.RefreshToken}}, c)
}

func (a *amazon) auth(c *Creds) map[string]string {
	return map[string]string{"x-amz-access-token": c.AccessToken}
}

func (a *amazon) Check(ctx context.Context, c *Creds) (string, error) {
	if c.SellerID == "" {
		return "", Rejected{"Escribe tu identificador de vendedor (Merchant Token) de Amazon"}
	}
	if err := a.Refresh(ctx, c); err != nil {
		return "", err
	}
	var out struct {
		Payload []struct {
			Marketplace struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"marketplace"`
			Participation struct {
				IsParticipating bool `json:"isParticipating"`
			} `json:"participation"`
		} `json:"payload"`
	}
	if err := call(ctx, a.http, "GET", a.ep.AmazonAPI+"/sellers/v1/marketplaceParticipations", a.auth(c), nil, &out); err != nil {
		return "", err
	}
	for _, p := range out.Payload {
		if p.Marketplace.ID == a.ep.AmazonMarketplace && p.Participation.IsParticipating {
			return p.Marketplace.Name, nil
		}
	}
	return "", Rejected{"Tu cuenta no vende en Amazon México"}
}

func (a *amazon) itemURL(c *Creds, sku string) string {
	return a.ep.AmazonAPI + "/listings/2021-08-01/items/" + url.PathEscape(c.SellerID) + "/" + url.PathEscape(sku) + "?marketplaceIds=" + url.QueryEscape(a.ep.AmazonMarketplace)
}

func (a *amazon) offer(l Listing) (any, any) {
	mk := a.ep.AmazonMarketplace
	price := []map[string]any{{"marketplace_id": mk, "currency": l.Currency, "our_price": []map[string]any{{"schedule": []map[string]any{{"value_with_tax": decimal(l.Price)}}}}}}
	qty := []map[string]any{{"fulfillment_channel_code": "DEFAULT", "quantity": l.Qty, "marketplace_id": mk}}
	return price, qty
}

// listingResult is how the Listings API answers: accepted or with issues.
type listingResult struct {
	Status string `json:"status"`
	Issues []struct {
		Message  string `json:"message"`
		Severity string `json:"severity"`
	} `json:"issues"`
}

func (r listingResult) err() error {
	if r.Status == "ACCEPTED" || r.Status == "" {
		return nil
	}
	var msgs []string
	for _, i := range r.Issues {
		if i.Severity == "ERROR" {
			msgs = append(msgs, i.Message)
		}
	}
	if len(msgs) == 0 {
		msgs = append(msgs, "Amazon no aceptó la publicación")
	}
	return Rejected{strings.Join(msgs, "; ")}
}

func (a *amazon) Publish(ctx context.Context, c *Creds, _ string, l Listing) (string, error) {
	if err := a.Refresh(ctx, c); err != nil {
		return "", err
	}
	mk := a.ep.AmazonMarketplace
	price, qty := a.offer(l)
	pt := l.Category
	if pt == "" {
		pt = "PRODUCT"
	}
	attrs := map[string]any{
		"item_name":                []map[string]any{{"value": l.Title, "language_tag": "es_MX", "marketplace_id": mk}},
		"condition_type":           []map[string]any{{"value": "new_new", "marketplace_id": mk}},
		"purchasable_offer":        price,
		"fulfillment_availability": qty,
	}
	if l.Brand != "" {
		attrs["brand"] = []map[string]any{{"value": l.Brand, "language_tag": "es_MX", "marketplace_id": mk}}
	}
	if l.Description != "" {
		attrs["product_description"] = []map[string]any{{"value": l.Description, "language_tag": "es_MX", "marketplace_id": mk}}
	}
	if l.GTIN != "" {
		attrs["externally_assigned_product_identifier"] = []map[string]any{{"type": "ean", "value": l.GTIN, "marketplace_id": mk}}
	}
	for i, u := range l.Images {
		key := "main_product_image_locator"
		if i > 0 {
			key = "other_product_image_locator_" + strconv.Itoa(i)
		}
		if i > 8 {
			break
		}
		attrs[key] = []map[string]any{{"media_location": u, "marketplace_id": mk}}
	}
	var res listingResult
	if err := call(ctx, a.http, "PUT", a.itemURL(c, l.SKU), a.auth(c), map[string]any{"productType": pt, "requirements": "LISTING", "attributes": attrs}, &res); err != nil {
		return "", err
	}
	return l.SKU, res.err()
}

func (a *amazon) Update(ctx context.Context, c *Creds, _ string, l Listing) error {
	if err := a.Refresh(ctx, c); err != nil {
		return err
	}
	price, qty := a.offer(l)
	pt := l.Category
	if pt == "" {
		pt = "PRODUCT"
	}
	var res listingResult
	if err := call(ctx, a.http, "PATCH", a.itemURL(c, l.SKU), a.auth(c), map[string]any{"productType": pt, "patches": []map[string]any{
		{"op": "replace", "path": "/attributes/purchasable_offer", "value": price},
		{"op": "replace", "path": "/attributes/fulfillment_availability", "value": qty},
	}}, &res); err != nil {
		return err
	}
	return res.err()
}

func (a *amazon) Close(ctx context.Context, c *Creds, _, sku string) error {
	if err := a.Refresh(ctx, c); err != nil {
		return err
	}
	return call(ctx, a.http, "DELETE", a.itemURL(c, sku), a.auth(c), nil, nil)
}

func (a *amazon) Orders(ctx context.Context, c *Creds, since time.Time) ([]Order, error) {
	if err := a.Refresh(ctx, c); err != nil {
		return nil, err
	}
	var out []Order
	next := ""
	for page := 0; page < 20; page++ {
		q := url.Values{"MarketplaceIds": {a.ep.AmazonMarketplace}, "CreatedAfter": {since.UTC().Format(time.RFC3339)}}
		if next != "" {
			q = url.Values{"MarketplaceIds": {a.ep.AmazonMarketplace}, "NextToken": {next}}
		}
		var res struct {
			Payload struct {
				Orders []struct {
					AmazonOrderID string `json:"AmazonOrderId"`
					OrderStatus   string `json:"OrderStatus"`
					PurchaseDate  string `json:"PurchaseDate"`
					OrderTotal    struct {
						CurrencyCode string `json:"CurrencyCode"`
						Amount       string `json:"Amount"`
					} `json:"OrderTotal"`
				} `json:"Orders"`
				NextToken string `json:"NextToken"`
			} `json:"payload"`
		}
		if err := call(ctx, a.http, "GET", a.ep.AmazonAPI+"/orders/v0/orders?"+q.Encode(), a.auth(c), nil, &res); err != nil {
			return nil, err
		}
		for _, r := range res.Payload.Orders {
			o := Order{ExternalID: r.AmazonOrderID, Buyer: "Cliente de Amazon", Currency: r.OrderTotal.CurrencyCode, Total: minor(r.OrderTotal.Amount),
				Paid: r.OrderStatus == "Unshipped" || r.OrderStatus == "Shipped" || r.OrderStatus == "PartiallyShipped", Cancelled: r.OrderStatus == "Canceled"}
			o.Created, _ = time.Parse(time.RFC3339, r.PurchaseDate)
			if o.Paid {
				var items struct {
					Payload struct {
						OrderItems []struct {
							SellerSKU       string `json:"SellerSKU"`
							QuantityOrdered int64  `json:"QuantityOrdered"`
							ItemPrice       struct {
								Amount string `json:"Amount"`
							} `json:"ItemPrice"`
						} `json:"OrderItems"`
					} `json:"payload"`
				}
				if err := call(ctx, a.http, "GET", a.ep.AmazonAPI+"/orders/v0/orders/"+url.PathEscape(r.AmazonOrderID)+"/orderItems", a.auth(c), nil, &items); err != nil {
					return nil, err
				}
				for _, it := range items.Payload.OrderItems {
					unit := minor(it.ItemPrice.Amount)
					if it.QuantityOrdered > 0 {
						unit /= it.QuantityOrdered // ItemPrice is the line total
					}
					o.Lines = append(o.Lines, OrderLine{SKU: it.SellerSKU, Qty: it.QuantityOrdered, Unit: unit})
				}
			}
			out = append(out, o)
		}
		next = res.Payload.NextToken
		if next == "" {
			break
		}
	}
	return out, nil
}
