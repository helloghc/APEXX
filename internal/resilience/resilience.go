// Package resilience keeps one store's problem from becoming everyone's:
// panic recovery per request, a concurrency bulkhead per tenant, circuit
// breakers for external providers, and a supervisor that restarts background
// workers with backoff. Process-level restarts are handled by Docker/systemd
// and node-level ones by the GCP managed instance group (see deploy/).
package resilience

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

// Counters exposed as metrics.
var (
	Panics           atomic.Int64
	BulkheadRejects  atomic.Int64
	WorkerRestarts   atomic.Int64
	BreakerOpenTotal atomic.Int64
)

// Recover turns a panic in a handler into a 500 problem response.
func Recover(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				Panics.Add(1)
				log.Error("panic", "path", r.URL.Path, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"type":"https://docs.apexx.dev/errors/internal","title":"Internal Server Error","status":500,"code":"internal"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Bulkhead limits concurrent requests per key (tenant).
type Bulkhead struct {
	Max int
	mu  sync.Mutex
	in  map[string]int
}

// NewBulkhead creates a bulkhead with max in-flight requests per key.
func NewBulkhead(max int) *Bulkhead { return &Bulkhead{Max: max, in: map[string]int{}} }

// Acquire reserves a slot for key; release must be called when done.
func (b *Bulkhead) Acquire(key string) (release func(), ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.in[key] >= b.Max {
		BulkheadRejects.Add(1)
		return nil, false
	}
	b.in[key]++
	return func() {
		b.mu.Lock()
		b.in[key]--
		if b.in[key] == 0 {
			delete(b.in, key)
		}
		b.mu.Unlock()
	}, true
}

// ErrOpen is returned while a breaker is open.
var ErrOpen = errors.New("resilience: circuit open")

// Breaker opens after Threshold consecutive failures and lets one trial call
// through after Cooldown.
type Breaker struct {
	Threshold int
	Cooldown  time.Duration
	mu        sync.Mutex
	fails     int
	openUntil time.Time
}

// Do runs fn unless the breaker is open.
func (b *Breaker) Do(fn func() error) error {
	b.mu.Lock()
	if time.Now().Before(b.openUntil) {
		b.mu.Unlock()
		return ErrOpen
	}
	b.mu.Unlock()
	err := fn()
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		b.fails++
		if b.fails >= b.Threshold {
			b.openUntil = time.Now().Add(b.Cooldown)
			b.fails = 0
			BreakerOpenTotal.Add(1)
		}
		return err
	}
	b.fails = 0
	return nil
}

// Breakers keeps one breaker per key (tenant/provider).
type Breakers struct {
	Threshold int
	Cooldown  time.Duration
	mu        sync.Mutex
	m         map[string]*Breaker
}

// Get returns the breaker for key.
func (bs *Breakers) Get(key string) *Breaker {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.m == nil {
		bs.m = map[string]*Breaker{}
	}
	b := bs.m[key]
	if b == nil {
		b = &Breaker{Threshold: bs.Threshold, Cooldown: bs.Cooldown}
		bs.m[key] = b
	}
	return b
}

// Supervise runs fn until ctx ends, restarting it after a panic or error with
// exponential backoff (1s → 30s). A run longer than a minute resets the backoff.
func Supervise(ctx context.Context, log *slog.Logger, name string, fn func(context.Context) error) {
	go func() {
		backoff := time.Second
		for ctx.Err() == nil {
			start := time.Now()
			err := func() (err error) {
				defer func() {
					if v := recover(); v != nil {
						Panics.Add(1)
						err = fmt.Errorf("panic: %v\n%s", v, debug.Stack())
					}
				}()
				return fn(ctx)
			}()
			if ctx.Err() != nil {
				return
			}
			WorkerRestarts.Add(1)
			if time.Since(start) > time.Minute {
				backoff = time.Second
			}
			log.Error("worker stopped, restarting", "worker", name, "err", err, "in", backoff.String())
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
		}
	}()
}

// Every runs fn on an interval under supervision.
func Every(ctx context.Context, log *slog.Logger, name string, interval time.Duration, fn func(context.Context) error) {
	Supervise(ctx, log, name, func(ctx context.Context) error {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			start := time.Now()
			err := fn(ctx)
			recordRun(name, interval, start, err)
			if err != nil {
				log.Warn("periodic task failed", "worker", name, "err", err)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-t.C:
			}
		}
	})
}
