package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Custom entities: validated definitions, validated and encrypted records, a
// public form only when the store opens it, and no leaks between stores.
func TestCustomEntitiesAndForms(t *testing.T) {
	e := setup(t)
	e.must(200, "POST", "/v1/sites/main/publish", e.adminA, nil)
	def := m{"name": "Solicitudes de mayoreo", "public": false, "success": "Te contactamos en 24 horas.", "fields": []m{
		{"key": "nombre", "label": "Nombre", "type": "text", "required": true},
		{"key": "correo", "label": "Correo", "type": "email", "required": true},
		{"key": "giro", "label": "Giro", "type": "select", "options": []string{"Tienda", "Restaurante"}},
		{"key": "acepto", "label": "Acepto el aviso de privacidad", "type": "checkbox", "required": true}}}
	if st, _ := e.do("PUT", "/v1/entities/Mal ID", e.adminA, def); st != 400 {
		t.Fatalf("bad entity id = %d", st)
	}
	if st, _ := e.do("PUT", "/v1/entities/x", e.adminA, m{"name": "X", "fields": []m{{"key": "1bad", "label": "x", "type": "text"}}}); st != 400 {
		t.Fatalf("bad field key = %d", st)
	}
	e.must(200, "PUT", "/v1/entities/mayoreo", e.adminA, def)
	good := m{"data": m{"nombre": "Ana", "correo": "Ana@Example.com", "giro": "Tienda", "acepto": "true", "extra": "se ignora"}}
	// Closed form: the public endpoint and page do not exist.
	if st, _ := e.do("POST", "/s/store-a/api/forms/mayoreo", "", good); st != 404 {
		t.Fatalf("submission to a closed form = %d", st)
	}
	def["public"] = true
	e.must(200, "PUT", "/v1/entities/mayoreo", e.adminA, def)
	if st, b := e.do("POST", "/s/store-a/api/forms/mayoreo", "", m{"data": m{"nombre": "Ana", "correo": "no", "giro": "Otro"}}); st != 400 || len(b["errors"].([]any)) != 3 {
		t.Fatalf("invalid submission = %d %v", st, b)
	}
	if st, b := e.do("POST", "/s/store-a/api/forms/mayoreo", "", good); st != 201 || b["message"] != "Te contactamos en 24 horas." {
		t.Fatalf("valid submission = %d %v", st, b)
	}
	// A bot that fills the hidden field is ignored without being told.
	bot := m{"data": good["data"], "website": "http://spam"}
	if st, _ := e.do("POST", "/s/store-a/api/forms/mayoreo", "", bot); st != 204 {
		t.Fatalf("honeypot = %d", st)
	}
	if st, _ := e.do("POST", "/s/store-b/api/forms/mayoreo", "", good); st != 404 {
		t.Fatalf("store B accepted store A's form: %d", st)
	}
	res, err := http.Get(e.srv.URL + "/s/store-a/main/formulario/mayoreo")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(page), `name="correo"`) || !strings.Contains(string(page), "<option>Restaurante</option>") {
		t.Fatalf("form page = %d", res.StatusCode)
	}
	list := e.must(200, "GET", "/v1/entities/mayoreo/records", e.adminA, nil)["items"].([]any)
	if len(list) != 1 {
		t.Fatalf("records = %d", len(list))
	}
	rec := list[0].(map[string]any)
	data := rec["data"].(map[string]any)
	if data["correo"] != "ana@example.com" || data["acepto"] != "sí" || data["extra"] != nil || rec["source"] != "form" {
		t.Fatalf("stored record = %v", rec)
	}
	var clear int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM entity_records WHERE encode(data,'escape') LIKE '%ana@example.com%'`).Scan(&clear)
	if clear != 0 {
		t.Fatal("form answers are stored in clear")
	}
	if st, _ := e.do("GET", "/v1/entities/mayoreo/records", e.sfA, nil); st != 403 {
		t.Fatalf("a storefront key read the records: %d", st)
	}
	if st, _ := e.do("GET", "/v1/entities/mayoreo/records", e.adminB, nil); st != 404 {
		t.Fatalf("another store read the records: %d", st)
	}
	st, csv, _ := e.raw("GET", "/v1/entities/mayoreo/export", e.adminA, nil, "")
	if st != 200 || !strings.Contains(string(csv), "Nombre,Correo,Giro") || !strings.Contains(string(csv), "ana@example.com") {
		t.Fatalf("export = %d %s", st, csv)
	}
	e.must(204, "DELETE", "/v1/entities/mayoreo/records/"+rec["id"].(string), e.adminA, nil)
	e.must(204, "DELETE", "/v1/entities/mayoreo", e.adminA, nil)
	if st, _ := e.do("POST", "/s/store-a/api/forms/mayoreo", "", good); st != 404 {
		t.Fatalf("deleted form still accepts answers: %d", st)
	}
}
