package inventory

import (
	"sync"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/events"
)

func TestReserveNeverOversells(t *testing.T) {
	inv := New(nil)
	inv.Set("A", "w1", 14)
	inv.Set("A", "w2", 6)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if inv.Reserve(string(rune('a'+i%26))+string(rune(i)), "A", 1, time.Minute) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if ok != 20 {
		t.Fatalf("reserved %d units, want exactly 20", ok)
	}
	if a := inv.Available("A"); a != 0 {
		t.Fatalf("available %d, want 0", a)
	}
}

func TestReleaseAndExpiry(t *testing.T) {
	inv := New(nil)
	now := time.Unix(0, 0)
	inv.now = func() time.Time { return now }
	inv.Set("A", "w1", 5)
	if err := inv.Reserve("cart1", "A", 5, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := inv.Reserve("cart2", "A", 1, time.Minute); err != ErrInsufficient {
		t.Fatalf("want ErrInsufficient, got %v", err)
	}
	now = now.Add(2 * time.Minute)
	if a := inv.Available("A"); a != 5 {
		t.Fatalf("after expiry available %d, want 5", a)
	}
}

func TestEventsOnChange(t *testing.T) {
	bus := events.NewBus()
	var got []int64
	bus.Subscribe(TopicChanged, func(e events.Event) { got = append(got, e.Data.(Snapshot).Available) })
	inv := New(bus)
	inv.Set("A", "w1", 20)
	_ = inv.Reserve("c", "A", 6, time.Minute)
	if len(got) != 2 || got[1] != 14 {
		t.Fatalf("events %v, want [20 14]", got)
	}
}

func TestUnknownSKU(t *testing.T) {
	if _, ok := New(nil).Get("nope"); ok {
		t.Fatal("unknown SKU must report ok=false, not a silent zero")
	}
}
