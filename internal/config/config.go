// Package config reads every crawler tunable from the environment.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// DefaultUserAgent is the browser signature used for requests when the
// USER_AGENT variable is not set.
const DefaultUserAgent = "Mozilla/5.0 (X11; Datanyze; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/65.0.3325.181 Safari/537.36"

// PROGRESS modes: the live task table turns on automatically in a terminal,
// or can be forced on or off.
const (
	ProgressAuto = "auto"
	ProgressOn   = "on"
	ProgressOff  = "off"
)

// Config holds every tunable of the crawler. Each field comes from an
// environment variable and falls back to a sane default.
type Config struct {
	// Workers is the number of queue workers (CONCURENT_DOWNLOADS).
	Workers int
	// MaxConcurrentRequests caps the number of HTTP requests in flight
	// (MAX_CONCURRENT_REQUESTS), independently of the pool size.
	MaxConcurrentRequests int
	// DelayMin and DelayMax bound the random pause before each request
	// (DELAY_MIN, DELAY_MAX, milliseconds).
	DelayMin, DelayMax time.Duration
	// Retries is how many times a failed request is repeated (RETRIES).
	Retries int
	// RetryBaseDelay and RetryMaxDelay bound the exponential retry pause
	// (RETRY_BASE_DELAY_MS, RETRY_MAX_DELAY_MS).
	RetryBaseDelay, RetryMaxDelay time.Duration
	// HTTPTimeout is the per-request timeout (HTTP_TIMEOUT_MS).
	HTTPTimeout time.Duration
	// UserAgent is sent with every request (USER_AGENT).
	UserAgent string
	// HTTPProxies is the raw HTTP_PROXIES value: a comma-separated proxy
	// list, a path to a file with one proxy per line, or empty for no
	// proxies. Parsing happens in internal/proxypool.
	HTTPProxies string
	// ProxyErrorBudget is how many consecutive failed attempts through one
	// proxy block it until the run restarts (PROXY_ERROR_BUDGET).
	ProxyErrorBudget int
	// LogLevel is the minimum log level (LOG_LEVEL: debug|info|warn|error).
	LogLevel slog.Level
	// Progress controls the live task table (PROGRESS: auto|on|off).
	Progress string
}

// Load reads optional .env files (real environment variables win) and builds
// the configuration, validating the result afterwards.
func Load() (*Config, error) {
	// .env is optional; godotenv never overrides existing variables.
	_ = godotenv.Load()

	cfg := &Config{
		Workers:        envInt("CONCURENT_DOWNLOADS", 5),
		DelayMin:       envDuration("DELAY_MIN", 300*time.Millisecond),
		DelayMax:       envDuration("DELAY_MAX", 700*time.Millisecond),
		Retries:        envInt("RETRIES", 50),
		RetryBaseDelay: envDuration("RETRY_BASE_DELAY_MS", time.Second),
		RetryMaxDelay:  envDuration("RETRY_MAX_DELAY_MS", time.Minute),
		HTTPTimeout:    envDuration("HTTP_TIMEOUT_MS", 30*time.Second),
		UserAgent:      envStr("USER_AGENT", DefaultUserAgent),
		HTTPProxies:    envStr("HTTP_PROXIES", ""),
		LogLevel:       envLevel("LOG_LEVEL", slog.LevelInfo),
		Progress:       envStr("PROGRESS", ProgressAuto),
	}
	cfg.MaxConcurrentRequests = envInt("MAX_CONCURRENT_REQUESTS", cfg.Workers)
	cfg.ProxyErrorBudget = envInt("PROXY_ERROR_BUDGET", 5)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch {
	case c.Workers < 1:
		return errors.New("CONCURENT_DOWNLOADS must be at least 1")
	case c.MaxConcurrentRequests < 1:
		return errors.New("MAX_CONCURRENT_REQUESTS must be at least 1")
	case c.DelayMin < 0 || c.DelayMax < c.DelayMin:
		return fmt.Errorf("need 0 <= DELAY_MIN <= DELAY_MAX, got %s..%s", c.DelayMin, c.DelayMax)
	case c.Retries < 0:
		return errors.New("RETRIES must not be negative")
	case c.RetryBaseDelay <= 0 || c.RetryMaxDelay < c.RetryBaseDelay:
		return fmt.Errorf("need 0 < RETRY_BASE_DELAY_MS <= RETRY_MAX_DELAY_MS, got %s..%s", c.RetryBaseDelay, c.RetryMaxDelay)
	case c.HTTPTimeout <= 0:
		return errors.New("HTTP_TIMEOUT_MS must be positive")
	case c.UserAgent == "":
		return errors.New("USER_AGENT must not be empty")
	case c.ProxyErrorBudget < 1:
		return errors.New("PROXY_ERROR_BUDGET must be at least 1")
	case c.Progress != ProgressAuto && c.Progress != ProgressOn && c.Progress != ProgressOff:
		return fmt.Errorf("PROGRESS must be %s, %s or %s, got %q", ProgressAuto, ProgressOn, ProgressOff, c.Progress)
	}
	return nil
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

func envInt(key string, def int) int {
	s, ok := os.LookupEnv(key)
	if !ok || s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s=%q, using default %d\n", key, s, def)
		return def
	}
	return n
}

func envDuration(key string, def time.Duration) time.Duration {
	s, ok := os.LookupEnv(key)
	if !ok || s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s=%q, using default %s\n", key, s, def)
		return def
	}
	return time.Duration(n) * time.Millisecond
}

// envLevel parses a slog level name (DEBUG, INFO, WARN, ERROR, any case)
// via slog's own UnmarshalText.
func envLevel(key string, def slog.Level) slog.Level {
	s, ok := os.LookupEnv(key)
	if !ok || s == "" {
		return def
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		fmt.Fprintf(os.Stderr, "invalid %s=%q, using default %s\n", key, s, def)
		return def
	}
	return level
}
