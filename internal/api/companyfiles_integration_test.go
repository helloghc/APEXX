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

// Company documents are stored encrypted, typed by content, readable only by
// the store; the CSV import previews exactly what it will do, then creates,
// updates and links parents line by line, reporting bad lines.
func TestCompanyFilesAndImport(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	co := e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Documentos SA"})["id"].(string)
	pdf := []byte("%PDF-1.4\n% constancia de situacion fiscal\n1 0 obj << >> endobj\ntrailer << >>\n%%EOF\n")
	st, f := e.upload("/v1/companies/"+co+"/files", e.adminA, "constancia.pdf", pdf, nil)
	if st != 201 || f["contentType"] != "application/pdf" {
		t.Fatalf("upload = %d %v", st, f)
	}
	if st, _ := e.upload("/v1/companies/"+co+"/files", e.adminA, "virus.pdf", []byte("MZ\x90\x00\x03programa"), nil); st != 415 {
		t.Fatalf("executable accepted: %d", st)
	}
	id := f["id"].(string)
	obj, err := e.server.Media.Open(ctx, "store-a", id+"/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(obj)
	obj.Close()
	if bytes.Contains(raw, []byte("constancia")) {
		t.Fatal("document stored in clear")
	}
	get := func(path, key string) (int, []byte, string) {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header.Get("Content-Type")
	}
	if st, b, ct := get("/v1/companies/"+co+"/files/"+id, e.adminA); st != 200 || !bytes.Equal(b, pdf) || ct != "application/pdf" {
		t.Fatalf("download = %d %s", st, ct)
	}
	if st, _, _ := get("/v1/companies/"+co+"/files/"+id, e.adminB); st != 404 {
		t.Fatalf("store B downloaded the document: %d", st)
	}
	if st, _ := e.do("GET", "/v1/companies/"+co+"/files", e.sfA, nil); st != 403 {
		t.Fatalf("storefront key listed documents: %d", st)
	}
	e.must(204, "DELETE", "/v1/companies/"+co+"/files/"+id, e.adminA, nil)
	if st, _, _ := get("/v1/companies/"+co+"/files/"+id, e.adminA); st != 404 {
		t.Fatalf("deleted document still served: %d", st)
	}

	// CSV import: preview, then apply.
	e.must(201, "POST", "/v1/companies", e.adminA, m{"name": "Existente SA", "rfc": "EXI010101AAA"})
	csvBody := "nombre,rfc,plazo,limite,descuento,compradores,matriz,medios\n" +
		"Importada Uno,IMP010101AAA,30,\"50,000\",10%,compras@imp.example;jefe@imp.example,,credito;spei\n" +
		"Filial Importada,,contado,,,,Importada Uno,\n" +
		"Mala,,90,,,,,\n" +
		"Existente SA,EXI010101AAA,15,1000,,,,\n" +
		"Sin Matriz,,contado,,,,No Existe,\n"
	imp := func(apply bool) m {
		q := ""
		if apply {
			q = "?apply=1"
		}
		req, _ := http.NewRequest("POST", e.srv.URL+"/v1/companies/import"+q, strings.NewReader(csvBody))
		req.Header.Set("Authorization", "Bearer "+e.adminA)
		req.Header.Set("Content-Type", "text/csv")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out m
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("import = %d %s", resp.StatusCode, b)
		}
		_ = jsonUnmarshalTest(b, &out)
		return out
	}
	before := len(e.must(200, "GET", "/v1/companies", e.adminA, nil)["items"].([]any))
	pre := imp(false)
	c := pre["counts"].(map[string]any)
	if c["create"].(float64) != 2 || c["update"].(float64) != 1 || c["error"].(float64) != 2 {
		t.Fatalf("preview counts = %v rows=%v", c, pre["rows"])
	}
	if after := len(e.must(200, "GET", "/v1/companies", e.adminA, nil)["items"].([]any)); after != before {
		t.Fatalf("the preview created companies: %d -> %d", before, after)
	}
	done := imp(true)
	if dc := done["counts"].(map[string]any); dc["create"].(float64) != 2 || dc["update"].(float64) != 1 || dc["error"].(float64) != 2 {
		t.Fatalf("apply counts = %v", dc)
	}
	var parent, child string
	for _, r := range done["rows"].([]any) {
		row := r.(map[string]any)
		switch row["name"] {
		case "Importada Uno":
			parent = row["id"].(string)
		case "Filial Importada":
			child = row["id"].(string)
		}
	}
	p := e.must(200, "GET", "/v1/companies/"+parent, e.adminA, nil)["company"].(map[string]any)
	if p["creditLimit"].(float64) != 5000000 || p["discountBps"].(float64) != 1000 || p["buyers"].(float64) != 2 || len(p["allowedPayments"].([]any)) != 2 {
		t.Fatalf("imported company = %v", p)
	}
	if k := e.must(200, "GET", "/v1/companies/"+child, e.adminA, nil)["company"].(map[string]any); k["parentId"] != parent {
		t.Fatalf("subsidiary parent = %v", k["parentId"])
	}
}

func jsonUnmarshalTest(b []byte, v any) error { return json.Unmarshal(b, v) }
