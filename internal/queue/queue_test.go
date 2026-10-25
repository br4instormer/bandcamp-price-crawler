package queue

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
	"github.com/br4instormer/bandcamp-price-crawler/internal/counter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/httpclient"
	"github.com/br4instormer/bandcamp-price-crawler/internal/limiter"
)

const freePage = `<div class="buyItem digital">
	<div class="compound-button"><a class="download-link">Free Download</a></div>
</div>`

const paidPage = `<div class="buyItem digital">
	<div class="nobreak"><span class="base-text-color">$7.50</span></div>
</div>`

// countingServer tracks how many requests it serves at once.
func countingServer(t *testing.T, body string, maxSeen *atomic.Int64, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	var inFlight atomic.Int64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		cur := inFlight.Add(1)
		for {
			old := maxSeen.Load()
			if cur <= old || maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = io.WriteString(w, body)
	}))
}

func TestRunProcessesAllURLs(t *testing.T) {
	var hits atomic.Int64
	srv := countingServer(t, paidPage, &atomic.Int64{}, &hits)
	defer srv.Close()

	const n = 12
	urls := make([]string, n)
	for i := range urls {
		urls[i] = srv.URL
	}

	cfg := &config.Config{
		Workers:               4,
		MaxConcurrentRequests: 4,
		Retries:               2,
		RetryBaseDelay:        time.Millisecond,
		RetryMaxDelay:         5 * time.Millisecond,
		HTTPTimeout:           2 * time.Second,
		UserAgent:             "test-agent",
	}
	log := slog.New(slog.DiscardHandler)
	th := backoff.NewThrottle(backoff.MaxMultiplier)
	cnt := &counter.Counter{}

	Run(t.Context(), urls, Options{
		Workers:  cfg.Workers,
		DelayMin: time.Millisecond,
		DelayMax: 2 * time.Millisecond,
		Limiter:  limiter.New(cfg.MaxConcurrentRequests),
		Client:   httpclient.New(cfg, log, th),
		Throttle: th,
		Counter:  cnt,
		Log:      log,
	})

	if got := hits.Load(); got != n {
		t.Errorf("served = %d, want %d", got, n)
	}
	if got := cnt.Get(cost.RegularDownload); got != n {
		t.Errorf("RegularDownload count = %d, want %d", got, n)
	}
}

func TestRunRespectsRequestLimit(t *testing.T) {
	const limit = 2

	var hits, maxSeen atomic.Int64
	srv := countingServer(t, freePage, &maxSeen, &hits)
	defer srv.Close()

	urls := make([]string, 12)
	for i := range urls {
		urls[i] = srv.URL
	}

	cfg := &config.Config{
		Workers:               6, // pool wider than the in-flight limit
		MaxConcurrentRequests: limit,
		Retries:               2,
		RetryBaseDelay:        time.Millisecond,
		RetryMaxDelay:         5 * time.Millisecond,
		HTTPTimeout:           2 * time.Second,
		UserAgent:             "test-agent",
	}
	log := slog.New(slog.DiscardHandler)
	th := backoff.NewThrottle(backoff.MaxMultiplier)
	cnt := &counter.Counter{}

	Run(t.Context(), urls, Options{
		Workers:  cfg.Workers,
		DelayMin: time.Millisecond,
		DelayMax: 2 * time.Millisecond,
		Limiter:  limiter.New(limit),
		Client:   httpclient.New(cfg, log, th),
		Throttle: th,
		Counter:  cnt,
		Log:      log,
	})

	if got := maxSeen.Load(); got > limit {
		t.Errorf("max concurrent requests = %d, want <= %d", got, limit)
	}
	if got := hits.Load(); got != int64(len(urls)) {
		t.Errorf("served = %d, want %d", got, len(urls))
	}
}
