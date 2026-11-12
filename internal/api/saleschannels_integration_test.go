package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/helloghc/apexx/internal/saleschannels"
)

// fakeMarket plays Mercado Libre and Amazon from their public API shapes.
type fakeMarket struct {
	mu        sync.Mutex
	calls     []string
	bodies    map[string][]map[string]any
	orders    []map[string]any
	amzOrders []map[string]any
	reject    map[string]bool // titles Mercado Libre rejects
}

func (f *fakeMarket) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, key)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if body != nil {
		f.bodies[key] = append(f.bodies[key], body)
	}
	w.Header().Set("Content-Type", "application/json")
	write := func(code int, v any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(v) }
	switch {
	case key == "POST /oauth/token" || key == "POST /auth/o2/token":
		form, _ := url.ParseQuery(string(raw))
		if form.Get("refresh_token") == "malo" {
			write(400, map[string]any{"error": "invalid_grant", "error_description": "refresh token revoked"})
			return
		}
		write(200, map[string]any{"access_token": "tok-" + form.Get("client_id"), "refresh_token": "rotado", "expires_in": 21600})
	case key == "GET /users/me":
		if r.Header.Get("Authorization") != "Bearer tok-app1" {
			write(401, map[string]any{"message": "invalid token"})
			return
		}
		write(200, map[string]any{"id": 123, "nickname": "TIENDAQA"})
	case key == "GET /sites/MLM/domain_discovery/search":
		write(200, []map[string]any{{"category_id": "MLM1234"}})
	case key == "POST /items":
		if f.reject[body["title"].(string)] {
			write(400, map[string]any{"message": "Validation error", "cause": []map[string]any{{"message": "El atributo MODEL es obligatorio"}}})
			return
		}
		write(201, map[string]any{"id": "MLM999"})
	case strings.HasPrefix(key, "PUT /items/") || strings.HasPrefix(key, "POST /items/"):
		write(200, map[string]any{"id": "MLM999"})
	case key == "GET /orders/search":
		write(200, map[string]any{"results": f.orders, "paging": map[string]any{"total": len(f.orders)}})
	case key == "GET /sellers/v1/marketplaceParticipations":
		write(200, map[string]any{"payload": []map[string]any{{"marketplace": map[string]any{"id": "A1AM78C64UM0Y8", "name": "Amazon.com.mx"}, "participation": map[string]any{"isParticipating": true}}}})
	case strings.HasPrefix(key, "PUT /listings/") || strings.HasPrefix(key, "PATCH /listings/") || strings.HasPrefix(key, "DELETE /listings/"):
		if r.Header.Get("x-amz-access-token") != "tok-amz1" {
			write(403, map[string]any{"errors": []map[string]any{{"message": "Access denied"}}})
			return
		}
		write(200, map[string]any{"status": "ACCEPTED", "sku": "S1"})
	case key == "GET /orders/v0/orders":
		write(200, map[string]any{"payload": map[string]any{"Orders": f.amzOrders}})
	case strings.HasPrefix(key, "GET /orders/v0/orders/"):
		write(200, map[string]any{"payload": map[string]any{"OrderItems": []map[string]any{{"SellerSKU": "S1", "QuantityOrdered": 2, "ItemPrice": map[string]any{"Amount": "300.00"}}}}})
	default:
		write(404, map[string]any{"message": "no route " + key})
	}
}

func (f *fakeMarket) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeMarket) last(key string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.bodies[key]
	if len(b) == 0 {
		return nil
	}
	return b[len(b)-1]
}

