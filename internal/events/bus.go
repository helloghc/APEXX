// Package events is an in-process publish/subscribe bus. Changes to stock,
// prices or catalog are published as events so caches, search and the
// storefront update immediately instead of waiting for a re-index
// (audit findings H1 and H9). In production this interface is backed by
// NATS JetStream; the in-process version keeps the monolith dependency-free.
package events

import (
	"sync"
	"time"
)

// Event is a domain change notification.
type Event struct {
	Topic string    `json:"topic"`
	Key   string    `json:"key"`
	At    time.Time `json:"at"`
	Data  any       `json:"data,omitempty"`
}

// Handler receives events synchronously, in publish order per topic.
type Handler func(Event)

// Bus fans events out to subscribers.
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]Handler
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{subs: map[string][]Handler{}} }

// Subscribe registers h for topic. Topic "*" receives everything.
func (b *Bus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	b.subs[topic] = append(b.subs[topic], h)
	b.mu.Unlock()
}

// Publish delivers e to subscribers of its topic and of "*".
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b.mu.RLock()
	hs := append(append([]Handler(nil), b.subs[e.Topic]...), b.subs["*"]...)
	b.mu.RUnlock()
	for _, h := range hs {
		h(e)
	}
}
