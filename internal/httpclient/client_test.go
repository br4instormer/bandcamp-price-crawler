package httpclient

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
	"github.com/br4instormer/bandcamp-price-crawler/internal/solver"
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
		ProxyErrorBudget:      5,
	}
}

func newClient(cfg *config.Config, th *backoff.Throttle) *Client {
	return New(cfg, slog.New(slog.DiscardHandler), th,
		proxypool.New(nil, cfg, slog.New(slog.DiscardHandler), nil), nil)
}

// albumPage is the smallest body Fetch accepts: it carries the one marker
// real album pages have and challenge placeholders lack.
const albumPage = `page body<script id="collectors-data" data-blob="{}"></script>`

// challengePage is a trimmed copy of the placeholder the CDN serves instead
// of the album: a 200 with the challenge script and no album markup.
const challengePage = `<!DOCTYPE html><html><head><title>Client Challenge</title></head>
<body><script src="/_fs-ch-1T1wmsGaOgGaSxcX/script.js?reload=true"></script></body></html>`

func TestFetchRetriesOn429(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, albumPage)
	}))
	defer srv.Close()

	th := backoff.NewThrottle(backoff.MaxMultiplier)
	c := newClient(testConfig(5), th)

	body, err := c.Fetch(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != albumPage {
		t.Errorf("body = %q, want %q", body, albumPage)
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
		_, _ = io.WriteString(w, albumPage)
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

func TestFetchReturnsOnContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // the exchange hangs until the test is over
	}))
	// the defers unwind in reverse: release first so the blocked handler
	// lets go of its connection before the server shuts down
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	c := newClient(testConfig(5), backoff.NewThrottle(backoff.MaxMultiplier))
	start := time.Now()
	if _, err := c.Fetch(ctx, srv.URL); !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch() error = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("Fetch() returned after %s, want immediately on cancel", d)
	}
}

func TestFetchFallsBackToDirectWhenProxyBlocked(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, albumPage)
	}))
	defer srv.Close()

	// a closed port: every attempt through the proxy fails at once
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadHost, deadPort, _ := net.SplitHostPort(dead.Addr().String())
	_ = dead.Close()

	cfg := testConfig(5)
	cfg.ProxyErrorBudget = 2
	log := slog.New(slog.DiscardHandler)
	pool := proxypool.New([]proxypool.Entry{{
		Raw: "http://" + net.JoinHostPort(deadHost, deadPort), Scheme: "http",
		Host: deadHost, Port: deadPort,
	}}, cfg, log, nil)
	c := New(cfg, log, backoff.NewThrottle(backoff.MaxMultiplier), pool, nil)

	body, err := c.Fetch(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != albumPage {
		t.Errorf("body = %q, want the direct answer", body)
	}
	if _, blocked := pool.Stats(); blocked != 1 {
		t.Errorf("blocked proxies = %d, want 1", blocked)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("direct hits = %d, want 1 (only after the proxy blocked)", got)
	}
}

// fakeSolver answers Fetch with a canned result, counting its calls.
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

func TestFetchWaitsOutChallengePage(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= 2 {
			_, _ = io.WriteString(w, challengePage)
			return
		}
		_, _ = io.WriteString(w, albumPage)
	}))
	defer srv.Close()

	th := backoff.NewThrottle(backoff.MaxMultiplier)
	c := newClient(testConfig(5), th)

	body, err := c.Fetch(t.Context(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(body) != albumPage {
		t.Errorf("body = %q, want the album page", body)
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3 (two placeholders + the real page)", got)
	}
	if got := th.Multiplier(); got != 1 {
		t.Errorf("throttle multiplier = %v, want 1: a placeholder is neither a 429 nor penalized", got)
	}
}

func TestFetchGivesUpOnChallengePlaceholder(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, challengePage)
	}))
	defer srv.Close()

	c := newClient(testConfig(2), backoff.NewThrottle(backoff.MaxMultiplier))

	_, err := c.Fetch(t.Context(), srv.URL)
	if got := hits.Load(); got != 3 {
		t.Errorf("hits = %d, want 3 (first try + 2 retries)", got)
	}
	var ce *ChallengeError
	if !errors.As(err, &ce) {
		t.Errorf("error = %v, want ChallengeError", err)
	}
}