// Mercado Libre and Amazon: credentials checked and kept secret, listings
// published with markup and stock buffer, rejections shown with the
// channel's reason, orders imported once (sharing stock) and cancelled when
// the channel cancels, and stores never see each other's channels.
func TestSalesChannels(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	f := &fakeMarket{bodies: map[string][]map[string]any{}, reject: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	e.server.ChannelEndpoints = saleschannels.Endpoints{MeliAPI: srv.URL, MeliAuth: srv.URL, AmazonAPI: srv.URL, AmazonAuth: srv.URL, AmazonMarketplace: "A1AM78C64UM0Y8"}

	if st, r := e.do("PUT", "/v1/sales-channels/mercadolibre", e.adminA, m{"clientId": "app1", "clientSecret": "secreto-super", "refreshToken": "malo"}); st != 422 || !strings.Contains(toJSON(r), "revoked") {
		t.Fatalf("bad token = %d %v", st, r)
	}
	if st, _ := e.do("PUT", "/v1/sales-channels/mercadolibre", e.sfA, m{"clientId": "app1", "clientSecret": "x", "refreshToken": "y"}); st != 403 {
		t.Fatalf("storefront key connected a channel: %d", st)
	}
	st, r := e.do("PUT", "/v1/sales-channels/mercadolibre", e.adminA, m{"clientId": "app1", "clientSecret": "secreto-super", "refreshToken": "bueno",
		"settings": m{"markupBps": 1000, "stockBuffer": 2, "importOrders": true}})
	if st != 200 || strings.Contains(toJSON(r), "secreto-super") || strings.Contains(toJSON(r), "bueno") {
		t.Fatalf("connect = %d %v", st, r)
	}
	if acc := r["items"].([]any)[0].(map[string]any); acc["connected"] != true || acc["sellerId"] != "123" {
		t.Fatalf("account = %v", acc)
	}
	// List S1 and S2; S2 is rejected by the channel with its reason.
	tA, _ := e.server.Proj.Tenant("store-a")
	_, p2, _ := tA.Catalog.VariantBySKU("S2")
	f.reject[p2.Title] = true
	e.must(202, "POST", "/v1/sales-channels/mercadolibre/listings", e.adminA, m{"skus": []string{"S1", "S2"}})
	if st, _ := e.do("POST", "/v1/sales-channels/mercadolibre/listings", e.adminA, m{"skus": []string{"NOPE"}}); st != 400 {
		t.Fatalf("unknown sku listed: %d", st)
	}
	if _, err := e.server.RunSalesChannels(ctx); err != nil {
		t.Fatal(err)
	}
	q, _ := tA.Pricing.Resolve("mx", "S1", 1)
	item := f.last("POST /items")
	if item == nil || item["category_id"] != "MLM1234" || item["price"].(float64) != float64(q.Unit.Amount*11/10)/100 || item["available_quantity"].(float64) != float64(e.available("S1")-2) {
		t.Fatalf("published item = %v (price %d, stock %v)", item, q.Unit.Amount, e.available("S1"))
	}
	ls := e.must(200, "GET", "/v1/sales-channels/mercadolibre/listings", e.adminA, nil)["items"].([]any)
	byS := map[string]map[string]any{}
	for _, x := range ls {
		byS[x.(map[string]any)["sku"].(string)] = x.(map[string]any)
	}
	if byS["S1"]["status"] != "active" || byS["S1"]["externalId"] != "MLM999" || byS["S2"]["status"] != "error" || !strings.Contains(byS["S2"]["error"].(string), "MODEL") {
		t.Fatalf("listings = %v", ls)
	}
	// Nothing changed: no new calls. A new markup reaches the channel.
	puts := f.count("PUT /items/")
	e.must(200, "PUT", "/v1/sales-channels/mercadolibre", e.adminA, m{"settings": m{"markupBps": 0, "stockBuffer": 2, "importOrders": true}})
	if _, err := e.server.RunSalesChannels(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("PUT /items/") != puts+1 || f.last("PUT /items/MLM999")["price"].(float64) != float64(q.Unit.Amount)/100 {
		t.Fatalf("markup change pushed %d times: %v", f.count("PUT /items/")-puts, f.last("PUT /items/MLM999"))
	}
	if _, err := e.server.RunSalesChannels(ctx); err != nil || f.count("PUT /items/") != puts+1 {
		t.Fatalf("an unchanged listing was pushed again")
	}
	// A paid order on the channel becomes an order here, once, and takes stock.
	before := e.available("S1")
	f.orders = []map[string]any{{"id": 2000001, "status": "paid", "date_created": "2026-11-12T10:00:00.000-06:00", "total_amount": 250.0, "currency_id": "MXN",
		"buyer": map[string]any{"nickname": "COMPRADOR1"}, "order_items": []map[string]any{{"item": map[string]any{"seller_sku": "S1"}, "quantity": 1, "unit_price": 250.0}}}}
	if r := e.must(200, "POST", "/v1/sales-channels/mercadolibre/sync", e.adminA, nil); r["orders"].(float64) != 1 {
		t.Fatalf("sync = %v", r)
	}
	e.must(200, "POST", "/v1/sales-channels/mercadolibre/sync", e.adminA, nil)
	var orderID, source string
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*), max(order_id) FROM channel_orders WHERE tenant_id='store-a'`).Scan(&n, &orderID)
	_ = e.db.System.QueryRow(ctx, `SELECT source FROM orders WHERE id=$1`, orderID).Scan(&source)
	if n != 1 || source != "marketplace" || e.available("S1") != before-1 {
		t.Fatalf("imported %d orders, source %q, stock %v -> %v", n, source, before, e.available("S1"))
	}
	o := e.must(200, "GET", "/v1/orders/"+orderID, e.adminA, nil)
	if o["grandTotal"].(float64) < 25000 || !strings.Contains(toJSON(o["customer"]), "COMPRADOR1") {
		t.Fatalf("imported order = %v %v", o["grandTotal"], o["customer"])
	}
	// Cancelled on the channel: cancelled here and the stock comes back.
	f.orders[0]["status"] = "cancelled"
	e.must(200, "POST", "/v1/sales-channels/mercadolibre/sync", e.adminA, nil)
	if st := e.must(200, "GET", "/v1/orders/"+orderID, e.adminA, nil)["status"]; st != "refunded" && st != "cancelled" {
		t.Fatalf("status after channel cancel = %v", st)
	}
	if e.available("S1") != before {
		t.Fatalf("stock after cancel = %v, want %v", e.available("S1"), before)
	}
	// Closing a listing.
	e.must(204, "DELETE", "/v1/sales-channels/mercadolibre/listings/S1", e.adminA, nil)
	if _, err := e.server.RunSalesChannels(ctx); err != nil {
		t.Fatal(err)
	}
	if f.last("PUT /items/MLM999")["status"] != "closed" {
		t.Fatalf("close = %v", f.last("PUT /items/MLM999"))
	}
	// Store B sees nothing of store A's channels.
	if acc := e.must(200, "GET", "/v1/sales-channels", e.adminB, nil)["items"].([]any)[0].(map[string]any); acc["connected"] != false {
		t.Fatalf("store B sees store A's account: %v", acc)
	}
	if st, _ := e.do("GET", "/v1/sales-channels/mercadolibre/listings", e.adminB, nil); st != 404 {
		t.Fatalf("store B listings: %d", st)
	}
	// The secrets are stored encrypted.
	var blob []byte
	_ = e.db.System.QueryRow(ctx, `SELECT creds FROM channel_accounts WHERE tenant_id='store-a'`).Scan(&blob)
	if strings.Contains(string(blob), "secreto-super") {
		t.Fatal("credentials stored in clear")
	}

	// Amazon: listing through the Listings API, orders with their items.
	if st, r := e.do("PUT", "/v1/sales-channels/amazon", e.adminA, m{"clientId": "amz1", "clientSecret": "s", "refreshToken": "r"}); st != 422 {
		t.Fatalf("amazon without seller id = %d %v", st, r)
	}
	e.must(200, "PUT", "/v1/sales-channels/amazon", e.adminA, m{"clientId": "amz1", "clientSecret": "s", "refreshToken": "r", "sellerId": "A2SELLER", "settings": m{"importOrders": true}})
	e.must(202, "POST", "/v1/sales-channels/amazon/listings", e.adminA, m{"skus": []string{"S1"}})
	if _, err := e.server.RunSalesChannels(ctx); err != nil {
		t.Fatal(err)
	}
	put := f.last("PUT /listings/2021-08-01/items/A2SELLER/S1")
	if put == nil || put["productType"] != "PRODUCT" || !strings.Contains(toJSON(put["attributes"]), "fulfillment_availability") {
		t.Fatalf("amazon listing = %v", put)
	}
	f.amzOrders = []map[string]any{{"AmazonOrderId": "702-1", "OrderStatus": "Unshipped", "PurchaseDate": "2026-11-12T16:00:00Z", "OrderTotal": map[string]any{"CurrencyCode": "MXN", "Amount": "300.00"}},
		{"AmazonOrderId": "702-2", "OrderStatus": "Pending", "PurchaseDate": "2026-11-12T16:05:00Z"}}
	if r := e.must(200, "POST", "/v1/sales-channels/amazon/sync", e.adminA, nil); r["orders"].(float64) != 1 {
		t.Fatalf("amazon sync = %v", r)
	}
}
