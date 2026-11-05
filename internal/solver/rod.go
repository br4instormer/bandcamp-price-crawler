package solver

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/stealth"

	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/htmlparser"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

// solveWait bounds one page's challenge+navigation in the browser. It is
// independent of the fasthttp HTTP_TIMEOUT_MS: executing the challenge
// script and reloading legitimately takes seconds.
const solveWait = 60 * time.Second

// pollEvery paces the album-marker polling while the challenge works.
const pollEvery = 500 * time.Millisecond

// Rod solves the challenge with a real Chromium: one headless browser per
// proxy route, launched lazily, pages carrying the stealth scripts. The
// browser binary comes from CHROME_PATH, then a system chrome/chromium; only
// when neither exists does rod's managed browser download lazily, with the
// progress pushed to the dashboard status line.
type Rod struct {
	cfg    *config.Config
	log    *slog.Logger
	status func(string) // the dashboard status line; nil-safe

	lookPath  func() (string, bool)  // injectable for tests
	downloadF func() (string, error) // managed browser download; injectable for tests

	mu       sync.Mutex
	binOnce  sync.Once
	binPath  string
	binErr   error
	download chan struct{} // closed when the managed browser is ready or failed
	browsers map[string]*browserSlot
}

// NewRod builds the solver. status, when not nil, receives the managed
// browser download progress for the dashboard.
func NewRod(cfg *config.Config, log *slog.Logger, status func(string)) *Rod {
	return &Rod{
		cfg:      cfg,
		log:      log,
		status:   status,
		lookPath: launcher.LookPath,
		downloadF: func() (string, error) {
			b := launcher.NewBrowser()
			if status != nil {
				b.Logger = statusLogger{status: status}
			}
			return b.Get()
		},
		browsers: map[string]*browserSlot{},
	}
}

// Name identifies the solver in logs and sticky modes.
func (r *Rod) Name() string { return "rod" }

// browserSlot is one proxy route's browser; its pages are used one at a
// time, so a route never fights itself over tabs.
type browserSlot struct {
	mu      sync.Mutex
	browser *rod.Browser
}

// Preflight resolves the browser binary early: a system chrome/chromium is
// adopted, and when neither CHROME_PATH nor the system has one the managed
// browser starts downloading in the background, while the crawl keeps
// parsing the urls direct transport can still serve.
func (r *Rod) Preflight() {
	r.binOnce.Do(r.resolveBin)
}

// Close shuts every launched browser down. A solver is run-scoped: the
// client closes it when a run ends.
func (r *Rod) Close() {
	r.mu.Lock()
	slots := make([]*browserSlot, 0, len(r.browsers))
	for _, slot := range r.browsers {
		slots = append(slots, slot)
	}
	r.browsers = map[string]*browserSlot{}
	r.mu.Unlock()

	for _, slot := range slots {
		slot.mu.Lock()
		if slot.browser != nil {
			_ = slot.browser.Close()
			slot.browser = nil
		}
		slot.mu.Unlock()
	}
}

// Fetch opens url in the route's browser and waits for the album markup,
// which appears only after the challenge script has reloaded the page. A
// canceled context aborts at once, without launching or acquiring browsers.
func (r *Rod) Fetch(ctx context.Context, url string, entry proxypool.Entry) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err // an aborted crawl acquires no browsers
	}

	bin, err := r.bin(ctx)
	if err != nil {
		return nil, err
	}

	key := entry.Raw // "" is the direct route
	r.mu.Lock()
	slot := r.browsers[key]
	if slot == nil {
		slot = &browserSlot{}
		r.browsers[key] = slot
	}
	r.mu.Unlock()

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if err := ctx.Err(); err != nil {
		// the wait for the browser may have outlived the request: never
		// launch a browser for an aborted crawl
		return nil, err
	}
	if slot.browser == nil {
		slot.browser, err = r.launch(bin, entry)
		if err != nil {
			return nil, err
		}
	}

	html, err := r.solve(ctx, slot.browser, url)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr // the request aborted: the browser stays healthy
		}
		// a browser can die between fetches; drop it so the next attempt
		// launches a fresh one instead of reusing a corpse
		_ = slot.browser.Close()
		slot.browser = nil
		return nil, err
	}
	return []byte(html), nil
}

