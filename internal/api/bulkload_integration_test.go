package api

import (
	"context"
	"reflect"
	"testing"

	"github.com/helloghc/apexx/internal/catalog"
)

// The start-up loaders read, in one query per kind, exactly what the
// one-by-one loaders read.
func TestBulkLoadersMatchOneByOne(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	n := 0
	err := e.db.EachProduct(ctx, "store-a", func(p catalog.Product) {
		n++
		one, err := e.db.LoadProduct(ctx, "store-a", p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(one, p) {
			t.Fatalf("product %s differs:\n one: %+v\nbulk: %+v", p.ID, one, p)
		}
	})
	if err != nil || n == 0 {
		t.Fatalf("EachProduct = %d, %v", n, err)
	}
	stock, err := e.db.AllStock(ctx, "store-a")
	if err != nil || len(stock) == 0 {
		t.Fatalf("AllStock = %v, %v", stock, err)
	}
	for sku, levels := range stock {
		one, err := e.db.LoadStock(ctx, "store-a", sku)
		if err != nil || !reflect.DeepEqual(one, levels) {
			t.Fatalf("stock %s: %v vs %v (%v)", sku, one, levels, err)
		}
	}
	prices, err := e.db.AllPrices(ctx, "store-a")
	if err != nil || len(prices) == 0 {
		t.Fatalf("AllPrices = %v, %v", prices, err)
	}
	for k, tiers := range prices {
		one, err := e.db.LoadPrices(ctx, "store-a", k[0], k[1])
		if err != nil || !reflect.DeepEqual(one, tiers) {
			t.Fatalf("prices %v: %v vs %v (%v)", k, one, tiers, err)
		}
	}
	// Each store loads only its own rows.
	_ = e.db.EachProduct(ctx, "store-b", func(p catalog.Product) {
		if one, err := e.db.LoadProduct(ctx, "store-b", p.ID); err != nil || !reflect.DeepEqual(one, p) {
			t.Fatalf("store B product %s is not store B's: %v", p.ID, err)
		}
	})
}
