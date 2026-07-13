package api

import (
	"net/http"
	"sync"
	"time"
)

// authRateLimiter is a small per-IP sliding-window limiter for credential
// endpoints. In-memory on purpose: limits reset on restart, which is fine
// for brute-force protection, and no dependency is needed.
type authRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	buckets map[string][]time.Time
	lastGC  time.Time
}

func newAuthRateLimiter(max int, window time.Duration) *authRateLimiter {
	return &authRateLimiter{
		window:  window,
		max:     max,
		buckets: map[string][]time.Time{},
		lastGC:  time.Now(),
	}
}

func (l *authRateLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Periodic GC keeps the map bounded.
	if now.Sub(l.lastGC) > 10*l.window {
		for k, times := range l.buckets {
			if len(times) == 0 || now.Sub(times[len(times)-1]) > l.window {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}

	times := l.buckets[ip]
	// Drop entries outside the window.
	keep := times[:0]
	for _, ts := range times {
		if now.Sub(ts) <= l.window {
			keep = append(keep, ts)
		}
	}
	if len(keep) >= l.max {
		l.buckets[ip] = keep
		return false
	}
	l.buckets[ip] = append(keep, now)
	return true
}

// limitAuth wraps credential handlers with the limiter.
func (a *API) limitAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.authLimiter.allow(remoteIP(r)) {
			a.Audit.Write(r.Context(), 0, "auth.rate_limited", "", "", remoteIP(r), nil)
			writeError(w, http.StatusTooManyRequests, "rate_limited",
				"too many attempts; try again in a minute")
			return
		}
		next(w, r)
	}
}
