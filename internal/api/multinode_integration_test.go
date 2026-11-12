package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// A background job runs on one node at a time: the lease holder renews it,
// hands it over at once on shutdown, and loses it when it dies (expiry).
func TestJobLeaseOneNodeAtATime(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	take := func(node string, ttl time.Duration) bool {
		ok, err := e.db.TakeLease(ctx, "mailer", node, ttl)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !take("a", time.Minute) || take("b", time.Minute) || !take("a", time.Minute) {
		t.Fatal("first node must hold and renew the lease, second must wait")
	}
	// Graceful shutdown of a: b takes over on its next tick.
	if err := e.db.ReleaseLeases(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if !take("b", 300*time.Millisecond) || take("a", time.Minute) {
		t.Fatal("released lease must go to the other node")
	}
	// b dies without releasing: a takes over once the lease expires.
	time.Sleep(400 * time.Millisecond)
	if !take("a", time.Minute) {
		t.Fatal("expired lease must be taken")
	}
	// Leases of different jobs are independent.
	if ok, _ := e.db.TakeLease(ctx, "invoicer", "b", time.Minute); !ok {
		t.Fatal("another job's lease is free")
	}
}

// The limit of failed sign-ins is the same whichever node answers: ten
// failures spread over two nodes block the eleventh on both.
func TestLoginLimitSharedByNodes(t *testing.T) {
	e := setup(t)
	nodes := []string{e.srv.URL, e.newSrv().URL}
	try := func(base string) int {
		b, _ := json.Marshal(map[string]string{"store": "store-a", "email": "nadie@a.example", "password": "incorrecta-123456"})
		resp, err := http.Post(base+"/v1/auth/login", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < 10; i++ {
		if c := try(nodes[i%2]); c != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, c)
		}
	}
	for _, n := range nodes {
		if c := try(n); c != http.StatusTooManyRequests {
			t.Fatalf("node %s after 10 failures: %d, want 429", n, c)
		}
	}
	// Only a hash of address and email is stored.
	var raw int
	if err := e.db.System.QueryRow(t.Context(), `SELECT count(*) FROM login_failures WHERE position('nadie' in encode(key_hash,'escape')) > 0`).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("plain email stored (%d, %v)", raw, err)
	}
}

// A node that is shutting down tells the load balancer to stop sending
// traffic (readyz 503) while it keeps serving what is in flight.
func TestDrainingNodeLeavesBalancer(t *testing.T) {
	e := setup(t)
	get := func(path string) int {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/readyz"); c != 200 {
		t.Fatalf("readyz before drain: %d", c)
	}
	e.server.Drain()
	if c := get("/readyz"); c != http.StatusServiceUnavailable {
		t.Fatalf("readyz while draining: %d", c)
	}
	if c := get("/healthz"); c != 200 {
		t.Fatalf("requests while draining: %d", c)
	}
}
