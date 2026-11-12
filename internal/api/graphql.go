package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	graphql "github.com/graph-gophers/graphql-go"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/projection"
)

// Storefront GraphQL: the public catalog and the cart, asking for exactly the
// fields a page needs. Read-only plus cart pricing; no personal data. Limits:
// depth 7, 16 KB per query, 50 items per page, the same rate limit as the cart.

const storefrontSchema = `
schema { query: Query }
type Query {
  shop: Shop!
  product(handle: String!): Product
  products(first: Int = 20, after: String, query: String): ProductConnection!
  categories: [Category!]!
  cart(items: [CartItemInput!]!, postalCode: String, channel: String): Cart!
}
type Shop { name: String!, currency: String!, channel: String! }
type Money { amount: Int!, currency: String!, formatted: String! }
type Image { url: String!, alt: String! }
type Variant { sku: String!, title: String!, available: Int!, price: Money, compareAtPrice: Money, preorder: Boolean! }
type Product {
  id: ID!
  handle: String!
  title: String!
  description: String!
  brand: String!
  url: String!
  available: Boolean!
  price: Money
  images: [Image!]!
  variants: [Variant!]!
  kit: [KitComponent!]!
}
type KitComponent { sku: String!, qty: Int! }
type ProductConnection { nodes: [Product!]!, endCursor: String, hasNextPage: Boolean! }
type Category { id: ID!, handle: String!, name: String!, parentId: String! }
input CartItemInput { sku: String!, qty: Int! }
type CartLine { sku: String!, title: String!, qty: Int!, unit: Money!, total: Money! }
type Cart { lines: [CartLine!]!, items: Money!, discounts: Money!, shipping: Money!, total: Money!, payLater: Money!, messages: [String!]! }
`

type gqlRoot struct{}

type gqlCtxKey struct{}

type gqlCtx struct {
	t       *projection.Tenant
	channel string
	cur     money.Currency
	base    string
}

func gctx(ctx context.Context) gqlCtx { return ctx.Value(gqlCtxKey{}).(gqlCtx) }

func gmoney(m money.Money) *gqlMoney { return &gqlMoney{m} }

type gqlMoney struct{ m money.Money }

func (m *gqlMoney) Amount() int32     { return int32(min(m.m.Amount, 2147483647)) }
func (m *gqlMoney) Currency() string  { return string(m.m.Currency) }
func (m *gqlMoney) Formatted() string { return pesos(m.m) }

type gqlShop struct{ c gqlCtx }

func (g *gqlShop) Name() string     { return g.c.t.Site().Settings.Name }
func (g *gqlShop) Currency() string { return string(g.c.cur) }
func (g *gqlShop) Channel() string  { return g.c.channel }

func (r *gqlRoot) Shop(ctx context.Context) *gqlShop { return &gqlShop{gctx(ctx)} }

type gqlProduct struct {
	c gqlCtx
	p catalog.Product
}

func (r *gqlRoot) Product(ctx context.Context, a struct{ Handle string }) *gqlProduct {
	c := gctx(ctx)
	p, ok := c.t.Catalog.ByHandle(a.Handle)
	if !ok || p.Status != "active" {
		return nil
	}
	return &gqlProduct{c, p}
}

type gqlConnection struct {
	nodes []*gqlProduct
	end   *string
}

func (g *gqlConnection) Nodes() []*gqlProduct { return g.nodes }
func (g *gqlConnection) EndCursor() *string   { return g.end }
func (g *gqlConnection) HasNextPage() bool    { return g.end != nil }

func (r *gqlRoot) Products(ctx context.Context, a struct {
	First int32
	After *string
	Query *string
}) (*gqlConnection, error) {
	c := gctx(ctx)
	n := int(a.First)
	if n < 1 || n > 50 {
		return nil, gqlErr("first va de 1 a 50")
	}
	after, q := "", ""
	if a.After != nil {
		after = *a.After
	}
	if a.Query != nil {
		q = *a.Query
	}
	list, next := c.t.Catalog.Search(q, after, n)
	out := &gqlConnection{}
	for _, p := range list {
		out.nodes = append(out.nodes, &gqlProduct{c, p})
	}
	if next != "" {
		out.end = &next
	}
	return out, nil
}

