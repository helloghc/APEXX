package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"
)

func appCode(secret string, at time.Time) string {
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30))
	h := hmac.New(sha1.New, key)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(sum[off:off+4])&0x7fffffff)%1_000_000)
}

// Invitations create members once; two-step verification guards the login,
// codes work once, recovery codes are single use, and sessions can be revoked.
func TestInvitationsTwoStepAndSessions(t *testing.T) {
	e := setup(t)
	// Invite, accept once, and the new member signs in with the role given.
	inv := e.must(201, "POST", "/v1/team/invitations", e.adminA, m{"email": "Nuevo@Example.com", "name": "Nora Nueva", "permissions": m{"catalog": "edit"}})
	link := inv["link"].(string)
	token := link[strings.LastIndex(link, "/")+1:]
	if st, _ := e.do("POST", "/v1/team/invitations", e.adminA, m{"email": "x@example.com", "permissions": m{"catalog": "superuser"}}); st != 400 {
		t.Fatalf("invalid permission level accepted: %d", st)
	}
	info := e.must(200, "GET", "/v1/invitations/"+token, "", nil)
	if info["email"] != "nuevo@example.com" || info["store"] != "store-a" {
		t.Fatalf("invitation info = %v", info)
	}
	if st, _ := e.do("POST", "/v1/invitations/"+token+"/accept", "", m{"name": "Nora", "password": "corta"}); st != 400 {
		t.Fatalf("weak password accepted: %d", st)
	}
	e.must(201, "POST", "/v1/invitations/"+token+"/accept", "", m{"name": "Nora Nueva", "password": "Una-clave-larga-2026"})
	if st, _ := e.do("POST", "/v1/invitations/"+token+"/accept", "", m{"name": "Otra", "password": "Una-clave-larga-2026"}); st != 410 {
		t.Fatalf("invitation used twice: %d", st)
	}
	if st, _ := e.do("POST", "/v1/team/invitations", e.adminA, m{"email": "nuevo@example.com", "permissions": m{}}); st != 409 {
		t.Fatalf("inviting an existing member = %d", st)
	}
	login := func(code string) (int, map[string]any) {
		return e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "nuevo@example.com", "password": "Una-clave-larga-2026", "code": code})
	}
	st, s1 := login("")
	if st != 200 {
		t.Fatalf("login of the invited member = %d %v", st, s1)
	}
	tok := s1["token"].(string)
	if st, _ := e.do("GET", "/v1/products", tok, nil); st != 200 {
		t.Fatalf("invited member cannot read the catalog: %d", st)
	}
	if st, _ := e.do("GET", "/v1/team/members", tok, nil); st != 403 {
		t.Fatalf("invited member reads the team without permission: %d", st)
	}
	// A revoked invitation stops working; another store cannot see or revoke it.
	inv2 := e.must(201, "POST", "/v1/team/invitations", e.adminA, m{"email": "otro@example.com", "permissions": m{"orders": "read"}})
	id2 := inv2["invitation"].(map[string]any)["id"].(string)
	link2 := inv2["link"].(string)
	if st, _ := e.do("DELETE", "/v1/team/invitations/"+id2, e.adminB, nil); st != 404 {
		t.Fatalf("another store revoked the invitation: %d", st)
	}
	e.must(204, "DELETE", "/v1/team/invitations/"+id2, e.adminA, nil)
	if st, _ := e.do("GET", "/v1/invitations/"+link2[strings.LastIndex(link2, "/")+1:], "", nil); st != 410 {
		t.Fatalf("revoked invitation still valid: %d", st)
	}

	// Two-step verification.
	if st, _ := e.do("POST", "/v1/me/2fa/begin", e.adminA, nil); st != 403 {
		t.Fatalf("an API key enrolled in two-step verification: %d", st)
	}
	b := e.must(200, "POST", "/v1/me/2fa/begin", tok, nil)
	secret := b["secret"].(string)
	if !strings.HasPrefix(b["uri"].(string), "otpauth://totp/") {
		t.Fatalf("uri = %v", b["uri"])
	}
	if st, _ := e.do("POST", "/v1/me/2fa/confirm", tok, m{"code": "000000"}); st != 400 {
		t.Fatalf("wrong code confirmed: %d", st)
	}
	if st, _ := login(""); st != 200 {
		t.Fatal("two-step must not be required before it is confirmed")
	}
	now := time.Now()
	conf := e.must(200, "POST", "/v1/me/2fa/confirm", tok, m{"code": appCode(secret, now)})
	codes := conf["recoveryCodes"].([]any)
	if len(codes) != 8 {
		t.Fatalf("recovery codes = %d", len(codes))
	}
	if st, b := login(""); st != 401 || b["code"] != "totp_required" {
		t.Fatalf("login without code = %d %v", st, b)
	}
	if st, b := login("123456"); st != 401 || b["code"] != "invalid_code" {
		t.Fatalf("login with a wrong code = %d %v", st, b)
	}
	if st, _ := login(appCode(secret, now)); st != 401 {
		t.Fatal("the code used to confirm was accepted again (replay)")
	}
	if st, _ := login(appCode(secret, now.Add(30*time.Second))); st != 200 {
		t.Fatal("a fresh app code was rejected")
	}
	rc := codes[0].(string)
	if st, _ := login(strings.ToLower(rc)); st != 200 {
		t.Fatal("a recovery code was rejected")
	}
	if st, _ := login(rc); st != 401 {
		t.Fatal("a recovery code worked twice")
	}
	var plain int
	_ = e.db.System.QueryRow(context.Background(), `SELECT count(*) FROM members WHERE encode(totp_secret,'escape') LIKE '%'||$1||'%' OR recovery_codes::text LIKE '%'||$2||'%'`, secret, rc).Scan(&plain)
	if plain != 0 {
		t.Fatal("two-step secrets are stored in clear")
	}
	if me := e.must(200, "GET", "/v1/me", tok, nil); me["twoStep"] != true {
		t.Fatalf("me.twoStep = %v", me["twoStep"])
	}

	// Sessions: list, sign out the others, and a revoked session stops working.
	list := e.must(200, "GET", "/v1/me/sessions", tok, nil)["items"].([]any)
	if len(list) < 3 {
		t.Fatalf("sessions listed = %d", len(list))
	}
	current := 0
	for _, x := range list {
		if x.(map[string]any)["current"] == true {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("%d sessions marked as current", current)
	}
	_, other := login(codes[1].(string))
	otherTok := other["token"].(string)
	e.must(200, "POST", "/v1/me/sessions/revoke-others", tok, nil)
	if st, _ := e.do("GET", "/v1/me", otherTok, nil); st != 401 {
		t.Fatalf("a revoked session still works: %d", st)
	}
	if st, _ := e.do("GET", "/v1/me", tok, nil); st != 200 {
		t.Fatal("revoking the others signed out the current session")
	}
	// Signing devices out is a security action: it is on the record.
	if items := e.must(200, "GET", "/v1/ops/audit?action=session.revoke_others", e.adminA, nil)["items"].([]any); len(items) != 1 {
		t.Fatalf("signing out the other sessions left %d audit entries", len(items))
	}
	// Turning it off needs the password; an admin can reset someone locked out.
	if st, _ := e.do("POST", "/v1/me/2fa/disable", tok, m{"password": "equivocada"}); st != 403 {
		t.Fatalf("disable with a wrong password = %d", st)
	}
	members := e.must(200, "GET", "/v1/team/members", e.adminA, nil)["items"].([]any)
	var memberID string
	for _, x := range members {
		if mm := x.(map[string]any); mm["email"] == "nuevo@example.com" {
			memberID = mm["id"].(string)
			if mm["twoStep"] != true {
				t.Fatal("team list does not show who has two-step verification")
			}
		}
	}
	if st, _ := e.do("POST", "/v1/team/members/"+memberID+"/2fa/reset", e.adminB, nil); st == 204 {
		t.Fatal("another store reset two-step verification")
	}
	e.must(204, "POST", "/v1/team/members/"+memberID+"/2fa/reset", e.adminA, nil)
	if st, _ := login(""); st != 200 {
		t.Fatal("login after an admin reset still asks for a code")
	}

	// Custom roles are private to the store.
	e.must(200, "PUT", "/v1/team/roles/atencion", e.adminA, m{"name": "Atención a clientes", "permissions": m{"orders": "edit", "customers": "read"}})
	if st, _ := e.do("PUT", "/v1/team/roles/Mal ID", e.adminA, m{"name": "x", "permissions": m{}}); st != 400 {
		t.Fatalf("bad role id accepted: %d", st)
	}
	if roles := e.must(200, "GET", "/v1/team/roles", e.adminB, nil)["items"].([]any); len(roles) != 0 {
		t.Fatalf("roles leaked to another store: %v", roles)
	}
	e.must(204, "DELETE", "/v1/team/roles/atencion", e.adminA, nil)
}

