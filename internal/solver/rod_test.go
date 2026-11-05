package solver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/br4instormer/bandcamp-price-crawler/internal/config"
	"github.com/br4instormer/bandcamp-price-crawler/internal/proxypool"
)

func rodConfig() *config.Config {
	return &config.Config{HTTPTimeout: 2 * time.Second, ProxyErrorBudget: 5}
}

func TestRodPrefersCHROMEPath(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := rodConfig()
	cfg.ChromePath = bin

	called := false
	r := NewRod(cfg, slog.New(slog.DiscardHandler), nil)
	r.lookPath = func() (string, bool) { called = true; return "/usr/bin/chrome", true }

	path, err := r.bin(t.Context())
	if err != nil || path != bin {
		t.Fatalf("bin() = %q, %v; want %q, nil", path, err, bin)
	}
	if called {
		t.Error("lookPath ran although CHROME_PATH was set")
	}
}

func TestRodRejectsMissingCHROMEPath(t *testing.T) {
	cfg := rodConfig()
	cfg.ChromePath = filepath.Join(t.TempDir(), "absent")

	r := NewRod(cfg, slog.New(slog.DiscardHandler), nil)
	if _, err := r.bin(t.Context()); err == nil {
		t.Fatal("bin() succeeded, want an error about the missing binary")
	}
}

func TestRodAdoptsSystemBrowserWithoutDownload(t *testing.T) {
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), nil)
	r.lookPath = func() (string, bool) { return "/usr/bin/chromium", true }

	path, err := r.bin(t.Context())
	if err != nil || path != "/usr/bin/chromium" {
		t.Fatalf("bin() = %q, %v; want the system browser", path, err)
	}
}

func TestRodDownloadsLazilyWithProgress(t *testing.T) {
	var mu sync.Mutex
	var statuses []string
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), func(s string) {
		mu.Lock()
		defer mu.Unlock()
		statuses = append(statuses, s)
	})
	r.lookPath = func() (string, bool) { return "", false }
	r.downloadF = func() (string, error) {
		r.reportStatus("browser download: 34%")
		return "/managed/chromium", nil
	}

	// Preflight must return at once: the download runs while the caller
	// keeps working
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Preflight()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Preflight blocked on the download")
	}

	path, err := r.bin(t.Context())
	if err != nil || path != "/managed/chromium" {
		t.Fatalf("bin() = %q, %v; want the managed browser", path, err)
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, s := range statuses {
		if strings.Contains(s, "34%") {
			found = true
		}
	}
	if !found {
		t.Errorf("statuses = %v, want the download progress", statuses)
	}
}

func TestRodDownloadFailureSurfaces(t *testing.T) {
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), nil)
	r.lookPath = func() (string, bool) { return "", false }
	r.downloadF = func() (string, error) { return "", errors.New("offline") }

	if _, err := r.bin(t.Context()); err == nil {
		t.Fatal("bin() succeeded, want the download error")
	}
}

func TestRodBinWaitsForDownloadOnFetch(t *testing.T) {
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), nil)
	r.lookPath = func() (string, bool) { return "", false }

	release := make(chan struct{})
	r.downloadF = func() (string, error) {
		<-release
		return "/managed/chromium", nil
	}
	r.Preflight()

	// bin must block until the download is over, then answer
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	path, err := r.bin(ctx)
	if err != nil || path != "/managed/chromium" {
		t.Fatalf("bin() = %q, %v; want the managed browser after waiting", path, err)
	}
}

func TestStatusLoggerFormatsProgress(t *testing.T) {
	var got string
	l := statusLogger{status: func(s string) { got = s }}
	l.Println("12.34MB/312.34MB", "4%", "20.81MB/s")
	if !strings.HasPrefix(got, "browser download: 12.34MB/312.34MB") {
		t.Errorf("status = %q, want the prefixed progress", got)
	}
}

func TestRodContextCancelAbortsBinWait(t *testing.T) {
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), nil)
	r.lookPath = func() (string, bool) { return "", false }
	r.downloadF = func() (string, error) { time.Sleep(time.Minute); return "", fmt.Errorf("nope") }
	r.Preflight()

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := r.bin(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("bin() error = %v, want context.Canceled", err)
	}
}

func TestBrowserProxyNormalizesScheme(t *testing.T) {
	tests := []struct {
		name  string
		entry proxypool.Entry
		want  string
	}{
		{"socks5h becomes socks5", proxypool.Entry{Scheme: "socks5h", Host: "lserver", Port: "1080"}, "socks5://lserver:1080"},
		{"socks5 stays", proxypool.Entry{Scheme: "socks5", Host: "h", Port: "1080"}, "socks5://h:1080"},
		{"http stays", proxypool.Entry{Scheme: "http", Host: "p", Port: "8080"}, "http://p:8080"},
		{"https stays", proxypool.Entry{Scheme: "https", Host: "p", Port: "443"}, "https://p:443"},
		{"credentials dropped", proxypool.Entry{Scheme: "socks5h", Host: "h", Port: "1080", Username: "u", Password: "s"}, "socks5://h:1080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := browserProxy(tt.entry); got != tt.want {
				t.Errorf("browserProxy() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRodFetchAbortsOnCanceledContext(t *testing.T) {
	r := NewRod(rodConfig(), slog.New(slog.DiscardHandler), nil)
	// the fast-fail must fire before the browser is even resolved
	r.lookPath = func() (string, bool) {
		t.Error("lookPath ran for an aborted request")
		return "", false
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := r.Fetch(ctx, "http://album.example/x", proxypool.Entry{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch() error = %v, want context.Canceled", err)
	}
}
