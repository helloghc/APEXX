package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// Quote documents and line comments: both sides comment on one line, the
// PDF carries prices (or "por cotizar"), the comments and the messages, the
// buyer downloads it with their link, closed quotes take no comments, and
// another store cannot read it.
func TestQuotePDFAndLineComments(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	st, r := e.do("POST", "/s/store-a/api/quotes", "", m{"channel": "mx", "items": []m{{"sku": "S1", "qty": 3}, {"sku": "S2", "qty": 1}},
		"customer": m{"email": "q@example.com", "name": "Quique Compras"}, "message": "Precio por volumen"}, "Origin", e.srv.URL)
	if st != 201 {
		t.Fatalf("request = %d %v", st, r)
	}
	token := r["url"].(string)[strings.LastIndex(r["url"].(string), "/")+1:]
	id := e.must(200, "GET", "/v1/quotes?status=requested", e.adminA, nil)["items"].([]any)[0].(map[string]any)["id"].(string)
	buyer := func(path string, body any) (int, m) {
		return e.do("POST", "/s/store-a/api/quotes/"+token+path, "", body, "Origin", e.srv.URL)
	}
	if st, _ := buyer("/comments", m{"sku": "NOPE", "body": "?"}); st != 400 {
		t.Fatalf("comment on a line that is not in the quote: %d", st)
	}
	if st, c := buyer("/comments", m{"sku": "S1", "body": "Necesitamos entrega en dos partes"}); st != 201 || c["who"] != "buyer" {
		t.Fatalf("buyer comment = %d %v", st, c)
	}
	if st, _ := e.do("POST", "/v1/quotes/"+id+"/comments", e.adminA, m{"sku": "S1", "body": "Sin problema: 2 y 1"}); st != 201 {
		t.Fatalf("store comment = %d", st)
	}
	if st, _ := e.do("POST", "/v1/quotes/"+id+"/comments", e.adminB, m{"sku": "S1", "body": "intruso"}); st != 404 {
		t.Fatalf("store B commented: %d", st)
	}
	pdf := func(path, key string) (int, string, []byte) {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), b.Bytes()
	}
	st, ct, b := pdf("/v1/quotes/"+id+"/pdf", e.adminA)
	if st != 200 || ct != "application/pdf" || !bytes.HasPrefix(b, []byte("%PDF-")) || !bytes.Contains(b, []byte("Por cotizar")) || !bytes.Contains(b, []byte("entrega en dos partes")) {
		t.Fatalf("request PDF = %d %s %d bytes", st, ct, len(b))
	}
	// With the offer, the PDF carries prices and totals.
	e.must(200, "POST", "/v1/quotes/"+id+"/offer", e.adminA, m{"items": []m{{"sku": "S1", "qty": 3, "unit": 7000}, {"sku": "S2", "qty": 1, "unit": 9000}}, "shipping": 0, "validDays": 7, "message": "Precio especial"})
	st, _, b = pdf("/s/store-a/api/quotes/"+token+"/pdf", "")
	if st != 200 || !bytes.Contains(b, []byte("$70.00 MXN")) || !bytes.Contains(b, []byte("$300.00 MXN")) || !bytes.Contains(b, []byte("Precio especial")) {
		t.Fatalf("offer PDF for the buyer = %d %d bytes", st, len(b))
	}
	q := e.must(200, "GET", "/v1/quotes/"+id, e.adminA, nil)
	if len(q["comments"].([]any)) != 2 {
		t.Fatalf("comments = %v", q["comments"])
	}
	if st, _, _ := pdf("/v1/quotes/"+id+"/pdf", e.adminB); st != 404 {
		t.Fatalf("store B read the PDF: %d", st)
	}
	if st, _, _ := pdf("/s/store-a/api/quotes/apc_nope/pdf", ""); st != 404 {
		t.Fatalf("bad token PDF: %d", st)
	}
	e.must(204, "POST", "/v1/quotes/"+id+"/cancel", e.adminA, nil)
	if st, _ := buyer("/comments", m{"sku": "S1", "body": "¿Sigue en pie?"}); st != 400 {
		t.Fatalf("comment on a cancelled quote: %d", st)
	}
}