// One person, several stores: the list only shows stores where the password
// is really theirs, so nobody learns where an email belongs.
func TestStoresForOnePerson(t *testing.T) {
	e := setup(t)
	mk := func(key, email, pass string) {
		t.Helper()
		e.must(201, "POST", "/v1/team/members", key, m{"email": email, "name": "Paula", "password": pass, "permissions": m{"catalog": "read"}})
	}
	mk(e.adminA, "paula@example.com", "Clave-de-paula-2026")
	mk(e.adminB, "paula@example.com", "Clave-de-paula-2026")
	// An attacker who owns store B adds the victim's email with a password of their own.
	mk(e.adminA, "victima@example.com", "Clave-real-de-victima")
	mk(e.adminB, "victima@example.com", "Clave-del-atacante-1")
	stores := func(email, pass string) (int, []any) {
		st, b := e.do("POST", "/v1/auth/stores", "", m{"email": email, "password": pass})
		list, _ := b["stores"].([]any)
		return st, list
	}
	if st, l := stores("PAULA@example.com", "Clave-de-paula-2026"); st != 200 || len(l) != 2 {
		t.Fatalf("stores of a person in two stores = %d %v", st, l)
	}
	if st, l := stores("victima@example.com", "Clave-del-atacante-1"); st != 200 || len(l) != 1 || l[0].(map[string]any)["id"] != "store-b" {
		t.Fatalf("the attacker's password revealed other stores: %d %v", st, l)
	}
	if st, _ := stores("paula@example.com", "equivocada-equivocada"); st != 401 {
		t.Fatalf("wrong password = %d", st)
	}
	if st, _ := stores("nadie@example.com", "equivocada-equivocada"); st != 401 {
		t.Fatalf("unknown email = %d", st)
	}
	// Each store keeps its own session: entering B does not open A.
	_, lg := e.do("POST", "/v1/auth/login", "", m{"store": "store-b", "email": "victima@example.com", "password": "Clave-del-atacante-1"})
	if st, _ := e.do("POST", "/v1/auth/login", "", m{"store": "store-a", "email": "victima@example.com", "password": "Clave-del-atacante-1"}); st != 401 {
		t.Fatalf("the attacker's password opened the victim's store: %d", st)
	}
	me := e.must(200, "GET", "/v1/me", lg["token"].(string), nil)
	if me["tenant"] != "store-b" {
		t.Fatalf("session tenant = %v", me["tenant"])
	}
}
