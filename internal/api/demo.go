package api

import (
	"net/http"
	"time"

	"github.com/helloghc/apexx/internal/apierr"
)

// demoEntryLimiter caps how often one address can start a demo session.
var demoEntryLimiter = &shopLimiter{n: 20} // per address per minute

// demoLogin signs a visitor into the public, self-service demo panel.
//
// Security model (why this is safe to expose publicly):
//   - It is enabled ONLY when a demo tenant/account is configured, which never
//     happens in production. If unset, the route 404s.
//   - The visitor never chooses the tenant or the user: both are fixed from the
//     server's config. A caller can ONLY ever land in the single, isolated demo
//     tenant, with the restricted "visitor" role seeded for it.
//   - It performs the normal, password-checked Login with server-held demo
//     credentials — no auth is bypassed and no session is minted by hand.
//   - Tenant isolation (RLS) guarantees the resulting session can touch only the
//     demo tenant's data; it can never reach a paying tenant or the platform.
//   - It is rate-limited per address.
//
// The maximum blast radius of any abuse is the demo tenant itself, which is
// disposable and meant to be reset on a schedule.
func (s *Server) demoLogin(w http.ResponseWriter, r *http.Request) {
	if s.DemoTenant == "" || s.DemoEmail == "" || s.DemoPassword == "" {
		http.NotFound(w, r)
		return
	}
	ip := s.clientIP(r)
	if !demoEntryLimiter.allow(ip) {
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "rate_limited",
			Detail: "Demasiadas entradas a la demo; intenta de nuevo en un minuto."})
		return
	}
	token, _, err := s.Auth.Login(r.Context(), s.DemoTenant, s.DemoEmail, s.DemoPassword, "", ip, r.UserAgent())
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "apexx_session", Value: token, Path: "/", HttpOnly: true, Secure: s.SecureCookies,
		SameSite: http.SameSiteStrictMode, MaxAge: int((12 * time.Hour).Seconds())})
	http.Redirect(w, r, "/admin/", http.StatusFound)
}
