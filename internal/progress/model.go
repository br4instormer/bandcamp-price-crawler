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
	// msgFail marks one processed URL as errored: the row renders a red
	// "error" instead of a price.
	msgFail struct {
		url string
	}
	msgInc struct{}
	msgLog struct {
		level string
		line  string
	}
	// msgProxy reports the proxy pool state: how many proxies serve the run
	// and how many are blocked for exhausting their error budgets.
	msgProxy struct {
		total   int
		blocked int
	}
	// msgStatus shows or clears the persistent status line under the proxy
	// line (the managed browser download progress).
	msgStatus struct {
		text string
	}
	// msgRefused marks a run that never started: proxy-only mode found no
	// usable proxy. The reason shows under the table, the restart key works
	// and the quit keys close the program.
	msgRefused struct {
		reason string
	}
	// msgReset wipes the dashboard before a restarted crawl: the model
	// starts over as if the run had never happened.
	msgReset struct{}
)

// tickMsg refreshes the elapsed/ETA readouts once a second.
type tickMsg time.Time

// result is one processed page; rows appear only after a URL is done.
type result struct {
	url    string
	price  string
	failed bool
}

const (
	logTailLen    = 6
	urlColMin     = 24
	priceColWidth = 26
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	metaStyle    = lipgloss.NewStyle().Faint(true)
	logStyle     = lipgloss.NewStyle().Faint(true)
	spinStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	cursorStyle  = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("236"))
	helpStyle    = lipgloss.NewStyle().Faint(true)
	doneStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("46"))
	pauseStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	restartStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	errStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	priceStyle   = lipgloss.NewStyle().Width(priceColWidth).Align(lipgloss.Right)
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
	proxies   int    // proxy pool size; 0 hides the proxy line
	blocked   int    // proxies currently blocked by the error budget
	status    string // persistent process-wide note; "" hides the line

	start      time.Time
	cancel     func()     // invoked on ctrl+c/q; stops the crawler
	onPause    func(bool) // pushes space pause/resume to the crawler gate
	onRestart  func()     // fires r: the caller aborts the run and starts over
	onReady    func()     // fired once the event loop runs
	width      int
	height     int
	stopping   bool
	paused     bool   // space pause: no new urls are dequeued by the workers
	restarting bool   // r pressed: the view freezes until the fresh crawl lands
	finished   bool   // every url handled: note under the table, keys exit
	refused    string // the run never started: reason shown, keys like finished
}

