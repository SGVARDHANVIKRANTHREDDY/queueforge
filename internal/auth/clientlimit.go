package auth

import (
	"math"
	"sync"
	"time"
)

// ClientRateLimiter is a per-client token bucket for ALL API traffic (authenticated or not). It
// complements FailureLimiter (which only counts failed logins) and the per-tenant enqueue token
// bucket (which only limits enqueues, and only after authentication and a database round trip): a
// flood of valid-key reads or cheap requests is rejected here, in memory, before any database work.
//
// State is per process: with N API replicas a client can get up to N times the rate. Use the
// reverse proxy for a global limit. It uses the process clock on purpose (abuse control, not queue
// logic; it must keep working while the database is slow).
type ClientRateLimiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*clientBucket
	now     func() time.Time
	purged  time.Time
}

type clientBucket struct {
	tokens float64
	last   time.Time
}

const maxTrackedClients = 100000

// NewClientRateLimiter returns a limiter allowing ratePerSecond sustained requests per client with
// bursts up to burst. A non-positive rate or burst yields a limiter that allows everything.
func NewClientRateLimiter(ratePerSecond float64, burst int) *ClientRateLimiter {
	return &ClientRateLimiter{
		rate: ratePerSecond, burst: float64(burst),
		buckets: map[string]*clientBucket{}, now: time.Now,
	}
}

// Enabled reports whether the limiter actually limits anything.
func (l *ClientRateLimiter) Enabled() bool { return l.rate > 0 && l.burst >= 1 }

// Allow consumes one token for key. When it returns false, retryAfter says how long until a token
// is available.
func (l *ClientRateLimiter) Allow(key string) (bool, time.Duration) {
	if !l.Enabled() {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.purgeLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &clientBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	if wait < 10*time.Millisecond {
		wait = 10 * time.Millisecond
	}
	return false, wait
}

// purgeLocked drops clients whose bucket would be full again (idle), bounding memory. If an attack
// with many spoofed addresses still exceeds the cap, the table is reset: failing open here is
// better than exhausting memory.
func (l *ClientRateLimiter) purgeLocked(now time.Time) {
	fullAfter := time.Duration(l.burst / l.rate * float64(time.Second))
	if now.Sub(l.purged) < time.Minute && len(l.buckets) < maxTrackedClients {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) >= fullAfter {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= maxTrackedClients {
		l.buckets = map[string]*clientBucket{}
	}
	l.purged = now
}
