// Command crawler visits Bandcamp album pages listed in a file and prints the
// price of every product to stdout.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
	"github.com/br4instormer/bandcamp-price-crawler/internal/counter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/httpclient"
	"github.com/br4instormer/bandcamp-price-crawler/internal/limiter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/logger"
	"github.com/br4instormer/bandcamp-price-crawler/internal/progress"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
	"github.com/br4instormer/bandcamp-price-crawler/internal/queue"
	"github.com/br4instormer/bandcamp-price-crawler/internal/solver"

	"github.com/spf13/pflag"
	"golang.org/x/term"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cli := parseFlags()

	cfg, err := config.Load(cli.overrides)
	if err != nil {
		return err
	}

	urls, err := readURLs(cli.include)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// While the dashboard is live the logger must write through it
	// (progress.UI.LogWriter) instead of stdout. The dashboard keys cancel
	// the current run via WithCancel (both ctrl+c and r); the space pause
	// toggles the gate, workers block on it before taking the next url;
	// the r restart aborts the current run and the loop below starts over
	// from the first url.
	gate := queue.NewGate()
	var runCancel atomic.Pointer[context.CancelFunc]
	cancelRun := func() {
		if c := runCancel.Load(); c != nil {
			(*c)()
		}
	}
	ui := progress.New(cfg.Progress, os.Stdout, term.IsTerminal(int(os.Stdout.Fd())), len(urls),
		progress.WithCancel(cancelRun),
		progress.WithPause(gate.Set),
		progress.WithRestart(cancelRun),
	)
	log := logger.New(ui.LogWriter(), cfg.LogLevel)

	if len(urls) == 0 {
		log.Warn("No urls found in " + cli.include)
		ui.Stop()
		return nil
	}

	// challenge solvers: FlareSolverr first when configured, rod as the
	// always-present fallback. Preflight adopts a system browser or starts
	// the lazy managed download in the background while the crawl runs.
	rodSolver := solver.NewRod(cfg, log, ui.Status)
	var solvers []solver.Solver
	if cfg.FlareSolverrURL != "" {
		solvers = append(solvers, solver.NewFlareSolverr(cfg.FlareSolverrURL, cfg.FlareSolverrMaxTimeout, log))
	}
	solvers = append(solvers, rodSolver)
	chain := solver.NewChain(log, solvers...)
	defer chain.Close()
	rodSolver.Preflight()

	for {
		runCtx, cancel := context.WithCancel(ctx)
		runCancel.Store(&cancel)

		// a restarted crawl starts from a clean slate: rate limiting,
		// retries, statistics and proxy blocks are all run-scoped state.
		// The proxy list is re-read too, so a restart (r) picks up an
		// edited proxy file or .env value.
		proxyEntries := proxypool.Parse(cfg.HTTPProxies, log)
		throttle := backoff.NewThrottle(backoff.MaxMultiplier)
		cnt := &counter.Counter{}
		pool := proxypool.New(proxyEntries, cfg, log, ui.ProxyStats)
		client := httpclient.New(cfg, log, throttle, pool, chain)

		// an exit or a restart must not wait for in-flight parses: the
		// browsers die together with the run's context, so every rod fetch
		// in flight aborts at once and queued ones never launch. Close is
		// idempotent — the call after queue.Run below stays harmless.
		abortDone := make(chan struct{})
		go func() {
			select {
			case <-runCtx.Done():
				client.Close()
			case <-abortDone:
			}
		}()

		// the probe pins every proxy to its transport before the crawl:
		// direct where the real page answers, a solver where only one can.
		// Proxy-only mode refuses to crawl without proxies: an empty list
		// or a probe that every proxy failed means an error and no parsing
		// this run — the dashboard stays up and r restarts it as usual.
		refused := ""
		if cfg.ProxyOnly {
			switch {
			case len(proxyEntries) == 0:
				refused = "PROXY_ONLY: HTTP_PROXIES holds no usable proxy"
			default:
				client.Probe(runCtx, urls)
				if total, blocked := pool.Stats(); blocked == total {
					refused = "PROXY_ONLY: every proxy failed the startup probe"
				}
			}
		} else {
			client.Probe(runCtx, urls)
		}
		if refused != "" {
			log.Error(refused + " — not crawling; fix HTTP_PROXIES or turn PROXY_ONLY off")
			ui.Refused(refused)
		}

		if refused == "" {
			queue.Run(runCtx, urls, queue.Options{
				Workers:  cfg.Workers,
				DelayMin: cfg.DelayMin,
				DelayMax: cfg.DelayMax,
				OnlyFree: cli.free,
				Limiter:  limiter.New(cfg.MaxConcurrentRequests),
				Client:   client,
				Throttle: throttle,
				Counter:  cnt,
				Log:      log,
				Progress: ui,
				Gate:     gate,
			})
		}
		close(abortDone) // the run ended on its own: disarm the kill switch
		client.Close()   // the rod browsers are run-scoped state as well

		// r aborts the run either paused or after completion: erase what
		// it left behind and crawl the same urls from the start
		if ui.RestartPending() {
			gate.Resume() // the aborted run may have died paused
			ui.Reset()
			continue
		}
		if runCtx.Err() != nil {
			break // the user stopped the crawl
		}

		if refused == "" && cli.free && cnt.Get(cost.FreeDownload) == 0 {
			log.Warn("No free download albums at all!")
		}

		// do not terminate after the last url: keep the dashboard (and the
		// process) up until the user closes it or asks to restart; a refused
		// crawl waits here the same way, error on screen, restart key live.
		// a ctrl+c that cancelled the crawl took the self-terminating path
		// above
		if !ui.WaitUserExit() {
			break
		}
		gate.Resume()
		ui.Reset()
	}

	// stats of the last run: earlier ones were wiped by the restarts
	processed := ui.Processed()
	elapsed := ui.Elapsed().Round(time.Second) // pure crawling time
	ui.Stop()
	log.Info(fmt.Sprintf("Processed %d/%d urls in %s", processed, len(urls), elapsed))
	return nil
}

