// Package httpclient fetches album pages, retrying rate-limited, failing and
// server-side responses with exponentially growing pauses.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// StatusError marks a non-2xx response.
type StatusError struct {
	URL    string
	Status int
	Reason string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: %d %s", e.URL, e.Status, e.Reason)
}

// Client fetches pages through the proxy pool with retry pacing and the
// shared throttle.
type Client struct {
	pool     *proxypool.Pool
	cfg      *config.Config
	log      *slog.Logger
	throttle *backoff.Throttle
}

// New builds a Client. Every attempt takes the next proxy from the pool,
// which also serves the direct connection when proxies are absent or all
// blocked.
func New(cfg *config.Config, log *slog.Logger, throttle *backoff.Throttle, pool *proxypool.Pool) *Client {
	return &Client{pool: pool, cfg: cfg, log: log, throttle: throttle}
}

// Fetch downloads url and returns the response body. Network errors, 429 and
// 5xx responses are retried up to cfg.Retries times with exponentially growing
// delays; every 429 additionally widens the global throttle so the pauses
// between all future requests grow until the server recovers. Each attempt
// takes the next proxy in the pool's round-robin.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		proxy := c.pool.Next()
		body, retryAfter, err := c.once(ctx, proxy, url)
		c.pool.Report(proxy, healthy(err))
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

// once performs a single GET through proxy and classifies the outcome.
func (c *Client) once(ctx context.Context, proxy *proxypool.Proxy, url string) (body []byte, retryAfter time.Duration, err error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	// A cancelled context abandons the in-flight exchange: the background
	// exchange keeps writing into these buffers until its own timeouts end
	// it, so they must never go back to the sync pools.
	abandoned := false
	defer func() {
		if !abandoned {
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(resp)
		}
	}()

	req.Header.SetMethod(fasthttp.MethodGet)
	req.SetRequestURI(url)
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	// fasthttp has no context-aware Do: the exchange runs in the background
	// and the select returns as soon as it ends or the caller cancelled.
	errCh := make(chan error, 1)
	go func() { errCh <- proxy.Do(req, resp) }()
	select {
	case err = <-errCh:
	case <-ctx.Done():
		abandoned = true
		return nil, 0, ctx.Err()
	}
	if err != nil {
		return nil, 0, err
	}

	status := resp.StatusCode()
	if status < 200 || status > 299 {
		return nil, parseRetryAfter(string(resp.Header.Peek("Retry-After"))), &StatusError{
			URL:    url,
			Status: status,
			Reason: fasthttp.StatusMessage(status),
		}
	}

	// Body lives in the response's own buffer: copy it out before the
	// deferred release hands the buffer to the next request.
	return append([]byte(nil), resp.Body()...), 0, nil
}

// healthy reports whether the proxy did its part: a success, or a status it
// merely delivered (an ordinary 404, say), keeps it in rotation, while
// transport failures, 429 — the proxy IP being filtered — and 5xx responses
// count toward its error budget.
func healthy(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Status < 500 && se.Status != http.StatusTooManyRequests
	}
	return err == nil
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

func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	return 0 // HTTP-date form is rare; exponential delay takes over
}
