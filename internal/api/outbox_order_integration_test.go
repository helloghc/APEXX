package api

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/helloghc/apexx/internal/store"
)

// Two writers at once: A writes its event first (smaller id) but commits
// after B. The store must still apply A's change. While A is open, B's event
// waits (positions follow commit order); once A ends, both are applied. Before the outbox had a
// commit-ordered position, the projector moved past A's id when it applied
// B, and A's change was lost for good (deleted products stayed on sale).
func TestOutboxLateCommitIsNotLost(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.drain()
	write := func(tx pgx.Tx, id, title string) error {
		if _, err := tx.Exec(ctx, `UPDATE products SET title=$3 WHERE tenant_id=$1 AND id=$2`, "store-a", id, title); err != nil {
			return err
		}
		return store.Emit(ctx, tx, "store-a", store.TopicProduct, id, nil)
	}
	emitted, release, done := make(chan struct{}), make(chan struct{}), make(chan error)
	go func() {
		done <- e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error {
			if err := write(tx, "P1", "Escrito primero, confirmado al final"); err != nil {
				return err
			}
			close(emitted)
			<-release
			return nil
		})
	}()
	var once bool
	finish := func() {
		if !once {
			once = true
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(finish)
	<-emitted
	if err := e.db.Tx(ctx, "store-a", func(tx pgx.Tx) error { return write(tx, "P2", "Escrito después, confirmado antes") }); err != nil {
		t.Fatal(err)
	}
	finish()
	e.drain()
	ta, _ := e.proj.Tenant("store-a")
	if p, _ := ta.Catalog.Get("P2"); p.Title != "Escrito después, confirmado antes" {
		t.Fatalf("B not applied: %q", p.Title)
	}
	if p, _ := ta.Catalog.Get("P1"); p.Title != "Escrito primero, confirmado al final" {
		t.Fatalf("the late commit was lost: P1 = %q", p.Title)
	}
	// Webhook feed: both events appear, with strictly growing positions. A
	// position is handed out only once no smaller one can appear later.
	evs, err := e.server.Webhooks.Feed(ctx, "store-a", 0, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string]int64{}
	var last int64
	for _, ev := range evs {
		if ev.Sequence <= last {
			t.Fatalf("feed not in order: %d after %d", ev.Sequence, last)
		}
		last = ev.Sequence
		pos[ev.Subject] = ev.Sequence
	}
	if pos["P1"] == 0 || pos["P2"] == 0 {
		t.Fatalf("feed lost an event: P1=%d P2=%d", pos["P1"], pos["P2"])
	}
}
