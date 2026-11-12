package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/projection"
)

// MCP server per store (Model Context Protocol, Streamable HTTP transport,
// JSON responses). AI assistants can search the catalogue, read a product,
// price a cart with shipping and hand the shopper a link to that cart. There
// is no tool that places or pays an order: the shopper always confirms in
// the store's own checkout, with its limits and fraud checks. The store opts
// in from the panel; until then the endpoint answers that it is closed.

var mcpLimiter = &shopLimiter{n: 120}

var mcpVersions = map[string]bool{"2025-06-18": true, "2025-03-26": true, "2024-11-05": true}

const mcpLatest = "2025-06-18"

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) routesMCP(handle func(string, http.HandlerFunc)) {
	handle("POST /mcp/{tenant}", s.mcp)
	handle("GET /mcp/{tenant}", s.mcpGet)
	handle("DELETE /mcp/{tenant}", s.mcpGet)
}

func (s *Server) mcpGet(w http.ResponseWriter, r *http.Request) {
	// No server-initiated stream: every answer comes with its request.
	w.Header().Set("Allow", "POST")
	http.Error(w, "Use POST", http.StatusMethodNotAllowed)
}

func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, e *rpcErr) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if len(id) == 0 {
		out["id"] = nil
	}
	if e != nil {
		out["error"] = e
	} else {
		out["result"] = result
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) mcpOpen(r *http.Request, tenant string) bool {
	if l := s.learned(tenant); l != nil {
		return l.cfg.MCP
	}
	var cfg aiConfig
	ok, err := s.DB.GetDoc(r.Context(), tenant, aiDocKind, aiDocID, &cfg)
	return err == nil && ok && cfg.MCP
}

func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	t, ok := s.Proj.Tenant(tenant)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// A web page may not drive the server from a shopper's browser.
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			http.Error(w, "Origin not allowed", http.StatusForbidden)
			return
		}
	}
	if !mcpLimiter.allow(tenant + "|" + s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10+1))
	if err != nil || len(body) > 64<<10 {
		writeRPC(w, nil, nil, &rpcErr{-32600, "Request too large"})
		return
	}
	var req rpcReq
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		writeRPC(w, nil, nil, &rpcErr{-32700, "Parse error: send one JSON-RPC 2.0 request"})
		return
	}
	if len(req.ID) == 0 { // a notification: nothing to answer
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !s.mcpOpen(r, tenant) {
		writeRPC(w, req.ID, nil, &rpcErr{-32001, "This store has not enabled AI assistants"})
		return
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		v := mcpLatest
		if mcpVersions[p.ProtocolVersion] {
			v = p.ProtocolVersion
		}
		name := t.Site().Settings.Name
		if name == "" {
			name = tenant
		}
		writeRPC(w, req.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "apexx-store", "title": name, "version": "1.0"},
			"instructions": "Tools of the online store «" + name + "». Prices are in the store's currency, taxes included. " +
				"You can search products, read one, price a cart with shipping to a postal code and create a link to that cart. " +
				"You cannot place or pay orders: give the shopper the cart link so they review it and pay in the store's checkout.",
		}, nil)
	case "ping":
		writeRPC(w, req.ID, map[string]any{}, nil)
	case "tools/list":
		writeRPC(w, req.ID, map[string]any{"tools": mcpTools}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeRPC(w, req.ID, nil, &rpcErr{-32602, "Invalid params"})
			return
		}
		res, err := s.mcpCall(r, t, p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			writeRPC(w, req.ID, nil, &rpcErr{-32602, "Unknown tool: " + p.Name})
			return
		}
		if err != nil {
			// Tool errors go back to the model so it can correct itself.
			writeRPC(w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil)
			return
		}
		b, _ := json.Marshal(res)
		writeRPC(w, req.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": res}, nil)
	default:
		writeRPC(w, req.ID, nil, &rpcErr{-32601, "Method not found: " + req.Method})
	}
}

var errUnknownTool = errors.New("unknown tool")

func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

var itemsSchema = map[string]any{"type": "array", "minItems": 1, "maxItems": 50, "items": obj(map[string]any{
	"sku": map[string]any{"type": "string", "description": "Variant SKU from get_product"},
	"qty": map[string]any{"type": "integer", "minimum": 1, "maximum": 999},
}, "sku", "qty")}

var readOnly = map[string]any{"readOnlyHint": true, "openWorldHint": false}

