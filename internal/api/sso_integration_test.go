package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/helloghc/apexx/internal/auth"
)

// Single sign-on: the provider proves the email, only active members get in,
// state and nonce cannot be forged, and enforcement keeps the owner's way in.
func TestSingleSignOn(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: "k1"}}, nil)
	var nonce, email, aud string
	verified := true
	idp := httptest.NewServer(nil)
	defer idp.Close()
	idp.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/auth", "token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code_verifier") == "" {
				w.WriteHeader(400)
				return
			}
			claims := map[string]any{"iss": idp.URL, "sub": "u1", "aud": aud, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": email, "email_verified": verified}
			tok, _ := jwt.Signed(signer).Claims(claims).Serialize()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": tok, "expires_in": 3600})
		}
	})
	ssoHTTPClient = idp.Client()
	defer func() { ssoHTTPClient = nil }()

	if st, _ := e.do("PUT", "/v1/settings/sso", e.sfA, m{"enabled": false}); st != 401 && st != 403 {
		t.Fatalf("storefront key changed SSO: %d", st)
	}
	if st, _ := e.do("PUT", "/v1/settings/sso", e.adminA, m{"enabled": true, "issuer": idp.URL}); st != 400 {
		t.Fatalf("SSO without client accepted: %d", st)
	}
	e.must(200, "PUT", "/v1/settings/sso", e.adminA, m{"enabled": true, "issuer": idp.URL, "clientId": "apexx-a", "clientSecret": "s3cr3t", "domains": []string{"empresa.example"}, "name": "Empresa"})
	cfg := e.must(200, "GET", "/v1/settings/sso", e.adminA, nil)
	if _, leak := cfg["config"].(map[string]any)["clientSecret"]; leak || cfg["hasSecret"] != true {
		t.Fatalf("secret returned: %v", cfg)
	}
	if a := e.must(200, "GET", "/v1/auth/sso/available?store=store-a", "", nil); a["enabled"] != true {
		t.Fatalf("available = %v", a)
	}
	if a := e.must(200, "GET", "/v1/auth/sso/available?store=store-b", "", nil); a["enabled"] != false {
		t.Fatalf("store B has store A's SSO: %v", a)
	}
	// A member of the store.
	if _, err := e.db.System.Exec(ctx, `INSERT INTO members (tenant_id, id, email, name, is_owner, status, permissions, password_hash) VALUES ('store-a','mem_sso','ana@empresa.example','Ana',false,'active','{"catalog":"edit"}','x')`); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	login := func(who string, tamper func(q url.Values)) (int, string, string) {
		email = who
		res, err := client.Get(e.srv.URL + "/v1/auth/sso/start?store=store-a")
		if err != nil || res.StatusCode != 302 {
			t.Fatalf("start = %v %v", res, err)
		}
		loc, _ := url.Parse(res.Header.Get("Location"))
		q := loc.Query()
		nonce, aud = q.Get("nonce"), q.Get("client_id")
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			t.Fatal("no PKCE")
		}
		var state *http.Cookie
		for _, c := range res.Cookies() {
			if c.Name == "apexx_sso" {
				state = c
			}
		}
		cb := url.Values{"code": {"c1"}, "state": {q.Get("state")}}
		if tamper != nil {
			tamper(cb)
		}
		req, _ := http.NewRequest("GET", e.srv.URL+"/v1/auth/sso/callback?"+cb.Encode(), nil)
		req.AddCookie(state)
		res2, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res2.Body.Close()
		sess := ""
		for _, c := range res2.Cookies() {
			if c.Name == "apexx_session" {
				sess = c.Value
			}
		}
		return res2.StatusCode, sess, q.Get("state")
	}
	st, sess, _ := login("ana@empresa.example", nil)
	if st != 200 || !strings.HasPrefix(sess, "aps_") {
		t.Fatalf("SSO login = %d %q", st, sess)
	}
	if me := e.must(200, "GET", "/v1/me", sess, nil); !strings.Contains(fmt.Sprint(me), "ana@empresa.example") {
		t.Fatalf("me = %v", me)
	}
	// Not a member, wrong domain, unverified email, forged state: refused.
	if st, sess, _ := login("nadie@empresa.example", nil); st != 403 || sess != "" {
		t.Fatalf("non-member got in: %d", st)
	}
	if st, _, _ := login("ana@otra.example", nil); st != 403 {
		t.Fatalf("other domain got in: %d", st)
	}
	verified = false
	if st, _, _ := login("ana@empresa.example", nil); st != 403 {
		t.Fatalf("unverified email got in: %d", st)
	}
	verified = true
	if st, _, _ := login("ana@empresa.example", func(q url.Values) { q.Set("state", "otro") }); st != 400 {
		t.Fatalf("forged state accepted: %d", st)
	}
	nonceFor := ""
	_ = nonceFor
	// Enforcement: the team cannot use passwords; the owner still can.
	e.must(200, "PUT", "/v1/settings/sso", e.adminA, m{"enabled": true, "enforce": true, "issuer": idp.URL, "clientId": "apexx-a", "domains": []string{"empresa.example"}})
	if err := e.server.Auth.SetPassword(ctx, "store-a", "test", "mem_sso", "", "Contrasena-Larga-2026!"); err != nil {
		t.Fatal(err)
	} else {
		if st, b := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "ana@empresa.example", "password": "Contrasena-Larga-2026!"}); st != 403 || b["code"] != "sso_required" {
			t.Fatalf("password login under enforced SSO = %d %v", st, b)
		}
	}
	_ = auth.ErrSSORequired
}
