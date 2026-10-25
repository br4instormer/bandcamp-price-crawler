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
	"syscall"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/cost"
	"github.com/br4instormer/bandcamp-price-crawler/internal/counter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/httpclient"
	"github.com/br4instormer/bandcamp-price-crawler/internal/limiter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/logger"
	"github.com/br4instormer/bandcamp-price-crawler/internal/queue"
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
	log := logger.New(os.Stdout, cfg.Verbose)

	urls, err := readURLs(cli.include)
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		log.Warn("No urls found in " + cli.include)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	throttle := backoff.NewThrottle(backoff.MaxMultiplier)
	cnt := &counter.Counter{}
	queue.Run(ctx, urls, queue.Options{
		Workers:  cfg.Workers,
		DelayMin: cfg.DelayMin,
		DelayMax: cfg.DelayMax,
		OnlyFree: cli.free,
		Limiter:  limiter.New(cfg.MaxConcurrentRequests),
		Client:   httpclient.New(cfg, log, throttle),
		Throttle: throttle,
		Counter:  cnt,
		Log:      log,
	})

	if cli.free && cnt.Get(cost.FreeDownload) == 0 {
		log.Warn("No free download albums at all!")
	}
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
