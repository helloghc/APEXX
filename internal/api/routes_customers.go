package api

import (
	"net/http"

	"github.com/helloghc/apexx/internal/auth"
)

// routesCustomers registers the customers, after-sale and notifications area:
// customers and segments, order depth (shipments, refunds, returns, exchanges,
// edits, drafts, notes, views, export), transactional email, abandoned carts
// and the shopper account on the storefront.
func (s *Server) routesCustomers(handle func(string, http.HandlerFunc)) {
	// Customers.
	handle("GET /v1/customers", s.authed("customers", auth.Read, s.listCustomers))
	handle("POST /v1/customers", s.authed("customers", auth.Edit, s.createCustomer))
	handle("GET /v1/customers/tags", s.authed("customers", auth.Read, s.customerTags))
	handle("POST /v1/customers/bulk-tag", s.authed("customers", auth.Edit, s.bulkTagCustomers))
	handle("GET /v1/customers/{id}", s.authed("customers", auth.Read, s.getCustomer))
	handle("PATCH /v1/customers/{id}", s.authed("customers", auth.Edit, s.updateCustomer))
	handle("DELETE /v1/customers/{id}", s.authed("customers", auth.Admin, s.deleteCustomer))
	handle("GET /v1/customers/{id}/orders", s.authed("customers", auth.Read, s.customerOrders))
	handle("POST /v1/customers/{id}/addresses", s.authed("customers", auth.Edit, s.saveCustomerAddress))
	handle("PUT /v1/customers/{id}/addresses/{address}", s.authed("customers", auth.Edit, s.saveCustomerAddress))
	handle("DELETE /v1/customers/{id}/addresses/{address}", s.authed("customers", auth.Edit, s.deleteCustomerAddress))
	handle("POST /v1/customers/{id}/notes", s.authed("customers", auth.Edit, s.addCustomerNote))
	handle("DELETE /v1/customers/{id}/notes/{note}", s.authed("customers", auth.Edit, s.deleteCustomerNote))

	// Groups and segments.
	handle("GET /v1/customer-groups", s.authed("customers", auth.Read, s.listGroups))
	handle("POST /v1/customer-groups", s.authed("customers", auth.Edit, s.saveGroup))
	handle("POST /v1/customer-groups/preview", s.authed("customers", auth.Read, s.previewGroup))
	handle("GET /v1/customer-groups/{id}", s.authed("customers", auth.Read, s.getGroup))
	handle("PUT /v1/customer-groups/{id}", s.authed("customers", auth.Edit, s.saveGroup))
	handle("DELETE /v1/customer-groups/{id}", s.authed("customers", auth.Edit, s.deleteGroup))
	handle("POST /v1/customer-groups/{id}/members", s.authed("customers", auth.Edit, s.setGroupMembers))

	// Abandoned carts.
	handle("GET /v1/abandoned-carts", s.authed("customers", auth.Read, s.listCarts))
	handle("POST /v1/abandoned-carts/{id}/remind", s.authed("customers", auth.Edit, s.remindCart))
	handle("POST /v1/abandoned-carts/{id}/dismiss", s.authed("customers", auth.Edit, s.dismissCart))

	// Orders: list, export, saved views.
	handle("GET /v1/orders", s.authed("orders", auth.Read, s.queryOrders))
	handle("GET /v1/orders/export.csv", s.authed("orders", auth.Read, s.exportOrders))
	handle("GET /v1/order-views", s.authed("orders", auth.Read, s.listOrderViews))
	handle("POST /v1/order-views", s.authed("orders", auth.Edit, s.saveOrderView))
	handle("DELETE /v1/order-views/{id}", s.authed("orders", auth.Edit, s.deleteOrderView))

	// Orders: after-sale actions.
	handle("POST /v1/orders/{id}/fulfillments", s.authed("orders", auth.Edit, s.createFulfillment))
	handle("PATCH /v1/orders/{id}/fulfillments/{fulfillment}", s.authed("orders", auth.Edit, s.updateFulfillment))
	handle("POST /v1/orders/{id}/refunds/quote", s.authed("orders", auth.Edit, s.quoteRefund))
	handle("POST /v1/orders/{id}/refunds", s.authed("orders", auth.Edit, s.createRefund))
	handle("POST /v1/orders/{id}/returns", s.authed("orders", auth.Edit, s.createReturn))
	handle("POST /v1/orders/{id}/edit", s.authed("orders", auth.Edit, s.editOrder))
	handle("POST /v1/orders/{id}/notes", s.authed("orders", auth.Edit, s.addOrderNote))
	handle("DELETE /v1/orders/{id}/notes/{note}", s.authed("orders", auth.Edit, s.deleteOrderNote))
	handle("POST /v1/orders/{id}/payments/manual", s.authed("orders", auth.Edit, s.manualPayment))
	handle("POST /v1/orders/{id}/payment-link", s.authed("orders", auth.Edit, s.sendBalanceLink))
	handle("POST /v1/orders/{id}/settle-credit", s.authed("orders", auth.Edit, s.settleCredit))
	handle("GET /v1/returns", s.authed("orders", auth.Read, s.listReturns))
	handle("GET /v1/returns/{id}", s.authed("orders", auth.Read, s.getReturn))
	handle("POST /v1/returns/{id}/receive", s.authed("orders", auth.Edit, s.receiveReturn))
	handle("POST /v1/returns/{id}/refund", s.authed("orders", auth.Edit, s.refundReturn))
	handle("POST /v1/returns/{id}/cancel", s.authed("orders", auth.Edit, s.cancelReturn))

	// Draft orders.
	handle("GET /v1/draft-orders", s.authed("orders", auth.Read, s.listDrafts))
	handle("POST /v1/draft-orders", s.authed("orders", auth.Edit, s.saveDraft))
	handle("GET /v1/draft-orders/{id}", s.authed("orders", auth.Read, s.getDraftOrder))
	handle("PUT /v1/draft-orders/{id}", s.authed("orders", auth.Edit, s.saveDraft))
	handle("POST /v1/draft-orders/{id}/send", s.authed("orders", auth.Edit, s.sendDraft))
	handle("POST /v1/draft-orders/{id}/complete", s.authed("orders", auth.Edit, s.completeDraft))
	handle("POST /v1/draft-orders/{id}/cancel", s.authed("orders", auth.Edit, s.cancelDraft))

	// Notifications: sender, templates, message log.
	handle("GET /v1/notifications/settings", s.authed("settings", auth.Read, s.getNotifySettings))
	handle("PUT /v1/notifications/settings", s.authed("settings", auth.Admin, s.putNotifySettings))
	handle("GET /v1/notifications/templates", s.authed("settings", auth.Read, s.listNotifyTemplates))
	handle("PUT /v1/notifications/templates/{key}", s.authed("settings", auth.Edit, s.putNotifyTemplate))
	handle("POST /v1/notifications/templates/{key}/preview", s.authed("settings", auth.Read, s.previewNotifyTemplate))
	handle("POST /v1/notifications/templates/{key}/test", s.authed("settings", auth.Edit, s.testNotifyTemplate))
	handle("GET /v1/notifications/messages", s.authed("settings", auth.Read, s.listNotifyMessages))
	handle("GET /v1/notifications/messages/{id}", s.authed("settings", auth.Read, s.getNotifyMessage))
	handle("POST /v1/notifications/messages/{id}/retry", s.authed("settings", auth.Edit, s.retryNotifyMessage))

	// Storefront: newsletter, cart tracking, shopper account, payment links.
	handle("POST /s/{tenant}/api/newsletter", s.shopNewsletter)
	handle("POST /s/{tenant}/api/cart/track", s.shopCartTrack)
	handle("GET /s/{tenant}/api/cart/restore", s.shopCartRestore)
	handle("POST /s/{tenant}/api/account/code", s.shopAccountCode)
	handle("POST /s/{tenant}/api/account/verify", s.shopAccountVerify)
	handle("POST /s/{tenant}/api/account/login", s.shopAccountLogin)
	handle("POST /s/{tenant}/api/account/logout", s.shopAccountLogout)
	handle("GET /s/{tenant}/api/account/me", s.shopAccountMe)
	handle("GET /s/{tenant}/api/account/orders", s.shopAccountOrders)
	handle("POST /s/{tenant}/api/pay", s.shopPay)
	handle("GET /s/{tenant}/{site}/cuenta", s.shopAccountPage)
	handle("POST /s/{tenant}/{site}/cuenta", s.shopAccountForm)
	handle("GET /s/{tenant}/{site}/pagar", s.shopPayPage)
}