type gqlCategory struct{ c catalog.Category }

func (g *gqlCategory) ID() graphql.ID   { return graphql.ID(g.c.ID) }
func (g *gqlCategory) Handle() string   { return g.c.Handle }
func (g *gqlCategory) Name() string     { return g.c.Name }
func (g *gqlCategory) ParentID() string { return g.c.ParentID }

func (r *gqlRoot) Categories(ctx context.Context) []*gqlCategory {
	var out []*gqlCategory
	for _, c := range gctx(ctx).t.Taxo.Categories() {
		if c.Active {
			out = append(out, &gqlCategory{c})
		}
	}
	return out
}

func (g *gqlProduct) ID() graphql.ID      { return graphql.ID(g.p.ID) }
func (g *gqlProduct) Handle() string      { return g.p.Handle }
func (g *gqlProduct) Title() string       { return g.p.Title }
func (g *gqlProduct) Description() string { return g.p.Description }
func (g *gqlProduct) Brand() string       { return g.p.Brand }
func (g *gqlProduct) URL() string         { return g.c.base + "/p/" + g.p.Handle }
func (g *gqlProduct) Available() bool {
	for _, v := range g.p.Variants {
		if g.c.t.Inventory.Available(v.SKU) > 0 {
			return true
		}
	}
	return false
}
func (g *gqlProduct) Price() *gqlMoney {
	for _, v := range g.p.Variants {
		if q, ok := g.c.t.Pricing.Resolve(g.c.channel, v.SKU, 1); ok {
			return gmoney(q.Unit)
		}
	}
	return nil
}
func (g *gqlProduct) Images() []*gqlImage {
	var out []*gqlImage
	for _, id := range g.p.Media {
		if m, ok := g.c.t.Taxo.Media(id); ok {
			mv := viewMedia(g.c.t.ID, m)
			out = append(out, &gqlImage{mv.URL, m.Alt})
		}
	}
	return out
}
func (g *gqlProduct) Variants() []*gqlVariant {
	out := make([]*gqlVariant, len(g.p.Variants))
	for i, v := range g.p.Variants {
		out[i] = &gqlVariant{g.c, v}
	}
	return out
}
func (g *gqlProduct) Kit() []*gqlKit {
	var out []*gqlKit
	if len(g.p.Variants) > 0 {
		for _, c := range g.c.t.Inventory.Bundle(g.p.Variants[0].SKU) {
			out = append(out, &gqlKit{c.SKU, int32(c.Qty)})
		}
	}
	return out
}

type gqlKit struct {
	sku string
	qty int32
}

func (k *gqlKit) SKU() string { return k.sku }
func (k *gqlKit) Qty() int32  { return k.qty }

type gqlImage struct{ url, alt string }

func (i *gqlImage) URL() string { return i.url }
func (i *gqlImage) Alt() string { return i.alt }

type gqlVariant struct {
	c gqlCtx
	v catalog.Variant
}

func (g *gqlVariant) SKU() string   { return g.v.SKU }
func (g *gqlVariant) Title() string { return g.v.Title }
func (g *gqlVariant) Available() int32 {
	return int32(min(g.c.t.Inventory.Available(g.v.SKU), 2147483647))
}
func (g *gqlVariant) Price() *gqlMoney {
	if q, ok := g.c.t.Pricing.Resolve(g.c.channel, g.v.SKU, 1); ok {
		return gmoney(q.Unit)
	}
	return nil
}
func (g *gqlVariant) CompareAtPrice() *gqlMoney {
	if q, ok := g.c.t.Pricing.Resolve(g.c.channel, g.v.SKU, 1); ok && q.CompareAt != nil && q.CompareAt.Amount > q.Unit.Amount {
		return gmoney(*q.CompareAt)
	}
	return nil
}
func (g *gqlVariant) Preorder() bool { _, ok := g.c.t.Inventory.PreorderOf(g.v.SKU); return ok }

type gqlCartLine struct{ l cart.Line }

