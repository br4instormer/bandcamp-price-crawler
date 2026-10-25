// Package queue distributes album URLs over a bounded worker pool.
package queue

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/backoff"
	"github.com/br4instormer/bandcamp-price-crawler/internal/counter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/htmlparser"
	"github.com/br4instormer/bandcamp-price-crawler/internal/httpclient"
	"github.com/br4instormer/bandcamp-price-crawler/internal/limiter"
	"github.com/br4instormer/bandcamp-price-crawler/internal/progress"
)

// Options wires together the collaborators of the queue.
type Options struct {
	Workers  int           // size of the worker pool
	DelayMin time.Duration // random pause bounds before each request
	DelayMax time.Duration
	OnlyFree bool // print free downloads only
	Limiter  *limiter.Limiter
	Client   *httpclient.Client
	Throttle *backoff.Throttle
	Counter  *counter.Counter
	Log      *slog.Logger
	Progress *progress.UI // may be nil
}

// Run feeds urls into a worker pool of opts.Workers goroutines and returns
// once every url is processed or ctx is cancelled.
func Run(ctx context.Context, urls []string, opts Options) {
	jobs := make(chan string, opts.Workers)

	var wg sync.WaitGroup
	for range opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for url := range jobs {
				opts.process(ctx, url)
			}
		}()
	}

feed:
	for _, url := range urls {
		select {
		case jobs <- url:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
}

// process handles one url end to end: pause, in-flight slot, fetch, parse,
// statistics and output. Every dequeued url counts as processed, whatever
// the outcome; only parsed results get a dashboard row.
func (o Options) process(ctx context.Context, url string) {
	if err := o.pause(ctx); err != nil {
		o.Progress.Inc()
		return
	}
	if err := o.Limiter.Acquire(ctx); err != nil {
		o.Progress.Inc()
		return
	}
	body, err := o.Client.Fetch(ctx, url)
	o.Limiter.Release()
	if err != nil {
		o.Log.Warn("Cannot get " + url + " because of " + err.Error())
		o.Progress.Inc()
		return
	}

	parsed, err := htmlparser.ParseCost(string(body))
	if err != nil {
		o.Log.Warn("Cannot parse " + url + " because of " + err.Error())
		o.Progress.Inc()
		return
	}

	o.Counter.Add(parsed.State)
	if parsed.IsFree() || !o.OnlyFree {
		o.Progress.Done(url, parsed.Price)
		o.Log.Info("[" + parsed.Price + "] " + url)
	} else {
		o.Progress.Inc()
	}
}

// pause sleeps a random [DelayMin..DelayMax] interval stretched by the global
// throttle, so it grows automatically while the server reports 429.
func (o Options) pause(ctx context.Context) error {
	select {
	case <-time.After(o.Throttle.Scale(randomDelay(o.DelayMin, o.DelayMax))):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomDelay(minD, maxD time.Duration) time.Duration {
	if maxD <= minD {
		return minD
	}
	return minD + time.Duration(rand.Int64N(int64(maxD-minD)+1))
}
