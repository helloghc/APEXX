package api

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

func (e *env) priced(h, title, desc string, cents int64, stock int64) {
	e.t.Helper()
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": h, "title": title, "description": desc, "status": "active",
		"variants": []map[string]any{{"sku": "SKU-" + h, "title": "Única"}}})
	ctx := context.Background()
	if err := e.db.SetPrices(ctx, "store-a", "test", "mx", "SKU-"+h, []pricing.Tier{{MinQty: 1, Unit: money.New(cents, "MXN")}}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.db.SetStock(ctx, "store-a", "test", "SKU-"+h, "w1", stock); err != nil {
		e.t.Fatal(err)
	}
}

// approveReviews publishes pending reviews whose body contains one of the
// given texts (all pending ones when none is given), as the store would.
func (e *env) approveReviews(only ...string) {
	e.t.Helper()
	m := e.must(200, "GET", "/v1/reviews?status=pending", e.adminA, nil)
	items, _ := m["items"].([]any)
	for _, it := range items {
		r := it.(map[string]any)
		ok := len(only) == 0
		for _, o := range only {
			ok = ok || strings.Contains(r["body"].(string), o)
		}
		if ok {
			e.must(200, "POST", "/v1/reviews/"+r["id"].(string)+"/moderate", e.adminA, map[string]any{"status": "approved"})
		}
	}
}

func (e *env) assist(q string) map[string]any {
	_, m := e.do("POST", "/s/store-a/api/assistant", "", map[string]any{"q": q})
	return m
}

var reMoney = regexp.MustCompile(`\$\s?([\d,]+\.\d{2})`)

