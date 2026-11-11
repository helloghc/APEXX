package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/auth"
)

// routesPromotions registers promotions, campaigns, coupons, the simulator,
// price lists, the bulk price tool and the price history.
func (s *Server) routesPromotions(handle func(string, http.HandlerFunc)) {
	// Promotions.
	handle("GET /v1/promotions", s.authed("promotions", auth.Read, s.listPromotions))
	handle("POST /v1/promotions", s.authed("promotions", auth.Edit, s.createPromotion))
	handle("POST /v1/promotions/bulk", s.authed("promotions", auth.Edit, s.promotionsBulk))
	handle("POST /v1/promotions/preview", s.authed("promotions", auth.Read, s.previewPromotion))
	handle("POST /v1/promotions/simulate", s.authed("promotions", auth.Read, s.simulatePromotions))
	handle("GET /v1/promotions/targets", s.authed("", "", s.searchTargets))
	handle("GET /v1/promotions/facets", s.authed("", "", s.promotionFacets))
	handle("GET /v1/promotions/{id}", s.authed("promotions", auth.Read, s.getPromotion))
	handle("PUT /v1/promotions/{id}", s.authed("promotions", auth.Edit, s.putPromotion))
	handle("DELETE /v1/promotions/{id}", s.authed("promotions", auth.Admin, s.deletePromotion))
	handle("POST /v1/promotions/{id}/duplicate", s.authed("promotions", auth.Edit, s.duplicatePromotion))

	// Campaigns.
	handle("GET /v1/campaigns", s.authed("promotions", auth.Read, s.listCampaigns))
	handle("POST /v1/campaigns", s.authed("promotions", auth.Edit, s.createCampaign))
	handle("GET /v1/campaigns/{id}", s.authed("promotions", auth.Read, s.getCampaign))
	handle("PUT /v1/campaigns/{id}", s.authed("promotions", auth.Edit, s.putCampaign))
	handle("DELETE /v1/campaigns/{id}", s.authed("promotions", auth.Admin, s.deleteCampaign))

	// Coupons.
	handle("GET /v1/coupons", s.authed("promotions", auth.Read, s.listCoupons))
	handle("POST /v1/coupons", s.authed("promotions", auth.Edit, s.createCoupon))
	handle("POST /v1/coupons/generate", s.authed("promotions", auth.Edit, s.generateCoupons))
	handle("POST /v1/coupons/bulk", s.authed("promotions", auth.Edit, s.couponsBulk))
	handle("GET /v1/coupons/batches", s.authed("promotions", auth.Read, s.listCouponBatches))
	handle("GET /v1/coupons/export", s.authed("promotions", auth.Read, s.exportCoupons))

	// Price lists, bulk change and history.
	handle("GET /v1/price-lists", s.authed("pricing", auth.Read, s.listPriceLists))
	handle("POST /v1/price-lists", s.authed("pricing", auth.Edit, s.createPriceList))
	handle("GET /v1/price-lists/{id}", s.authed("pricing", auth.Read, s.getPriceList))
	handle("PUT /v1/price-lists/{id}", s.authed("pricing", auth.Edit, s.putPriceList))
	handle("DELETE /v1/price-lists/{id}", s.authed("pricing", auth.Admin, s.deletePriceList))
	handle("POST /v1/pricing/bulk/preview", s.authed("pricing", auth.Read, s.bulkPricesPreview))
	handle("POST /v1/pricing/bulk/apply", s.authed("pricing", auth.Admin, s.bulkPricesApply))
	handle("GET /v1/pricing/history", s.authed("pricing", auth.Read, s.priceHistory))
}
