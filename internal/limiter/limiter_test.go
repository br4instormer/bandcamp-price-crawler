package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterCapsConcurrency(t *testing.T) {
	const (
		holders = 10
		limit   = 2
	)
	l := New(limit)

	var inFlight, maxInFlight atomic.Int64
	var wg sync.WaitGroup
	for range holders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire() error = %v", err)
				return
			}
			defer l.Release()

			cur := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if cur <= old || maxInFlight.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got > limit {
		t.Errorf("max in-flight = %d, want <= %d", got, limit)
	}
}

func TestLimiterAcquireRespectsContext(t *testing.T) {
	l := New(1)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer l.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.Acquire(ctx); err == nil {
		t.Error("Acquire() on exhausted limiter succeeded, want context error")
	}
}