func (g *gqlCartLine) SKU() string      { return g.l.SKU }
func (g *gqlCartLine) Title() string    { return g.l.Title }
func (g *gqlCartLine) Qty() int32       { return int32(g.l.Qty) }
func (g *gqlCartLine) Unit() *gqlMoney  { return gmoney(g.l.Unit) }
func (g *gqlCartLine) Total() *gqlMoney { return gmoney(g.l.Total) }

type gqlCart struct{ r cart.Result }

func (g *gqlCart) Lines() []*gqlCartLine {
	out := make([]*gqlCartLine, len(g.r.Lines))
	for i, l := range g.r.Lines {
		out[i] = &gqlCartLine{l}
	}
	return out
}
func (g *gqlCart) Items() *gqlMoney     { return gmoney(g.r.Totals.Items) }
func (g *gqlCart) Discounts() *gqlMoney { return gmoney(g.r.Totals.Discounts) }
func (g *gqlCart) Shipping() *gqlMoney  { return gmoney(g.r.Totals.Shipping) }
func (g *gqlCart) Total() *gqlMoney     { return gmoney(g.r.Totals.Grand) }
func (g *gqlCart) PayLater() *gqlMoney  { return gmoney(g.r.Totals.DueLater) }
func (g *gqlCart) Messages() []string {
	out := []string{}
	for _, m := range g.r.Messages {
		out = append(out, m.Text)
	}
	return out
}

func (r *gqlRoot) Cart(ctx context.Context, a struct {
	Items []struct {
		SKU string
		Qty int32
	}
	PostalCode *string
	Channel    *string
}) (*gqlCart, error) {
	c := gctx(ctx)
	if len(a.Items) == 0 || len(a.Items) > 50 {
		return nil, gqlErr("De 1 a 50 productos en el carrito")
	}
	req := cart.Request{Channel: c.channel}
	if a.Channel != nil {
		req.Channel = *a.Channel
	}
	if a.PostalCode != nil {
		req.PostalCode = *a.PostalCode
	}
	for _, it := range a.Items {
		if it.Qty < 1 || it.Qty > 999 {
			return nil, gqlErr("La cantidad va de 1 a 999")
		}
		req.Items = append(req.Items, cart.Item{SKU: it.SKU, Qty: int64(it.Qty)})
	}
	if !shopChannel(c.t, req.Channel) {
		return nil, gqlErr("Canal de venta no disponible")
	}
	res, err := c.t.Simulate(req)
	if err != nil {
		return nil, gqlErr("Canal de venta no disponible")
	}
	return &gqlCart{res}, nil
}

type gqlError string

func (e gqlError) Error() string { return string(e) }
func gqlErr(s string) error      { return gqlError(s) }

var (
	gqlOnce   sync.Once
	gqlSchema *graphql.Schema
)

// storefrontSchema is parsed once; resolvers read the store from the context.
func (s *Server) storefrontSchema() *graphql.Schema {
	gqlOnce.Do(func() {
		gqlSchema = graphql.MustParseSchema(storefrontSchema, &gqlRoot{}, graphql.MaxDepth(7), graphql.MaxParallelism(4), graphql.UseFieldResolvers())
	})
	return gqlSchema
}

// shopGraphQL answers POST /s/{tenant}/api/graphql.
func (s *Server) shopGraphQL(w http.ResponseWriter, r *http.Request) {
	t, release, ok := s.publicTenant(w, r, simLimiter)
	if !ok {
		return
	}
	defer release()
	var in struct {
		Query         string                 `json:"query"`
		OperationName string                 `json:"operationName"`
		Variables     map[string]interface{} `json:"variables"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&in); err != nil || len(in.Query) > 16<<10 || strings.TrimSpace(in.Query) == "" {
		apierr.BadRequest(w, "Envía {query, variables} en JSON; la consulta tiene hasta 16 KB")
		return
	}
	cur := money.Currency("MXN")
	if ch, ok := t.Channel(mainChannel); ok && ch.Currency != "" {
		cur = ch.Currency
	}
	ctx := context.WithValue(r.Context(), gqlCtxKey{}, gqlCtx{t: t, channel: mainChannel, cur: cur, base: "/s/" + t.ID + "/main"})
	res := s.storefrontSchema().Exec(ctx, in.Query, in.OperationName, in.Variables)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
}
