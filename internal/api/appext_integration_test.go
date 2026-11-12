package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"
)

// App extensions: the panel gets a signed, short-lived session for an app's
// page or order block; only members get one, only for their store's orders,
// and a revoked app shows nothing.
func TestAppExtensions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if st, _ := e.do("POST", "/v1/apps", e.adminA, m{"name": "Mala", "adminPage": "http://x.example", "permissions": m{"orders": "read"}}); st != 400 {
		t.Fatalf("plain http page accepted: %d", st)
	}
	app := e.must(201, "POST", "/v1/apps", e.adminA, m{"name": "Envíos Pro", "adminPage": "https://app.envios.example/panel", "orderBlock": "https://app.envios.example/pedido?x=1", "permissions": m{"orders": "read"}})["app"].(map[string]any)
	id := app["id"].(string)
	secret := e.must(200, "GET", "/v1/apps/"+id+"/extension-secret", e.adminA, nil)["secret"].(string)
	if st, _ := e.do("GET", "/v1/apps/"+id+"/extension-secret", e.adminB, nil); st != 404 {
		t.Fatalf("store B read the secret: %d", st)
	}
	// API keys (and apps) do not get panel sessions.
	if st, _ := e.do("POST", "/v1/app-extensions/"+id+"/session", e.adminA, m{"place": "page"}); st != 403 {
		t.Fatalf("API key got a panel session: %d", st)
	}
	if _, err := e.db.System.Exec(ctx, `INSERT INTO members (tenant_id, id, email, name, is_owner, status, permissions, password_hash) VALUES ('store-a','mem_ext','ext@a.example','Ext',false,'active','{"orders":"read"}','x')`); err != nil {
		t.Fatal(err)
	}
	_ = e.server.Auth.SetPassword(ctx, "store-a", "test", "mem_ext", "", "Contrasena-Larga-2026!")
	sess, _, err := e.server.Auth.Login(ctx, "store-a", "ext@a.example", "Contrasena-Larga-2026!", "", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if l := e.must(200, "GET", "/v1/app-extensions", sess, nil)["items"].([]any); len(l) != 1 {
		t.Fatalf("extensions = %v", l)
	}
	verify := func(u string) map[string]any {
		pu, _ := url.Parse(u)
		session := pu.Query().Get("session")
		body, sig, _ := strings.Cut(session, ".")
		m := hmac.New(sha256.New, []byte(secret))
		m.Write([]byte(body))
		if hex.EncodeToString(m.Sum(nil)) != sig {
			t.Fatal("session signature does not verify with the app's secret")
		}
		raw, _ := base64.RawURLEncoding.DecodeString(body)
		var claims map[string]any
		_ = json.Unmarshal(raw, &claims)
		return claims
	}
	r := e.must(200, "POST", "/v1/app-extensions/"+id+"/session", sess, m{"place": "page"})
	c := verify(r["url"].(string))
	if c["store"] != "store-a" || c["member"] != "mem_ext" || c["exp"].(float64) > float64(time.Now().Add(6*time.Minute).Unix()) {
		t.Fatalf("claims = %v", c)
	}
	// Order block: an order of this store, with the order in the session.
	_, o := e.do("POST", "/v1/checkout", e.sfA, m{"channel": "mx", "postalCode": "53129", "items": []m{{"sku": "S1", "qty": 1}},
		"customer": m{"email": "ext@cliente.example", "name": "Ext"}, "paymentMethod": "pm_card_visa"}, "Idempotency-Key", "ext-order-1")
	r = e.must(200, "POST", "/v1/app-extensions/"+id+"/session", sess, m{"place": "order", "order": o["id"]})
	if !strings.HasPrefix(r["url"].(string), "https://app.envios.example/pedido?x=1&session=") || verify(r["url"].(string))["order"] != o["id"] {
		t.Fatalf("order block url = %v", r["url"])
	}
	if st, _ := e.do("POST", "/v1/app-extensions/"+id+"/session", sess, m{"place": "order", "order": "ord_de_otra_tienda"}); st != 404 {
		t.Fatalf("session for an unknown order: %d", st)
	}
	// Revoked: nothing to show.
	e.must(204, "DELETE", "/v1/apps/"+id, e.adminA, nil)
	if st, _ := e.do("POST", "/v1/app-extensions/"+id+"/session", sess, m{"place": "page"}); st != 404 {
		t.Fatalf("revoked app still gets sessions: %d", st)
	}
}
