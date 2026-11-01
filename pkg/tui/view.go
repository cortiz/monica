package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"jmpeax.com/sec/monica/pkg/report"
)

var errNoFiles = errors.New("no .mon files")

var (
	borderFocused = lipgloss.AdaptiveColor{Light: "#2563eb", Dark: "#7aa2f7"}
	borderDim     = lipgloss.AdaptiveColor{Light: "#d4d4d8", Dark: "#3b3f51"}
	dim           = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#71717a", Dark: "#737aa2"})
	bold          = lipgloss.NewStyle().Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#9ece6a"})
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f7768e"})
	selStyle      = lipgloss.NewStyle().Reverse(true)
	tabActive     = lipgloss.NewStyle().Bold(true).Underline(true)
	methodStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#7e22ce", Dark: "#bb9af7"})
)

// Pane sizes, from the window size.
func (m *Model) treeWidth() int {
	w := m.width / 3
	if w < 24 {
		w = 24
	}
	if w > 48 {
		w = 48
	}
	return w
}

func (m *Model) rightWidth() int { return max0(m.width - m.treeWidth()) }

// bodyHeight is the height of the area between the header and the footer.
func (m *Model) bodyHeight() int { return max0(m.height - 2) }

func (m *Model) treeHeight() int { return max0(m.bodyHeight() - 2) }

func (m *Model) requestHeight() int {
	h := m.bodyHeight() * 2 / 5
	if h < 5 {
		h = 5
	}
	return h
}

func (m *Model) responseHeight() int { return max0(m.bodyHeight() - m.requestHeight()) }

// layout resizes the response viewport after a size change.
func (m *Model) layout() {
	m.resp.Width = max0(m.rightWidth() - 2)
	m.resp.Height = max0(m.responseHeight() - 4) // borders, tabs, status
	m.move(0)
	m.refreshResponse()
}

// refreshResponse fills the viewport with the shown result's current tab.
func (m *Model) refreshResponse() {
	m.resp.SetContent(m.tabContent())
	m.resp.GotoTop()
}

func (m *Model) tabContent() string {
	if m.tab == tabLog {
		lines := m.opts.Log.Lines()
		if len(lines) == 0 {
			return dim.Render("No log output yet (console.log from scripts lands here).")
		}
		return strings.Join(lines, "\n")
	}
	r := m.shown
	if r == nil {
		return dim.Render("Select a request and press enter to send it.")
	}
	switch m.tab {
	case tabBody:
		if r.ResponseStatus == "" {
			if r.Error != "" {
				return errStyle.Render(r.Error)
			}
			if r.Skipped {
				return dim.Render("skipped: " + r.SkipReason)
			}
			return dim.Render("No response.")
		}
		if r.ResponseBody == "" {
			return dim.Render("(empty body)")
		}
		body, _ := prettyBody(r.ResponseBody, true)
		return wrap(body, m.resp.Width)
	case tabHeaders:
		if r.ResponseStatus == "" {
			return dim.Render("No response.")
		}
		names := make([]string, 0, len(r.ResponseHeaders))
		for n := range r.ResponseHeaders {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		b.WriteString(bold.Render(r.ResponseStatus) + "\n")
		for _, n := range names {
			for _, v := range r.ResponseHeaders[n] {
				b.WriteString(jsonKey.Render(n) + ": " + v + "\n")
			}
		}
		return wrap(strings.TrimRight(b.String(), "\n"), m.resp.Width)
	case tabTimings:
		if r.ResponseStatus == "" {
			return dim.Render("No response.")
		}
		t := r.Timings
		rows := [][2]string{
			{"DNS lookup", fmtDur(t.DNS)}, {"TCP connect", fmtDur(t.Connect)}, {"TLS handshake", fmtDur(t.TLS)},
			{"Waiting (TTFB)", fmtDur(t.Waiting)}, {"Receiving", fmtDur(t.Receiving)}, {"Total", fmtDur(r.Duration)},
			{"Sent", fmt.Sprintf("%d B body", r.BytesSent)}, {"Received", fmt.Sprintf("%d B body", r.BytesReceived)},
		}
		var b strings.Builder
		for _, row := range rows {
			fmt.Fprintf(&b, "%-15s %s\n", row[0], row[1])
		}
		return strings.TrimRight(b.String(), "\n")
	case tabTests:
		var b strings.Builder
		if r.Error != "" {
			b.WriteString(errStyle.Render("✗ "+r.Error) + "\n")
		}
		if len(r.Tests) == 0 && r.Error == "" {
			return dim.Render("No tests.")
		}
		for _, t := range r.Tests {
			if t.Passed {
				b.WriteString(okStyle.Render("✓") + " " + t.Name + "\n")
			} else {
				b.WriteString(errStyle.Render("✗") + " " + t.Name + "\n")
				for _, l := range strings.Split(t.Error, "\n") {
					b.WriteString("    " + dim.Render(l) + "\n")
				}
			}
		}
		return wrap(strings.TrimRight(b.String(), "\n"), m.resp.Width)
	}
	return ""
}

// View implements tea.Model.
func (m *Model) View() string {
	header := m.headerLine()
	if m.overlay == overlayLoad {
		// The load summary is a wide table: give it the whole window.
		body := box(m.loadView(m.width-4), m.width, m.bodyHeight(), true)
		return lipgloss.JoinVertical(lipgloss.Left, header, body, m.footerLine())
	}
	var right string
	if m.overlay != overlayNone {
		right = box(m.overlayView(), m.rightWidth(), m.bodyHeight(), true)
	} else {
		right = lipgloss.JoinVertical(lipgloss.Left, m.requestPane(), m.responsePane())
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.treePane(), right)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, m.footerLine())
}

