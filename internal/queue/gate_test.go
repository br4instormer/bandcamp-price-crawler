package queue

import (
	"context"
	"testing"
	"time"
)

func TestGateBlocksAndReleasesWorkers(t *testing.T) {
	g := NewGate()
	if g.Paused() {
		t.Fatal("a new gate must be running")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waited := make(chan struct{})
	go func() {
		g.Wait(ctx)
		close(waited)
	}()

	g.Pause()
	select {
	case <-waited:
		t.Fatal("Wait returned while the gate was paused")
	case <-time.After(50 * time.Millisecond):
	}
	g.Pause() // repeated pause must not deadlock or reset the wait

	g.Resume()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait stayed blocked after resume")
	}
	if g.Paused() {
		t.Error("the gate must run after resume")
	}
	g.Resume() // repeated resume must not panic
}

func TestGateSetTogglesPause(t *testing.T) {
	g := NewGate()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	g.Set(true)
	if !g.Paused() {
		t.Error("Set(true) must pause the gate")
	}
	select {
	case <-newWaitDone(g, ctx):
		t.Fatal("Wait returned while paused")
	case <-time.After(50 * time.Millisecond):
	}

	g.Set(false)
	if g.Paused() {
		t.Error("Set(false) must resume the gate")
	}
	select {
	case <-newWaitDone(g, ctx):
	case <-time.After(time.Second):
		t.Fatal("Wait stayed blocked after Set(false)")
	}
}

func TestGateWaitBlocksUntilCtxDone(t *testing.T) {
	g := NewGate()
	g.Pause()

	ctx, cancel := context.WithCancel(t.Context())
	waited := make(chan struct{})
	go func() {
		g.Wait(ctx)
		close(waited)
	}()

	cancel()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait must unblock when ctx is cancelled")
	}
	g.Resume() // the gate must stay reusable afterwards
}

func TestNilGateIsInert(t *testing.T) {
	var g *Gate
	if g.Paused() {
		t.Error("a nil gate must never report paused")
	}
	g.Pause()
	g.Resume()
	g.Set(true)
	g.Set(false) // every method must be nil-safe, nothing left to hold

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		g.Wait(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Error("Wait on a nil gate must return immediately")
	}
}

// newWaitDone runs g.Wait in the background for the toggling tests.
func newWaitDone(g *Gate, ctx context.Context) chan struct{} {
	done := make(chan struct{})
	go func() {
		g.Wait(ctx)
		close(done)
	}()
	return done
}
