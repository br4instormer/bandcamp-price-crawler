// Package limiter caps the number of HTTP requests in flight.
package limiter

import "context"

// Limiter is a counting semaphore. Acquire blocks until a slot is free or ctx
// is done; Release gives the slot back. It is safe for concurrent use.
type Limiter struct {
	slots chan struct{}
}

// New builds a Limiter allowing n simultaneous holders.
func New(n int) *Limiter {
	return &Limiter{slots: make(chan struct{}, n)}
}

// Acquire takes a slot, blocking while all slots are busy.
func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release returns a slot taken by a successful Acquire.
func (l *Limiter) Release() {
	<-l.slots
}
