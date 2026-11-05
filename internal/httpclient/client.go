// Package httpclient fetches album pages, retrying rate-limited, failing and
// server-side responses with exponentially growing pauses, waiting out
// client-challenge placeholder pages, and escalating to the challenge solver
// chain when the placeholder survives the direct wait.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/htmlparser"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
	"github.com/br4instormer/bandcamp-price-crawler/internal/solver"
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

// ErrNoProxy marks a fetch refused in proxy-only mode: every pool proxy is
// blocked and the direct connection does not exist. The blockage cannot lift
// before the run restarts, so Fetch returns it without retrying.
var ErrNoProxy = errors.New("no working proxy left (PROXY_ONLY)")

// ChallengeError marks the client-challenge placeholder: a 200 response
// whose body is the challenge script instead of album markup. To the proxy
// and the throttle it is an ordinary success — the server answered — but
// there is nothing to parse, so Fetch turns it into a retryable error and
// waits for the real page.
type ChallengeError struct {
	URL string
}

func (e *ChallengeError) Error() string {
	return e.URL + ": client challenge placeholder page"
}

// Client fetches pages through the proxy pool with retry pacing and the
// shared throttle, escalating to the solver chain when a route keeps
// answering with the challenge placeholder. A proxy the probe or a previous
// escalation handed to a solver skips the direct transport from then on. In
// proxy-only mode the pool never serves the direct connection: once every
// proxy is blocked the fetches fail with ErrNoProxy instead.
type Client struct {
	pool     *proxypool.Pool
	cfg      *config.Config
	log      *slog.Logger
	throttle *backoff.Throttle
	chain    *solver.Chain

	mu    sync.Mutex
	modes map[*proxypool.Proxy]string // proxy → sticky solver name; absent = direct
}

// New builds a Client. chain may be nil — fetches then never escalate and a
// surviving placeholder fails the url. Every attempt takes the next proxy
// from the pool, which also serves the direct connection when proxies are
// absent or all blocked.
func New(cfg *config.Config, log *slog.Logger, throttle *backoff.Throttle, pool *proxypool.Pool, chain *solver.Chain) *Client {
	return &Client{
		pool:     pool,
		cfg:      cfg,
		log:      log,
		throttle: throttle,
		chain:    chain,
		modes:    map[*proxypool.Proxy]string{},
	}
}

// Probe classifies every configured proxy once, in parallel: one direct
// attempt at a url of its own from the list, then the solver chain. Proxies
// answering with the real page keep the direct transport (4.2); proxies only
// a solver can serve get that solver as their sticky mode (4.1); proxies
// nothing can serve are blocked. The outcomes land in the logs.
func (c *Client) Probe(ctx context.Context, urls []string) {
	proxies := c.pool.Proxies()
	if len(proxies) == 0 || len(urls) == 0 {
		return
	}
	c.log.Info("probing proxies", "count", len(proxies))

	var wg sync.WaitGroup
	for i, proxy := range proxies {
		url := urls[i%len(urls)] // every proxy gets its own page from the list
		wg.Add(1)
		go func(proxy *proxypool.Proxy, url string) {
			defer wg.Done()

			// one direct attempt, no retries: a placeholder is a
			// classification here, not a failure
			body, _, err := c.once(ctx, proxy, url)
			if err == nil && htmlparser.Ready(string(body)) {
				c.log.Info("proxy serves the real page directly", "proxy", proxy.Label(), "probe", url)
				return
			}
			if ctx.Err() != nil {
				return
			}

			_, name, err := c.chain.Solve(ctx, url, proxy.Entry(), "")
			switch {
			case err == nil:
				c.setMode(proxy, name)
				c.log.Info("proxy needs a solver", "proxy", proxy.Label(), "solver", name, "probe", url)
			default:
				c.pool.BlockProxy(proxy)
				c.log.Warn("proxy is unusable: direct and all solvers failed", "proxy", proxy.Label(), "error", err)
			}
		}(proxy, url)
	}
	wg.Wait()
}

// Close releases the solver chain's resources — the rod browsers. A Client
// is run-scoped; main closes it when a run ends.
func (c *Client) Close() {
	c.chain.Close()
}

// modeOf answers the route's sticky solver, "" for the direct transport.
func (c *Client) modeOf(proxy *proxypool.Proxy) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.modes[proxy]
}

func (c *Client) setMode(proxy *proxypool.Proxy, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modes[proxy] = name
}

// Fetch downloads url and returns the response body. Network errors, 429 and
// 5xx responses are retried up to cfg.Retries times with exponentially growing
// delays; every 429 additionally widens the global throttle so the pauses
// between all future requests grow until the server recovers. A 200 that is
// really the client-challenge placeholder is retried the same way: it carries
// no album markup yet (htmlparser.Ready), so waiting it out belongs here,
// before parsing — without penalizing the proxy or the throttle. A
// placeholder that survives the retries escalates to the solver chain over
// the same route, and the winning solver becomes the route's sticky mode.
// Each attempt takes the next proxy in the pool's round-robin.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		proxy := c.pool.Next()
		if proxy == nil {
			// proxy-only mode with every proxy blocked: there is no direct
			// connection to fall back on, and nothing can unblock a proxy
			// before the run restarts
			return nil, ErrNoProxy
		}

		// a route a solver owns: direct attempts would only collect
		// placeholders again
		if name := c.modeOf(proxy); name != "" {
			return c.solve(ctx, url, proxy, name, nil)
		}

		body, retryAfter, err := c.once(ctx, proxy, url)
		c.pool.Report(proxy, healthy(err))

		// The challenge placeholder arrives as a 200, so only the body
		// gives it away; the exchange itself was a healthy success.
		if err == nil && !htmlparser.Ready(string(body)) {
			body, retryAfter, err = nil, 0, &ChallengeError{URL: url}
		}

		switch {
		case err == nil:
			c.throttle.Ease()
			return body, nil
		case !retryable(err) || attempt >= c.cfg.Retries:
			var challenge *ChallengeError
			if errors.As(err, &challenge) {
				// the placeholder survived the direct wait: hand the url to
				// the solver chain through the same route; with no solvers
				// configured the challenge error itself is the answer
				return c.solve(ctx, url, proxy, "", err)
			}
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

// solve fetches url through the solver chain over the proxy's route, starting
// from the route's sticky solver when it has one. The winner sticks to the
// route; a chain defeat fails the url (no false page ever leaves here). When
// the chain has no solvers, fallback — the direct attempt's own error —
// surfaces instead.
func (c *Client) solve(ctx context.Context, url string, proxy *proxypool.Proxy, name string, fallback error) ([]byte, error) {
	body, winner, err := c.chain.Solve(ctx, url, proxy.Entry(), name)
	if err != nil {
		if fallback != nil && c.chain.Empty() {
			return nil, fallback
		}
		return nil, err
	}
	if winner != name {
		c.setMode(proxy, winner)
		c.log.Info("solver took over the route", "proxy", proxy.Label(), "solver", winner)
	}
	return body, nil
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