var mcpTools = []map[string]any{
	{"name": "search_products", "title": "Search products", "annotations": readOnly,
		"description": "Search the store's catalogue by words (typos and synonyms are understood). Returns products with price, availability and handle.",
		"inputSchema": obj(map[string]any{"query": map[string]any{"type": "string", "maxLength": 80}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}, "query")},
	{"name": "get_product", "title": "Get a product", "annotations": readOnly,
		"description": "Details of one product by handle: description, brand, and every variant with SKU, price and stock.",
		"inputSchema": obj(map[string]any{"handle": map[string]any{"type": "string", "maxLength": 200}}, "handle")},
	{"name": "ask_store", "title": "Ask the store", "annotations": readOnly,
		"description": "Answer a question with the store's own information: shipping, returns, warranty, payment, and (with handle) one product's description, specifications and reviews. Prices, stock and variants come from the store's system. Returns the answer, its kind (fact, quote, written or unknown) and its sources; kind unknown means the store does not say it: do not guess.",
		"inputSchema": obj(map[string]any{"question": map[string]any{"type": "string", "maxLength": 300}, "handle": map[string]any{"type": "string", "maxLength": 200}}, "question")},
	{"name": "quote_cart", "title": "Price a cart", "annotations": readOnly,
		"description": "Price a cart as the store would: promotions, taxes and, with a postal code, the shipping options with their cost and delivery estimate.",
		"inputSchema": obj(map[string]any{"items": itemsSchema, "postalCode": map[string]any{"type": "string", "maxLength": 10}}, "items")},
	{"name": "create_cart_link", "title": "Create a cart link", "annotations": map[string]any{"readOnlyHint": true, "openWorldHint": false},
		"description": "Create a link that opens the store with this cart ready. Give it to the shopper: they review it and pay in the store's checkout. This does not place an order.",
		"inputSchema": obj(map[string]any{"items": itemsSchema}, "items")},
}

type mcpItem struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

func decodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %v", err)
	}
	return nil
}

func mcpAmount(m money.Money) string { return fmt.Sprintf("%d.%02d", m.Amount/100, m.Amount%100) }

var tagRe = regexp.MustCompile(`<[^>]*>`)

