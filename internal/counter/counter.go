// Package counter accumulates download-state statistics across workers.
package counter

import (
	"sync/atomic"

	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
)

// Counter counts how many parsed albums fall into each cost state.
// It is safe for concurrent use.
type Counter struct {
	items [cost.States]atomic.Int64
}

// Add records one album with the given state.
func (c *Counter) Add(state cost.State) {
	c.items[state].Add(1)
}

// Get returns how many albums were recorded for the state.
func (c *Counter) Get(state cost.State) int64 {
	return c.items[state].Load()
}
