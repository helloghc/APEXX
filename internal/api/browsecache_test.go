package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helloghc/apexx/internal/catalog"
)

func TestBrowseCacheOneComputationPerVersion(t *testing.T) {
	var c browseCache
	var calls atomic.Int32
	slow := func(total int) func() catalog.BrowseResult {
		return func() catalog.BrowseResult {
			calls.Add(1)
			time.Sleep(30 * time.Millisecond) // a big catalog
			return catalog.BrowseResult{Total: total}
		}
	}
	// Sixteen shoppers at once: one computation, everybody gets it.
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := c.browse("a", 1, "k", slow(1)); r.Total != 1 {
				t.Errorf("total = %d", r.Total)
			}
		}()
	}
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d computations for one version", n)
	}
	// Same version: reused.
	c.browse("a", 1, "k", slow(9))
	if calls.Load() != 1 {
		t.Fatal("recomputed without a change")
	}
	// New version of a costly result: the old one now, the new one right after.
	if r := c.browse("a", 2, "k", slow(2)); r.Total != 1 {
		t.Fatalf("stale answer = %d", r.Total)
	}
	deadline := time.Now().Add(time.Second)
	for c.browse("a", 2, "k", slow(2)).Total != 2 {
		if time.Now().After(deadline) {
			t.Fatal("background recomputation never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Other stores and other keys never share a result.
	if r := c.browse("b", 2, "k", func() catalog.BrowseResult { return catalog.BrowseResult{Total: 7} }); r.Total != 7 {
		t.Fatalf("store b got %d", r.Total)
	}
}

func TestBrowseCacheCheapResultIsAlwaysFresh(t *testing.T) {
	var c browseCache
	for v := uint64(1); v <= 3; v++ {
		got := c.browse("a", v, "k", func() catalog.BrowseResult { return catalog.BrowseResult{Total: int(v)} })
		if got.Total != int(v) {
			t.Fatalf("version %d answered %d", v, got.Total)
		}
	}
}

func TestBrowseCachePanicReleasesWaiters(t *testing.T) {
	var c browseCache
	func() {
		defer func() { _ = recover() }()
		c.browse("a", 1, "k", func() catalog.BrowseResult { panic("boom") })
	}()
	if r := c.browse("a", 1, "k2", func() catalog.BrowseResult { return catalog.BrowseResult{Total: 3} }); r.Total != 3 {
		t.Fatalf("after a panic = %d", r.Total)
	}
	// The key that panicked works again on the next try.
	done := make(chan catalog.BrowseResult, 1)
	go func() {
		done <- c.browse("a", 2, "k", func() catalog.BrowseResult { return catalog.BrowseResult{Total: 4} })
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a panic left the key locked")
	}
}

type fakeCatalog map[string]catalog.Product

func (f fakeCatalog) Get(id string) (catalog.Product, error) {
	p, ok := f[id]
	if !ok {
		return p, catalog.ErrNotFound
	}
	return p, nil
}

func TestLiveOnlyDropsGoneProductsWithoutTouchingTheCache(t *testing.T) {
	a, b, c := &catalog.Product{ID: "a"}, &catalog.Product{ID: "b"}, &catalog.Product{ID: "c"}
	cached := catalog.BrowseResult{Items: []*catalog.Product{a, b, c}, Offers: []catalog.Offer{{Price: 1}, {Price: 2}, {Price: 3}}, Total: 30}
	now := fakeCatalog{"a": {ID: "a", Status: "active"}, "c": {ID: "c", Status: "archived"}} // b deleted, c hidden
	got := liveOnly(now, cached)
	if len(got.Items) != 1 || got.Items[0].ID != "a" || got.Offers[0].Price != 1 || got.Total != 28 {
		t.Fatalf("live result = %+v", got)
	}
	if len(cached.Items) != 3 || cached.Items[1].ID != "b" || cached.Offers[2].Price != 3 || cached.Total != 30 {
		t.Fatalf("the cached result was modified: %+v", cached)
	}
	all := fakeCatalog{"a": {Status: "active"}, "b": {Status: "active"}, "c": {Status: "active"}}
	if r := liveOnly(all, cached); len(r.Items) != 3 {
		t.Fatalf("nothing gone, but got %d", len(r.Items))
	}
}
