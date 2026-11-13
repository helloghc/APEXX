package api

// resetLimiters gives every test a clean rate-limit window: the limiters are
// per process, and tests share the loopback address.
func resetLimiters() {
	for _, l := range []*shopLimiter{suggestLimiter, reviewLimiter, formLimiter, feedLimiter, quoteLimiter, listLimiter, askLimiter, simLimiter, couponLimiter,
		checkoutLimiter, browseLimiter, codeLimiter, verifyLimiter, newsletterLimiter, trackLimiter, accountLimiter, subLimiter, giftCardLimiter,
		passwordIPLimiter, passwordAllLimiter} {
		l.reset()
	}
}
