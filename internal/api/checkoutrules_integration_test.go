package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Checkout rules: declarative rules hide payments and delivery; the store's
// app adds its answer within 600 ms or is ignored; the checkout enforces it.
func TestCheckoutRules(t *testing.T) {
	e := setup(t)
	slow := false
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow {
			time.Sleep(2 * time.Second)
		}
		var in struct {
			Lines []struct{ SKU string } `json:"lines"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := map[string]any{}
		for _, l := range in.Lines {
			if l.SKU == "S3" {
				out["hidePayments"] = []string{"card", "rm -rf"}
				out["messages"] = []string{"S3 solo se paga por transferencia"}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer app.Close()
	rulesHTTPClient = app.Client()
	defer func() { rulesHTTPClient = nil }()

	if st, _ := e.do("PUT", "/v1/settings/checkout-rules", e.adminA, m{"rules": []m{{"name": "Vacía", "active": true}}}); st != 400 {
		t.Fatalf("a rule that hides nothing accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/checkout-rules", e.adminA, m{"rules": []m{{"name": "x", "active": true, "hidePayments": []string{"bitcoin"}}}}); st != 400 {
		t.Fatalf("unknown payment kind accepted: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/checkout-rules", e.sfA, m{"rules": []m{}}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed rules: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/checkout-rules", e.adminA, m{
		"rules": []m{
			{"name": "Sin OXXO en pedidos grandes", "active": true, "minTotal": 25000, "hidePayments": []string{"oxxo"}, "message": "En pedidos grandes no aceptamos OXXO"},
			{"name": "Sin envío a la zona 99", "active": true, "postalPrefixes": []string{"99"}, "hideShipping": []string{"channel"}},
		},
		"app": m{"active": true, "name": "Reglas ERP", "endpoint": app.URL},
	})
	e.drain()
	sim := func(sku string, qty int, postal string) m {
		return e.must(200, "POST", "/s/store-a/api/simulate", "", m{"channel": "mx", "postalCode": postal, "items": []m{{"sku": sku, "qty": qty}}})
	}
	hidden := func(r m) []string {
		var out []string
		for _, x := range r["hiddenPayments"].([]any) {
			out = append(out, x.(string))
		}
		return out
	}
	if r := sim("S1", 1, "53129"); r["hiddenPayments"] != nil {
		t.Fatalf("small cart hides %v", r["hiddenPayments"])
	}
	big := sim("S1", 3, "53129")
	if h := hidden(big); !slices.Equal(h, []string{"oxxo"}) || !strings.Contains(fmt.Sprint(big["messages"]), "no aceptamos OXXO") {
		t.Fatalf("big cart: %v %v", h, big["messages"])
	}
	if r := sim("S1", 1, "99001"); r["shipping"] != nil {
		t.Fatalf("delivery to a hidden zone: %v", r["shipping"])
	}
	// The app's answer is merged; nonsense kinds are dropped.
	if h := hidden(sim("S3", 1, "53129")); !slices.Equal(h, []string{"card"}) {
		t.Fatalf("app answer: %v", h)
	}
	// Checkout refuses a hidden method.
	st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S3", "qty": 1}},
		"customer": m{"email": "reglas@cliente.example", "name": "Reglas"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "rules-order-1")
	if st != 409 || o["code"] != "payment_method_unavailable" {
		t.Fatalf("hidden payment accepted: %d %v", st, o)
	}
	// Another store is not affected.
	if r := e.must(200, "POST", "/s/store-b/api/simulate", "", m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S3", "qty": 1}}}); r["hiddenPayments"] != nil {
		t.Fatalf("store B got store A's rules: %v", r["hiddenPayments"])
	}
	// A slow app is cut at 600 ms and ignored: the cart answers, without its rule.
	slow = true
	start := time.Now()
	r := sim("S3", 2, "53129")
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("slow app held the cart %v", d)
	}
	if slices.Contains(hidden2(r), "card") {
		t.Fatalf("a timed-out app still applied: %v", r["hiddenPayments"])
	}
}

func hidden2(r m) []string {
	var out []string
	if l, ok := r["hiddenPayments"].([]any); ok {
		for _, x := range l {
			out = append(out, x.(string))
		}
	}
	return out
}