func newModel(total int, cancel func(), onPause func(paused bool), onRestart func(), onReady func()) model {
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
		table:     tbl,
		bar:       progress.New(progress.WithScaledGradient("#5A56E0", "#EE6FF8")),
		spinner:   sp,
		total:     total,
		start:     time.Now(),
		cancel:    cancel,
		onPause:   onPause,
		onRestart: onRestart,
		onReady:   onReady,
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
			if m.restarting {
				// too late to stop anything: the run is already being
				// replaced, keys stay frozen until the fresh crawl lands
				return m, nil
			}
			if m.finished || m.refused != "" {
				// nothing left to cancel: these keys now close the
				// dashboard and end the program
				return m, tea.Quit
			}
			if !m.stopping && m.cancel != nil {
				m.cancel()
			}
			m.stopping = true
			m.setPaused(false) // the pause ends with the crawl
			return m, nil
		case " ":
			// the pause exists only while the crawl is running: after
			// completion or once a stop or restart was requested space
			// is inert — a refused run has nothing to pause either
			if m.stopping || m.restarting || m.finished || m.refused != "" || m.onPause == nil {
				return m, nil
			}
			m.setPaused(!m.paused)
			return m, nil
		case "r":
			// crawl the urls again from the first one: only a finished,
			// paused or refused run can be replaced — in-flight work is
			// winding down, not rewound
			if m.restarting || m.onRestart == nil || !(m.finished || m.paused || m.refused != "") {
				return m, nil
			}
			m.restarting = true
			m.finished = false // the completed run is being replaced
			m.reflow()
			m.onRestart()
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
		if m.restarting || m.stopping {
			// the aborted run's stragglers must not repaint the view
			return m, nil
		}
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
	case msgFail:
		if m.restarting || m.stopping {
			// the aborted run's stragglers must not repaint the view
			return m, nil
		}
		follow := m.table.Cursor() >= len(m.results)-1
		m.processed++
		m.results = append(m.results, result{url: msg.url, failed: true})
		m.syncRows()
		if follow {
			m.table.SetCursor(len(m.results) - 1)
		}
		m.markFinished()
	case msgInc:
		if m.restarting || m.stopping {
			return m, nil
		}
		m.processed++
		m.markFinished()
	case msgLog:
		if m.restarting {
			return m, nil
		}
		if msg.level == "INFO" {
			// results already live in the table; INFO lines would duplicate
			return m, nil
		}
		m.logs = append(m.logs, msg.line)
		if len(m.logs) > logTailLen {
			m.logs = m.logs[len(m.logs)-logTailLen:]
		}
		m.reflow()
	case msgProxy:
		// not dropped while restarting: the fresh pool reports itself right
		// after msgReset, and this line outlives the per-run reset anyway
		if msg.total != m.proxies || msg.blocked != m.blocked {
			m.proxies = msg.total
			m.blocked = msg.blocked
			m.reflow()
		}
	case msgStatus:
		// like the proxy line, the status note is process-wide and outlives
		// a per-run reset
		if msg.text != m.status {
			m.status = msg.text
			m.reflow()
		}
	case msgRefused:
		if m.refused != msg.reason {
			m.refused = msg.reason
			m.setPaused(false) // the pause is meaningless without a run
			m.reflow()
		}
	case msgReset:
		// the restarted crawl begins: drop everything the previous run
		// left behind. The proxy line is kept — the fresh pool re-reports
		// itself (unblocked, budgets reset) right after this.
		m.results = nil
		m.logs = nil
		m.processed = 0
		m.finished = false
		m.stopping = false
		m.paused = false
		m.restarting = false
		m.refused = ""
		m.start = time.Now()
		m.syncRows()
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
	switch {
	case m.restarting:
		b.WriteString(restartStyle.Render("↻ restarting"))
	case m.paused:
		b.WriteString(pauseStyle.Render("⏸ paused"))
	case m.refused == "" && m.processed < m.total:
		b.WriteString(m.spinner.View() + " ")
	}
	b.WriteString(titleStyle.Render("bandcamp crawler"))
	b.WriteString(fmt.Sprintf("  %d/%d (%.0f%%)", m.processed, m.total, pct*100))
	b.WriteString(metaStyle.Render(fmt.Sprintf("   elapsed %s   ETA %s", fmtDur(time.Since(m.start)), m.eta())))
	b.WriteString("\n")
	if m.proxies > 0 {
		b.WriteString(metaStyle.Render(fmt.Sprintf("   proxies: %d (blocked: %d)", m.proxies, m.blocked)))
		b.WriteString("\n")
	}
	if m.status != "" {
		b.WriteString(metaStyle.Render("   " + shorten(m.status, max(m.width-8, 20))))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.table.View())
	if m.finished {
		b.WriteString("\n")
		b.WriteString(doneStyle.Render("  ✓ all sources processed"))
	}
	if m.refused != "" {
		b.WriteString("\n")
		b.WriteString(errStyle.Render("  ✗ " + m.refused))
	}
	b.WriteString("\n\n")
	b.WriteString(m.bar.ViewAs(pct))
	b.WriteString("\n\n")
	b.WriteString(m.logsView())
	b.WriteString("\n ")
	switch {
	case m.restarting:
		b.WriteString(helpStyle.Render("restarting — crawling from the first url again…"))
	case m.stopping:
		b.WriteString(helpStyle.Render("stopping — aborting in-flight urls…"))
	case m.finished || m.refused != "":
		help := "↑/↓ — scroll   ctrl+c or q — exit"
		if m.onRestart != nil {
			help = "↑/↓ — scroll   r — restart   ctrl+c or q — exit"
		}
		b.WriteString(helpStyle.Render(help))
	default:
		pause := "space — pause"
		if m.paused {
			pause = "space — resume"
		}
		help := "↑/↓ — scroll   " + pause + "   ctrl+c — stop"
		if !m.following() {
			help = "↑/↓ — scroll   G — latest   " + pause + "   ctrl+c — stop"
		}
		if m.paused && m.onRestart != nil {
			help += "   r — restart"
		}
		b.WriteString(helpStyle.Render(help))
	}
	return b.String()
}

// following reports whether the table cursor rests on the newest row.
func (m model) following() bool {
	return len(m.results) == 0 || m.table.Cursor() >= len(m.results)-1
}

// setPaused flips the space pause and pushes it to the crawler gate.
func (m *model) setPaused(paused bool) {
	if m.paused == paused {
		return
	}
	m.paused = paused
	if m.onPause != nil {
		m.onPause(paused)
	}
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
	m.setPaused(false) // the pause is over once the crawl is done
	m.reflow()
}

// eta estimates the remaining time from the current processing rate.
func (m model) eta() string {
	switch {
	case m.total == 0 || m.processed >= m.total, m.paused, m.refused != "":
		// paused: the queue is held, so there is no meaningful estimate
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
	if m.proxies > 0 {
		fixed++ // the proxy line under the title
	}
	if m.status != "" {
		fixed++ // the status note under the proxy line
	}
	if m.finished || m.refused != "" {
		fixed++ // the note under the table
	}
	m.table.SetHeight(max(3, m.height-logLines-fixed))
}

// syncRows rebuilds the table rows from the results, oldest first. The price
// cell aligns through lipgloss, so the styled red "error" keeps its width.
func (m *model) syncRows() {
	urlWidth := m.table.Columns()[0].Width
	rows := make([]table.Row, len(m.results))
	for i, r := range m.results {
		price := r.price
		if r.failed {
			price = errStyle.Render("error")
		}
		rows[i] = table.Row{
			shorten(r.url, max(urlWidth-2, 8)),
			priceStyle.Render(price),
		}
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
