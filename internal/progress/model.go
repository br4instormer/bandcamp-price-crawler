package progress

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// messages exchanged with the crawler goroutines via Program.Send.
type (
	msgDone struct {
		url   string
		price string
	}
	msgInc struct{}
	msgLog struct {
		level string
		line  string
	}
)

// tickMsg refreshes the elapsed/ETA readouts once a second.
type tickMsg time.Time

// result is one processed page; rows appear only after a URL is done.
type result struct {
	url   string
	price string
}

const (
	logTailLen    = 6
	urlColMin     = 24
	priceColWidth = 26
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	metaStyle   = lipgloss.NewStyle().Faint(true)
	logStyle    = lipgloss.NewStyle().Faint(true)
	spinStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	cursorStyle = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("236"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
	doneStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
)

// model is the Bubble Tea model behind the live dashboard.
type model struct {
	table   table.Model
	bar     progress.Model
	spinner spinner.Model

	results   []result // processed pages, oldest first
	logs      []string // warnings/errors only
	processed int
	total     int

	start    time.Time
	cancel   func() // invoked on ctrl+c/q; stops the crawler
	onReady  func() // fired once the event loop runs
	width    int
	height   int
	stopping bool
	finished bool // every url handled: note under the table, keys exit
}

func newModel(total int, cancel, onReady func()) model {
	tbl := table.New(
		table.WithColumns([]table.Column{
			{Title: "URL", Width: urlColMin},
			{Title: "PRICE", Width: priceColWidth},
		}),
		table.WithHeight(3),
		table.WithFocused(true),
	)
	styles := table.DefaultStyles()
	styles.Header = styles.Header.BorderStyle(lipgloss.NormalBorder()).BorderBottom(true)
	styles.Selected = cursorStyle // highlight the scroll position
	tbl.SetStyles(styles)

	sp := spinner.New()
	sp.Style = spinStyle

	return model{
		table:   tbl,
		bar:     progress.New(progress.WithScaledGradient("#5A56E0", "#EE6FF8")),
		spinner: sp,
		total:   total,
		start:   time.Now(),
		cancel:  cancel,
		onReady: onReady,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg {
			if m.onReady != nil {
				m.onReady()
			}
			return nil
		},
		m.spinner.Tick,
		tick(),
	)
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			if m.finished {
				// nothing left to cancel: these keys now close the
				// dashboard and end the program
				return m, tea.Quit
			}
			if !m.stopping && m.cancel != nil {
				m.cancel()
			}
			m.stopping = true
			return m, nil
		}
		// everything else feeds the table (scrolling)
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
	case tickMsg:
		if m.width == 0 {
			// non-terminal output never delivers WindowSizeMsg; fall back
			// to a sane size so a forced-on dashboard still renders
			m.width, m.height = 80, 24
			m.layout()
		}
		return m, tick()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case msgDone:
		// keep following the tail only while the cursor rests on the last
		// row; a user scrolled up must not be yanked around
		follow := m.table.Cursor() >= len(m.results)-1
		m.processed++
		m.results = append(m.results, result{url: msg.url, price: msg.price})
		m.syncRows()
		if follow {
			m.table.SetCursor(len(m.results) - 1)
		}
		m.markFinished()
	case msgInc:
		m.processed++
		m.markFinished()
	case msgLog:
		if msg.level == "INFO" {
			// results already live in the table; INFO lines would duplicate
			return m, nil
		}
		m.logs = append(m.logs, msg.line)
		if len(m.logs) > logTailLen {
			m.logs = m.logs[len(m.logs)-logTailLen:]
		}
		m.reflow()
	}
	return m, nil
}

