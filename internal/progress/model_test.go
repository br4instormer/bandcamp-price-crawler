package progress

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func update(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func TestModelResultsTable(t *testing.T) {
	m := newModel(10, func() {}, nil)

	if view := m.View(); !strings.Contains(view, "starting") {
		t.Errorf("view before WindowSizeMsg should be a placeholder, got %q", view)
	}

	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = update(m, msgDone{url: "https://label.bandcamp.com/album/a", price: "$7.50"})
	m = update(m, msgDone{url: "https://label.bandcamp.com/album/b", price: "Free download"})
	m = update(m, msgInc{}) // failure: counted, no row
	m = update(m, msgDone{url: "https://label.bandcamp.com/album/c", price: "Name your price"})

	if m.processed != 4 {
		t.Errorf("processed = %d, want 4", m.processed)
	}
	if len(m.results) != 3 {
		t.Fatalf("results = %d rows, want 3 (failures get no row)", len(m.results))
	}
	// oldest first, auto-following the newest row
	if m.results[0].url != "https://label.bandcamp.com/album/a" {
		t.Errorf("first result = %q, want oldest (album/a)", m.results[0].url)
	}
	if m.table.Cursor() != len(m.results)-1 {
		t.Errorf("cursor = %d, want %d (following the tail)", m.table.Cursor(), len(m.results)-1)
	}

	rows := m.table.Rows()
	if rows[0][0] != "https://label.bandcamp.com/album/a" {
		t.Errorf("row 0 url = %q", rows[0][0])
	}
	if !strings.HasSuffix(rows[0][1], "$7.50") {
		t.Errorf("row 0 price = %q, want right-aligned $7.50", rows[0][1])
	}
	if len(rows[0][1]) != priceColWidth {
		t.Errorf("price cell width = %d, want %d", len(rows[0][1]), priceColWidth)
	}

	view := m.View()
	for _, want := range []string{"bandcamp crawler", "4/10", "40%", "URL", "PRICE", "$7.50", "Free download", "ctrl+c"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
	if strings.Contains(view, "WORKER") {
		t.Error("view must not contain a WORKER column")
	}
	// the progress bar sits below the table
	if strings.Index(view, "PRICE") > strings.Index(view, "█") {
		t.Error("progress bar must render below the table")
	}
}

func TestModelScrollFollow(t *testing.T) {
	m := newModel(50, nil, nil)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	for i := range 20 {
		m = update(m, msgDone{url: fmt.Sprintf("https://x.bandcamp.com/album/%02d", i), price: "$1.00"})
	}
	if got := m.table.Cursor(); got != 19 {
		t.Fatalf("cursor = %d, want 19 (auto-follow)", got)
	}

	// scrolling up pauses following
	for range 5 {
		m = update(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if got := m.table.Cursor(); got != 14 {
		t.Fatalf("cursor after scroll = %d, want 14", got)
	}
	if m.following() {
		t.Error("model must not report following while scrolled up")
	}
	if !strings.Contains(m.View(), "G — latest") {
		t.Error("help should offer the jump-to-latest key while scrolled")
	}

	// new results must not yank the cursor while reading
	m = update(m, msgDone{url: "https://x.bandcamp.com/album/new", price: "$2.00"})
	if got := m.table.Cursor(); got != 14 {
		t.Errorf("cursor jumped to %d while scrolled up, want 14", got)
	}

	// jumping back to the end resumes following
	m = update(m, tea.KeyMsg{Type: tea.KeyEnd})
	if got := m.table.Cursor(); got != len(m.results)-1 {
		t.Errorf("cursor after end = %d, want %d", got, len(m.results)-1)
	}
	m = update(m, msgDone{url: "https://x.bandcamp.com/album/newer", price: "$3.00"})
	if got := m.table.Cursor(); got != len(m.results)-1 {
		t.Errorf("cursor = %d after new result, want to follow the tail", got)
	}
}

func TestModelInfoLogsFiltered(t *testing.T) {
	m := newModel(5, nil, nil)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = update(m, msgLog{level: "INFO", line: "21:00:00 INFO  [Free download] https://x/album/a"})
	m = update(m, msgLog{level: "WARN", line: "21:00:01 WARN  Cannot get https://x/album/b"})

	if len(m.logs) != 1 {
		t.Fatalf("logs = %d lines, want 1 (INFO filtered)", len(m.logs))
	}
	if !strings.Contains(m.logs[0], "Cannot get") {
		t.Errorf("kept log = %q, want the WARN line", m.logs[0])
	}

	view := m.View()
	if strings.Contains(view, "Free download]") {
		t.Error("view must not repeat INFO result lines")
	}
	if !strings.Contains(view, "Cannot get") {
		t.Error("view should show the warning")
	}
}

func TestModelWarningsTailCapped(t *testing.T) {
	m := newModel(5, nil, nil)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	for i := range logTailLen + 4 {
		m = update(m, msgLog{level: "WARN", line: strings.Repeat("w", i+1)})
	}
	if len(m.logs) != logTailLen {
		t.Errorf("logs kept = %d, want %d", len(m.logs), logTailLen)
	}
	if !strings.Contains(m.View(), "warnings:") {
		t.Error("view missing warnings header")
	}
}

func TestModelCtrlC(t *testing.T) {
	cancelled := false
	m := newModel(2, func() { cancelled = true }, nil)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !cancelled {
		t.Error("ctrl+c must fire the cancel callback")
	}
	if !m.stopping {
		t.Error("ctrl+c must set stopping")
	}
	if !strings.Contains(m.View(), "stopping") {
		t.Error("view should show stopping status")
	}

	// repeated presses must not re-fire cancel
	cancelled = false
	m = update(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cancelled {
		t.Error("second ctrl+c must not re-fire cancel")
	}
}

func TestModelURLTruncatedToColumn(t *testing.T) {
	m := newModel(20, nil, nil)
	m = update(m, tea.WindowSizeMsg{Width: 60, Height: 20}) // narrow terminal

	long := "https://label.bandcamp.com/album/" + strings.Repeat("x", 80)
	m = update(m, msgDone{url: long, price: "$1.00"})

	col := m.table.Columns()[0].Width
	if got := m.table.Rows()[0][0]; len(got) > col-2 {
		t.Errorf("url cell len %d exceeds column width %d", len(got), col)
	}
}

func TestModelInitBatchesCommands(t *testing.T) {
	m := newModel(1, nil, nil)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() must return a command")
	}
	if _, ok := cmd().(tea.BatchMsg); !ok {
		t.Errorf("Init() should return a batch, got %T", cmd())
	}
}
