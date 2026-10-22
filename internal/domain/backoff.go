package domain

import (
	"math"
	"math/rand"
	"time"
)

type BackoffPolicy struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
	RandSrc   *rand.Rand
}

func NewBackoffPolicy(base, max time.Duration, r *rand.Rand) *BackoffPolicy {
	return &BackoffPolicy{
		BaseDelay: base,
		MaxDelay:  max,
		RandSrc:   r,
	}
}

// ComputeDelay computes exponential backoff with full jitter for attemptNumber (1-based).
// upper = min(cap, base * 2^(n-1))
// delay = uniform_random(0, upper)
func (p *BackoffPolicy) ComputeDelay(attemptNumber int) time.Duration {
	if attemptNumber < 1 {
		attemptNumber = 1
	}

	// Guard against integer overflow in exponent
	var upper time.Duration
	if attemptNumber > 30 {
		upper = p.MaxDelay
	} else {
		multiplier := math.Pow(2, float64(attemptNumber-1))
		calcDelay := time.Duration(float64(p.BaseDelay) * multiplier)
		if calcDelay > p.MaxDelay || calcDelay <= 0 {
			upper = p.MaxDelay
		} else {
			upper = calcDelay
		}
	}

	if upper <= 0 {
		return 0
	}

	var randomFraction float64
	if p.RandSrc != nil {
		randomFraction = p.RandSrc.Float64()
	} else {
		randomFraction = rand.Float64()
	}

	delay := time.Duration(float64(upper) * randomFraction)
	return delay
}
