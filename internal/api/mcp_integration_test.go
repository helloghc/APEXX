package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// MCP: closed until the store turns it on; then an assistant can search,
// read a product, price a cart with shipping and create a cart link, and no
// tool can place an order. Bad input comes back as a tool error the model can
// read, other stores' products do not exist, and browser pages from other
// sites cannot call it.
func TestMCPServer(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	call := func(tenant string, body any, hdr ...string) (int, map[string]any) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", e.srv.URL+"/mcp/"+tenant, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out
	}
	rpc := func(id int, method string, params any) map[string]any {
		return m{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	}
	tool := func(tenant, name string, args any) map[string]any {
		st, r := call(tenant, rpc(9, "tools/call", m{"name": name, "arguments": args}))
		if st != 200 || r["result"] == nil {
			t.Fatalf("%s = %d %v", name, st, r)
		}
		return r["result"].(map[string]any)
	}
	// Closed by default.
	if _, r := call("store-a", rpc(1, "initialize", m{"protocolVersion": "2025-06-18"})); r["error"] == nil || r["error"].(map[string]any)["code"].(float64) != -32001 {
		t.Fatalf("closed store answered: %v", r)
	}
	e.must(200, "PUT", "/v1/ai/settings", e.adminA, m{"mcp": true})
	_, r := call("store-a", rpc(1, "initialize", m{"protocolVersion": "2025-06-18", "capabilities": m{}, "clientInfo": m{"name": "test", "version": "1"}}))
	res := r["result"].(map[string]any)
	if res["protocolVersion"] != "2025-06-18" || res["capabilities"].(map[string]any)["tools"] == nil {
		t.Fatalf("initialize = %v", r)
	}
	if _, r := call("store-a", rpc(2, "initialize", m{"protocolVersion": "1999-01-01"})); r["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unknown version not negotiated: %v", r)
	}
	if st, _ := call("store-a", m{"jsonrpc": "2.0", "method": "notifications/initialized"}); st != 202 {
		t.Fatalf("notification = %d", st)
	}
	_, r = call("store-a", rpc(3, "tools/list", m{}))
	tools := r["result"].(map[string]any)["tools"].([]any)
	names := []string{}
	for _, x := range tools {
		n := x.(map[string]any)["name"].(string)
		names = append(names, n)
		if strings.Contains(n, "order") || strings.Contains(n, "pay") || strings.Contains(n, "checkout") {
			t.Fatalf("a tool can place orders: %s", n)
		}
	}
	if strings.Join(names, ",") != "search_products,get_product,quote_cart,create_cart_link" {
		t.Fatalf("tools = %v", names)
	}
	found := tool("store-a", "search_products", m{"query": "prodcto", "limit": 5})["structuredContent"].(map[string]any)
	if found["total"].(float64) != 3 || found["items"].([]any)[0].(map[string]any)["price"] != "100.00" {
		t.Fatalf("search = %v", found)
	}
	p := tool("store-a", "get_product", m{"handle": "p1"})["structuredContent"].(map[string]any)
	v := p["variants"].([]any)[0].(map[string]any)
	if v["sku"] != "S1" || v["price"] != "100.00" || v["available"].(float64) != 10 {
		t.Fatalf("product = %v", p)
	}
	q := tool("store-a", "quote_cart", m{"items": []m{{"sku": "S1", "qty": 2}, {"sku": "S2", "qty": 1}}, "postalCode": "53129"})["structuredContent"].(map[string]any)
	if q["items"] != "300.00" || len(q["shippingOptions"].([]any)) == 0 {
		t.Fatalf("quote = %v", q)
	}
	link := tool("store-a", "create_cart_link", m{"items": []m{{"sku": "S1", "qty": 2}}})["structuredContent"].(map[string]any)["url"].(string)
	if !strings.HasSuffix(link, "/s/store-a/main/cart?from=ai&add=S1:2") {
		t.Fatalf("link = %s", link)
	}
	// Errors the model can read and fix.
	if bad := tool("store-a", "quote_cart", m{"items": []m{{"sku": "NOPE", "qty": 1}}}); bad["isError"] != true {
		t.Fatalf("unknown sku = %v", bad)
	}
	if bad := tool("store-a", "get_product", m{"handle": "p1", "extra": 1}); bad["isError"] != true {
		t.Fatalf("unknown argument = %v", bad)
	}
	if _, r := call("store-a", rpc(5, "tools/call", m{"name": "place_order", "arguments": m{}})); r["error"] == nil {
		t.Fatalf("unknown tool = %v", r)
	}
	if _, r := call("store-a", rpc(6, "orders/create", m{})); r["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Fatalf("unknown method = %v", r)
	}
	// Nothing was ordered.
	var n int
	_ = e.db.System.QueryRow(ctx, `SELECT count(*) FROM orders WHERE tenant_id='store-a'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d orders created through MCP", n)
	}
	// Another site's page cannot call it from a shopper's browser.
	if st, _ := call("store-a", rpc(7, "ping", m{}), "Origin", "https://evil.example"); st != 403 {
		t.Fatalf("foreign origin = %d", st)
	}
	// Store B is closed and does not know A's products.
	if _, r := call("store-b", rpc(8, "tools/list", m{})); r["error"] == nil {
		t.Fatalf("store B open: %v", r)
	}
	if st, _ := call("nope", rpc(8, "ping", m{})); st != 404 {
		t.Fatalf("unknown store = %d", st)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/mcp/store-a", nil)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 405 {
		t.Fatalf("GET = %d", res.StatusCode)
	}
}
