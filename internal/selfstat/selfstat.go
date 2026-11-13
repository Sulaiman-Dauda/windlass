// Package selfstat measures the Windlass process itself, for the panel's
// health tile: its resident memory now and once a minute for the last hour.
// The ring is 60 numbers, so keeping it costs nothing worth counting against
// the idle memory budget.
package selfstat

import (
	"context"
	"sync"
	"time"
)

// Sample is the panel's resident memory at one moment.
type Sample struct {
	Time     time.Time `json:"time"`
	RSSBytes uint64    `json:"rss_bytes"`
}

// Ring keeps the last Size samples, oldest first.
type Ring struct {
	mu      sync.Mutex
	samples []Sample
	next    int
	full    bool
}

// Size is one hour of samples taken once a minute.
const Size = 60

// Interval is the time between samples.
const Interval = time.Minute

// NewRing returns an empty ring.
func NewRing() *Ring { return &Ring{samples: make([]Sample, Size)} }

// Add stores a sample, replacing the oldest once the ring is full.
func (r *Ring) Add(s Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.samples[r.next] = s
	r.next = (r.next + 1) % Size
	if r.next == 0 {
		r.full = true
	}
}

// Samples returns the stored samples, oldest first.
func (r *Ring) Samples() []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Never nil, so the JSON is [] rather than null before the first sample.
	out := make([]Sample, 0, Size)
	if !r.full {
		return append(out, r.samples[:r.next]...)
	}
	return append(append(out, r.samples[r.next:]...), r.samples[:r.next]...)
}

// Run samples into r every Interval until ctx ends, starting now.
func (r *Ring) Run(ctx context.Context) {
	sample := func() {
		if rss, ok := RSS(); ok {
			r.Add(Sample{Time: time.Now().UTC(), RSSBytes: rss})
		}
	}
	sample()
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample()
		}
	}
}