func (m *Model) headerLine() string {
	s := bold.Render("monica") + dim.Render(" · env ") + envName(m.backend.Env())
	if n := len(m.history); n > 0 {
		s += dim.Render(fmt.Sprintf(" · %d sent", n))
	}
	if m.busy {
		s += "  " + m.spin.View() + " sending " + m.sending
	}
	if m.load.running {
		s += "  " + m.spin.View() + " load test running (L)"
	}
	return truncate(s, m.width)
}

func (m *Model) footerLine() string {
	if m.status != "" {
		st := m.status
		if m.statusErr {
			st = errStyle.Render(st)
		}
		return truncate(st, m.width)
	}
	return truncate(dim.Render("enter send · tab focus · 1-5 tabs · e env · v vars · H history · o edit · r reload · L load · ? help · q quit"), m.width)
}

func (m *Model) treePane() string {
	w, h := m.treeWidth(), m.treeHeight()
	var lines []string
	if m.treeErr != nil {
		lines = append(lines, errStyle.Render(m.treeErr.Error()))
	}
	if len(m.rows) == 0 && m.treeErr == nil {
		lines = append(lines, dim.Render("No .mon files here."), dim.Render("monica init creates one."))
	}
	end := m.treeTop + h
	if end > len(m.rows) {
		end = len(m.rows)
	}
	for i := m.treeTop; i < end; i++ {
		n := m.rows[i]
		line := strings.Repeat("  ", n.depth) + m.rowText(n)
		line = truncate(line, w-2)
		if i == m.cursor {
			line = highlight(line, w-2)
		}
		lines = append(lines, line)
	}
	return box(strings.Join(lines, "\n"), w, m.bodyHeight(), m.focus == focusTree && m.overlay == overlayNone)
}

func (m *Model) rowText(n *node) string {
	switch n.kind {
	case kindDir:
		return arrow(n.open) + n.name + "/"
	case kindFile:
		s := arrow(n.open) + n.name
		if n.err != nil {
			s += " " + errStyle.Render("!")
		}
		return s
	default:
		mark := " "
		if res, ok := m.results[n.key()]; ok {
			switch {
			case res.Skipped:
				mark = dim.Render("-")
			case res.Failed():
				mark = errStyle.Render("✗")
			default:
				mark = okStyle.Render("✓")
			}
		}
		label := n.name
		if n.req.Name == "" && n.req.Title == "" {
			label = n.req.URL // "METHOD URL" would repeat the method
		}
		return mark + " " + methodStyle.Render(fmt.Sprintf("%-4s", shortMethod(n.req.Method))) + " " + label
	}
}

func (m *Model) requestPane() string {
	w, h := m.rightWidth(), m.requestHeight()
	n := m.selected()
	var content string
	switch {
	case n == nil:
		content = dim.Render("Nothing selected.")
	case n.kind == kindDir:
		content = bold.Render(n.path+"/") + "\n" + dim.Render(fmt.Sprintf("%d requests", countRequests(n)))
	case n.kind == kindFile && n.err != nil:
		content = bold.Render(n.path) + "\n" + errStyle.Render(n.err.Error())
	case n.kind == kindFile:
		content = bold.Render(n.path) + "\n" + dim.Render(fmt.Sprintf("%d requests · o opens it in $EDITOR · L load-tests it", len(n.children)))
	default:
		content = dim.Render(fmt.Sprintf("%s:%d", n.path, n.req.Line)) + "\n" + section(n.req)
	}
	return box(clipLines(content, h-2, w-2), w, h, false)
}

