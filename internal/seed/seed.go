// Package seed fills the modules with demo data so the API can be exercised
// and benchmarked at the scale of the audited store (~18,500 products).
package seed

import (
	"fmt"
	"time"

	"github.com/helloghc/apexx/internal/cart"
	"github.com/helloghc/apexx/internal/catalog"
	"github.com/helloghc/apexx/internal/inventory"
	"github.com/helloghc/apexx/internal/money"
	"github.com/helloghc/apexx/internal/pricing"
	"github.com/helloghc/apexx/internal/promo"
)

// Channels returns the demo sales channels.
func Channels() map[string]cart.Channel {
	return map[string]cart.Channel{
		"mx": {
			ID: "mx", Currency: "MXN", TaxBps: 1600, MaxQtyPerItem: 50,
			Shipping: cart.ShippingRate{
				Name: "Estándar", Base: money.New(9900, "MXN"), PerKg: money.New(1500, "MXN"),
				FreeOver: money.New(149900, "MXN"), EstimateDays: 2,
			},
		},
	}
}

// Promotions returns demo promotions.
func Promotions() *promo.Engine {
	return &promo.Engine{Rules: []promo.Rule{
		promo.PercentOverThreshold{
			Window: promo.Window{ID: "promo-10-3000", Name: "10% en compras desde $3,000", Channels: []string{"mx"}},
			Bps:    1000, Min: money.New(300000, "MXN"),
		},
		promo.BuyXPayY{
			Window: promo.Window{ID: "3x2-calcetas", Name: "3x2 en calcetas"},
			SKU:    "SKU-000002-1", X: 3, Y: 2,
		},
	}}
}

// Load creates n products with two variants each.
func Load(n int, c *catalog.Store, inv *inventory.Service, pr *pricing.Service) {
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("P-%06d", i)
		var vs []catalog.Variant
		for j := 1; j <= 2; j++ {
			sku := fmt.Sprintf("SKU-%06d-%d", i, j)
			vs = append(vs, catalog.Variant{
				SKU: sku, Title: fmt.Sprintf("Talla %d", 24+j), WeightGrams: 400 + int64(i%7)*100,
				Options: map[string]string{"talla": fmt.Sprint(24 + j)},
			})
			inv.Set(sku, "cdmx-1", int64(10+i%40))
			base := int64(19900 + (i%200)*1000)
			cmp := money.New(base+base/5, "MXN")
			pr.Set("mx", sku,
				pricing.Tier{MinQty: 1, Unit: money.New(base, "MXN"), CompareAt: &cmp},
				pricing.Tier{MinQty: 10, Unit: money.New(base*95/100, "MXN")},
			)
		}
		c.Upsert(catalog.Product{
			ID: id, Handle: fmt.Sprintf("producto-%d", i), Title: fmt.Sprintf("Producto %d", i),
			Brand: []string{"Norte", "Sierra", "Pacífico"}[i%3], Categories: []string{"deportes"},
			Variants: vs, UpdatedAt: time.Now(),
		})
	}
}
