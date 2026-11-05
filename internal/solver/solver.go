// Package solver fetches pages through browser-grade engines that pass the
// client challenge the direct transport cannot: FlareSolverr first when
// configured, rod as the always-present fallback. Every solver takes the
// proxy route the crawler is on and returns the final HTML only when it
// carries the album markup.
package solver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/br4instormer/bandcamp-price-crawler/internal/htmlparser"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// Solver fetches one page's final HTML through a challenge-capable engine.
// The entry is the route the page must take: an empty one is the direct
// connection.
type Solver interface {
	Name() string
	Fetch(ctx context.Context, url string, entry proxypool.Entry) ([]byte, error)
}

// Chain runs the solvers in order and names the winner, so the caller can
// stick to it for the route.
type Chain struct {
	solvers []Solver
	log     *slog.Logger
}

// NewChain assembles the chain from the given solvers, usually a
// FlareSolverr (optional) followed by rod.
func NewChain(log *slog.Logger, solvers ...Solver) *Chain {
	return &Chain{solvers: solvers, log: log}
}

// Empty reports whether the chain has no solvers at all.
func (c *Chain) Empty() bool {
	return c == nil || len(c.solvers) == 0
}

// Solve tries the solvers in chain order — starting from start when it names
// one of them, a route's sticky solver — and returns the first success with
// the winning solver's name. Only pages carrying the album markup
// (htmlparser.Ready) count as success; everything else falls through to the
// next solver, and a chain defeat returns the last error.
func (c *Chain) Solve(ctx context.Context, url string, entry proxypool.Entry, start string) ([]byte, string, error) {
	if c == nil || len(c.solvers) == 0 {
		return nil, "", errors.New("no challenge solvers configured")
	}

	order := c.solvers
	for i, s := range c.solvers {
		if s.Name() == start {
			order = append(append([]Solver(nil), c.solvers[i:]...), c.solvers[:i]...)
			break
		}
	}

	var last error
	for _, s := range order {
		body, err := s.Fetch(ctx, url, entry)
		if err == nil && !htmlparser.Ready(string(body)) {
			err = errors.New("page without album markup")
		}
		if err == nil {
			return body, s.Name(), nil
		}
		c.log.Debug("solver failed", "solver", s.Name(), "url", url, "error", err)
		last = err
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
	}
	return nil, "", fmt.Errorf("%s: %w", url, last)
}

// Close releases the solvers' resources — the rod browsers. Safe on a nil
// chain.
func (c *Chain) Close() {
	if c == nil {
		return
	}
	for _, s := range c.solvers {
		if closer, ok := s.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}