func (m *Model) responsePane() string {
	w, h := m.rightWidth(), m.responseHeight()
	var tabs []string
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab {
			tabs = append(tabs, tabActive.Render(label))
		} else {
			tabs = append(tabs, dim.Render(label))
		}
	}
	status := dim.Render("—")
	if m.busy {
		status = m.spin.View() + " sending " + m.sending + dim.Render(" (esc cancels)")
	} else if m.shown != nil {
		status = resultLine(*m.shown)
		if m.shown.Failed() {
			status = errStyle.Render(status)
		}
	}
	content := truncate(strings.Join(tabs, dim.Render(" │ ")), w-2) + "\n" + truncate(status, w-2) + "\n" + m.resp.View()
	return box(content, w, h, m.focus == focusResponse && m.overlay == overlayNone)
}

func (m *Model) overlayView() string {
	w := m.rightWidth() - 4
	switch m.overlay {
	case overlayEnv:
		var b strings.Builder
		b.WriteString(bold.Render("Environment") + dim.Render("  enter select · esc close") + "\n\n")
		for i, e := range m.envChoices() {
			line := "  " + envName(e)
			if e == m.backend.Env() {
				line += dim.Render("  (current)")
			}
			if i == m.envCursor {
				line = highlight(line, w)
			}
			b.WriteString(line + "\n")
		}
		if len(m.backend.Envs()) == 0 {
			b.WriteString("\n" + dim.Render("monica.yml has no envs: section."))
		}
		b.WriteString("\n" + dim.Render("Switching starts a new session: vars.set values and cookies are dropped."))
		return b.String()
	case overlayVars:
		var b strings.Builder
		b.WriteString(bold.Render("Variables") + dim.Render(" for "+m.varsFile+" · env "+envName(m.backend.Env())+" · esc close") + "\n\n")
		switch {
		case m.varsErr != nil:
			b.WriteString(errStyle.Render(m.varsErr.Error()))
		case m.vars == nil:
			b.WriteString(dim.Render("loading…"))
		case len(m.vars) == 0:
			b.WriteString(dim.Render("No variables."))
		}
		width := 0
		for _, v := range m.vars {
			if len(v.Name) > width {
				width = len(v.Name)
			}
		}
		for _, v := range m.vars {
			b.WriteString(truncate(fmt.Sprintf("%-*s = %s", width, v.Name, v.Value)+dim.Render("  ("+v.Source+")"), w) + "\n")
		}
		b.WriteString("\n" + dim.Render("Secrets from ${NAME} show as ****. Built-ins: {{$uuid}} {{$timestamp}} {{$randomInt}}"))
		return b.String()
	case overlayHistory:
		var b strings.Builder
		b.WriteString(bold.Render("History") + dim.Render("  enter show · esc close") + "\n\n")
		if len(m.history) == 0 {
			b.WriteString(dim.Render("Nothing sent yet."))
		}
		lines := m.bodyHeight() - 6
		start := clamp(m.histCursor-lines+1, 0, len(m.history))
		for i := start; i < len(m.history) && i < start+lines; i++ {
			e := m.history[len(m.history)-1-i]
			line := fmt.Sprintf("%s  %s  %s", e.at.Format("15:04:05"), resultLine(e.res), dim.Render(fmt.Sprintf("%s:%d [%s]", e.res.File, e.res.Line, envName(e.env))))
			line = truncate(line, w)
			if i == m.histCursor {
				line = highlight(line, w)
			}
			b.WriteString(line + "\n")
		}
		return b.String()
	case overlayHelp:
		return bold.Render("Keys") + "\n\n" + strings.Join([]string{
			"↑/↓ j/k       move          ←/→ h/l   fold / unfold",
			"enter, s      send the request (enter on a folder folds it)",
			"esc, x        cancel the running request",
			"tab           switch focus between tree and response",
			"1-5, [ ]      response tabs: body, headers, timings, tests, log",
			"↑/↓ pgup/pgdn scroll the response (when focused)",
			"e             pick the environment",
			"v             variables the selected request would see",
			"H             history of this session's sends",
			"o             open the file in $EDITOR (reloads after)",
			"r             reload files (also automatic when they change)",
			"L             load-test the selected file (load: in monica.yml)",
			"q, ctrl+c     quit",
		}, "\n") + "\n\n" + dim.Render("Sends share one session: vars.set values and cookies carry over, as in one monica run.")
	}
	return ""
}

