package mcp

import (
	"sync"
	"time"
)

// limiter is a fixed-window counter per key. The MCP specification requires
// servers to rate limit tool calls; this bounds a runaway agent loop, not a
// determined attacker, who would hit the REST API directly.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	counts map[string]*windowCount
}

type windowCount struct {
	start time.Time
	n     int
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, now: time.Now, counts: map[string]*windowCount{}}
}

// allow counts one call for key and reports whether it is within the limit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.counts) > 1024 {
		for k, c := range l.counts {
			if now.Sub(c.start) >= l.window {
				delete(l.counts, k)
			}
		}
	}
	c, ok := l.counts[key]
	if !ok || now.Sub(c.start) >= l.window {
		l.counts[key] = &windowCount{start: now, n: 1}
		return true
	}
	if c.n >= l.limit {
		return false
	}
	c.n++
	return true
}
