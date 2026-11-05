package progress

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewInactiveCountsAnyway(t *testing.T) {
	var buf bytes.Buffer
	u := New(ModeOff, &buf, true, 10)

	if u.Active() {
		t.Error("Active() = true for mode off, want false")
	}
	u.Done("https://example.com/album", "$7.50")
	u.Inc()
	if got := u.Processed(); got != 2 {
		t.Errorf("Processed() = %d, want 2", got)
	}

	// log lines pass straight through to the output stream
	if _, err := u.LogWriter().Write([]byte("12:00:00 INFO  hello\n")); err != nil {
		t.Fatalf("LogWriter.Write error = %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("passthrough log missing, got %q", buf.String())
	}
	if u.Elapsed() < 0 {
		t.Error("Elapsed() < 0")
	}
	u.Stop() // no-op, must not panic
}

func TestNewAutoModeNeedsTTY(t *testing.T) {
	var buf bytes.Buffer
	if New(ModeAuto, &buf, false, 1).Active() {
		t.Error("auto mode without TTY should be inactive")
	}
	u := New(ModeAuto, &buf, true, 1)
	defer u.Stop()
	if !u.Active() {
		t.Error("auto mode with TTY should be active")
	}
}

func TestActiveUILifecycle(t *testing.T) {
	var buf bytes.Buffer
	u := New(ModeOn, &buf, false, 5)
	defer u.Stop()

	// New must not return before the tea event loop has started
	select {
	case <-u.ready:
	default:
		t.Error("event loop not ready after New()")
	}

	u.Done("https://label.bandcamp.com/album/a", "$7.50")
	u.Done("https://label.bandcamp.com/album/b", "Free download")
	u.Inc()

	if got := u.Processed(); got != 3 {
		t.Errorf("Processed() = %d, want 3", got)
	}
	if _, err := u.LogWriter().Write([]byte("12:00:00 WARN  cannot get https://x\n")); err != nil {
		t.Fatalf("LogWriter.Write error = %v", err)
	}
}

func TestStopIdempotentAndFreezesElapsed(t *testing.T) {
	var buf bytes.Buffer
	u := New(ModeOn, &buf, false, 2)

	u.Inc()
	u.Stop()
	elapsed := u.Elapsed()
	u.Stop() // second call must be a no-op

	if u.Elapsed() != elapsed {
		t.Error("Elapsed() changed after second Stop()")
	}

	// after Stop the LogWriter falls back to the raw stream
	if _, err := u.LogWriter().Write([]byte("done\n")); err != nil {
		t.Fatalf("LogWriter.Write error = %v", err)
	}
	if !strings.Contains(buf.String(), "done") {
		t.Errorf("post-stop log not written, got %q", buf.String())
	}
}

func TestNilUIIsInert(t *testing.T) {
	var u *UI
	u.Done("x", "$1")
	u.Inc()
	u.Stop()
	u.WaitUserExit()
	if u.Active() {
		t.Error("nil UI must not be active")
	}
	if u.Processed() != 0 || u.Elapsed() != 0 {
		t.Error("nil UI must report zeros")
	}
}

func TestWaitUserExitReturnsWithoutListening(t *testing.T) {
	var buf bytes.Buffer
	New(ModeOff, &buf, true, 1).WaitUserExit() // dashboard off: must not block

	u := New(ModeOn, &buf, false, 1)
	defer u.Stop()
	// stdin is not a terminal under go test: no quit key can ever arrive,
	// so waiting would hang the process forever
	u.WaitUserExit() // must return immediately
}

func TestWaitUserExitBlocksUntilUserQuits(t *testing.T) {
	var buf bytes.Buffer
	u := New(ModeOn, &buf, false, 1)
	defer u.Stop()
	u.interactive = true // pretend stdin is a terminal so keys can arrive

	u.Done("https://label.bandcamp.com/album/a", "$7.50")

	waited := make(chan struct{})
	go func() {
		u.WaitUserExit()
		close(waited)
	}()

	// the crawl is over but the user has not quit yet: keep the process up
	select {
	case <-waited:
		t.Fatal("WaitUserExit returned before the user pressed a quit key")
	case <-time.After(150 * time.Millisecond):
	}

	// quit keys after completion close the dashboard itself
	u.prog.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("ctrl+c after completion must end the dashboard and unblock WaitUserExit")
	}
}

// TestWithPausePlumbsSpaceKey drives a real program loop: the space key
// from stdin emulation must reach the WithPause callback.
func TestWithPausePlumbsSpaceKey(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	var got []bool
	seen := func() []bool {
		mu.Lock()
		defer mu.Unlock()
		return append([]bool(nil), got...)
	}
	u := New(ModeOn, &buf, false, 3, WithPause(func(paused bool) {
		mu.Lock()
		got = append(got, paused)
		mu.Unlock()
	}))
	defer u.Stop()

	u.prog.Send(tea.KeyMsg{Type: tea.KeySpace})
	deadline := time.Now().Add(3 * time.Second)
	for len(seen()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("space never fired the pause callback")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state := seen(); len(state) != 1 || !state[0] {
		t.Errorf("pause callback got %v, want [true]", state)
	}

	u.prog.Send(tea.KeyMsg{Type: tea.KeySpace})
	deadline = time.Now().Add(3 * time.Second)
	for len(seen()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("second space never fired the resume callback")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state := seen(); state[1] {
		t.Errorf("pause callback got %v, want [true, false]", state)
	}
}

func TestShorten(t *testing.T) {
	short := "https://x.bandcamp.com/album/short"
	if got := shorten(short, 60); got != short {
		t.Errorf("shorten(%q) = %q, want unchanged", short, got)
	}
	long := strings.Repeat("a", 70)
	if got := shorten(long, 60); len(got) != 60 || !strings.HasPrefix(got, "...") {
		t.Errorf("shorten(long, 60) = %q (len %d), want 60 chars with ... prefix", got, len(got))
	}
}

func TestConcurrentUse(t *testing.T) {
	var buf bytes.Buffer
	u := New(ModeOn, &buf, false, 100)
	defer u.Stop()

	var wg sync.WaitGroup
	for w := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 12 {
				if i%3 == 0 {
					u.Inc()
				} else {
					u.Done("https://x.bandcamp.com/album/y", "$1.00")
				}
			}
			_ = w
		}()
	}
	wg.Wait()

	if got := u.Processed(); got != 96 {
		t.Errorf("Processed() = %d, want 96", got)
	}
}
