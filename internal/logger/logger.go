// Package logger provides a leveled logger with the compact
// "15:04:05 LEVEL message" format of the TypeScript original.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// New builds a slog logger writing HH:MM:SS LEVEL records to w. Verbose mode
// lowers the level from info to debug (VERBOSE=1 parity).
func New(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(&plainHandler{w: w, level: level})
}

type plainHandler struct {
	mu    sync.Mutex
	w     io.Writer
	level slog.Level
}

func (h *plainHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *plainHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	_, err := fmt.Fprintf(h.w, "%s %-5s %s\n",
		r.Time.Format("15:04:05"),
		levelName(r.Level),
		r.Message,
	)
	return err
}

func (h *plainHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *plainHandler) WithGroup(name string) slog.Handler {
	return h
}

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARN"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}
