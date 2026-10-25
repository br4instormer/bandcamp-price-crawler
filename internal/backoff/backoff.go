// Package backoff computes retry pauses and keeps the global request pacing
// that reacts to rate limiting (HTTP 429).
package backoff

import (
	"math"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

// jitterFraction is the ± share applied to a computed delay.
const jitterFraction = 0.2 // Delay returns the pause before the next retry: base halved by the attempt
// count grows exponentially (base * 2^attempt), is capped at max and gets ±20%
// jitter so concurrent workers do not retry in lockstep. A larger Retry-After
// from the server overrides our estimate verbatim, without jitter.
func Delay(base, max time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	d := expDelay(base, max, attempt)
	if retryAfter > d {
		return retryAfter
	}
	return jitter(d)
}

func expDelay(base, max time.Duration, attempt int) time.Duration {
	if attempt < 0 || attempt > 62 {
		return max
	}
	if d := base << attempt; d > 0 && d < max {
		return d
	}
	return max
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := float64(d) * jitterFraction
	return time.Duration(float64(d) - span + rand.Float64()*2*span)
}

// Throttle is the global multiplier applied to the random pre-request pause.
// Every 429 doubles it up to Max; every success eases it back towards 1, so
// pauses between requests grow while the server is unhappy and shrink again
// when it recovers. It is safe for concurrent use.
type Throttle struct {
	mult atomic.Uint64 // float64 bits, see multStore/multLoad
	max  float64
}

func (t *Throttle) multStore(v float64) { t.mult.Store(math.Float64bits(v)) }
func (t *Throttle) multLoad() float64   { return math.Float64frombits(t.mult.Load()) }

// MaxMultiplier bounds how far the Throttle may stretch a pause.
const MaxMultiplier = 16

// NewThrottle returns a Throttle starting at multiplier 1.
func NewThrottle(max float64) *Throttle {
	t := &Throttle{max: max}
	t.multStore(1)
	return t
}

// Penalty doubles the multiplier after a rate-limited response.
func (t *Throttle) Penalty() {
	t.multStore(math.Min(t.multLoad()*2, t.max))
}

// Ease relaxes the multiplier after a successful response.
func (t *Throttle) Ease() {
	t.multStore(math.Max(t.multLoad()*0.9, 1))
}

// Scale stretches a delay by the current multiplier.
func (t *Throttle) Scale(d time.Duration) time.Duration {
	return time.Duration(float64(d) * t.multLoad())
}

// Multiplier exposes the current multiplier (for logs and tests).
func (t *Throttle) Multiplier() float64 {
	return t.multLoad()
}
