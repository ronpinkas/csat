package httpx

import (
	"net/http"
	"sync"
	"time"
)

// Limiter is a simple per-key token-bucket rate limiter (keyed by client IP).
type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	rate        float64 // tokens added per second
	burst       float64
	lastCleanup time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter builds a limiter allowing `perMinute` requests/min with the given
// burst capacity.
func NewLimiter(perMinute, burst float64) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		rate:        perMinute / 60.0,
		burst:       burst,
		lastCleanup: time.Now(),
	}
}

// Allow reports whether a request for key may proceed, consuming a token.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	l.cleanup(now)

	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// cleanup evicts idle buckets occasionally to bound memory. Caller holds lock.
func (l *Limiter) cleanup(now time.Time) {
	if now.Sub(l.lastCleanup) < 10*time.Minute {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
	l.lastCleanup = now
}

// Middleware rate-limits by client IP, returning 429 when exhausted.
func (l *Limiter) Middleware(trustProxy bool, trusted []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(ClientIP(r, trustProxy, trusted)) {
				http.Error(w, "Too many requests. Please slow down and try again.", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