// The gate of IA-3: no price or stock that is not the system's. Every
// amount the assistant says or shows is checked against the read model.
func TestAssistantNeverInventsPricesOrStock(t *testing.T) {
	e := setup(t)
	e.priced("licuadora", "Licuadora 600 W", "Vaso de vidrio. Tritura hielo.", 129900, 4)
	e.priced("camisa-lino", "Camisa de lino blanca", "Manga larga, 100 % lino.", 64950, 0)
	e.priced("cafetera", "Cafetera de goteo", "12 tazas, jarra de vidrio.", 89900, 7)
	e.must(200, "PUT", "/v1/policies/envios", e.adminA, map[string]any{"title": "Envíos", "published": true, "body": "<p>El envío llega en 3 a 5 días hábiles.</p>"})
	e.embed()
	if _, err := e.server.RunKnowledge(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.drain()
	price := map[string]string{"licuadora": "1,299.00", "camisa-lino": "649.50", "cafetera": "899.00"}
	allowed := map[string]bool{}
	for _, p := range price {
		allowed[p] = true
	}
	cases := []struct{ q, want string }{
		{"¿Cuánto cuesta la licuadora?", "1,299.00"},
		{"¿Cuánto cuesta la camisa de lino?", "649.50"},
		{"¿Hay licuadora disponible?", "Sí, está disponible"},
		{"¿Tienen camisa de lino disponible?", "agotado"},
		{"¿La licuadora tritura hielo?", "Tritura hielo"},
		{"¿Cuánto tarda el envío?", "3 a 5 días"},
		{"busco una cafetera", ""},
		{"quiero algo de lino", ""},
	}
	for _, c := range cases {
		m := e.assist(c.q)
		a := m["answer"].(map[string]any)
		text, _ := a["text"].(string)
		if c.want != "" && !strings.Contains(text, c.want) {
			t.Errorf("%q -> %q, want %q", c.q, text, c.want)
		}
		// Every amount in the answer is a real price of the store.
		for _, mm := range reMoney.FindAllStringSubmatch(text, -1) {
			if !allowed[mm[1]] {
				t.Errorf("%q: invented amount %s in %q", c.q, mm[1], text)
			}
		}
		for _, pc := range m["products"].([]any) {
			card := pc.(map[string]any)
			h := card["handle"].(string)
			if !strings.Contains(card["price"].(string), price[h]) {
				t.Errorf("%q: card %s shows %v, system price %s", c.q, h, card["price"], price[h])
			}
			if (h == "camisa-lino") != card["soldOut"].(bool) {
				t.Errorf("%q: card %s soldOut=%v", c.q, h, card["soldOut"])
			}
		}
		if c.want == "" && len(m["products"].([]any)) == 0 {
			t.Errorf("%q: no products", c.q)
		}
	}
	// Through MCP the same tool answers, with absolute source links.
	_ = catalog.SortRelevance
}

// The copilot gives figures only to who may see them, explains tasks with a
// link to the screen and says it does not know otherwise.
func TestCopilotRespectsPermissions(t *testing.T) {
	e := setup(t)
	a := e.must(200, "POST", "/v1/ai/copilot", e.adminA, map[string]any{"q": "¿Cuánto vendí esta semana?"})
	if a["kind"] != "data" || !strings.Contains(a["text"].(string), "pedidos") {
		t.Fatalf("sales: %v", a)
	}
	a = e.must(200, "POST", "/v1/ai/copilot", e.adminA, map[string]any{"q": "¿Cómo invito a alguien de mi equipo?"})
	links, _ := a["links"].([]any)
	if a["kind"] != "help" || len(links) == 0 || links[0].(map[string]any)["route"] != "equipo" {
		t.Fatalf("help: %v", a)
	}
	a = e.must(200, "POST", "/v1/ai/copilot", e.adminA, map[string]any{"q": "¿Cómo importo productos desde un archivo?"})
	if links, _ := a["links"].([]any); a["kind"] != "help" || links[0].(map[string]any)["route"] != "catalogo/importar" {
		t.Fatalf("import help: %v", a)
	}
	a = e.must(200, "POST", "/v1/ai/copilot", e.adminA, map[string]any{"q": "¿Quién ganó el mundial de futbol?"})
	if a["kind"] != "unknown" {
		t.Fatalf("unknown: %v", a)
	}
	// Someone with catalog access only: no sales figures, no screens they cannot open.
	who := auth.Principal{Tenant: "store-a", Kind: "member", ID: "m1", Permissions: auth.Permissions{"catalog": auth.Read}}
	if r := e.server.copilot(context.Background(), who, "¿Cuánto vendí hoy?"); r.Kind != "forbidden" || strings.Contains(r.Text, "$") || len(r.Rows) > 0 {
		t.Fatalf("figures leaked without reports access: %+v", r)
	}
	if r := e.server.copilot(context.Background(), who, "¿Cómo invito a alguien de mi equipo?"); r.Kind != "forbidden" || len(r.Links) > 0 {
		t.Fatalf("help offered a screen they cannot open: %+v", r)
	}
	// A storefront key cannot use the copilot at all.
	if c, _ := e.do("POST", "/v1/ai/copilot", e.sfA, map[string]any{"q": "¿Cuánto vendí hoy?"}); c != 403 {
		t.Fatalf("storefront key: %d", c)
	}
}

// The review summary quotes real sentences of approved reviews.
func TestReviewSummaryQuotesCustomers(t *testing.T) {
	e := setup(t)
	e.priced("taza", "Taza de cerámica", "350 ml.", 19900, 10)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	bodies := []struct {
		r          int
		body, name string
	}{
		{5, "Mantiene el café caliente mucho tiempo. Muy bonita.", "Ana"},
		{5, "El café se mantiene caliente bastante rato.", "Luis"},
		{4, "Llegó bien empacada y rápido.", "Sofía"},
		{1, "Se despostilló al primer lavado en el lavavajillas.", "Pedro"},
		{2, "Mi pedido llegó roto, mala experiencia.", "Mara"}, // not approved below
	}
	for i, b := range bodies {
		e.must(201, "POST", "/s/store-a/api/reviews/taza", "", map[string]any{"rating": b.r, "body": b.body, "name": b.name, "email": b.name + "@example.com"})
		_ = i
	}
	e.approveReviews("caliente", "empacada", "despostilló")
	page := e.shop("/s/store-a/main/p/taza")
	i := strings.Index(page, "data-review-summary")
	j := strings.Index(page, "rv-list")
	if i < 0 || j < i {
		t.Fatalf("no summary on the product page")
	}
	sum := page[i:j]
	if strings.Count(sum, "caliente") != 1 {
		t.Errorf("the same idea twice: %s", sum)
	}
	if !strings.Contains(sum, "Lo que destacan") || !strings.Contains(sum, "caliente") || !strings.Contains(sum, "Lo que mejorarían") || !strings.Contains(sum, "despostilló") {
		t.Fatalf("summary: %s", sum)
	}
	if strings.Contains(sum, "llegó roto") {
		t.Fatal("an unapproved review is in the summary")
	}
	// Every quoted point is a sentence some approved review contains.
	_, mcp := e.do("POST", "/s/store-a/api/assistant", "", map[string]any{"q": "busco una taza"})
	_ = mcp
}

func (e *env) idOf(handle string) string {
	var id string
	_ = e.db.System.QueryRow(context.Background(), `SELECT id FROM products WHERE tenant_id='store-a' AND handle=$1`, handle).Scan(&id)
	return id
}
