package api

import (
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/helloghc/apexx/internal/catalog"
)

// Browse cache: the catalog listing, a category, a search or the suggestions
// box with the same filters give the same result until the store changes.
// With 50 000 products, listing everything costs a few hundred milliseconds
// and sixteen shoppers at once used to wait seconds. Now one shopper
// computes it, everyone else asking for the same thing waits for that one
// result, and later visits reuse it.
//
// A result belongs to the tenant's projection version. When the version
// moves (a product, price, stock or rule changed), the next visit still gets
// the previous result while one recomputation runs in the background, so a
// change shows within the time of one computation and nobody waits for it.
// Results older than browseMaxAge are recomputed anyway: learned search
// boosts change outside the projection.

const (
	browseMaxAge     = 30 * time.Second
	browseMaxEntries = 4096
	// browseSyncBelow: a result that took less than this to compute is redone
	// in the request when it is out of date; a costlier one is served stale
	// while one recomputation runs in the background.
	browseSyncBelow = 20 * time.Millisecond
)

type browseEntry struct {
	mu      sync.Mutex
	ready   chan struct{} // closed once the first result is stored
	res     catalog.BrowseResult
	version uint64
	at      time.Time
	cost    time.Duration // how long the last computation took
	busy    bool
}

type browseCache struct {
	mu sync.Mutex
	m  map[string]*browseEntry
}

func (c *browseCache) entry(key string) *browseEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= browseMaxEntries {
		c.m = map[string]*browseEntry{} // simple bound: start over
	}
	e, ok := c.m[key]
	if !ok {
		e = &browseEntry{ready: make(chan struct{})}
		c.m[key] = e
	}
	return e
}

// browse returns the result for key at the tenant's current version,
// computing it at most once at a time per key.
func (c *browseCache) browse(tenant string, v uint64, key string, compute func() catalog.BrowseResult) catalog.BrowseResult {
	e := c.entry(tenant + "\x00" + key)
	// run stores a new result. If compute panics, the entry is released
	// (waiters get the previous or an empty result) and the panic goes on
	// only when the caller is waiting for it; a background run swallows it
	// so it cannot bring the process down.
	run := func(v uint64, background bool) {
		done := false
		defer func() {
			if done {
				return
			}
			rec := recover()
			e.mu.Lock()
			e.busy = false
			select {
			case <-e.ready:
			default:
				close(e.ready)
			}
			e.mu.Unlock()
			if rec != nil && !background {
				panic(rec)
			}
		}()
		start := time.Now()
		res := compute()
		e.mu.Lock()
		e.res, e.version, e.at, e.cost, e.busy = res, v, time.Now(), time.Since(start), false
		select {
		case <-e.ready:
		default:
			close(e.ready)
		}
		e.mu.Unlock()
		done = true
	}
	e.mu.Lock()
	select {
	case <-e.ready:
		if e.version == v && time.Since(e.at) < browseMaxAge {
			res := e.res
			e.mu.Unlock()
			return res
		}
		stale := e.res
		if !e.busy && e.cost < browseSyncBelow {
			// Cheap to redo (a small store): answer with the fresh result,
			// so whoever just saved a change sees it on the next page.
			e.busy = true
			e.mu.Unlock()
			run(v, false)
			e.mu.Lock()
			res := e.res
			e.mu.Unlock()
			return res
		}
		if !e.busy {
			e.busy = true
			go run(v, true)
		}
		e.mu.Unlock()
		return stale
	default:
	}
	if e.busy { // someone is computing the first result: wait for it
		e.mu.Unlock()
		<-e.ready
		e.mu.Lock()
		res := e.res
		e.mu.Unlock()
		return res
	}
	e.busy = true
	e.mu.Unlock()
	run(v, false)
	e.mu.Lock()
	res := e.res
	e.mu.Unlock()
	return res
}

// ruleKey fingerprints what the store's search rules and learning add to a
// query (synonyms, pins, hidden products, learned boosts). Learned boosts
// change outside the projection, so they go into the key: a new boost means
// a new result, never a stale one.
func ruleKey(q catalog.BrowseQuery) string {
	if len(q.Alt) == 0 && len(q.Pinned) == 0 && len(q.Exclude) == 0 && len(q.Boost) == 0 {
		return ""
	}
	h := fnv.New64a()
	w := func(s string) { _, _ = h.Write([]byte(s)); _, _ = h.Write([]byte{0}) }
	for _, a := range q.Alt {
		w("a" + a)
	}
	for _, p := range q.Pinned {
		w("p" + p)
	}
	ex := make([]string, 0, len(q.Exclude))
	for id, on := range q.Exclude {
		if on {
			ex = append(ex, id)
		}
	}
	sort.Strings(ex)
	for _, id := range ex {
		w("x" + id)
	}
	bs := make([]string, 0, len(q.Boost))
	for id, n := range q.Boost {
		bs = append(bs, id+"="+strconv.Itoa(n))
	}
	sort.Strings(bs)
	for _, b := range bs {
		w("b" + b)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}
