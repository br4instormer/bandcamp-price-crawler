// Package config reads every crawler tunable through the priority cascade:
// explicit CLI flags beat .env file values, which beat real environment
// variables; unset everywhere falls back to a sane default.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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

// Config holds every tunable of the crawler. Each field resolves through the
// cascade (CLI flag > .env > environment) and falls back to a sane default.
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
	// ProxyOnly forbids the direct connection (PROXY_ONLY): the crawl starts
	// only with at least one working proxy and fails fast once every proxy
	// is blocked instead of leaking an unproxied request.
	ProxyOnly bool
	// LogLevel is the minimum log level (LOG_LEVEL: debug|info|warn|error).
	LogLevel slog.Level
	// Progress controls the live task table (PROGRESS: auto|on|off).
	Progress string
	// FlareSolverrURL is the base URL of a FlareSolverr-compatible challenge
	// solver (FLARESOLVERR_URL); empty disables it and rod works alone.
	FlareSolverrURL string
	// FlareSolverrMaxTimeout bounds one FlareSolverr request
	// (FLARESOLVERR_MAX_TIMEOUT_MS).
	FlareSolverrMaxTimeout time.Duration
	// ChromePath is the chrome/chromium binary the rod solver launches
	// (CHROME_PATH). Empty means autodetect a system browser first and
	// download the managed one only as a last resort.
	ChromePath string
}

// sources resolves one setting through the cascade layers, highest priority
// first: cli holds the flags explicitly passed on the command line (keyed by
// their environment-variable name), dotEnv holds the parsed .env file, and
// the real process environment comes last before the built-in default.
type sources struct {
	cli    map[string]string
	dotEnv map[string]string
}

func (s sources) get(key string) (string, bool) {
	if v, ok := s.cli[key]; ok {
		return v, true
	}
	if v, ok := s.dotEnv[key]; ok {
		return v, true
	}
	return os.LookupEnv(key)
}

// Load reads the optional .env file in the working directory and builds the
// configuration through the cascade, validating the result afterwards. The
// overrides map is the CLI layer — only values of flags actually passed,
// keyed by their environment-variable name. godotenv.Read never touches the
// process environment, so the layers stay separate until get() orders them.
func Load(overrides map[string]string) (*Config, error) {
	// .env is optional; a missing or malformed file is silently skipped.
	dotEnv, _ := godotenv.Read()
	for k, v := range dotEnv {
		dotEnv[k] = strings.TrimSpace(v)
	}
	return load(dotEnv, overrides)
}

// load is Load without the file access, so tests can inject both lower
// layers without touching the working directory.
func load(dotEnv, overrides map[string]string) (*Config, error) {
	src := sources{cli: overrides, dotEnv: dotEnv}

	cfg := &Config{
		Workers:                resolveInt(src, "CONCURENT_DOWNLOADS", 5),
		DelayMin:               resolveDuration(src, "DELAY_MIN", 300*time.Millisecond),
		DelayMax:               resolveDuration(src, "DELAY_MAX", 700*time.Millisecond),
		Retries:                resolveInt(src, "RETRIES", 50),
		RetryBaseDelay:         resolveDuration(src, "RETRY_BASE_DELAY_MS", time.Second),
		RetryMaxDelay:          resolveDuration(src, "RETRY_MAX_DELAY_MS", time.Minute),
		HTTPTimeout:            resolveDuration(src, "HTTP_TIMEOUT_MS", 30*time.Second),
		UserAgent:              resolveStr(src, "USER_AGENT", DefaultUserAgent),
		HTTPProxies:            resolveStr(src, "HTTP_PROXIES", ""),
		LogLevel:               resolveLevel(src, "LOG_LEVEL", slog.LevelInfo),
		Progress:               resolveStr(src, "PROGRESS", ProgressAuto),
		FlareSolverrURL:        resolveStr(src, "FLARESOLVERR_URL", ""),
		FlareSolverrMaxTimeout: resolveDuration(src, "FLARESOLVERR_MAX_TIMEOUT_MS", 60*time.Second),
		ChromePath:             resolveStr(src, "CHROME_PATH", ""),
	}
	cfg.MaxConcurrentRequests = resolveInt(src, "MAX_CONCURRENT_REQUESTS", cfg.Workers)
	cfg.ProxyErrorBudget = resolveInt(src, "PROXY_ERROR_BUDGET", 5)
	// a mistyped safety switch must not silently allow direct traffic
	proxyOnly, err := resolveBool(src, "PROXY_ONLY", false)
	if err != nil {
		return nil, err
	}
	cfg.ProxyOnly = proxyOnly

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
	case c.ProxyOnly && c.HTTPProxies == "":
		return errors.New("PROXY_ONLY forbids the direct connection, but HTTP_PROXIES is empty — set a proxy list or file, or turn PROXY_ONLY off")
	case c.Progress != ProgressAuto && c.Progress != ProgressOn && c.Progress != ProgressOff:
		return fmt.Errorf("PROGRESS must be %s, %s or %s, got %q", ProgressAuto, ProgressOn, ProgressOff, c.Progress)
	case c.FlareSolverrMaxTimeout <= 0:
		return errors.New("FLARESOLVERR_MAX_TIMEOUT_MS must be positive")
	}
	if c.FlareSolverrURL != "" {
		u, err := url.Parse(c.FlareSolverrURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("FLARESOLVERR_URL must be an http(s) URL, got %q", c.FlareSolverrURL)
		}
	}
	return nil
}

func resolveStr(src sources, key, def string) string {
	if v, ok := src.get(key); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return def
}

// resolveBool reads a strict on/off switch: true|on|1 and false|off|0, any
// case; an empty or missing value means the default. Anything else is an
// error — a mistyped safety switch must not fall back to a silent default.
func resolveBool(src sources, key string, def bool) (bool, error) {
	s, ok := src.get(key)
	if !ok || strings.TrimSpace(s) == "" {
		return def, nil
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "on", "1":
		return true, nil
	case "false", "off", "0":
		return false, nil
	default:
		return def, fmt.Errorf("%s must be true|on|1 or false|off|0, got %q", key, s)
	}
}

func resolveInt(src sources, key string, def int) int {
	s, ok := src.get(key)
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

func resolveDuration(src sources, key string, def time.Duration) time.Duration {
	s, ok := src.get(key)
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

// resolveLevel parses a slog level name (DEBUG, INFO, WARN, ERROR, any case)
// via slog's own UnmarshalText.
func resolveLevel(src sources, key string, def slog.Level) slog.Level {
	s, ok := src.get(key)
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
