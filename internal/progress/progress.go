// Package progress renders a live Bubble Tea dashboard for the crawler: an
// overall progress bar, a table of the URLs each worker is handling and a
// tail of recent log lines.
package progress

import (
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// PROGRESS modes.
const (
	ModeAuto = "auto" // live dashboard when the output is a terminal
	ModeOn   = "on"   // always live
	ModeOff  = "off"  // never live
)

// UI tracks crawler progress. The zero value and nil are inert: every method
// is nil-safe, so callers may wire no UI at all. Counting works even when
// the dashboard is off.
type UI struct {
	prog        *tea.Program
	out         io.Writer
	live        bool
	interactive bool // stdin is a terminal: quit keys can end the program
	total       int64
	processed   atomic.Int64
	start       time.Time
	duration    atomic.Int64 // nanoseconds; frozen by Stop
	stopped     atomic.Bool
	stopOnce    sync.Once
	done        chan struct{}
	ready       chan struct{}
	cancelFn    func()
	pauseFn     func(paused bool)
}

// Option customizes the UI.
type Option func(*UI)

// WithCancel registers a callback fired when the user presses ctrl+c or q
// in the live dashboard; use it to cancel the crawler context.
func WithCancel(cancel func()) Option {
	return func(u *UI) { u.cancelFn = cancel }
}

// WithPause registers a callback fired when the user presses space in the
// live dashboard: true pauses the crawl, false resumes it. Wire it to a
// queue.Gate (its Set method fits the signature directly). The key works
// only while urls are still being processed — after completion or once a
// stop was requested the dashboard ignores it.
func WithPause(onPause func(paused bool)) Option {
	return func(u *UI) { u.pauseFn = onPause }
}

// New builds a UI tracking total URLs. The live dashboard runs only when
// mode forces it on, or in auto mode when isTTY reports a terminal.
func New(mode string, out io.Writer, isTTY bool, total int, opts ...Option) *UI {
	u := &UI{out: out, total: int64(total), start: time.Now()}
	for _, opt := range opts {
		opt(u)
	}
	if mode != ModeOn && !(mode == ModeAuto && isTTY) {
		return u
	}

	u.ready = make(chan struct{})
	u.done = make(chan struct{})
	u.interactive = term.IsTerminal(int(os.Stdin.Fd()))

	popts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(), // SIGINT is handled by the crawler itself
	}
	if !u.interactive {
		popts = append(popts, tea.WithInput(nil))
	}
	u.prog = tea.NewProgram(newModel(total, u.cancelFn, u.pauseFn, func() { close(u.ready) }), popts...)
	u.live = true

	go func() {
		defer close(u.done)
		_, _ = u.prog.Run()
	}()

	// wait until the event loop consumes its first message so that Send()
	// never races an unstarted program
	select {
	case <-u.ready:
	case <-time.After(time.Second):
	}

	// non-terminal output never receives a WindowSizeMsg; force a sane
	// size so a forced-on dashboard renders immediately even when piped
	if f, ok := out.(*os.File); !ok || !term.IsTerminal(int(f.Fd())) {
		u.prog.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	}
	return u
}

// Active reports whether the live dashboard is being rendered.
func (u *UI) Active() bool { return u != nil && u.live }

// Done records a processed URL with its price; live dashboards add a table
// row. URLs without a result (fetch/parse failures) are counted by Inc.
func (u *UI) Done(url, price string) {
	if u == nil {
		return
	}
	if n := u.processed.Add(1); u.live && n <= u.total {
		u.prog.Send(msgDone{url: url, price: price})
	}
}

// Inc counts one processed URL that produced no result row.
func (u *UI) Inc() {
	if u == nil {
		return
	}
	if n := u.processed.Add(1); u.live && n <= u.total {
		u.prog.Send(msgInc{})
	}
}

// Processed returns how many URLs have been handled so far.
func (u *UI) Processed() int64 {
	if u == nil {
		return 0
	}
	return u.processed.Load()
}

// LogWriter returns an io.Writer for log lines: while the dashboard is live
// they are rendered in its warnings tail (INFO result lines are skipped —
// results already live in the table); otherwise they go straight to the
// output stream.
func (u *UI) LogWriter() io.Writer {
	if u == nil {
		return io.Discard
	}
	return logWriter{u: u}
}

type logWriter struct{ u *UI }

func (lw logWriter) Write(p []byte) (int, error) {
	u := lw.u
	if u.live && !u.stopped.Load() {
		if line := strings.TrimRight(string(p), "\n"); line != "" {
			// lines come from our own handler as "HH:MM:SS LEVEL message"
			level := ""
			if fields := strings.Fields(line); len(fields) >= 2 {
				level = fields[1]
			}
			u.prog.Send(msgLog{level: level, line: line})
		}
		return len(p), nil
	}
	return u.out.Write(p)
}

// Stop quits the dashboard and freezes the elapsed time. Safe to call more
// than once.
func (u *UI) Stop() {
	if u == nil {
		return
	}
	u.stopOnce.Do(func() {
		if u.live {
			u.prog.Quit()
			select {
			case <-u.done:
			case <-time.After(3 * time.Second):
				u.prog.Kill()
				<-u.done
			}
		}
		u.stopped.Store(true)
		u.duration.Store(int64(time.Since(u.start)))
	})
}

// Elapsed returns the time since start, frozen by Stop.
func (u *UI) Elapsed() time.Duration {
	if u == nil {
		return 0
	}
	if d := u.duration.Load(); d != 0 {
		return time.Duration(d)
	}
	return time.Since(u.start)
}

// WaitUserExit blocks until the user closes the live dashboard: once every
// url is handled its quit keys (ctrl+c/q/esc) terminate the program, so the
// results stay on screen as long as the user wants. It returns immediately
// when nothing can capture keys — the dashboard is off or stdin is not a
// terminal — so callers keep the previous self-terminating behaviour.
func (u *UI) WaitUserExit() {
	if u == nil || !u.live || !u.interactive {
		return
	}
	<-u.done
}
