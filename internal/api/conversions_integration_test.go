package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Server-side conversions: only purchases with advertising consent are
// reported, with hashed personal data and the order id as event id; a
// failing platform is retried; secrets never come back.
func TestServerSideConversions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var mu sync.Mutex
	var got []string
	fail := false
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.URL.Path+"?"+r.URL.RawQuery+" "+string(b))
		f := fail
		mu.Unlock()
		if f {
			w.WriteHeader(500)
		}
	}))
	defer platform.Close()
	conversionsHTTPClient = platform.Client()
	metaEventsURL, ga4CollectURL = platform.URL+"/meta", platform.URL+"/ga4"
	defer func() {
		conversionsHTTPClient = nil
		metaEventsURL, ga4CollectURL = "https://graph.facebook.com/v21.0", "https://www.google-analytics.com/mp/collect"
	}()
	if st, _ := e.do("PUT", "/v1/settings/ad-conversions", e.adminA, m{"meta": m{"active": true, "id": "abc"}}); st != 400 {
		t.Fatalf("bad pixel accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/ad-conversions", e.adminA, m{"meta": m{"active": true, "id": "123456789", "secret": "EAAtoken", "testCode": "TEST1"},
		"ga4": m{"active": true, "id": "G-ABC123", "secret": "ga4secret"}})
	cfg := e.must(200, "GET", "/v1/settings/ad-conversions", e.adminA, nil)
	if strings.Contains(fmt.Sprint(cfg), "EAAtoken") || strings.Contains(fmt.Sprint(cfg), "ga4secret") {
		t.Fatalf("secret returned: %v", cfg)
	}
	n := 0
	buy := func(consent bool) string {
		n++
		_, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}}, "adsConsent": consent,
			"customer": m{"email": "Pixel@Cliente.example", "name": "Pixel", "phone": "55 1234 5678"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", fmt.Sprintf("conversion-%d", n))
		return o["id"].(string)
	}
	yes, no := buy(true), buy(false)
	if _, err := e.server.RunConversions(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	all := strings.Join(got, "\n")
	mu.Unlock()
	if len(got) != 2 || strings.Contains(all, no) {
		t.Fatalf("reports = %d, without-consent order reported: %v", len(got), strings.Contains(all, no))
	}
	if strings.Contains(strings.ToLower(all), "pixel@cliente.example") || strings.Contains(all, "1234 5678") {
		t.Fatal("personal data sent in clear")
	}
	var meta struct {
		Data []struct {
			EventID  string `json:"event_id"`
			UserData struct {
				Em []string `json:"em"`
			} `json:"user_data"`
		} `json:"data"`
		TestCode string `json:"test_event_code"`
	}
	for _, g := range got {
		if strings.HasPrefix(g, "/meta/123456789/events?access_token=EAAtoken") {
			_ = json.Unmarshal([]byte(g[strings.Index(g, " ")+1:]), &meta)
		}
	}
	if len(meta.Data) != 1 || meta.Data[0].EventID != yes || meta.TestCode != "TEST1" || meta.Data[0].UserData.Em[0] != sha("pixel@cliente.example") {
		t.Fatalf("meta event = %+v", meta)
	}
	if !strings.Contains(all, "/ga4?measurement_id=G-ABC123&api_secret=ga4secret") || !strings.Contains(all, `"transaction_id":"`+yes+`"`) {
		t.Fatalf("ga4 event missing: %s", all)
	}
	// A failing platform is retried later, not lost.
	mu.Lock()
	fail = true
	mu.Unlock()
	third := buy(true)
	_, _ = e.server.RunConversions(ctx)
	var status, lastErr string
	_ = e.db.System.QueryRow(ctx, `SELECT status, last_error FROM conversion_jobs WHERE order_id=$1 AND platform='meta'`, third).Scan(&status, &lastErr)
	if status != "pending" || !strings.Contains(lastErr, "500") {
		t.Fatalf("failed report = %s %q", status, lastErr)
	}
	if b := e.must(200, "GET", "/v1/settings/ad-conversions", e.adminB, nil); b["meta"].(map[string]any)["active"] != false {
		t.Fatalf("store B has store A's settings: %v", b)
	}
}
