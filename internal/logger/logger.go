// Package logger provides a leveled logger with the compact
// "15:04:05 LEVEL message key=value" format of the TypeScript original.
package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// New builds a slog logger writing HH:MM:SS LEVEL records to w, filtering
// out records below level (LOG_LEVEL=debug|info|warn|error).
func New(w io.Writer, level slog.Level) *slog.Logger {
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

	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s %s",
		r.Time.Format("15:04:05"),
		levelName(r.Level),
		r.Message,
	)
	r.Attrs(func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	b.WriteByte('\n')

	// One Write per record keeps whole lines intact when the writer is a
	// live-rendered progress table.
	_, err := io.WriteString(h.w, b.String())
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
