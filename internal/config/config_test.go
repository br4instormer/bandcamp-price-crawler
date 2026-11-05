package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(nil, nil)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5", cfg.Workers)
	}
	if cfg.MaxConcurrentRequests != cfg.Workers {
		t.Errorf("MaxConcurrentRequests = %d, want = Workers (%d)", cfg.MaxConcurrentRequests, cfg.Workers)
	}
	if cfg.DelayMin != 300*time.Millisecond || cfg.DelayMax != 700*time.Millisecond {
		t.Errorf("delays = %s..%s, want 300ms..700ms", cfg.DelayMin, cfg.DelayMax)
	}
	if cfg.Retries != 50 {
		t.Errorf("Retries = %d, want 50", cfg.Retries)
	}
	if cfg.RetryBaseDelay != time.Second || cfg.RetryMaxDelay != time.Minute {
		t.Errorf("retry delays = %s..%s, want 1s..1m", cfg.RetryBaseDelay, cfg.RetryMaxDelay)
	}
	if cfg.HTTPTimeout != 30*time.Second {
		t.Errorf("HTTPTimeout = %s, want 30s", cfg.HTTPTimeout)
	}
	if cfg.UserAgent != DefaultUserAgent {
		t.Errorf("UserAgent = %q, want default", cfg.UserAgent)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", cfg.LogLevel)
	}
	if cfg.Progress != ProgressAuto {
		t.Errorf("Progress = %q, want %q", cfg.Progress, ProgressAuto)
	}
	if cfg.HTTPProxies != "" {
		t.Errorf("HTTPProxies = %q, want empty (no proxies)", cfg.HTTPProxies)
	}
	if cfg.ProxyErrorBudget != 5 {
		t.Errorf("ProxyErrorBudget = %d, want 5", cfg.ProxyErrorBudget)
	}
	if cfg.ProxyOnly {
		t.Error("ProxyOnly = true, want false (current mechanics by default)")
	}
	if cfg.FlareSolverrURL != "" {
		t.Errorf("FlareSolverrURL = %q, want empty (no solver)", cfg.FlareSolverrURL)
	}
	if cfg.FlareSolverrMaxTimeout != 60*time.Second {
		t.Errorf("FlareSolverrMaxTimeout = %s, want 60s", cfg.FlareSolverrMaxTimeout)
	}
	if cfg.ChromePath != "" {
		t.Errorf("ChromePath = %q, want empty (autodetect)", cfg.ChromePath)
	}
}

// TestLoadOverrides covers the lowest cascade layer: values set in the real
// environment are honored when no .env or CLI layer holds the variable.
func TestLoadOverrides(t *testing.T) {
	t.Setenv("CONCURENT_DOWNLOADS", "12")
	t.Setenv("MAX_CONCURRENT_REQUESTS", "3")
	t.Setenv("DELAY_MIN", "1")
	t.Setenv("DELAY_MAX", "2")
	t.Setenv("RETRIES", "7")
	t.Setenv("RETRY_BASE_DELAY_MS", "5")
	t.Setenv("RETRY_MAX_DELAY_MS", "500")
	t.Setenv("HTTP_TIMEOUT_MS", "250")
	t.Setenv("USER_AGENT", "test-agent")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("PROGRESS", "off")
	t.Setenv("HTTP_PROXIES", "socks5://socks5.proxies:1080,socks5://user:password@p:1080")
	t.Setenv("PROXY_ERROR_BUDGET", "3")
	t.Setenv("PROXY_ONLY", "on")
	t.Setenv("FLARESOLVERR_URL", "http://trawl.local")
	t.Setenv("FLARESOLVERR_MAX_TIMEOUT_MS", "15000")
	t.Setenv("CHROME_PATH", "/usr/bin/chromium")

	cfg, err := load(nil, nil)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if cfg.Workers != 12 || cfg.MaxConcurrentRequests != 3 {
		t.Errorf("workers/limit = %d/%d, want 12/3", cfg.Workers, cfg.MaxConcurrentRequests)
	}
	if cfg.DelayMin != time.Millisecond || cfg.DelayMax != 2*time.Millisecond {
		t.Errorf("delays = %s..%s, want 1ms..2ms", cfg.DelayMin, cfg.DelayMax)
	}
	if cfg.Retries != 7 {
		t.Errorf("Retries = %d, want 7", cfg.Retries)
	}
	if cfg.RetryBaseDelay != 5*time.Millisecond || cfg.RetryMaxDelay != 500*time.Millisecond {
		t.Errorf("retry delays = %s..%s, want 5ms..500ms", cfg.RetryBaseDelay, cfg.RetryMaxDelay)
	}
	if cfg.HTTPTimeout != 250*time.Millisecond {
		t.Errorf("HTTPTimeout = %s, want 250ms", cfg.HTTPTimeout)
	}
	if cfg.UserAgent != "test-agent" {
		t.Errorf("UserAgent = %q, want %q", cfg.UserAgent, "test-agent")
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want DEBUG", cfg.LogLevel)
	}
	if cfg.Progress != ProgressOff {
		t.Errorf("Progress = %q, want %q", cfg.Progress, ProgressOff)
	}
	if cfg.HTTPProxies != "socks5://socks5.proxies:1080,socks5://user:password@p:1080" {
		t.Errorf("HTTPProxies = %q, want the raw spec", cfg.HTTPProxies)
	}
	if cfg.ProxyErrorBudget != 3 {
		t.Errorf("ProxyErrorBudget = %d, want 3", cfg.ProxyErrorBudget)
	}
	if !cfg.ProxyOnly {
		t.Error("ProxyOnly = false, want true (PROXY_ONLY=on)")
	}
	if cfg.FlareSolverrURL != "http://trawl.local" {
		t.Errorf("FlareSolverrURL = %q, want the solver URL", cfg.FlareSolverrURL)
	}
	if cfg.FlareSolverrMaxTimeout != 15*time.Second {
		t.Errorf("FlareSolverrMaxTimeout = %s, want 15s", cfg.FlareSolverrMaxTimeout)
	}
	if cfg.ChromePath != "/usr/bin/chromium" {
		t.Errorf("ChromePath = %q, want the system chromium", cfg.ChromePath)
	}
}

