package carriers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestQuoterCachesAndFallsBack(t *testing.T) {
	q := &Quoter{Timeout: 50 * time.Millisecond}
	r := RateRequest{From: Address{PostalCode: "06600", Country: "MX"}, To: Address{PostalCode: "64000", Country: "MX"}, WeightGrams: 1500}
	rate, ok := q.Quote(context.Background(), "t|sbx", Sandbox{}, r)
	if !ok || rate.Amount != 9900+1500*2 {
		t.Fatalf("sandbox rate = %+v %v", rate, ok)
	}
	r.To.PostalCode = "99999" // never answers
	start := time.Now()
	if _, ok := q.Quote(context.Background(), "t|sbx", Sandbox{}, r); ok || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("a hanging carrier: ok=%v after %v", ok, time.Since(start))
	}
	// The failure is cached: the second ask is instant.
	start = time.Now()
	q.Quote(context.Background(), "t|sbx", Sandbox{}, r)
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("the failure was not cached")
	}
}

func TestHTTPConnectorSignsAndMaps(t *testing.T) {
	secret := []byte("s3cr3t")
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(r.Body)
		if Sign(secret, r.Header.Get("X-Apexx-Timestamp"), body) != r.Header.Get("X-Apexx-Signature") {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/rates":
			_ = json.NewEncoder(w).Encode(map[string]any{"rates": []Rate{{Service: "a", Amount: 500, Days: 2}, {Service: "b", Amount: 300, Days: 5}}})
		case "/labels":
			var in LabelRequest
			_ = json.Unmarshal(body, &in)
			if in.To.PostalCode == "00000" {
				w.WriteHeader(422)
				_, _ = w.Write([]byte(`{"detail":"Sin cobertura"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(Label{TrackingNumber: "T1", LabelURL: "http://insecure/label.pdf", Cost: 300})
		}
	}))
	defer srv.Close()
	h := HTTP{Endpoint: srv.URL, Secret: secret}
	q := &Quoter{}
	rate, ok := q.Quote(context.Background(), "k", h, RateRequest{To: Address{PostalCode: "1"}})
	if !ok || rate.Amount != 300 {
		t.Fatalf("cheapest rate = %+v %v", rate, ok)
	}
	l, err := h.BuyLabel(context.Background(), LabelRequest{RateRequest: RateRequest{To: Address{PostalCode: "1"}}})
	if err != nil || l.TrackingNumber != "T1" || l.LabelURL != "" {
		t.Fatalf("label = %+v %v (an http link must be dropped)", l, err)
	}
	if _, err := h.BuyLabel(context.Background(), LabelRequest{RateRequest: RateRequest{To: Address{PostalCode: "00000"}}}); !errors.Is(err, ErrRejected) {
		t.Fatalf("refused label err = %v", err)
	}
	bad := HTTP{Endpoint: srv.URL, Secret: []byte("otro")}
	if _, err := bad.Rates(context.Background(), RateRequest{}); !errors.Is(err, ErrRejected) {
		t.Fatalf("a wrong signature was accepted: %v", err)
	}
}
