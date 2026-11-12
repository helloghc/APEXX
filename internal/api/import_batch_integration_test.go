package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/store"
)

// The apply step checks every product again just before writing: a SKU taken
// after the preview is reported and skipped.
func TestImportApplyRechecksTakenSKU(t *testing.T) {
	e := setup(t)
	src := "handle,title,status,sku,price,stock\n" +
		"lote-a,Lote A,active,LOTE-A,100,5\n" +
		"lote-b,Lote B,active,LOTE-B,100,5\n" +
		"lote-c,Lote C,active,LOTE-C,100,5\n"
	c, pv := e.do2("POST", "/v1/catalog/imports", e.adminA, strings.NewReader(src), "text/csv")
	if c != 201 {
		t.Fatalf("preview: %d %v", c, pv)
	}
	id := pv["id"].(string)
	// Between the preview and the apply, another product takes LOTE-B.
	e.must(201, "POST", "/v1/products", e.adminA, map[string]any{"handle": "ganador", "title": "Ganador", "status": "active",
		"variants": []map[string]any{{"sku": "LOTE-B", "title": "Única"}}})
	if c, m := e.do("POST", "/v1/catalog/imports/"+id+"/apply", e.adminA, nil); c != 202 {
		t.Fatalf("apply: %d %v", c, m)
	}
	job := e.waitImport(id)
	if job["status"] != "done" || job["created"].(float64) != 2 {
		t.Fatalf("job: %v", job)
	}
	errs, _ := job["errors"].([]any)
	if len(errs) != 1 || !strings.Contains(errs[0].(map[string]any)["message"].(string), "ya pertenece al producto ganador") {
		t.Fatalf("errors: %v", errs)
	}
	var n int
	if err := e.db.System.QueryRow(t.Context(), `SELECT count(*) FROM products WHERE tenant_id='store-a' AND handle IN ('lote-a','lote-b','lote-c')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("products written = %d (%v), want lote-a and lote-c only", n, err)
	}
	if err := e.db.System.QueryRow(t.Context(), `SELECT count(*) FROM products WHERE tenant_id='store-a' AND handle='lote-b'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the failed product left %d rows", n)
	}
}

// Products of an import go in batches, one transaction per batch with a
// savepoint per product: a product that fails while being written is rolled
// back alone, with nothing of it left behind, and the rest are kept.
func TestImportBatchFailsOneProductAlone(t *testing.T) {
	e := setup(t)
	price := int64(10000)
	mk := func(h, sku string) catalog.ImportProduct {
		return catalog.ImportProduct{Handle: h, Title: h, Status: "active", Variants: []catalog.ImportVariant{{SKU: sku, Price: &price}}}
	}
	// S1 already belongs to P1: the middle product fails inside the batch.
	outs := e.db.ApplyImportBatch(t.Context(), "store-a", "test", []catalog.ImportProduct{mk("sp-a", "SP-A"), mk("sp-b", "S1"), mk("sp-c", "SP-C")},
		store.ImportOptions{Channel: "mx", Currency: "MXN", Location: "w1"})
	if outs[0].Err != nil || !outs[0].Created || outs[2].Err != nil || !outs[2].Created {
		t.Fatalf("good products: %+v", outs)
	}
	var ae *store.ApplyError
	if !errors.As(outs[1].Err, &ae) {
		t.Fatalf("middle product: %+v", outs[1])
	}
	var n int
	if err := e.db.System.QueryRow(t.Context(), `SELECT count(*) FROM products WHERE tenant_id='store-a' AND handle IN ('sp-a','sp-b','sp-c')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("products written = %d (%v), want sp-a and sp-c", n, err)
	}
	// The hash chain of the audit log stays whole across the rolled-back savepoint.
	if broken, err := e.db.VerifyAudit(t.Context(), "store-a"); err != nil || broken != 0 {
		t.Fatalf("audit chain broken at %d (%v)", broken, err)
	}
}
