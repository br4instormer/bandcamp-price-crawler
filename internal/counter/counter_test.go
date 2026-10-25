package counter

import (
	"sync"
	"testing"

	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
)

func TestCounterAddGet(t *testing.T) {
	c := &Counter{}
	c.Add(cost.FreeDownload)
	c.Add(cost.FreeDownload)
	c.Add(cost.RegularDownload)

	for state, want := range map[cost.State]int64{
		cost.FreeDownload:      2,
		cost.RegularDownload:   1,
		cost.NotAbleToDownload: 0,
		cost.NameYourPrice:     0,
	} {
		if got := c.Get(state); got != want {
			t.Errorf("Get(%d) = %d, want %d", state, got, want)
		}
	}
}

func TestCounterConcurrent(t *testing.T) {
	c := &Counter{}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				c.Add(cost.FreeDownload)
			}
		}()
	}
	wg.Wait()

	if got := c.Get(cost.FreeDownload); got != 4000 {
		t.Errorf("Get(FreeDownload) = %d, want 4000", got)
	}
}
