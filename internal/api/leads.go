package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"github.com/helloghc/apexx/internal/apierr"
	"github.com/helloghc/apexx/internal/httpx"
)

var (
	leadIPLimiter    = &shopLimiter{n: 6} // submissions per address per minute
	leadEmailLimiter = &shopLimiter{n: 3} // per email per minute
	leadEmailRe      = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// createLead stores a prospect from the public "Empezar" page so a person from
// sales can follow up to open their store. Public (no auth), rate-limited, with
// a honeypot field against bots. The data is platform-level (not a tenant's),
// so it is written with the system role.
func (s *Server) createLead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Phone    string `json:"phone"`
		Company  string `json:"company"`
		Country  string `json:"country"`
		Industry string `json:"industry"`
		Volume   string `json:"volume"`
		Platform string `json:"platform"`
		Message  string `json:"message"`
		Website  string `json:"website"` // honeypot: real people leave it empty
	}
	if !decode(w, r, &in) {
		return
	}
	// A bot filled the hidden field: accept silently and store nothing.
	if strings.TrimSpace(in.Website) != "" {
		httpx.JSON(w, r, http.StatusCreated, map[string]any{"ok": true})
		return
	}
	name := strings.TrimSpace(in.Name)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if name == "" {
		apierr.BadRequest(w, "Escribe tu nombre.")
		return
	}
	if !leadEmailRe.MatchString(email) {
		apierr.BadRequest(w, "Escribe un correo válido.")
		return
	}
	ip := s.clientIP(r)
	if !leadIPLimiter.allow(ip) || !leadEmailLimiter.allow(email) {
		apierr.Write(w, apierr.Problem{Status: http.StatusTooManyRequests, Code: "rate_limited",
			Detail: "Demasiados envíos. Intenta de nuevo en un minuto."})
		return
	}
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	id := "lead_" + hex.EncodeToString(raw)
	_, err := s.DB.System.Exec(r.Context(),
		`INSERT INTO platform_leads (id, name, email, phone, company, country, industry, volume, platform, message, ip)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, clip(name, 120), email, clip(in.Phone, 40), clip(in.Company, 120), clip(in.Country, 60),
		clip(in.Industry, 80), clip(in.Volume, 40), clip(in.Platform, 80), clip(in.Message, 2000), ip)
	if err != nil {
		internalError(w, s.Log, err)
		return
	}
	// No personal data in the log: just enough to know a lead arrived.
	s.Log.Info("new lead", "company", clip(in.Company, 60), "country", clip(in.Country, 40))
	httpx.JSON(w, r, http.StatusCreated, map[string]any{"ok": true})
}

// clip trims and caps a string to n runes (never cutting a character in half).
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
