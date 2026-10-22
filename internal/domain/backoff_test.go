package domain_test

import (
	"math/rand"
	"testing"
	"time"

	"queueforge/internal/domain"
)

func TestBackoffPolicy_DeterministicDelay(t *testing.T) {
	base := 1 * time.Second
	max := 15 * time.Minute

	// Seeded PRNG for reproducible test
	r := rand.New(rand.NewSource(42))
	policy := domain.NewBackoffPolicy(base, max, r)

	for attempt := 1; attempt <= 5; attempt++ {
		delay := policy.ComputeDelay(attempt)

		// Upper bound should be min(max, base * 2^(attempt-1))
		multiplier := float64(int(1) << (attempt - 1))
		expectedUpper := time.Duration(float64(base) * multiplier)
		if expectedUpper > max {
			expectedUpper = max
		}

		if delay < 0 {
			t.Errorf("Attempt %d produced negative delay: %v", attempt, delay)
		}
		if delay > expectedUpper {
			t.Errorf("Attempt %d delay %v exceeded upper bound %v", attempt, delay, expectedUpper)
		}
	}
}

func TestBackoffPolicy_EdgeCases(t *testing.T) {
	policy := domain.NewBackoffPolicy(1*time.Second, 10*time.Second, rand.New(rand.NewSource(1)))

	// Attempt <= 0 should behave as attempt 1
	delay0 := policy.ComputeDelay(0)
	if delay0 < 0 || delay0 > 1*time.Second {
		t.Errorf("Attempt 0 returned unexpected delay: %v", delay0)
	}

	// Large attempt number should not overflow and stay <= max
	delayLarge := policy.ComputeDelay(100)
	if delayLarge < 0 || delayLarge > 10*time.Second {
		t.Errorf("Attempt 100 overflowed max delay: %v", delayLarge)
	}
}
