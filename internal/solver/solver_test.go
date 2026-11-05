package solver

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// fakeSolver answers with a canned result and counts its calls.
type fakeSolver struct {
	name  string
	calls atomic.Int64
	fetch func(ctx context.Context, url string, entry proxypool.Entry) ([]byte, error)
}

func (f *fakeSolver) Name() string { return f.name }

func (f *fakeSolver) Fetch(ctx context.Context, url string, entry proxypool.Entry) ([]byte, error) {
	f.calls.Add(1)
	return f.fetch(ctx, url, entry)
}

var readyHTML = []byte(`<script id="collectors-data" data-blob="{}"></script>`)

func TestChainReturnsFirstReadyPage(t *testing.T) {
	stub := &fakeSolver{name: "stub", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return []byte("<html>Client Challenge</html>"), nil // never ready
	}}
	good := &fakeSolver{name: "good", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return readyHTML, nil
	}}

	chain := NewChain(slog.New(slog.DiscardHandler), stub, good)
	body, name, err := chain.Solve(t.Context(), "http://x", proxypool.Entry{}, "")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if name != "good" {
		t.Errorf("winner = %q, want good", name)
	}
	if string(body) != string(readyHTML) {
		t.Errorf("body = %q, want the ready page", body)
	}
	if stub.calls.Load() != 1 || good.calls.Load() != 1 {
		t.Errorf("calls = stub %d good %d, want 1 and 1", stub.calls.Load(), good.calls.Load())
	}
}

func TestChainStartsFromStickySolver(t *testing.T) {
	first := &fakeSolver{name: "first", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return nil, errors.New("first failed")
	}}
	second := &fakeSolver{name: "second", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return readyHTML, nil
	}}
	chain := NewChain(slog.New(slog.DiscardHandler), first, second)

	// sticky "second" starts the chain there; a failure still wraps around
	_, name, err := chain.Solve(t.Context(), "http://x", proxypool.Entry{}, "second")
	if err != nil {
		t.Fatalf("Solve() error = %v", err)
	}
	if name != "second" {
		t.Errorf("winner = %q, want second", name)
	}
	if first.calls.Load() != 0 {
		t.Errorf("first was called %d times, want 0: sticky starts there", first.calls.Load())
	}

	// the sticky solver failing hands the turn to the rest of the chain
	first.fetch = func(context.Context, string, proxypool.Entry) ([]byte, error) { return readyHTML, nil }
	second.fetch = func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return nil, errors.New("second failed")
	}
	_, name, err = chain.Solve(t.Context(), "http://x", proxypool.Entry{}, "second")
	if err != nil {
		t.Fatalf("Solve() after failure error = %v", err)
	}
	if name != "first" {
		t.Errorf("winner = %q, want first after the wrap-around", name)
	}
}

func TestChainUnknownStartKeepsCanonicalOrder(t *testing.T) {
	first := &fakeSolver{name: "first", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return readyHTML, nil
	}}
	chain := NewChain(slog.New(slog.DiscardHandler), first)
	_, name, err := chain.Solve(t.Context(), "http://x", proxypool.Entry{}, "")
	if err != nil || name != "first" {
		t.Fatalf("Solve() = %q, %v; want first, nil", name, err)
	}
}

func TestChainWithoutSolversFails(t *testing.T) {
	chain := NewChain(slog.New(slog.DiscardHandler))
	if _, _, err := chain.Solve(t.Context(), "http://x", proxypool.Entry{}, ""); err == nil {
		t.Fatal("Solve() succeeded, want an error")
	}
	chain.Close() // nil-safety through Close
	var nilChain *Chain
	nilChain.Close()
}

func TestChainClosesClosableSolvers(t *testing.T) {
	closed := atomic.Bool{}
	closer := &fakeSolver{name: "closer", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return readyHTML, nil
	}}
	chain := NewChain(slog.New(slog.DiscardHandler), closer, closableFunc(func() { closed.Store(true) }))
	chain.Close()
	if !closed.Load() {
		t.Error("the closable solver was not closed")
	}
}

// closableFunc adapts a func into a Solver that only carries Close.
type closableFunc func()

func (f closableFunc) Name() string { return "closable" }
func (f closableFunc) Fetch(context.Context, string, proxypool.Entry) ([]byte, error) {
	return nil, errors.New("not used")
}
func (f closableFunc) Close() { f() }