// cliOptions carries the parsed command line: the two legacy options plus
// the overrides layer of the config cascade — the values of flags actually
// passed, keyed by their environment-variable name.
type cliOptions struct {
	include   string
	free      bool
	overrides map[string]string
}

// msDuration is a pflag value accepting either a bare number of
// milliseconds — parity with the *_MS environment variables — or a Go
// duration like 500ms or 1m30s.
type msDuration time.Duration

func (d *msDuration) String() string { return time.Duration(*d).String() }
func (d *msDuration) Type() string   { return "duration" }

func (d *msDuration) Set(s string) error {
	if n, err := strconv.Atoi(s); err == nil {
		*d = msDuration(time.Duration(n) * time.Millisecond)
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("want milliseconds or a Go duration like 500ms, got %q", s)
	}
	*d = msDuration(v)
	return nil
}

// millis renders the value as plain milliseconds, the format the
// DELAY_MIN-style config parsers expect.
func (d *msDuration) millis() string {
	return strconv.Itoa(int(time.Duration(*d) / time.Millisecond))
}

// parseFlags parses the command line with pflag. Every environment-backed
// tunable gets a long flag whose usage names its env analogue, so --help
// doubles as the CLI-to-env mapping table; -h/--help prints that table and
// exits 0. Mutually exclusive sources never fight: flags actually passed
// land in overrides and win the config cascade (CLI > .env > env > default).
func parseFlags() cliOptions {
	fs := pflag.NewFlagSet(os.Args[0], pflag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: crawler [flags]\n\nFlags:")
		fmt.Fprint(os.Stderr, fs.FlagUsages())
	}

	include := fs.StringP("include", "i", "urls.txt", "File contains bandcamp urls")
	free := fs.BoolP("free", "f", false, "Show only free download albums")

	workers := fs.Int("workers", 5, "Queue workers (env: CONCURENT_DOWNLOADS)")
	msVar := func(name string, def time.Duration, usage string) *msDuration {
		d := msDuration(def)
		fs.Var(&d, name, usage)
		return &d
	}
	delayMin := msVar("delay-min", 300*time.Millisecond, "Min pause between requests (env: DELAY_MIN)")
	delayMax := msVar("delay-max", 700*time.Millisecond, "Max pause between requests (env: DELAY_MAX)")
	retries := fs.Int("retries", 50, "Retries per failed request (env: RETRIES)")
	retryBase := msVar("retry-base-delay", time.Second, "First exponential retry pause (env: RETRY_BASE_DELAY_MS)")
	retryMax := msVar("retry-max-delay", time.Minute, "Exponential retry pause ceiling (env: RETRY_MAX_DELAY_MS)")
	httpTimeout := msVar("http-timeout", 30*time.Second, "Per-request timeout (env: HTTP_TIMEOUT_MS)")
	userAgent := fs.String("user-agent", "", "Request signature; empty = built-in Chrome/65 one (env: USER_AGENT)")
	proxies := fs.String("proxies", "", "Comma-separated proxy list or a file with one per line; empty = direct (env: HTTP_PROXIES)")
	logLevel := fs.String("log-level", "info", "Minimum log level: debug|info|warn|error (env: LOG_LEVEL)")
	progress := fs.String("progress", "auto", "Live task table: auto|on|off (env: PROGRESS)")
	flareURL := fs.String("flaresolverr-url", "", "FlareSolverr base URL; empty = rod alone (env: FLARESOLVERR_URL)")
	flareTimeout := msVar("flaresolverr-timeout", 60*time.Second, "One FlareSolverr request bound (env: FLARESOLVERR_MAX_TIMEOUT_MS)")
	chromePath := fs.String("chrome-path", "", "chrome/chromium binary for rod; empty = autodetect (env: CHROME_PATH)")
	maxConcurrent := fs.Int("max-concurrent-requests", 0, "HTTP requests in flight; 0 = workers (env: MAX_CONCURRENT_REQUESTS)")
	proxyBudget := fs.Int("proxy-error-budget", 5, "Failed attempts through one proxy before the run restarts (env: PROXY_ERROR_BUDGET)")
	proxyOnly := fs.Bool("proxy-only", false, "Refuse to crawl without a working proxy (env: PROXY_ONLY)")
	help := fs.BoolP("help", "h", false, "Show this help and exit")

	fs.Parse(os.Args[1:]) // ExitOnError: a bad flag prints usage and exits 2

	if *help {
		fmt.Println("Usage: crawler [flags]\n\nFlags:")
		fmt.Print(fs.FlagUsages())
		os.Exit(0)
	}

	// only flags the user actually passed enter the cascade, serialized the
	// way the config parsers expect: durations as plain milliseconds,
	// booleans as true/false, the rest as-is
	overrides := make(map[string]string)
	add := func(name, envKey, value string) {
		if f := fs.Lookup(name); f != nil && f.Changed {
			overrides[envKey] = value
		}
	}
	add("workers", "CONCURENT_DOWNLOADS", strconv.Itoa(*workers))
	add("delay-min", "DELAY_MIN", delayMin.millis())
	add("delay-max", "DELAY_MAX", delayMax.millis())
	add("retries", "RETRIES", strconv.Itoa(*retries))
	add("retry-base-delay", "RETRY_BASE_DELAY_MS", retryBase.millis())
	add("retry-max-delay", "RETRY_MAX_DELAY_MS", retryMax.millis())
	add("http-timeout", "HTTP_TIMEOUT_MS", httpTimeout.millis())
	add("user-agent", "USER_AGENT", *userAgent)
	add("proxies", "HTTP_PROXIES", *proxies)
	add("log-level", "LOG_LEVEL", *logLevel)
	add("progress", "PROGRESS", *progress)
	add("flaresolverr-url", "FLARESOLVERR_URL", *flareURL)
	add("flaresolverr-timeout", "FLARESOLVERR_MAX_TIMEOUT_MS", flareTimeout.millis())
	add("chrome-path", "CHROME_PATH", *chromePath)
	add("max-concurrent-requests", "MAX_CONCURRENT_REQUESTS", strconv.Itoa(*maxConcurrent))
	add("proxy-error-budget", "PROXY_ERROR_BUDGET", strconv.Itoa(*proxyBudget))
	add("proxy-only", "PROXY_ONLY", strconv.FormatBool(*proxyOnly))

	return cliOptions{include: *include, free: *free, overrides: overrides}
}

// readURLs loads the URL list, dropping blank lines.
func readURLs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var urls []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			urls = append(urls, line)
		}
	}
	return urls, nil
}
