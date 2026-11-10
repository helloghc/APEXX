// Package importer migrates a store from VTEX into Apexx. It only reads from
// VTEX (GET requests) and writes into Apexx through the normal store
// functions, so outbox events, audit log and projections all apply.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrNotFound is returned for VTEX 404 responses.
var ErrNotFound = errors.New("vtex: not found")

// Client is a rate-limited, retrying VTEX API client.
type Client struct {
	Account    string
	AppKey     string
	AppToken   string
	BaseURL    string // https://{account}.vtexcommercestable.com.br
	PricingURL string // https://api.vtex.com/{account}
	HTTP       *http.Client
	MaxRetries int
	RPS        float64

	once     sync.Once
	mu       sync.Mutex
	next     time.Time
	Requests int64
	Retries  int64
}

// NewClient builds a client for account with sane defaults.
func NewClient(account, key, token string) *Client {
	return &Client{
		Account: account, AppKey: key, AppToken: token,
		BaseURL:    "https://" + account + ".vtexcommercestable.com.br",
		PricingURL: "https://api.vtex.com/" + account,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 5, RPS: 10,
	}
}

// wait blocks until the rate limiter allows one more request.
func (c *Client) wait(ctx context.Context) error {
	if c.RPS <= 0 {
		return nil
	}
	interval := time.Duration(float64(time.Second) / c.RPS)
	c.mu.Lock()
	now := time.Now()
	if c.next.Before(now) {
		c.next = now
	}
	at := c.next
	c.next = c.next.Add(interval)
	c.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// get fetches url into out, retrying 429 and 5xx with exponential backoff.
func (c *Client) get(ctx context.Context, url string, out any) error {
	backoff := 500 * time.Millisecond
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("X-VTEX-API-AppKey", c.AppKey)
		req.Header.Set("X-VTEX-API-AppToken", c.AppToken)
		req.Header.Set("Accept", "application/json")
		c.mu.Lock()
		c.Requests++
		c.mu.Unlock()
		resp, err := c.HTTP.Do(req)
		retry := false
		var status int
		var body []byte
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			retry = true
		} else {
			status = resp.StatusCode
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
			resp.Body.Close()
			retry = status == http.StatusTooManyRequests || status >= 500
			if ra := resp.Header.Get("Retry-After"); retry && ra != "" {
				if s, e := strconv.Atoi(ra); e == nil && s > 0 && s < 120 {
					backoff = time.Duration(s) * time.Second
				}
			}
		}
		if !retry {
			switch {
			case status == http.StatusNotFound:
				return ErrNotFound
			case status >= 400:
				return fmt.Errorf("vtex GET %s: status %d", stripQuery(url), status)
			}
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("vtex GET %s: decode: %w", stripQuery(url), err)
			}
			return nil
		}
		if attempt >= c.MaxRetries {
			if err != nil {
				return fmt.Errorf("vtex GET %s: %w", stripQuery(url), err)
			}
			return fmt.Errorf("vtex GET %s: status %d after %d retries", stripQuery(url), status, attempt)
		}
		c.mu.Lock()
		c.Retries++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func stripQuery(u string) string {
	for i := 0; i < len(u); i++ {
		if u[i] == '?' {
			return u[:i]
		}
	}
	return u
}

// ---------- VTEX payloads (only the fields we use) ----------

// ProductAndSkuIDs is one page of GetProductAndSkuIds.
type ProductAndSkuIDs struct {
	Data  map[string][]int64 `json:"data"`
	Range struct {
		Total int `json:"total"`
		From  int `json:"from"`
		To    int `json:"to"`
	} `json:"range"`
}

// Product is /api/catalog/pvt/product/{id}.
type Product struct {
	ID          int64  `json:"Id"`
	Name        string `json:"Name"`
	LinkID      string `json:"LinkId"`
	Title       string `json:"Title"`
	Description string `json:"Description"`
	BrandID     int64  `json:"BrandId"`
	CategoryID  int64  `json:"CategoryId"`
	IsActive    bool   `json:"IsActive"`
	IsVisible   bool   `json:"IsVisible"`
	RefID       string `json:"RefId"`
}

