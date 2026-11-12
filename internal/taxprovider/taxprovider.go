// Package taxprovider asks an external tax service (a tax engine app) for
// the tax of a cart, with the same signed contract as carrier apps. The
// answer is cached for five minutes; a slow or failing service falls back
// to Apexx's own calculation, so a sale never stops for it.
package taxprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/carriers"
)

// Part is an amount of one tax class (gross when prices include tax).
type Part struct {
	Class  string `json:"class"`
	Amount int64  `json:"amount"`
}

// Request is the cart as the tax service sees it.
type Request struct {
	Country    string `json:"country"`
	PostalCode string `json:"postalCode"`
	Currency   string `json:"currency"`
	Inclusive  bool   `json:"pricesIncludeTax"`
	Parts      []Part `json:"parts"`
}

// Line is the tax of one class.
type Line struct {
	Class   string `json:"class"`
	Label   string `json:"label"`
	RateBps int64  `json:"rateBps"`
	Tax     int64  `json:"tax"`
}

// Response is what the service answers.
type Response struct {
	Lines []Line `json:"lines"`
}

// Client calls POST {endpoint}/tax signed like carrier apps.
type Client struct {
	Endpoint string
	Secret   []byte
	HTTP     *http.Client
}

// Calculate asks the service.
func (c Client) Calculate(ctx context.Context, r Request) (Response, error) {
	body, _ := json.Marshal(r)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.Endpoint, "/")+"/tax", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Apexx-Timestamp", ts)
	req.Header.Set("X-Apexx-Signature", carriers.Sign(c.Secret, ts, body))
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 5 * time.Second}
	}
	res, err := h.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("servicio de impuestos: %s", res.Status)
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, err
	}
	for _, l := range out.Lines {
		if l.Tax < 0 || l.RateBps < 0 || l.RateBps > 10000 {
			return Response{}, fmt.Errorf("servicio de impuestos: respuesta fuera de rango")
		}
	}
	return out, nil
}

// Cache keeps answers for five minutes and failures for thirty seconds.
type Cache struct {
	mu sync.Mutex
	m  map[string]cached
}

type cached struct {
	r     Response
	ok    bool
	until time.Time
}

// Get answers r from the cache or asks c within timeout.
func (k *Cache) Get(ctx context.Context, key string, c Client, r Request, timeout time.Duration) (Response, bool) {
	b, _ := json.Marshal(r)
	ck := key + "|" + string(b)
	now := time.Now()
	k.mu.Lock()
	if k.m == nil {
		k.m = map[string]cached{}
	}
	x, hit := k.m[ck]
	k.mu.Unlock()
	if hit && now.Before(x.until) {
		return x.r, x.ok
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	res, err := c.Calculate(cctx, r)
	cancel()
	x = cached{r: res, ok: err == nil, until: now.Add(5 * time.Minute)}
	if err != nil {
		x.until = now.Add(30 * time.Second)
	}
	k.mu.Lock()
	if len(k.m) > 50000 {
		k.m = map[string]cached{}
	}
	k.m[ck] = x
	k.mu.Unlock()
	return x.r, x.ok
}
