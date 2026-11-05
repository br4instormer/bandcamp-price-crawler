package queue

import (
	"context"
	"sync"
)

// Gate pauses the crawl: every worker blocks on it before taking its next
// url, so no new requests start until the gate resumes. Urls already
// in-flight (fetched, retrying) finish as usual. The gate is safe for
// concurrent use; methods on a nil *Gate let everything through, which
// keeps the queue running when no dashboard is wired to pause it.
type Gate struct {
	mu     sync.Mutex
	paused bool
	resume chan struct{} // closed on resume; nil while the gate runs
}

// NewGate returns a running (unpaused) gate.
func NewGate() *Gate { return &Gate{} }

// Pause blocks the workers until Resume is called. Idempotent.
func (g *Gate) Pause() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.paused {
		g.paused = true
		g.resume = make(chan struct{})
	}
}

// Resume unblocks the workers; the gate can be paused again later.
// Idempotent.
func (g *Gate) Resume() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.paused {
		g.paused = false
		close(g.resume)
		g.resume = nil
	}
}

// Set pauses or resumes depending on paused. It is the callback shape the
// dashboard pushes through progress.WithPause.
func (g *Gate) Set(paused bool) {
	if paused {
		g.Pause()
	} else {
		g.Resume()
	}
}

// Paused reports whether the gate currently holds the workers back.
func (g *Gate) Paused() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused
}

// Wait blocks while the gate is paused and returns once the crawl resumes.
// A done ctx ends the wait early; the worker then hands its dequeued url to
// process, which releases it as a ctx-cancelled miss, so urls are never
// lost from the progress count.
func (g *Gate) Wait(ctx context.Context) {
	if g == nil {
		return
	}
	for {
		g.mu.Lock()
		resume := g.resume // nil while the gate is running
		g.mu.Unlock()
		if resume == nil {
			return
		}
		select {
		case <-resume:
			// it may have been paused again meanwhile: re-read
		case <-ctx.Done():
			return
		}
	}
}
