package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
	if cfg.Verbose {
		t.Error("Verbose = true, want false")
	}
}

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
	t.Setenv("VERBOSE", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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
	if !cfg.Verbose {
		t.Error("Verbose = false, want true")
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Error("Load() succeeded, want validation error")
			}
		})
	}
}