func mcpText(s string, max int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

func (s *Server) mcpCall(r *http.Request, t *projection.Tenant, name string, raw json.RawMessage) (any, error) {
	base := s.publicOrigin(r) + "/s/" + t.ID + "/main"
	of := offerOf(t, mainChannel)
	cur := "MXN"
	if c, ok := t.Channel(mainChannel); ok {
		cur = string(c.Currency)
	}
	checkItems := func(items []mcpItem) ([]cart.Item, error) {
		if len(items) == 0 || len(items) > 50 {
			return nil, fmt.Errorf("items: between 1 and 50 lines")
		}
		out := make([]cart.Item, 0, len(items))
		for _, it := range items {
			if it.Qty < 1 || it.Qty > 999 {
				return nil, fmt.Errorf("qty of %s must be between 1 and 999", it.SKU)
			}
			v, p, err := t.Catalog.VariantBySKU(it.SKU)
			if err != nil || p.Status != "active" {
				return nil, fmt.Errorf("unknown SKU %q: use search_products and get_product to find SKUs", it.SKU)
			}
			_ = v
			out = append(out, cart.Item{SKU: it.SKU, Qty: it.Qty})
		}
		return out, nil
	}
	switch name {
	case "search_products":
		var a struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		if a.Limit < 1 || a.Limit > 20 {
			a.Limit = 10
		}
		q := catalog.BrowseQuery{Q: strings.TrimSpace(a.Query), Limit: a.Limit}
		s.searchQuery(t, &q)
		s.narrow(t, &q)
		res := catalog.Browse(t.Catalog.ProductsIn(q.Only), t.Taxo, of, q)
		items := make([]map[string]any, 0, len(res.Items))
		for i, p := range res.Items {
			o := res.Offers[i]
			it := map[string]any{"handle": p.Handle, "title": p.Title, "brand": p.Brand, "inStock": o.InStock, "url": base + "/p/" + p.Handle}
			if o.HasPrice {
				it["price"] = mcpAmount(money.New(o.Price, money.Currency(cur)))
			}
			items = append(items, it)
		}
		return map[string]any{"total": res.Total, "currency": cur, "items": items}, nil
	case "get_product":
		var a struct {
			Handle string `json:"handle"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		p, ok := t.Catalog.ByHandle(strings.TrimSpace(a.Handle))
		if !ok || t.Search().HiddenSet()[p.ID] {
			return nil, fmt.Errorf("no product with handle %q", a.Handle)
		}
		req := cart.Request{Channel: mainChannel}
		for _, v := range p.Variants {
			req.Items = append(req.Items, cart.Item{SKU: v.SKU, Qty: 1})
		}
		lines := map[string]cart.Line{}
		if res, err := t.Simulate(req); err == nil {
			for _, l := range res.Lines {
				lines[l.SKU] = l
			}
		}
		vars := make([]map[string]any, 0, len(p.Variants))
		for _, v := range p.Variants {
			vv := map[string]any{"sku": v.SKU, "title": v.Title}
			if l, ok := lines[v.SKU]; ok {
				vv["price"], vv["available"] = mcpAmount(l.Unit), l.Available
			} else {
				vv["available"] = 0
			}
			vars = append(vars, vv)
		}
		out := map[string]any{"handle": p.Handle, "title": p.Title, "brand": p.Brand, "description": mcpText(p.Description, 1500),
			"currency": cur, "variants": vars, "url": base + "/p/" + p.Handle}
		if sum := s.reviewSummary(r.Context(), t.ID, p.ID); sum != nil {
			out["reviewSummary"] = sum // customers' own sentences, with their author
		}
		return out, nil
	case "ask_store":
		var a struct {
			Question string `json:"question"`
			Handle   string `json:"handle"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		q := strings.TrimSpace(a.Question)
		if n := len([]rune(q)); n < 3 || n > 300 {
			return nil, fmt.Errorf("question: between 3 and 300 characters")
		}
		var prod *catalog.Product
		if a.Handle != "" {
			pp, ok := t.Catalog.ByHandle(a.Handle)
			if !ok || pp.Status != "active" {
				return nil, fmt.Errorf("no product with handle %q", a.Handle)
			}
			prod = &pp
		}
		ans := s.answerQuestion(r.Context(), t, base, mainChannel, q, prod)
		for i := range ans.Sources {
			if strings.HasPrefix(ans.Sources[i].URL, "/") {
				ans.Sources[i].URL = s.publicOrigin(r) + ans.Sources[i].URL
			}
		}
		return ans, nil
	case "quote_cart":
		var a struct {
			Items      []mcpItem `json:"items"`
			PostalCode string    `json:"postalCode"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		items, err := checkItems(a.Items)
		if err != nil {
			return nil, err
		}
		res, err := t.Simulate(cart.Request{Channel: mainChannel, Items: items, PostalCode: strings.TrimSpace(a.PostalCode)})
		if err != nil {
			return nil, fmt.Errorf("the cart could not be priced: %v", err)
		}
		lines := make([]map[string]any, 0, len(res.Lines))
		for _, l := range res.Lines {
			lines = append(lines, map[string]any{"sku": l.SKU, "title": l.Title, "qty": l.Qty, "requested": l.RequestedQty, "unit": mcpAmount(l.Unit), "total": mcpAmount(l.Total)})
		}
		ship := []map[string]any{}
		for _, o := range res.ShippingOptions {
			if o.Available {
				ship = append(ship, map[string]any{"name": o.Name, "carrier": o.Carrier, "price": mcpAmount(money.New(o.Price, money.Currency(cur)))})
			}
		}
		msgs := []string{}
		for _, m := range res.Messages {
			msgs = append(msgs, m.Text)
		}
		return map[string]any{"currency": cur, "lines": lines, "items": mcpAmount(res.Totals.Items), "discounts": mcpAmount(res.Totals.Discounts),
			"shipping": mcpAmount(res.Totals.Shipping), "taxIncluded": mcpAmount(res.Totals.Tax), "total": mcpAmount(res.Totals.Grand), "shippingOptions": ship, "messages": msgs}, nil
	case "create_cart_link":
		var a struct {
			Items []mcpItem `json:"items"`
		}
		if err := decodeArgs(raw, &a); err != nil {
			return nil, err
		}
		items, err := checkItems(a.Items)
		if err != nil {
			return nil, err
		}
		parts := make([]string, 0, len(items))
		for _, it := range items {
			parts = append(parts, url.QueryEscape(it.SKU)+":"+strconv.FormatInt(it.Qty, 10))
		}
		return map[string]any{"url": base + "/cart?from=ai&add=" + strings.Join(parts, ","),
			"note": "Share this link with the shopper. They review the cart and pay in the store's checkout; no order exists until they do."}, nil
	}
	return nil, errUnknownTool
}
