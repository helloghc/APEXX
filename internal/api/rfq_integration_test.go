package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/pdfdoc"
)

func pdfOf(lines ...string) []byte {
	d := pdfdoc.New("Documento")
	for _, l := range lines {
		d.Text(40, l, 11, false, 520)
		d.Line(11)
	}
	return d.Bytes()
}

func (e *env) uploadPDF(method, path, key string, data []byte) (int, string) {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "ficha.pdf")
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest(method, e.srv.URL+path, &buf)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// A spec sheet in PDF: its text shows on the product page and answers
// questions with the sheet as the source; it goes with the product.
func TestDatasheetFromPDF(t *testing.T) {
	e := setup(t)
	e.priced("taladro", "Taladro percutor 650 W", "Para concreto y madera.", 150000, 5)
	id := e.idOf("taladro")
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	if c, b := e.uploadPDF("PUT", "/v1/products/"+id+"/datasheet", e.adminA, []byte("no soy un pdf")); c != 400 || !strings.Contains(b, "no es un PDF") {
		t.Fatalf("not a PDF: %d %s", c, b)
	}
	c, b := e.uploadPDF("PUT", "/v1/products/"+id+"/datasheet", e.adminA, pdfOf("Ficha técnica", "Potencia: 650 W", "Velocidad sin carga: 0 a 3000 rpm", "Capacidad de perforación en concreto: 13 mm", "Peso: 1.8 kg"))
	if c != 200 || !strings.Contains(b, "3000 rpm") {
		t.Fatalf("upload: %d %s", c, b)
	}
	page := e.shop("/s/store-a/main/p/taladro")
	if !strings.Contains(snippet(page, "ficha-tecnica"), "Peso: 1.8 kg") {
		t.Fatal("spec sheet not on the product page")
	}
	a := e.ask("taladro", "¿A cuántas revoluciones gira?")
	if !strings.Contains(a.Text, "3000 rpm") && !strings.Contains(a.Text, "0 a 3000") {
		t.Fatalf("answer from the sheet: %+v", a)
	}
	a = e.ask("taladro", "¿Cuánto pesa?")
	if !strings.Contains(a.Text, "1.8 kg") || len(a.Sources) == 0 || !strings.Contains(a.Sources[0].Title, "Ficha técnica") {
		t.Fatalf("weight from the sheet: %+v", a)
	}
	// The knowledge pipeline indexes it for store-wide questions too.
	e.embed()
	if _, err := e.server.RunKnowledge(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := e.docChunks("datasheet", id); n == 0 {
		t.Fatal("spec sheet not indexed")
	}
	// Deleting the product deletes its sheet and its chunks.
	e.must(204, "DELETE", "/v1/products/"+id, e.adminA, nil)
	var left int
	_ = e.db.System.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM tenant_docs WHERE tenant_id='store-a' AND kind='datasheet') + (SELECT count(*) FROM ai_chunks WHERE tenant_id='store-a' AND source_kind='datasheet')`).Scan(&left)
	if left != 0 {
		t.Fatalf("%d spec sheet rows left", left)
	}
}

// A quote request becomes lines with quantities and matched SKUs, from text
// or from a PDF; headings and greetings are not lines.
func TestQuoteRequestMatching(t *testing.T) {
	e := setup(t)
	e.priced("taladro", "Taladro percutor 650 W", "Para concreto.", 150000, 30)
	e.priced("brocas", "Juego de brocas para concreto", "20 piezas.", 25000, 200)
	e.priced("casco", "Casco de seguridad amarillo", "Norma ANSI.", 18000, 0)
	e.drain()
	e.embed()
	req := []string{
		"Solicitud de cotización - Constructora del Norte",
		"Buen día, les pedimos precio de lo siguiente:",
		"1. 20 pzas Taladro percutor 650W",
		"2. SKU-brocas x 50",
		"3. Casco de seguridad amarillo 100 piezas",
		"Entrega en Monterrey, NL.",
	}
	check := func(m map[string]any) {
		lines := m["lines"].([]any)
		if len(lines) != 3 {
			t.Fatalf("lines = %v", lines)
		}
		want := []struct {
			qty float64
			sku string
		}{{20, "SKU-taladro"}, {50, "SKU-brocas"}, {100, "SKU-casco"}}
		for i, w := range want {
			l := lines[i].(map[string]any)
			ms := l["matches"].([]any)
			if l["qty"].(float64) != w.qty || len(ms) == 0 || ms[0].(map[string]any)["sku"] != w.sku {
				t.Errorf("line %d = %v", i, l)
			}
		}
		if ms := lines[1].(map[string]any)["matches"].([]any); ms[0].(map[string]any)["exact"] != true {
			t.Error("a named SKU must be an exact match")
		}
		if c := lines[2].(map[string]any)["matches"].([]any)[0].(map[string]any); c["available"].(float64) != 0 || !strings.Contains(c["price"].(string), "180.00") {
			t.Errorf("price and stock come from the system: %v", c)
		}
	}
	check(e.must(200, "POST", "/v1/ai/rfq", e.adminA, map[string]any{"text": strings.Join(req, "\n")}))
	c, b := e.uploadPDF("POST", "/v1/ai/rfq", e.adminA, pdfOf(req...))
	if c != 200 {
		t.Fatalf("pdf: %d %s", c, b)
	}
	var fromPDF map[string]any
	if err := json.Unmarshal([]byte(b), &fromPDF); err != nil {
		t.Fatal(err)
	}
	check(fromPDF) // the same lines, quantities and matches as the text
	if c, _ := e.do("POST", "/v1/ai/rfq", e.adminA, map[string]any{"text": "Hola, buen día"}); c != 400 {
		t.Fatalf("no lines: %d", c)
	}
	if c, _ := e.do("POST", "/v1/ai/rfq", e.sfA, map[string]any{"text": "20 pzas taladro"}); c != 403 {
		t.Fatalf("storefront key: %d", c)
	}
}