// resolveBin runs once: CHROME_PATH, then the system browser, then the lazy
// managed download. The binary fields are written here and by the download
// goroutine only under mu.
func (r *Rod) resolveBin() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cfg.ChromePath != "" {
		if _, err := os.Stat(r.cfg.ChromePath); err != nil {
			r.binErr = fmt.Errorf("CHROME_PATH %q: %w", r.cfg.ChromePath, err)
			return
		}
		r.binPath = r.cfg.ChromePath
		return
	}
	if path, ok := r.lookPath(); ok {
		r.log.Info("using the system browser", "path", path)
		r.binPath = path
		return
	}
	r.startDownload()
}

// bin answers the resolved binary, waiting out the managed download when one
// is running.
func (r *Rod) bin(ctx context.Context) (string, error) {
	r.binOnce.Do(r.resolveBin)

	r.mu.Lock()
	path, err, download := r.binPath, r.binErr, r.download
	r.mu.Unlock()
	if err != nil {
		return "", err
	}
	if path != "" {
		return path, nil
	}
	select {
	case <-download:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.binPath, r.binErr
}

// startDownload downloads rod's managed chromium in the background, streaming
// the progress to the dashboard status line. The once-guard means at most one
// download per process; resolveBin holds mu when it calls this.
func (r *Rod) startDownload() {
	r.log.Warn("no CHROME_PATH and no system browser: downloading the managed chromium")
	r.download = make(chan struct{})
	go func() {
		defer close(r.download)
		defer r.reportStatus("")

		path, err := r.downloadF()
		r.mu.Lock()
		if err != nil {
			r.binErr = err
		} else {
			r.binPath = path
		}
		r.mu.Unlock()

		if err != nil {
			r.log.Error("managed browser download failed", "error", err)
			return
		}
		r.log.Info("managed browser ready", "path", path)
	}()
}

// reportStatus updates the dashboard status line; the empty string clears it.
func (r *Rod) reportStatus(text string) {
	if r.status != nil {
		r.status(text)
	}
}

// launch starts one headless browser bound to the proxy route. An empty
// entry dials directly.
func (r *Rod) launch(bin string, entry proxypool.Entry) (*rod.Browser, error) {
	l := launcher.New().Bin(bin).Headless(true)
	if entry.Raw != "" {
		l = l.Proxy(browserProxy(entry))
	}
	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("rod: launch browser: %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		l.Kill()
		return nil, fmt.Errorf("rod: connect browser: %w", err)
	}
	return browser, nil
}

// browserProxy builds the --proxy-server value Chromium accepts, from the
// parsed entry: chrome knows socks5 but not the socks5h spelling (a local
// error surfaced as net::ERR_NO_SUPPORTED_PROXIES). Chromium also ignores
// credentials in this value — authenticated routes belong to FlareSolverr.
func browserProxy(entry proxypool.Entry) string {
	scheme := entry.Scheme
	if scheme == "socks5h" {
		scheme = "socks5"
	}
	return scheme + "://" + net.JoinHostPort(entry.Host, entry.Port)
}

// solve navigates the page and polls for the album marker: the challenge
// script sets its cookie and reloads, so the real page shows up mid-wait.
// The page is bound to the caller's ctx, so navigation and polls abort the
// moment the crawl is canceled.
func (r *Rod) solve(ctx context.Context, browser *rod.Browser, url string) (string, error) {
	page, err := stealth.Page(browser)
	if err != nil {
		return "", fmt.Errorf("rod: open page: %w", err)
	}
	page = page.Context(ctx)
	defer func() { _ = page.Close() }()

	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("rod: navigate: %w", err)
	}

	deadline := time.Now().Add(solveWait)
	for {
		has, _, err := page.Has(htmlparser.ReadySelector)
		if err == nil && has {
			html, err := page.HTML()
			if err == nil && htmlparser.Ready(html) {
				return html, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("rod: the album markup never appeared within %s", solveWait)
		}
		select {
		case <-time.After(pollEvery):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// statusLogger routes the managed browser download progress — fetchup's
// Println lines — into the dashboard status line.
type statusLogger struct{ status func(string) }

func (l statusLogger) Println(args ...interface{}) {
	if l.status == nil {
		return
	}
	line := strings.TrimSpace(fmt.Sprintln(args...))
	if line != "" {
		l.status("browser download: " + line)
	}
}

// compile-time checks that the solvers satisfy the contract
var (
	_ Solver = (*FlareSolverr)(nil)
	_ Solver = (*Rod)(nil)
)
