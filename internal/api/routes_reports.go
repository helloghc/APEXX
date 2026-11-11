package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/auth"
	"github.com/helloghc/apexx/internal/reports"
)

// routesReports registers analytics, reports and operations.
//
//	public    POST /s/{tenant}/api/events      storefront beacon (rate limited)
//	reports   GET  /v1/reports/...             dashboard and reports (?format=csv)
//	settings  GET  /v1/ops/...                 activity log and system status (admin)
func (s *Server) routesReports(handle func(string, http.HandlerFunc)) {
	if s.Reports == nil {
		s.Reports = &reports.Service{DB: s.DB, Keys: s.Keys, Log: s.Log}
	}
	s.eventsLimiter = &shopLimiter{n: eventsPerMinute}
	handle("POST /s/{tenant}/api/events", s.shopEvents)

	handle("GET /v1/reports/dashboard", s.authed("reports", auth.Read, s.reportDashboard))
	handle("GET /v1/reports/sales", s.authed("reports", auth.Read, s.reportSales))
	handle("GET /v1/reports/products", s.authed("reports", auth.Read, s.reportProducts))
	handle("GET /v1/reports/customers", s.authed("reports", auth.Read, s.reportCustomers))
	handle("GET /v1/reports/funnel", s.authed("reports", auth.Read, s.reportFunnel))
	handle("GET /v1/reports/promotions", s.authed("reports", auth.Read, s.reportPromotions))
	handle("GET /v1/reports/inventory", s.authed("reports", auth.Read, s.reportInventory))
	handle("POST /v1/reports/rebuild", s.authed("reports", auth.Admin, s.reportRebuild))

	handle("GET /v1/ops/audit", s.authed("settings", auth.Admin, s.opsAudit))
	handle("GET /v1/ops/audit/facets", s.authed("settings", auth.Admin, s.opsAuditFacets))
	handle("GET /v1/ops/status", s.authed("settings", auth.Admin, s.opsStatus))
}