// TestProxyOnlyValues covers every accepted spelling of the switch, any case.
func TestProxyOnlyValues(t *testing.T) {
	// the mode requires a proxy to route through: an empty HTTP_PROXIES is
	// a validation error on its own, keep one at hand for the truthy cases
	t.Setenv("HTTP_PROXIES", "socks5://proxy.example:1080")

	truthy := []string{"true", "TRUE", "on", "On", "1"}
	for _, v := range truthy {
		t.Setenv("PROXY_ONLY", v)
		cfg, err := load(nil, nil)
		if err != nil {
			t.Fatalf("load() with PROXY_ONLY=%q error = %v", v, err)
		}
		if !cfg.ProxyOnly {
			t.Errorf("PROXY_ONLY=%q must turn the mode on", v)
		}
	}

	falsy := []string{"false", "OFF", "0", "", "   "}
	for _, v := range falsy {
		t.Setenv("PROXY_ONLY", v)
		cfg, err := load(nil, nil)
		if err != nil {
			t.Fatalf("load() with PROXY_ONLY=%q error = %v", v, err)
		}
		if cfg.ProxyOnly {
			t.Errorf("PROXY_ONLY=%q must keep the mode off", v)
		}
	}
}

// TestDotEnvBeatsEnvironment pins the cascade order between the two lower
// layers: a value from .env wins over the same variable in the real
// environment.
func TestDotEnvBeatsEnvironment(t *testing.T) {
	t.Setenv("CONCURENT_DOWNLOADS", "3")
	t.Setenv("USER_AGENT", "from-environment")

	dotEnv := map[string]string{
		"CONCURENT_DOWNLOADS": "9",
		"USER_AGENT":          "from-dotenv",
	}
	cfg, err := load(dotEnv, nil)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Workers != 9 {
		t.Errorf("Workers = %d, want 9 (.env beats the environment)", cfg.Workers)
	}
	if cfg.UserAgent != "from-dotenv" {
		t.Errorf("UserAgent = %q, want %q (.env beats the environment)", cfg.UserAgent, "from-dotenv")
	}
}

