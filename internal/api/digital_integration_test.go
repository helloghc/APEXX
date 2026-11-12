package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
)

// Digital products: the file is delivered after payment by a signed link
// with a download limit; cancelling takes the access away; links cannot be
// forged or used in another store; concurrent clicks never exceed the limit.
func TestDigitalDownloads(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.db.UpsertProduct(ctx, "store-a", "test", catalog.Product{ID: "D", Handle: "ebook", Title: "Guía digital",
		Variants: []catalog.Variant{{SKU: "D1", Title: "PDF"}}}, 0); err != nil {
		t.Fatal(err)
	}
	_ = e.db.SetStock(ctx, "store-a", "test", "D1", "w1", 1000)
	_ = e.db.SetPrices(ctx, "store-a", "test", "mx", "D1", []pricing.Tier{{MinQty: 1, Unit: money.New(9900, "MXN")}})
	e.drain()
	pdf := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("contenido "), 500)...)

	if st, _ := e.upload("/v1/digital/D1", e.sfA, "guia.pdf", pdf, nil); st != 401 && st != 403 {
		t.Fatalf("storefront key uploaded a file: %d", st)
	}
	if st, _ := e.upload("/v1/digital/D1", e.adminB, "guia.pdf", pdf, nil); st != 404 {
		t.Fatalf("store B attached a file to store A's SKU: %d", st)
	}
	if st, _ := e.upload("/v1/digital/D1", e.adminA, "guia.pdf", pdf, map[string]string{"maxDownloads": "0"}); st != 400 {
		t.Fatalf("zero downloads accepted: %d", st)
	}
	st, f := e.upload("/v1/digital/D1", e.adminA, `../../etc/guía "final".pdf`, pdf, map[string]string{"maxDownloads": "2", "linkDays": "7"})
	if st != 201 || f["filename"] != "guía final.pdf" || f["contentType"] != "application/pdf" {
		t.Fatalf("upload = %d %v", st, f)
	}
	fileID := f["id"].(string)
	if l := e.must(200, "GET", "/v1/digital/D1", e.adminB, nil); len(l["items"].([]any)) != 0 {
		t.Fatalf("store B lists store A's files: %v", l)
	}

	n := 0
	buy := func(items []m) m {
		n++
		st, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": items,
			"customer": m{"email": "lector@cliente.example", "name": "Lector"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("digital-%d", n))
		if st != 201 {
			t.Fatalf("checkout = %d %v", st, o)
		}
		return o
	}
	o := buy([]m{{"sku": "D1", "qty": 1}})
	dls, _ := o["downloads"].([]any)
	if len(dls) != 1 || o["shipping"].(float64) != 0 || o["fulfillmentStatus"] != "fulfilled" {
		t.Fatalf("digital order: downloads=%v shipping=%v fulfillment=%v", o["downloads"], o["shipping"], o["fulfillmentStatus"])
	}
	url := dls[0].(map[string]any)["url"].(string)
	// The confirmation email carries the link.
	var payload []byte
	var mid string
	if err := e.db.System.QueryRow(ctx, `SELECT id, payload FROM notification_messages WHERE tenant_id='store-a' AND order_id=$1 AND template='order_confirmed'`, o["id"]).Scan(&mid, &payload); err != nil {
		t.Fatalf("confirmation email: %v", err)
	}
	if plain, err := e.keys.Decrypt("store-a", payload, mid); err == nil {
		payload = plain
	}
	if !bytes.Contains(payload, []byte("/api/download/")) {
		t.Fatalf("the confirmation email has no download link: %.300s", payload)
	}
	get := func(path string, hdr ...string) (int, []byte, http.Header) {
		req, _ := http.NewRequest("GET", e.srv.URL+path, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b, res.Header
	}
	code, body, h := get(url)
	if code != 200 || !bytes.Equal(body, pdf) || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download = %d len=%d %v", code, len(body), h)
	}
	// Resuming does not spend a download.
	if code, body, _ := get(url, "Range", "bytes=100-"); code != 206 || !bytes.Equal(body, pdf[100:]) {
		t.Fatalf("resume = %d", code)
	}
	// Forged or moved links do not work.
	id, _, _ := strings.Cut(strings.TrimPrefix(url, "/s/store-a/api/download/"), ".")
	for _, bad := range []string{"/s/store-a/api/download/" + id + ".0000", "/s/store-a/api/download/" + id, "/s/store-b/api/download/" + strings.TrimPrefix(url, "/s/store-a/api/download/")} {
		if code, _, _ := get(bad); code != 404 {
			t.Fatalf("forged link %s = %d", bad, code)
		}
	}
	if code, _, _ := get(url); code != 200 {
		t.Fatalf("second download = %d", code)
	}
	if code, _, _ := get(url); code != 403 {
		t.Fatalf("third download over the limit = %d", code)
	}
	oid := o["id"].(string)
	if st, _ := e.do("POST", "/v1/orders/"+oid+"/downloads/"+id+"/reset", e.adminB, nil); st != 404 {
		t.Fatalf("store B reset a download: %d", st)
	}
	r := e.must(200, "POST", "/v1/orders/"+oid+"/downloads/"+id+"/reset", e.adminA, nil)
	if d := r["items"].([]any)[0].(map[string]any); d["downloads"].(float64) != 0 || d["url"] == nil {
		t.Fatalf("after reset: %v", d)
	}

	// Six simultaneous clicks with two downloads left: exactly two succeed.
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, _, _ := get(url); code == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 2 {
		t.Fatalf("concurrent downloads = %d, want 2", ok)
	}

	// Cancelling takes the access away.
	o2 := buy([]m{{"sku": "D1", "qty": 1}})
	url2 := o2["downloads"].([]any)[0].(map[string]any)["url"].(string)
	e.must(200, "POST", "/v1/orders/"+o2["id"].(string)+"/cancel", e.adminA, m{"reason": "prueba"})
	if code, _, _ := get(url2); code != 410 {
		t.Fatalf("download after cancel = %d", code)
	}
	if d := e.must(200, "GET", "/v1/orders/"+o2["id"].(string)+"/downloads", e.adminA, nil)["items"].([]any)[0].(map[string]any); d["revoked"] != true || d["url"] != nil {
		t.Fatalf("cancelled grant = %v", d)
	}
	if st, _ := e.do("POST", "/v1/orders/"+o2["id"].(string)+"/downloads/"+strings.SplitN(strings.TrimPrefix(url2, "/s/store-a/api/download/"), ".", 2)[0]+"/reset", e.adminA, nil); st != 404 {
		t.Fatalf("a revoked download was reset: %d", st)
	}

	// A mixed order still has a parcel to ship.
	o3 := buy([]m{{"sku": "D1", "qty": 1}, {"sku": "S1", "qty": 1}})
	if o3["fulfillmentStatus"] != "unfulfilled" || len(o3["downloads"].([]any)) != 1 || o3["shipping"].(float64) == 0 {
		t.Fatalf("mixed order: %v %v %v", o3["fulfillmentStatus"], o3["downloads"], o3["shipping"])
	}
	url3 := o3["downloads"].([]any)[0].(map[string]any)["url"].(string)

	// Removing the file stops every link to it.
	if st, _ := e.do("DELETE", "/v1/digital/D1/"+fileID, e.adminB, nil); st != 404 {
		t.Fatalf("store B deleted a file: %d", st)
	}
	e.must(204, "DELETE", "/v1/digital/D1/"+fileID, e.adminA, nil)
	if code, _, _ := get(url3); code != 410 {
		t.Fatalf("download of a removed file = %d", code)
	}
}