func (m *Model) loadView(w int) string {
	l := m.load
	var b strings.Builder
	b.WriteString(bold.Render("Load test") + dim.Render(" "+l.file) + "\n\n")
	if l.err != nil {
		b.WriteString(errStyle.Render(l.err.Error()) + "\n\n" + dim.Render("esc close"))
		return b.String()
	}
	p := l.progress
	if l.running || l.summary == "" {
		elapsed := fmtDur(p.Elapsed.Round(time.Second))
		if p.Planned > 0 {
			elapsed += " / " + fmtDur(p.Planned) + "  " + bar(float64(p.Elapsed)/float64(p.Planned), w-24)
		}
		failRate := 0.0
		if p.Requests > 0 {
			failRate = float64(p.Failed) / float64(p.Requests) * 100
		}
		fmt.Fprintf(&b, "elapsed     %s\n", elapsed)
		fmt.Fprintf(&b, "VUs         %d active / %d started\n", p.ActiveVUs, p.VUs)
		fmt.Fprintf(&b, "iterations  %d\n", p.Iterations)
		fmt.Fprintf(&b, "requests    %d · %.1f rps\n", p.Requests, p.RPS)
		fmt.Fprintf(&b, "p95         %s\n", fmtDur(p.P95))
		failed := fmt.Sprintf("%d (%.2f%%)", p.Failed, failRate)
		if p.Failed > 0 {
			failed = errStyle.Render(failed)
		}
		fmt.Fprintf(&b, "failed      %s\n\n", failed)
		b.WriteString("rps " + sparkline(l.rps, w-6) + "\n\n")
		b.WriteString(dim.Render("esc stops the test (the summary so far is shown)"))
		return b.String()
	}
	summary := strings.NewReplacer("  ✓ ", "  "+okStyle.Render("✓")+" ", "  ✗ ", "  "+errStyle.Render("✗")+" ").Replace(l.summary)
	b.WriteString(summary + "\n" + dim.Render("esc close · L runs it again after closing"))
	return b.String()
}

// resultLine is a one-line summary of a result.
func resultLine(r report.Request) string {
	label := r.Name
	switch {
	case r.Skipped:
		return "- " + label + "  skipped " + r.SkipReason
	case r.ResponseStatus == "" && r.Error != "":
		return "✗ " + label + "  " + firstLine(r.Error)
	}
	mark := "✓"
	if r.Failed() {
		mark = "✗"
	}
	s := fmt.Sprintf("%s %s  %d %s · %s", mark, label, r.Status, r.StatusText, fmtDur(r.Duration))
	if n := len(r.Tests); n > 0 {
		passed := 0
		for _, t := range r.Tests {
			if t.Passed {
				passed++
			}
		}
		s += fmt.Sprintf(" · %d/%d tests", passed, n)
	}
	if r.Attempts > 1 {
		s += fmt.Sprintf(" · %d attempts", r.Attempts)
	}
	return s
}

func envName(e string) string {
	if e == "" {
		return "(defaults)"
	}
	return e
}

// box draws content in a rounded border of the given outer size.
func box(content string, w, h int, focused bool) string {
	color := borderDim
	if focused {
		color = borderFocused
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).
		Width(max0(w - 2)).Height(max0(h - 2)).MaxHeight(max0(h)).
		Render(clipLines(content, max0(h-2), max0(w-2)))
}

// clipLines keeps at most n lines of s, each cut to width w.
func clipLines(s string, n, w int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	for i, l := range lines {
		lines[i] = truncate(l, w)
	}
	return strings.Join(lines, "\n")
}

// truncate cuts s to w display cells (ANSI-aware).
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(s)
}

// highlight shows a selected line in reverse video across width w. Inner
// colors are dropped: their resets would end the reverse video early.
func highlight(line string, w int) string {
	return selStyle.Render(padRight(ansi.Strip(line), w))
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// wrap breaks lines longer than w (bodies are often one long line).
func wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	return lipgloss.NewStyle().Width(w).Render(s)
}

func arrow(open bool) string {
	if open {
		return "▾ "
	}
	return "▸ "
}

func shortMethod(m string) string {
	if m == "DELETE" {
		return "DEL"
	}
	if m == "OPTIONS" {
		return "OPT"
	}
	if len(m) > 5 {
		return m[:5]
	}
	return m
}

func countRequests(n *node) int { return len(requests(n)) }

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < time.Second:
		return d.Round(10 * time.Microsecond).String()
	default:
		return d.Round(time.Millisecond).String()
	}
}

func bar(frac float64, w int) string {
	if w < 4 {
		return ""
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac * float64(w))
	return "[" + strings.Repeat("█", n) + strings.Repeat("·", w-n) + "]"
}

// sparkline draws the last w values as block characters.
func sparkline(vals []float64, w int) string {
	if len(vals) == 0 || w <= 0 {
		return dim.Render("…")
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	top := 0.0
	for _, v := range vals {
		if v > top {
			top = v
		}
	}
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if top > 0 {
			i = int(v / top * float64(len(blocks)-1))
		}
		b.WriteRune(blocks[i])
	}
	return b.String() + dim.Render(fmt.Sprintf(" max %.1f", top))
}
