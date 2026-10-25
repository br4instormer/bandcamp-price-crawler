package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewFiltersByLevel(t *testing.T) {
	tests := []struct {
		level    slog.Level
		contains []string
		missing  []string
	}{
		{slog.LevelDebug, []string{"debug-line", "info-line"}, nil},
		{slog.LevelInfo, []string{"info-line"}, []string{"debug-line"}},
		{slog.LevelWarn, nil, []string{"debug-line", "info-line"}},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		log := New(&buf, tt.level)
		log.Debug("debug-line")
		log.Info("info-line")

		out := buf.String()
		for _, want := range tt.contains {
			if !strings.Contains(out, want) {
				t.Errorf("level %v: output missing %q, got %q", tt.level, want, out)
			}
		}
		for _, unwanted := range tt.missing {
			if strings.Contains(out, unwanted) {
				t.Errorf("level %v: output must not contain %q, got %q", tt.level, unwanted, out)
			}
		}
	}
}

func TestHandleRendersAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	log.LogAttrs(nil, slog.LevelInfo, "retrying",
		slog.String("url", "https://example.com"),
		slog.Int("attempt", 2),
	)

	out := buf.String()
	for _, want := range []string{"retrying", "url=https://example.com", "attempt=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got %q", want, out)
		}
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("record must end with newline, got %q", out)
	}
}

// TestHandleWritesWholeLines guards the single-Write-per-record contract the
// live progress table relies on.
func TestHandleWritesWholeLines(t *testing.T) {
	cw := &countingWriter{}
	log := New(cw, slog.LevelInfo)
	log.Info("one")
	log.Info("two")
	if cw.writes != 2 {
		t.Errorf("writes = %d, want 2 (one per record)", cw.writes)
	}
}

type countingWriter struct {
	writes int
	buf    bytes.Buffer
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	return w.buf.Write(p)
}

func TestLevelName(t *testing.T) {
	// a record's own level decides the label, not the handler threshold
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)
	log.Debug("m")
	if !strings.Contains(buf.String(), "DEBUG") {
		t.Errorf("want DEBUG label, got %q", buf.String())
	}
}
