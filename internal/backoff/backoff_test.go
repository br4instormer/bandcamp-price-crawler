package backoff

import (
	"math"
	"testing"
	"time"
)

func TestDelayGrowsExponentially(t *testing.T) {
	base, max := 10*time.Millisecond, time.Hour

	for attempt, want := range map[int]time.Duration{
		0: 10 * time.Millisecond,
		1: 20 * time.Millisecond,
		2: 40 * time.Millisecond,
		3: 80 * time.Millisecond,
	} {
		got := Delay(base, max, attempt, 0)
		assertWithin(t, got, want)
	}
}

func TestDelayCappedAtMax(t *testing.T) {
	got := Delay(time.Second, 2*time.Second, 30, 0)
	if got < 1600*time.Millisecond || got > 2400*time.Millisecond {
		t.Errorf("delay = %s, want ~2s (cap ± 20%% jitter)", got)
	}
}

func TestDelayRetryAfterOverrides(t *testing.T) {
	retryAfter := 5 * time.Second
	if got := Delay(time.Second, time.Minute, 0, retryAfter); got != retryAfter {
		t.Errorf("delay = %s, want %s verbatim", got, retryAfter)
	}
	// A smaller Retry-After is ignored in favour of the exponential estimate.
	got := Delay(time.Second, time.Minute, 4, 100*time.Millisecond)
	assertWithin(t, got, 16*time.Second)
}

func assertWithin(t *testing.T, got, want time.Duration) {
	t.Helper()
	lo := time.Duration(float64(want) * 0.79)
	hi := time.Duration(float64(want) * 1.21)
	if got < lo || got > hi {
		t.Errorf("delay = %s, want %s ± 20%% jitter", got, want)
	}
}

func TestThrottlePenaltyAndEase(t *testing.T) {
	th := NewThrottle(16)

	for range 5 {
		th.Penalty()
	}
	if got := th.Multiplier(); got != 16 {
		t.Errorf("multiplier after 5 penalties = %v, want capped at 16", got)
	}

	for got := th.Multiplier(); got > 1.01; got = th.Multiplier() {
		th.Ease()
	}
	if got := th.Multiplier(); got != 1 {
		t.Errorf("multiplier never reached 1, got %v", got)
	}
}

func TestThrottleScale(t *testing.T) {
	th := NewThrottle(16)
	if got := th.Scale(time.Second); got != time.Second {
		t.Errorf("scale at rest = %s, want 1s", got)
	}
	th.Penalty()
	if got := th.Scale(time.Second); got != 2*time.Second {
		t.Errorf("scale after penalty = %s, want 2s", got)
	}
}

func TestThrottleConcurrent(t *testing.T) {
	th := NewThrottle(16)
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 1000 {
				th.Penalty()
				th.Ease()
				_ = th.Scale(time.Millisecond)
				_ = math.Floor(th.Multiplier())
			}
		}()
	}
	for range 8 {
		<-done
	}
}