// TestCLIBeatsDotEnvAndEnvironment pins the full cascade: an explicitly
// passed flag beats both the .env file and the real environment, for every
// helper type the config parses.
func TestCLIBeatsDotEnvAndEnvironment(t *testing.T) {
	t.Setenv("CONCURENT_DOWNLOADS", "3")
	t.Setenv("PROXY_ONLY", "off")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("DELAY_MIN", "100")

	dotEnv := map[string]string{
		"CONCURENT_DOWNLOADS": "9",
		"PROXY_ONLY":          "on",
		"LOG_LEVEL":           "debug",
		"DELAY_MIN":           "200",
	}
	overrides := map[string]string{
		"CONCURENT_DOWNLOADS": "17",
		"PROXY_ONLY":          "true",
		"HTTP_PROXIES":        "socks5://cli.example:1080",
		"LOG_LEVEL":           "error",
		"DELAY_MIN":           "400",
	}
	cfg, err := load(dotEnv, overrides)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.Workers != 17 {
		t.Errorf("Workers = %d, want 17 (the CLI flag wins)", cfg.Workers)
	}
	if !cfg.ProxyOnly {
		t.Error("ProxyOnly = false, want true (the CLI flag wins)")
	}
	if cfg.LogLevel != slog.LevelError {
		t.Errorf("LogLevel = %v, want ERROR (the CLI flag wins)", cfg.LogLevel)
	}
	if cfg.DelayMin != 400*time.Millisecond {
		t.Errorf("DelayMin = %s, want 400ms (the CLI flag wins)", cfg.DelayMin)
	}
}

// TestProxyOnlyStrictFromDotEnv keeps the strictness of the safety switch in
// the .env layer too: a mistyped value is an error, not a silent default.
func TestProxyOnlyStrictFromDotEnv(t *testing.T) {
	dotEnv := map[string]string{"PROXY_ONLY": "yes"}
	if _, err := load(dotEnv, nil); err == nil {
		t.Error("load() with .env PROXY_ONLY=yes succeeded, want an error")
	}
}

// TestProxyOnlyRequiresProxies rejects the mode without anything to route
// through: the contradiction between the safety switch and the empty proxy
// setting fails at the config layer, before urls are read or a browser
// starts. Unusable (but non-empty) proxy values stay a runtime refusal.
func TestProxyOnlyRequiresProxies(t *testing.T) {
	t.Setenv("PROXY_ONLY", "on")
	if _, err := load(nil, nil); err == nil {
		t.Error("load() with PROXY_ONLY=on and empty HTTP_PROXIES succeeded, want an error")
	}

	if _, err := load(map[string]string{"PROXY_ONLY": "true"}, nil); err == nil {
		t.Error("load() with .env PROXY_ONLY=true and empty HTTP_PROXIES succeeded, want an error")
	}

	if _, err := load(nil, map[string]string{"PROXY_ONLY": "true"}); err == nil {
		t.Error("load() with the CLI --proxy-only and empty HTTP_PROXIES succeeded, want an error")
	}

	cfg, err := load(map[string]string{"PROXY_ONLY": "true", "HTTP_PROXIES": "socks5://proxy.example:1080"}, nil)
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if !cfg.ProxyOnly {
		t.Error("ProxyOnly = false, want true")
	}
}

// TestLoadInvalidLogLevelFallsBack keeps the legacy default on garbage input
// instead of failing the run — from the environment as well as from the CLI
// layer.
func TestLoadInvalidLogLevelFallsBack(t *testing.T) {
	t.Setenv("LOG_LEVEL", "verbose")

	cfg, err := load(nil, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want fallback INFO", cfg.LogLevel)
	}

	cfg, err = load(nil, map[string]string{"LOG_LEVEL": "verbose"})
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want fallback INFO for a CLI override too", cfg.LogLevel)
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"no workers", map[string]string{"CONCURENT_DOWNLOADS": "0"}},
		{"no request limit", map[string]string{"MAX_CONCURRENT_REQUESTS": "0"}},
		{"delay bounds crossed", map[string]string{"DELAY_MIN": "500", "DELAY_MAX": "100"}},
		{"negative retries", map[string]string{"RETRIES": "-1"}},
		{"retry bounds crossed", map[string]string{"RETRY_BASE_DELAY_MS": "10", "RETRY_MAX_DELAY_MS": "5"}},
		{"no http timeout", map[string]string{"HTTP_TIMEOUT_MS": "0"}},
		{"bad progress mode", map[string]string{"PROGRESS": "yes"}},
		{"no proxy budget", map[string]string{"PROXY_ERROR_BUDGET": "0"}},
		{"bad proxy only", map[string]string{"PROXY_ONLY": "yes"}},
		{"no solver timeout", map[string]string{"FLARESOLVERR_MAX_TIMEOUT_MS": "0"}},
		{"bad solver url", map[string]string{"FLARESOLVERR_URL": "trawl.local"}},
		{"ftp solver url", map[string]string{"FLARESOLVERR_URL": "ftp://trawl.local"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := load(nil, nil); err == nil {
				t.Error("load() succeeded, want validation error")
			}
		})
	}
}
