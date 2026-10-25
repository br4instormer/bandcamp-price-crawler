package httpclient

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
)

func testConfig(retries int) *config.Config {
	return &config.Config{
		Workers:               1,
		MaxConcurrentRequests: 1,
		Retries:               retries,
		RetryBaseDelay:        time.Millisecond,
		RetryMaxDelay:         5 * time.Millisecond,
		HTTPTimeout:           2 * time.Second,
		UserAgent:             "test-agent",
	}
}

func newClient(cfg *config.Config, th *backoff.Throttle) *Client {
	return New(cfg, slog.New(slog.DiscardHandler), th)
}

func TestFetchRetriesOn429(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, "page body")
	}))
	defer srv.Close()

	th := backoff.NewThrottle(backoff.MaxMultiplier)
	c := newClient(testConfig(5), th)

	body, err := c.Fetch(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != "page body" {
		t.Errorf("body = %q, want %q", body, "page body")
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3 (two 429 + success)", got)
	}
	if got := th.Multiplier(); got <= 1 {
		t.Errorf("throttle multiplier = %v, want > 1 after 429s", got)
	}
}

func TestFetchSendsUserAgent(t *testing.T) {
	var ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.Header.Get("User-Agent"))
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c := newClient(testConfig(0), backoff.NewThrottle(backoff.MaxMultiplier))
	if _, err := c.Fetch(t.Context(), srv.URL); err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := ua.Load(); got != "test-agent" {
		t.Errorf("User-Agent = %v, want %q", got, "test-agent")
	}
}

func TestFetchFailsFastOn404(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	th := backoff.NewThrottle(backoff.MaxMultiplier)
	c := newClient(testConfig(5), th)

	_, err := c.Fetch(t.Context(), srv.URL)
	if err == nil {
		t.Fatal("Fetch() succeeded, want status error")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusNotFound {
		t.Errorf("error = %v, want 404 StatusError", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d, want 1 (404 is not retried)", got)
	}
}

func TestFetchGivesUpAfterRetries(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newClient(testConfig(2), backoff.NewThrottle(backoff.MaxMultiplier))

	_, err := c.Fetch(t.Context(), srv.URL)
	if err == nil {
		t.Fatal("Fetch() succeeded, want exhaustion error")
	}
	if got := hits.Load(); got != 3 { // first try + 2 retries
		t.Errorf("hits = %d, want 3", got)
	}
}
