package auth

import (
	"testing"
	"time"
)

func TestClientRateLimiter_BurstThenBlockThenRefill(t *testing.T) {
	l := NewClientRateLimiter(10, 5) // 10/s, burst 5
	clock := time.Unix(1000, 0)
	l.now = func() time.Time { return clock }

	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d inside the burst was rejected", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait <= 0 {
		t.Fatalf("6th request should be rejected with a wait, got ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("a different client must not be affected")
	}
	clock = clock.Add(200 * time.Millisecond) // refills 2 tokens
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token should have refilled")
	}
}

func TestClientRateLimiter_DisabledAllowsEverything(t *testing.T) {
	l := NewClientRateLimiter(0, 0)
	for i := 0; i < 1000; i++ {
		if ok, _ := l.Allow("x"); !ok {
			t.Fatal("disabled limiter rejected a request")
		}
	}
}

func TestClientRateLimiter_MemoryIsBounded(t *testing.T) {
	l := NewClientRateLimiter(1, 1)
	clock := time.Unix(1000, 0)
	l.now = func() time.Time { return clock }
	for i := 0; i < maxTrackedClients+50; i++ {
		l.Allow(string(rune(i)) + "-" + time.Duration(i).String())
	}
	if len(l.buckets) > maxTrackedClients {
		t.Fatalf("tracked clients grew past the cap: %d", len(l.buckets))
	}
}
