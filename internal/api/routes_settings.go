package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/auth"
)

// routesSettings registers the "Configuración" area: locations and transfers,
// shipping, taxes, channels, store identity, policies, navigation, redirects
// and custom domains, plus the public pages that depend on them.
func (s *Server) routesSettings(handle func(string, http.HandlerFunc)) {
	const mod = "settings"

	// Stock locations, transfers and low-stock alerts.
	handle("GET /v1/locations", s.authed(mod, auth.Read, s.listLocations))
	handle("PUT /v1/locations/{id}", s.authed(mod, auth.Admin, s.putLocation))
	handle("DELETE /v1/locations/{id}", s.authed(mod, auth.Admin, s.deleteLocation))
	handle("GET /v1/locations/{id}/stock", s.staff("inventory", auth.Read, s.locationStock))
	handle("GET /v1/inventory-transfers", s.authed("inventory", auth.Read, s.listTransfers))
	handle("POST /v1/inventory-transfers", s.authed("inventory", auth.Edit, s.createTransfer))
	handle("GET /v1/inventory-transfers/{id}", s.authed("inventory", auth.Read, s.getTransfer))
	handle("GET /v1/inventory-alerts", s.authed("inventory", auth.Read, s.lowStock))
	handle("PUT /v1/inventory-thresholds/{sku}", s.authed("inventory", auth.Edit, s.putThreshold))

	// Shipping engine.
	handle("GET /v1/shipping", s.authed(mod, auth.Read, s.getShipping))
	handle("POST /v1/shipping/defaults", s.authed(mod, auth.Admin, s.shippingDefaults))
	handle("POST /v1/shipping/quote", s.authed(mod, auth.Read, s.shippingQuote))
	handle("PUT /v1/shipping/holidays/{date}", s.authed(mod, auth.Admin, s.putHoliday))
	handle("DELETE /v1/shipping/holidays/{date}", s.authed(mod, auth.Admin, s.deleteHoliday))
	handle("POST /v1/shipping/holidays/defaults", s.authed(mod, auth.Admin, s.holidayDefaults))
	handle("POST /v1/shipping/{kind}", s.authed(mod, auth.Admin, s.putShippingDoc))
	handle("PUT /v1/shipping/{kind}/{id}", s.authed(mod, auth.Admin, s.putShippingDoc))
	handle("DELETE /v1/shipping/{kind}/{id}", s.authed(mod, auth.Admin, s.deleteShippingDoc))

	// Taxes and per-product classes.
	handle("GET /v1/tax/regions", s.authed(mod, auth.Read, s.listTaxRegions))
	handle("POST /v1/tax/regions", s.authed(mod, auth.Admin, s.putTaxRegion))
	handle("PUT /v1/tax/regions/{id}", s.authed(mod, auth.Admin, s.putTaxRegion))
	handle("DELETE /v1/tax/regions/{id}", s.authed(mod, auth.Admin, s.deleteTaxRegion))
	handle("POST /v1/tax/defaults", s.authed(mod, auth.Admin, s.taxDefaults))
	handle("POST /v1/tax/preview", s.authed(mod, auth.Read, s.taxPreview))
	handle("GET /v1/product-classes", s.authed(mod, auth.Read, s.listProductClasses))
	handle("GET /v1/product-classes/{id}", s.authed(mod, auth.Read, s.getProductClass))
	handle("PUT /v1/product-classes/{id}", s.authed(mod, auth.Admin, s.putProductClass))
	handle("GET /v1/product-search", s.authed(mod, auth.Read, s.productSearch))

	// Sales channels (GET/PUT by id live in commerce.go).
	handle("GET /v1/channels", s.authed(mod, auth.Read, s.listChannels))
	// Custom fields on customers, orders and companies.
	handle("GET /v1/fields/{resource}", s.staff(mod, auth.Read, s.getFieldDefs))
	handle("PUT /v1/fields/{resource}", s.authed(mod, auth.Admin, s.putFieldDefs))
	handle("GET /v1/fields/{resource}/{id}", s.staff("", "", s.getFieldValues))
	handle("PUT /v1/fields/{resource}/{id}", s.staff("", "", s.putFieldValues))
	// Master catalog copied from another store.
	handle("GET /v1/settings/catalog-source", s.staff(mod, auth.Read, s.getCatalogSource))
	handle("PUT /v1/settings/catalog-source", s.authed(mod, auth.Admin, s.putCatalogSource))
	handle("DELETE /v1/settings/catalog-source", s.authed(mod, auth.Admin, s.deleteCatalogSource))
	handle("POST /v1/settings/catalog-source/sync", s.authed(mod, auth.Admin, s.runCatalogSource))
	handle("DELETE /v1/channels/{id}", s.authed(mod, auth.Admin, s.deleteChannel))

	// Store identity and checkout rules.
	handle("GET /v1/store/settings", s.authed(mod, auth.Read, s.getSettings))
	handle("PUT /v1/store/settings", s.authed(mod, auth.Admin, s.putSettings))
	handle("PUT /v1/store/checkout", s.authed(mod, auth.Admin, s.putCheckout))
	handle("POST /v1/store/logo", s.authed(mod, auth.Admin, s.putLogo))
	handle("DELETE /v1/store/logo", s.authed(mod, auth.Admin, s.deleteLogo))

	// Policies, menus and redirects are content: editors can change them.
	handle("GET /v1/policies", s.authed(mod, auth.Read, s.listPolicies))
	handle("PUT /v1/policies/{slug}", s.authed(mod, auth.Edit, s.putPolicy))
	handle("DELETE /v1/policies/{slug}", s.authed(mod, auth.Edit, s.deletePolicy))
	handle("GET /v1/menus", s.authed(mod, auth.Read, s.listMenus))
	handle("PUT /v1/menus/{id}", s.authed(mod, auth.Edit, s.putMenu))
	handle("DELETE /v1/menus/{id}", s.authed(mod, auth.Edit, s.deleteMenu))
	handle("GET /v1/redirects", s.authed(mod, auth.Read, s.listRedirects))
	handle("POST /v1/redirects", s.authed(mod, auth.Edit, s.createRedirects))
	handle("PUT /v1/redirects/{id}", s.authed(mod, auth.Edit, s.updateRedirect))
	handle("POST /v1/redirects/delete", s.authed(mod, auth.Edit, s.deleteRedirects))
	handle("GET /v1/redirects/test", s.authed(mod, auth.Read, s.testRedirect))

	// Custom domains.
	handle("GET /v1/domains", s.authed(mod, auth.Read, s.listDomains))
	handle("POST /v1/domains", s.authed(mod, auth.Admin, s.createDomain))
	handle("POST /v1/domains/{id}/verify", s.authed(mod, auth.Admin, s.verifyDomain))
	handle("POST /v1/domains/{id}/primary", s.authed(mod, auth.Admin, s.primaryDomain))
	handle("DELETE /v1/domains/{id}", s.authed(mod, auth.Admin, s.deleteDomain))

	// Public pages and files of the store.
	handle("GET /s/{tenant}/{site}/politicas/{slug}", s.policyPage)
	handle("GET /s/{tenant}/{site}/sitemap.xml", s.sitemap)
	handle("GET /s/{tenant}/{site}/robots.txt", s.robots)
	handle("GET /s/{tenant}/assets/logo", s.logo)
	handle("GET /s/{tenant}/api/menus", s.publicMenus)
	// Caddy on-demand TLS asks here before issuing a certificate for a host.
	handle("GET /internal/tls/ask", s.tlsAsk)
}