func TestFetchEscalatesToSolverAndSticks(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, challengePage) // direct never answers with the real page
	}))
	defer srv.Close()

	solverHits := &fakeSolver{name: "fake", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return []byte(albumPage), nil
	}}
	log := slog.New(slog.DiscardHandler)
	cfg := testConfig(1)
	chain := solver.NewChain(log, solverHits)
	c := New(cfg, log, backoff.NewThrottle(backoff.MaxMultiplier),
		proxypool.New(nil, cfg, log, nil), chain)

	// the direct retries exhaust, then the solver takes over
	body, err := c.Fetch(t.Context(), srv.URL)
	if err != nil || string(body) != albumPage {
		t.Fatalf("Fetch() = %q, %v; want the solved album page", body, err)
	}
	directAfterFirst := hits.Load()
	if got := solverHits.calls.Load(); got != 1 {
		t.Errorf("solver calls = %d, want 1", got)
	}

	// the winner sticks to the route: the next fetch skips the direct
	// transport entirely
	if _, err := c.Fetch(t.Context(), srv.URL); err != nil {
		t.Fatalf("second Fetch() error = %v", err)
	}
	if got := hits.Load(); got != directAfterFirst {
		t.Errorf("direct hits = %d, want %d: the sticky solver answers", got, directAfterFirst)
	}
	if got := solverHits.calls.Load(); got != 2 {
		t.Errorf("solver calls = %d, want 2", got)
	}
}

// connectProxy spins an HTTP CONNECT tunnel so pool entries reach the target
// with a real proxy hop in the middle.
func connectProxy(t *testing.T) proxypool.Entry {
	t.Helper()
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		dst, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer dst.Close()
		src, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer src.Close()
		if _, err := src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
			return
		}
		go func() { _, _ = io.Copy(dst, buf); dst.Close() }() // client → target
		_, _ = io.Copy(src, dst)                              // target → client
	}))
	t.Cleanup(proxySrv.Close)

	host, port, _ := net.SplitHostPort(proxySrv.Listener.Addr().String())
	return proxypool.Entry{
		Raw:    "http://" + net.JoinHostPort(host, port),
		Scheme: "http", Host: host, Port: port,
	}
}

func TestProbeClassifiesEachProxy(t *testing.T) {
	// /ok answers the real page, /stub always the placeholder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			_, _ = io.WriteString(w, albumPage)
			return
		}
		_, _ = io.WriteString(w, challengePage)
	}))
	defer srv.Close()

	log := slog.New(slog.DiscardHandler)
	cfg := testConfig(2)
	entry := connectProxy(t)
	// two routes through the same tunnel: one probed clean, one challenged
	pool := proxypool.New([]proxypool.Entry{entry, entry}, cfg, log, nil)
	solverHits := &fakeSolver{name: "fake", fetch: func(_ context.Context, url string, _ proxypool.Entry) ([]byte, error) {
		return []byte(albumPage), nil
	}}
	c := New(cfg, log, backoff.NewThrottle(backoff.MaxMultiplier), pool, solver.NewChain(log, solverHits))

	c.Probe(t.Context(), []string{srv.URL + "/ok", srv.URL + "/stub"})

	// the clean route stays direct (no sticky solver), the challenged one
	// belongs to the solver now
	if got := c.modeOf(pool.Proxies()[0]); got != "" {
		t.Errorf("clean route mode = %q, want direct", got)
	}
	if got := c.modeOf(pool.Proxies()[1]); got != "fake" {
		t.Errorf("challenged route mode = %q, want fake", got)
	}
	if got := solverHits.calls.Load(); got != 1 {
		t.Errorf("solver calls = %d, want 1 (only the challenged route)", got)
	}
}

func TestProbeBlocksUnusableProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, challengePage)
	}))
	defer srv.Close()

	log := slog.New(slog.DiscardHandler)
	cfg := testConfig(2)
	// a closed port: the direct attempt fails at once
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadHost, deadPort, _ := net.SplitHostPort(dead.Addr().String())
	_ = dead.Close()

	failing := &fakeSolver{name: "fake", fetch: func(context.Context, string, proxypool.Entry) ([]byte, error) {
		return nil, errors.New("solver down")
	}}
	pool := proxypool.New([]proxypool.Entry{{
		Raw: "http://" + net.JoinHostPort(deadHost, deadPort), Scheme: "http",
		Host: deadHost, Port: deadPort,
	}}, cfg, log, nil)
	c := New(cfg, log, backoff.NewThrottle(backoff.MaxMultiplier), pool, solver.NewChain(log, failing))

	c.Probe(t.Context(), []string{srv.URL})

	if _, blocked := pool.Stats(); blocked != 1 {
		t.Errorf("blocked proxies = %d, want 1 (nothing could serve the probe)", blocked)
	}
}
