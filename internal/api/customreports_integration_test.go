package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Custom reports: figures equal the orders behind them, only listed pieces
// are accepted (nothing reaches SQL as text), cancelled orders are left out
// unless asked, saved reports run and export, and stores never mix.
func TestCustomReports(t *testing.T) {
	e := setup(t)
	var grand, units float64
	var ids []string
	for i, q := range []int{1, 2, 3} {
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": q}},
			"customer": m{"email": fmt.Sprintf("rep%d@cliente.example", i%2), "name": "Rep"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("custom-rep-%d", i))
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
		ids = append(ids, o["id"].(string))
		if i < 2 {
			grand += o["grandTotal"].(float64)
			units += float64(q)
		}
	}
	e.must(200, "POST", "/v1/orders/"+ids[2]+"/cancel", e.adminA, m{"reason": "prueba"})

	run := func(key string, def m) (int, m) { return e.do("POST", "/v1/reports/custom/run?range=7d", key, def) }
	// A paid order that is cancelled is refunded in full: it stays in the
	// sales with its refund, and net sales leave it out.
	st, r := run(e.adminA, m{"dataset": "orders", "dims": []string{"day"}, "metrics": []string{"orders", "net", "customers", "refunds"}})
	if st != 200 {
		t.Fatalf("run = %d %v", st, r)
	}
	tot := r["totals"].([]any)
	if tot[0].(float64) != 3 || tot[1].(float64) != grand || tot[2].(float64) != 2 || tot[3].(float64) <= 0 {
		t.Fatalf("totals = %v, want 3 orders, net %v, 2 customers, a refund", tot, grand)
	}
	if rows := r["rows"].([]any); len(rows) != 1 || rows[0].([]any)[2].(float64) != grand {
		t.Fatalf("rows = %v", rows)
	}
	// Products sold, by SKU and by brand.
	st, r = run(e.adminA, m{"dataset": "lines", "dims": []string{"sku"}, "metrics": []string{"units", "orders"}})
	if st != 200 || r["rows"].([]any)[0].([]any)[1].(float64) != units {
		t.Fatalf("units by sku = %d %v", st, r)
	}
	if st, r = run(e.adminA, m{"dataset": "lines", "dims": []string{"brand", "category"}, "metrics": []string{"units"}}); st != 200 || r["totals"].([]any)[0].(float64) != units {
		t.Fatalf("units by brand = %d %v", st, r)
	}
	// Statuses filter the orders.
	if _, r = run(e.adminA, m{"dataset": "orders", "metrics": []string{"orders"}, "statuses": []string{"refunded"}}); r["totals"].([]any)[0].(float64) != 1 {
		t.Fatalf("refunded = %v", r["totals"])
	}
	if _, r = run(e.adminA, m{"dataset": "orders", "metrics": []string{"orders"}, "statuses": []string{"cancelled"}}); r["totals"].([]any)[0].(float64) != 0 {
		t.Fatalf("cancelled = %v", r["totals"])
	}
	// Only listed pieces.
	for _, bad := range []m{
		{"dataset": "orders", "dims": []string{"day; DROP TABLE orders"}, "metrics": []string{"orders"}},
		{"dataset": "orders", "dims": []string{"sku"}, "metrics": []string{"orders"}},
		{"dataset": "orders", "metrics": []string{"units"}},
		{"dataset": "orders", "metrics": []string{"orders"}, "statuses": []string{"x' OR '1'='1"}},
		{"dataset": "everything", "metrics": []string{"orders"}},
		{"dataset": "orders", "metrics": []string{}},
	} {
		if st, _ := run(e.adminA, bad); st != 400 {
			t.Fatalf("accepted %v: %d", bad, st)
		}
	}
	// Store B sees only its own orders.
	if _, r = run(e.adminB, m{"dataset": "orders", "metrics": []string{"orders"}}); r["totals"].([]any)[0].(float64) != 0 {
		t.Fatalf("store B counted store A's orders: %v", r["totals"])
	}
	// Save, list, run as CSV.
	st, saved := e.do("POST", "/v1/reports/custom", e.adminA, m{"name": "Ventas por día", "dataset": "orders", "dims": []string{"day"}, "metrics": []string{"orders", "revenue"}, "range": "30d", "chart": "line"})
	if st != 201 {
		t.Fatalf("save = %d %v", st, saved)
	}
	id := saved["id"].(string)
	if l := e.must(200, "GET", "/v1/reports/custom", e.adminA, nil)["items"].([]any); len(l) != 1 {
		t.Fatalf("list = %v", l)
	}
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/reports/custom/"+id+"/run?format=csv", nil)
	req.Header.Set("Authorization", "Bearer "+e.adminA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/csv") || !strings.Contains(string(b), "Día,Pedidos,Ventas (total cobrado)") || !strings.Contains(string(b), "Total,3,") {
		t.Fatalf("csv = %s %q", resp.Header.Get("Content-Type"), b)
	}
	if st, _ := e.do("GET", "/v1/reports/custom/"+id+"/run", e.adminB, nil); st != 404 {
		t.Fatalf("store B ran store A's report: %d", st)
	}
	if st, _ := e.do("DELETE", "/v1/reports/custom/"+id, e.adminB, nil); st != 404 {
		t.Fatalf("store B deleted store A's report: %d", st)
	}
	e.must(204, "DELETE", "/v1/reports/custom/"+id, e.adminA, nil)
}
