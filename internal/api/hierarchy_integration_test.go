package api

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Company hierarchy: subsidiaries inherit the parent's terms and price, buy
// on one shared credit that two of them cannot overspend at once, the
// hierarchy refuses cycles and a fourth level, and another store cannot hang
// a company from this one.
func TestCompanyHierarchy(t *testing.T) {
	e := setup(t)
	p := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Grupo Matriz", "terms": "net30", "creditLimit": 1000000, "discountBps": 1000})["id"].(string)
	a := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Filial Norte", "parentId": p, "sharedCredit": true, "inheritTerms": true})["id"].(string)
	b := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Filial Sur", "parentId": p, "sharedCredit": true})["id"].(string)
	c := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Sucursal Norte 1", "parentId": a})["id"].(string)
	if st, r := e.do("POST", "/v1/companies", e.adminA, m{"name": "Cuarto nivel", "parentId": c}); st != 400 || !strings.Contains(toJSON(r), "tres niveles") {
		t.Fatalf("fourth level = %d %v", st, r)
	}
	if st, _ := e.do("PUT", "/v1/companies/"+p, e.adminA, m{"name": "Grupo Matriz", "terms": "net30", "creditLimit": 1000000, "discountBps": 1000, "parentId": c}); st != 400 {
		t.Fatalf("cycle accepted: %d", st)
	}
	if st, _ := e.do("POST", "/v1/companies", e.adminA, m{"name": "Sin matriz", "sharedCredit": true}); st != 400 {
		t.Fatalf("shared credit without a parent: %d", st)
	}
	if st, _ := e.do("POST", "/v1/companies", e.adminB, m{"name": "Intrusa", "parentId": p}); st != 400 {
		t.Fatalf("store B hung a company from store A's: %d", st)
	}
	n := 0
	buy := func(company string, qty int) (int, m) {
		n++
		return e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": qty}},
			"customer": m{"email": fmt.Sprintf("compras%d@grupo.example", n), "name": "Compras"}, "paymentMethod": "credit", "companyId": company}, "Idempotency-Key", fmt.Sprintf("hierarchy-%d", n))
	}
	// The inheriting subsidiary buys at the parent's price on the parent's credit.
	st, oa := buy(a, 2)
	if st != 201 || oa["discounts"].(float64) != oa["itemsTotal"].(float64)/10 {
		t.Fatalf("subsidiary order = %d %v", st, oa)
	}
	// The subsidiary with its own terms keeps its price (no discount) but shares the credit.
	st, ob := buy(b, 1)
	if st != 201 || ob["discounts"].(float64) != 0 {
		t.Fatalf("own-terms subsidiary = %d %v", st, ob["discounts"])
	}
	detail := e.must(200, "GET", "/v1/companies/"+p, e.adminA, nil)["company"].(map[string]any)
	pool := detail["pool"].(map[string]any)
	owed := oa["grandTotal"].(float64) + ob["grandTotal"].(float64)
	if pool["balance"].(float64) != owed || len(pool["members"].([]any)) != 3 || len(detail["children"].([]any)) != 2 {
		t.Fatalf("parent pool = %v children = %v (owed %v)", pool, detail["children"], owed)
	}
	// The pool runs out for the whole group, with the group's name.
	e.must(200, "PUT", "/v1/companies/"+p, e.adminA, m{"name": "Grupo Matriz", "terms": "net30", "creditLimit": int(owed) + 100, "discountBps": 1000})
	if st, r := buy(b, 1); st != 409 || r["code"] != "credit_limit" || !strings.Contains(toJSON(r), "Grupo Matriz") {
		t.Fatalf("over the group credit = %d %v", st, r)
	}
	// Two subsidiaries racing for the last credit: only one wins.
	e.must(200, "PUT", "/v1/companies/"+p, e.adminA, m{"name": "Grupo Matriz", "terms": "net30", "creditLimit": int(owed + ob["grandTotal"].(float64) + 50), "discountBps": 1000})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, co := range []string{b, b} {
		wg.Add(1)
		go func(co string) {
			defer wg.Done()
			mu.Lock()
			n++
			key := fmt.Sprintf("hier-race-%d", n)
			mu.Unlock()
			st, _ := e.do("POST", "/v1/checkout", e.adminA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
				"customer": m{"email": "carrera@grupo.example", "name": "Carrera"}, "paymentMethod": "credit", "companyId": co}, "Idempotency-Key", key)
			if st == 201 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(co)
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d racing orders fit in credit for one", ok)
	}
	// A sibling's detail never shows the other sibling; store B sees nothing.
	if d := e.must(200, "GET", "/v1/companies/"+b, e.adminA, nil)["company"].(map[string]any); d["children"] != nil && len(d["children"].([]any)) != 0 {
		t.Fatalf("subsidiary lists siblings: %v", d["children"])
	}
	if st, _ := e.do("GET", "/v1/companies/"+p, e.adminB, nil); st != 404 {
		t.Fatalf("store B read store A's group: %d", st)
	}
	// Leaving the group: the subsidiary goes back to its own (prepaid) credit.
	e.must(200, "PUT", "/v1/companies/"+b, e.adminA, m{"name": "Filial Sur", "parentId": ""})
	if st, r := buy(b, 1); st != 409 || r["code"] != "credit_unavailable" {
		t.Fatalf("after leaving the group = %d %v", st, r)
	}
}
