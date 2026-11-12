package api

import "net/http"

// Headless stores (another domain using the SDK) call these public storefront
// endpoints from the browser. No cookies cross origins: a headless store
// works as a guest, with the same rate limits as the store's own pages.
var headlessPaths = []string{
	"/s/{tenant}/api/graphql",
	"/s/{tenant}/api/simulate",
	"/s/{tenant}/api/checkout",
	"/s/{tenant}/api/checkout-blocks",
	"/s/{tenant}/api/payment-methods",
	"/s/{tenant}/api/config",
}

func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Vary", "Origin")
		h(w, r)
	}
}

func preflight(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
	h.Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}