// SKU is /api/catalog_system/pvt/sku/stockkeepingunitbyid/{id}.
type SKU struct {
	ID                int64             `json:"Id"`
	ProductID         int64             `json:"ProductId"`
	IsActive          bool              `json:"IsActive"`
	SkuName           string            `json:"SkuName"`
	NameComplete      string            `json:"NameComplete"`
	BrandName         string            `json:"BrandName"`
	ProductCategories map[string]string `json:"ProductCategories"`
	AlternateIDs      struct {
		Ean   string `json:"Ean"`
		RefID string `json:"RefId"`
	} `json:"AlternateIds"`
	Dimension struct {
		Weight float64 `json:"weight"`
	} `json:"Dimension"`
	SkuSpecifications []struct {
		FieldName   string   `json:"FieldName"`
		FieldValues []string `json:"FieldValues"`
	} `json:"SkuSpecifications"`
}

// Price is /pricing/prices/{sku}. All values are decimals in major units.
type Price struct {
	ItemID      string   `json:"itemId"`
	ListPrice   *float64 `json:"listPrice"`
	CostPrice   *float64 `json:"costPrice"`
	Markup      *float64 `json:"markup"`
	BasePrice   *float64 `json:"basePrice"`
	FixedPrices []struct {
		TradePolicyID string   `json:"tradePolicyId"`
		Value         float64  `json:"value"`
		ListPrice     *float64 `json:"listPrice"`
		MinQuantity   int64    `json:"minQuantity"`
	} `json:"fixedPrices"`
}

// Inventory is /api/logistics/pvt/inventory/skus/{sku}.
type Inventory struct {
	SkuID   string `json:"skuId"`
	Balance []struct {
		WarehouseID          string `json:"warehouseId"`
		WarehouseName        string `json:"warehouseName"`
		TotalQuantity        int64  `json:"totalQuantity"`
		ReservedQuantity     int64  `json:"reservedQuantity"`
		HasUnlimitedQuantity bool   `json:"hasUnlimitedQuantity"`
	} `json:"balance"`
}

// ProductPage returns products [from, to] (1-based, at most 250 per page).
func (c *Client) ProductPage(ctx context.Context, from, to int) (ProductAndSkuIDs, error) {
	var p ProductAndSkuIDs
	err := c.get(ctx, fmt.Sprintf("%s/api/catalog_system/pvt/products/GetProductAndSkuIds?_from=%d&_to=%d", c.BaseURL, from, to), &p)
	return p, err
}

// Product fetches one product.
func (c *Client) Product(ctx context.Context, id int64) (Product, error) {
	var p Product
	err := c.get(ctx, fmt.Sprintf("%s/api/catalog/pvt/product/%d", c.BaseURL, id), &p)
	return p, err
}

// SKU fetches one SKU with context (names, brand, categories).
func (c *Client) SKU(ctx context.Context, id int64) (SKU, error) {
	var s SKU
	err := c.get(ctx, fmt.Sprintf("%s/api/catalog_system/pvt/sku/stockkeepingunitbyid/%d", c.BaseURL, id), &s)
	return s, err
}

// Price fetches the price of a SKU.
func (c *Client) Price(ctx context.Context, id int64) (Price, error) {
	var p Price
	err := c.get(ctx, fmt.Sprintf("%s/pricing/prices/%d", c.PricingURL, id), &p)
	return p, err
}

// Inventory fetches stock of a SKU per warehouse.
func (c *Client) Inventory(ctx context.Context, id int64) (Inventory, error) {
	var inv Inventory
	err := c.get(ctx, fmt.Sprintf("%s/api/logistics/pvt/inventory/skus/%d", c.BaseURL, id), &inv)
	return inv, err
}
