// Package httpclient fetches album pages, retrying rate-limited, failing and
// server-side responses with exponentially growing pauses.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
)

// maxBodyBytes guards against pathological responses.
const maxBodyBytes = 16 << 20

// StatusError marks a non-2xx response.
type StatusError struct {
	URL    string
	Status int
	Reason string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: %d %s", e.URL, e.Status, e.Reason)
}

// Client wraps http.Client with retry pacing and the shared throttle.
type Client struct {
	http     *http.Client
	cfg      *config.Config
	log      *slog.Logger
	throttle *backoff.Throttle
}

// New builds a Client; connections are pooled per host according to the
// configured in-flight limit.
func New(cfg *config.Config, log *slog.Logger, throttle *backoff.Throttle) *Client {
	return &Client{
		http: &http.Client{
			Timeout: cfg.HTTPTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        cfg.MaxConcurrentRequests,
				MaxIdleConnsPerHost: cfg.MaxConcurrentRequests,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		cfg:      cfg,
		log:      log,
		throttle: throttle,
	}
}

// Fetch downloads url and returns the response body. Network errors, 429 and
// 5xx responses are retried up to cfg.Retries times with exponentially growing
// delays; every 429 additionally widens the global throttle so the pauses
// between all future requests grow until the server recovers.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		body, retryAfter, err := c.once(ctx, url)
		switch {
		case err == nil:
			c.throttle.Ease()
			return body, nil
		case !retryable(err) || attempt >= c.cfg.Retries:
			return nil, err
		}

		if rateLimited(err) {
			c.throttle.Penalty()
		}

		delay := backoff.Delay(c.cfg.RetryBaseDelay, c.cfg.RetryMaxDelay, attempt, retryAfter)
		c.log.Debug("retrying", "url", url, "attempt", attempt+1, "error", err, "delay", delay)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// once performs a single GET and classifies the outcome.
func (c *Client) once(ctx context.Context, url string) (body []byte, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Drain a bit of the body so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, parseRetryAfter(resp), &StatusError{
			URL:    url,
			Status: resp.StatusCode,
			Reason: http.StatusText(resp.StatusCode),
		}
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, 0, err
	}
	return body, 0, nil
}

func retryable(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status == http.StatusTooManyRequests || se.Status >= 500
	}
	return true // network-level failure, incl. per-request timeout
}

func rateLimited(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusTooManyRequests
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0 // HTTP-date form is rare; exponential delay takes over
}
