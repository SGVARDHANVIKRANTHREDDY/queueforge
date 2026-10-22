package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// FailureLimiter blocks a client address after too many failed authentication attempts inside a
// sliding window. It protects the API-key lookup (a hash plus an indexed query per guess) from
// brute force and cheap abuse. State is per process and in memory: with N API replicas an
// attacker gets up to N times the limit, which is still a hard cap; use the reverse proxy for a
// global limit if you need one. It deliberately uses the process clock: this is abuse control, not
// queue logic, and it must keep working while the database is slow.
type FailureLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
	now    func() time.Time
	last   time.Time // last purge
}

func NewFailureLimiter(max int, window time.Duration) *FailureLimiter {
	return &FailureLimiter{max: max, window: window, fails: map[string][]time.Time{}, now: time.Now}
}

func (l *FailureLimiter) prune(key string, now time.Time) []time.Time {
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

// Blocked reports whether the client is currently blocked and, if so, how long until it may retry.
func (l *FailureLimiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.prune(key, now)
	if len(f) < l.max {
		return false, 0
	}
	return true, l.window - now.Sub(f[len(f)-l.max])
}

// RecordFailure registers one failed attempt.
func (l *FailureLimiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.fails[key] = append(l.prune(key, now), now)
	if now.Sub(l.last) > l.window { // bound memory: occasionally drop idle clients
		for k := range l.fails {
			l.prune(k, now)
		}
		l.last = now
	}
}

// ClientKey identifies the caller. X-Forwarded-For is only honoured when trustForwarded is true,
// i.e. when the API is reachable exclusively through a reverse proxy you control; otherwise any
// client could dodge the limiter (or frame someone else) by forging the header.
func ClientKey(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
