// Command crawler visits Bandcamp album pages listed in a file and prints the
// price of every product to stdout.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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

	cfg, err := config.Load()
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

	// the proxy list parses once, the pool itself is run-scoped below
	proxyEntries := proxypool.Parse(cfg.HTTPProxies, log)

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
		// retries, statistics and proxy blocks are all run-scoped state
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
		// direct where the real page answers, a solver where only one can
		client.Probe(runCtx, urls)

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

		if cli.free && cnt.Get(cost.FreeDownload) == 0 {
			log.Warn("No free download albums at all!")
		}

		// do not terminate after the last url: keep the dashboard (and the
		// process) up until the user closes it or asks to restart; a
		// ctrl+c that cancelled the crawl took the self-terminating path
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

type cliOptions struct {
	include string
	free    bool
}

// parseFlags mirrors the yargs interface: -i/--include picks the URL file,
// -f/--free keeps only free downloads.
func parseFlags() cliOptions {
	include := flag.String("i", "urls.txt", "File contains bandcamp urls")
	includeLong := flag.String("include", "urls.txt", "File contains bandcamp urls")
	free := flag.Bool("f", false, "Show only free download albums")
	freeLong := flag.Bool("free", false, "Show only free download albums")
	flag.Parse()

	if *includeLong != "urls.txt" {
		*include = *includeLong
	}
	return cliOptions{include: *include, free: *free || *freeLong}
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