func (m model) View() string {
	if m.width == 0 {
		return "\n  starting…\n"
	}

	pct := 0.0
	if m.total > 0 {
		pct = float64(m.processed) / float64(m.total)
	}

	var b strings.Builder
	if m.processed < m.total {
		b.WriteString(m.spinner.View() + " ")
	}
	b.WriteString(titleStyle.Render("bandcamp crawler"))
	b.WriteString(fmt.Sprintf("  %d/%d (%.0f%%)", m.processed, m.total, pct*100))
	b.WriteString(metaStyle.Render(fmt.Sprintf("   elapsed %s   ETA %s", fmtDur(time.Since(m.start)), m.eta())))
	b.WriteString("\n\n")
	b.WriteString(m.table.View())
	if m.finished {
		b.WriteString("\n")
		b.WriteString(doneStyle.Render("  ✓ all sources processed"))
	}
	b.WriteString("\n\n")
	b.WriteString(m.bar.ViewAs(pct))
	b.WriteString("\n\n")
	b.WriteString(m.logsView())
	b.WriteString("\n ")
	if m.stopping {
		b.WriteString(helpStyle.Render("stopping — waiting for in-flight urls…"))
	} else if m.finished {
		b.WriteString(helpStyle.Render("↑/↓ — scroll   ctrl+c or q — exit"))
	} else {
		help := "↑/↓ — scroll   ctrl+c — stop"
		if !m.following() {
			help = "↑/↓ — scroll   G — latest   ctrl+c — stop"
		}
		b.WriteString(helpStyle.Render(help))
	}
	return b.String()
}

// following reports whether the table cursor rests on the newest row.
func (m model) following() bool {
	return len(m.results) == 0 || m.table.Cursor() >= len(m.results)-1
}

// markFinished flips the dashboard into its final state once every url has
// been accounted for: a completion note appears under the table and the
// quit keys start closing the program. The crawler itself keeps running
// until the user exits.
func (m *model) markFinished() {
	if m.finished || m.total == 0 || m.processed < m.total {
		return
	}
	m.finished = true
	m.reflow()
}

// eta estimates the remaining time from the current processing rate.
func (m model) eta() string {
	switch {
	case m.total == 0 || m.processed >= m.total:
		return "—"
	case m.processed == 0:
		return "…"
	}
	elapsed := time.Since(m.start)
	remain := time.Duration(float64(elapsed) * float64(m.total-m.processed) / float64(m.processed))
	return fmtDur(remain)
}

func (m model) logsView() string {
	if len(m.logs) == 0 {
		return logStyle.Render("  (no warnings)")
	}
	lines := make([]string, 0, len(m.logs)+1)
	lines = append(lines, logStyle.Render("  warnings:"))
	for _, l := range m.logs {
		lines = append(lines, "  "+logStyle.Render(shorten(l, max(m.width-4, 20))))
	}
	return strings.Join(lines, "\n")
}

// layout sizes the URL column, the bar and the table height to the terminal.
func (m *model) layout() {
	urlWidth := max(urlColMin, m.width-priceColWidth-8)
	cols := m.table.Columns()
	cols[0].Width = urlWidth
	m.table.SetColumns(cols)
	m.table.SetWidth(min(m.width, urlWidth+priceColWidth+8))
	m.bar.Width = min(m.width-4, 72)
	m.reflow()
	m.syncRows()
}

// reflow fits the table between the header and the bar/warnings footer.
// One spare line is kept: the renderer drops lines from the TOP when the
// view exceeds the terminal height, which would eat the title.
func (m *model) reflow() {
	logLines := 1 + min(len(m.logs), logTailLen)
	fixed := 7 // title + blanks + bar + help + one spare line
	if m.finished {
		fixed++ // the completion note under the table
	}
	m.table.SetHeight(max(3, m.height-logLines-fixed))
}

// syncRows rebuilds the table rows from the results, oldest first.
func (m *model) syncRows() {
	urlWidth := m.table.Columns()[0].Width
	rows := make([]table.Row, len(m.results))
	for i, r := range m.results {
		rows[i] = table.Row{shorten(r.url, max(urlWidth-2, 8)), fmt.Sprintf("%*s", priceColWidth, r.price)}
	}
	m.table.SetRows(rows)
}

func fmtDur(d time.Duration) string {
	return d.Round(time.Second).String()
}

// shorten keeps the distinguishing tail of long URLs (bandcamp slugs live at
// the end).
func shorten(s string, maxLen int) string {
	if maxLen < 4 || len(s) <= maxLen {
		return s
	}
	return "..." + s[len(s)-(maxLen-3):]
}
