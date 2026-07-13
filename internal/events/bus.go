// Package events is the in-process pub/sub bus behind SSE streams and the
// audit log. It is deliberately tiny: no persistence, no delivery guarantees
// beyond best-effort fan-out to live subscribers. Durable event history
// (deployment_events) is written by the deploy pipeline, not the bus.
package events

import (
	"sync"
	"time"
)

// Event is a structured platform event (principle 15).
type Event struct {
	// Topic routes subscriptions, e.g. "deployment", "project", "system".
	Topic string `json:"topic"`
	// Type is the specific action, e.g. "deployment.step", "project.created".
	Type string `json:"type"`
	// Resource identifies the subject, e.g. a project name.
	Resource string    `json:"resource,omitempty"`
	Time     time.Time `json:"time"`
	// Data is a small JSON-serializable payload.
	Data any `json:"data,omitempty"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[int]subscriber
	next int
}

type subscriber struct {
	ch     chan Event
	topics map[string]bool // nil = all topics
}

func NewBus() *Bus {
	return &Bus{subs: map[int]subscriber{}}
}

// Publish delivers e to matching subscribers. Slow subscribers lose events
// rather than block the publisher (their UIs recover via query refetch).
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if s.topics != nil && !s.topics[e.Topic] {
			continue
		}
		select {
		case s.ch <- e:
		default:
		}
	}
}

// Subscribe returns a buffered event channel and a cancel function. topics
// filters delivery; empty means all topics.
func (b *Bus) Subscribe(topics ...string) (<-chan Event, func()) {
	var filter map[string]bool
	if len(topics) > 0 {
		filter = make(map[string]bool, len(topics))
		for _, t := range topics {
			filter[t] = true
		}
	}

	b.mu.Lock()
	id := b.next
	b.next++
	ch := make(chan Event, 64)
	b.subs[id] = subscriber{ch: ch, topics: filter}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		if s, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(s.ch)
		}
		b.mu.Unlock()
	}
}
