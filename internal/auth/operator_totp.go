package auth

import (
	"crypto/rand"
	"net/url"
	"time"
)

// Two-step verification for the platform's operators (superadmin), who are
// not members of any store. Same algorithm as the members' (RFC 6238).

// NewTOTPSecret returns a new random secret and its base32 form.
func NewTOTPSecret() (raw []byte, encoded string, err error) {
	raw = make([]byte, 20)
	if _, err = rand.Read(raw); err != nil {
		return nil, "", err
	}
	return raw, b32.EncodeToString(raw), nil
}

// TOTPURI is the otpauth:// address an authenticator app reads.
func TOTPURI(issuer, account, encoded string) string {
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?secret=" + encoded + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
}

// MatchTOTP returns the 30-second step the code is valid for (0: invalid).
// The caller stores it and refuses a step already used (no replay).
func MatchTOTP(secret []byte, code string, now time.Time) int64 { return matchTOTP(secret, code, now) }
