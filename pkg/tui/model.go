// Package tui is the interactive terminal UI (`monica tui`): a collection
// tree, the selected request, and its response. It sends requests through
// the same runner as `monica run` (see Backend), so scripts, hooks,
// variables and cookies behave the same.
package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

type focus int

const (
	focusTree focus = iota
	focusResponse
)

type tab int

const (
	tabBody tab = iota
	tabHeaders
	tabTimings
	tabTests
	tabLog
	tabCount
)

var tabNames = [tabCount]string{"Body", "Headers", "Timings", "Tests", "Log"}

type overlay int

const (
	overlayNone overlay = iota
	overlayEnv
	overlayVars
	overlayHistory
	overlayLoad
	overlayHelp
)

// maxHistory bounds the session's run history.
const maxHistory = 200

// pollInterval is how often files are checked for changes.
const pollInterval = 2 * time.Second

// Options configure New.
type Options struct {
	// Files lists the .mon files in run order; it is called again on
	// reload.
	Files func() ([]string, error)
	// Editor is the command that opens a file ("" = $VISUAL, $EDITOR, vi).
	Editor string
	// Log holds the captured log output, for the Log tab; may be nil.
	Log *LogBuffer
	// NoPoll turns off watching files for changes (tests).
	NoPoll bool
}

type historyEntry struct {
	at  time.Time
	env string
	res report.Request
}

type loadState struct {
	file     string
	running  bool
	cancel   context.CancelFunc
	ch       chan tea.Msg
	progress load.Progress
	rps      []float64 // per progress tick, for the sparkline
	summary  string
	err      error
}

// Model is the TUI's bubbletea model.
type Model struct {
	backend Backend
	opts    Options

	root    *node
	rows    []*node
	cursor  int
	treeTop int
	treeErr error

	focus   focus
	tab     tab
	overlay overlay

	results map[string]report.Request // last result by request key
	shown   *report.Request           // in the response pane
	history []historyEntry

	busy    bool
	cancel  context.CancelFunc
	sending string

	envCursor  int
	vars       []runner.Variable
	varsErr    error
	varsFile   string
	histCursor int
	load       loadState

	width, height int
	status        string
	statusErr     bool
	resp          viewport.Model
	spin          spinner.Model
	mtimes        map[string]time.Time
	now           func() time.Time
}

// New returns the model; call Init through tea.NewProgram.
func New(b Backend, opts Options) *Model {
	m := &Model{
		backend: b, opts: opts,
		results: map[string]report.Request{},
		width:   100, height: 30,
		resp: viewport.New(60, 10),
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		now:  time.Now,
	}
	m.reload()
	m.layout()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.statCmd())
}

// Messages produced by commands.
type (
	sentMsg struct {
		key string
		res report.Request
		err error
	}
	envSetMsg struct{ err error }
	varsMsg   struct {
		vars []runner.Variable
		err  error
	}
	editorDoneMsg   struct{ err error }
	statMsg         map[string]time.Time
	loadProgressMsg load.Progress
	loadDoneMsg     struct {
		summary string
		err     error
	}
)

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		if m.focus == focusResponse {
			var cmd tea.Cmd
			m.resp, cmd = m.resp.Update(msg)
			return m, cmd
		}
		return m, nil
	case sentMsg:
		m.busy, m.cancel = false, nil
		m.results[msg.key] = msg.res
		res := msg.res
		m.shown = &res
		m.history = append(m.history, historyEntry{at: m.now(), env: m.backend.Env(), res: msg.res})
		if len(m.history) > maxHistory {
			m.history = m.history[len(m.history)-maxHistory:]
		}
		m.setStatus(resultLine(msg.res), msg.res.Failed())
		m.refreshResponse()
		return m, nil
	case envSetMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else {
			m.results = map[string]report.Request{}
			m.shown = nil
			m.setStatus("environment: "+envName(m.backend.Env())+" (new session: script variables and cookies cleared)", false)
			m.refreshResponse()
		}
		return m, nil
	case varsMsg:
		m.vars, m.varsErr = msg.vars, msg.err
		return m, nil
	case editorDoneMsg:
		if msg.err != nil {
			m.setStatus("editor: "+msg.err.Error(), true)
		}
		m.reload()
		return m, nil
	case statMsg:
		changed := len(m.mtimes) > 0 && !sameTimes(m.mtimes, msg)
		m.mtimes = msg
		if changed {
			m.reload()
			m.setStatus("files changed on disk: reloaded", false)
		}
		return m, m.pollCmd()
	case loadProgressMsg:
		m.load.progress = load.Progress(msg)
		m.load.rps = append(m.load.rps, msg.RPS)
		if len(m.load.rps) > 60 {
			m.load.rps = m.load.rps[len(m.load.rps)-60:]
		}
		return m, waitFor(m.load.ch)
	case loadDoneMsg:
		m.load.running, m.load.cancel = false, nil
		m.load.summary, m.load.err = msg.summary, msg.err
		if msg.err != nil {
			m.setStatus("load test: "+msg.err.Error(), true)
		} else {
			m.setStatus("load test finished", false)
		}
		return m, nil
	}
	return m, nil
}

// key handles a key press.
func (m *Model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		return m.quit()
	}
	if m.overlay != overlayNone {
		return m.overlayKey(s)
	}
	switch s {
	case "q":
		return m.quit()
	case "?":
		m.overlay = overlayHelp
		return nil
	case "tab", "shift+tab":
		if m.focus == focusTree {
			m.focus = focusResponse
		} else {
			m.focus = focusTree
		}
		return nil
	case "1", "2", "3", "4", "5":
		m.tab = tab(s[0] - '1')
		m.refreshResponse()
		return nil
	case "]":
		m.tab = (m.tab + 1) % tabCount
		m.refreshResponse()
		return nil
	case "[":
		m.tab = (m.tab + tabCount - 1) % tabCount
		m.refreshResponse()
		return nil
	case "esc", "x":
		if m.busy && m.cancel != nil {
			m.cancel()
			m.setStatus("cancelling…", false)
		}
		return nil
	case "e":
		m.overlay = overlayEnv
		m.envCursor = 0
		for i, e := range m.envChoices() {
			if e == m.backend.Env() {
				m.envCursor = i
			}
		}
		return nil
	case "v":
		m.overlay = overlayVars
		m.vars, m.varsErr, m.varsFile = nil, nil, m.currentFile()
		return m.varsCmd()
	case "H":
		m.overlay = overlayHistory
		m.histCursor = 0
		return nil
	case "L":
		return m.startLoad()
	case "r":
		m.reload()
		m.setStatus("reloaded", false)
		return nil
	case "o":
		return m.openEditor()
	}
	if m.focus == focusResponse {
		var cmd tea.Cmd
		m.resp, cmd = m.resp.Update(k)
		return cmd
	}
	return m.treeKey(s)
}

func (m *Model) treeKey(s string) tea.Cmd {
	n := m.selected()
	switch s {
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-m.treeHeight())
	case "pgdown":
		m.move(m.treeHeight())
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	case "left", "h":
		switch {
		case n == nil:
		case n.kind != kindRequest && n.open:
			n.open = false
			m.rows = visible(m.root)
		default:
			m.toParent()
		}
	case "right", "l":
		if n != nil && n.kind != kindRequest && !n.open {
			n.open = true
			m.rows = visible(m.root)
		}
	case " ":
		if n != nil && n.kind != kindRequest {
			n.open = !n.open
			m.rows = visible(m.root)
		}
	case "enter", "s":
		if n == nil {
			return nil
		}
		if n.kind != kindRequest {
			if s == "enter" {
				n.open = !n.open
				m.rows = visible(m.root)
			}
			return nil
		}
		return m.send(n)
	}
	m.showSelected()
	return nil
}

func (m *Model) overlayKey(s string) tea.Cmd {
	switch m.overlay {
	case overlayEnv:
		envs := m.envChoices()
		switch s {
		case "up", "k":
			m.envCursor = clamp(m.envCursor-1, 0, len(envs)-1)
		case "down", "j":
			m.envCursor = clamp(m.envCursor+1, 0, len(envs)-1)
		case "enter":
			m.overlay = overlayNone
			if m.busy || m.load.running {
				m.setStatus("wait for the running request or load test first", true)
				return nil
			}
			env := envs[m.envCursor]
			if env == m.backend.Env() {
				return nil
			}
			return func() tea.Msg { return envSetMsg{m.backend.SetEnv(env)} }
		case "esc", "q", "e":
			m.overlay = overlayNone
		}
	case overlayHistory:
		switch s {
		case "up", "k":
			m.histCursor = clamp(m.histCursor-1, 0, len(m.history)-1)
		case "down", "j":
			m.histCursor = clamp(m.histCursor+1, 0, len(m.history)-1)
		case "enter":
			if len(m.history) > 0 {
				res := m.history[len(m.history)-1-m.histCursor].res
				m.shown = &res
				m.refreshResponse()
			}
			m.overlay = overlayNone
		case "esc", "q", "H":
			m.overlay = overlayNone
		}
	case overlayLoad:
		switch s {
		case "esc", "x":
			if m.load.running && m.load.cancel != nil {
				m.load.cancel()
				m.setStatus("stopping the load test…", false)
				return nil
			}
			m.overlay = overlayNone
		case "q", "L":
			if !m.load.running {
				m.overlay = overlayNone
			}
		}
	default: // vars, help
		switch s {
		case "esc", "q", "v", "?", "enter":
			m.overlay = overlayNone
		}
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.load.cancel != nil {
		m.load.cancel()
	}
	return tea.Quit
}

// send starts the request of node n.
func (m *Model) send(n *node) tea.Cmd {
	if m.busy {
		m.setStatus("a request is running (esc cancels it)", true)
		return nil
	}
	if m.load.running {
		m.setStatus("a load test is running (L shows it)", true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.busy, m.cancel, m.sending = true, cancel, n.name
	m.setStatus("", false)
	key, file, line := n.key(), n.path, n.req.Line
	b := m.backend
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		defer cancel()
		res, err := b.Send(ctx, file, line)
		return sentMsg{key: key, res: res, err: err}
	})
}

func (m *Model) varsCmd() tea.Cmd {
	file, b := m.varsFile, m.backend
	if file == "" {
		return func() tea.Msg { return varsMsg{err: errNoFiles} }
	}
	return func() tea.Msg {
		vars, err := b.Variables(file)
		return varsMsg{vars: vars, err: err}
	}
}

// startLoad runs a load test of the selected file; progress arrives as
// loadProgressMsg through m.load.ch.
func (m *Model) startLoad() tea.Cmd {
	m.overlay = overlayLoad
	if m.load.running {
		return nil
	}
	if m.busy {
		m.setStatus("wait for the running request first", true)
		m.overlay = overlayNone
		return nil
	}
	file := m.currentFile()
	if file == "" {
		m.load = loadState{err: errNoFiles}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 8)
	m.load = loadState{file: file, running: true, cancel: cancel, ch: ch}
	b := m.backend
	go func() {
		defer close(ch)
		defer cancel()
		summary, err := b.Load(ctx, file, func(p load.Progress) {
			select {
			case ch <- loadProgressMsg(p):
			default: // the UI is behind: drop this tick
			}
		})
		ch <- loadDoneMsg{summary: summary, err: err}
	}()
	return waitFor(ch)
}

// waitFor delivers the next message from ch.
func waitFor(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *Model) openEditor() tea.Cmd {
	n := m.selected()
	if n == nil || n.kind == kindDir {
		m.setStatus("select a file or request to edit", true)
		return nil
	}
	line := 1
	if n.req != nil {
		line = n.req.Line
	}
	args := editorArgs(m.opts.Editor, n.path, line)
	c := exec.Command(args[0], args[1:]...) //nolint:gosec // the user's own $EDITOR
	return tea.ExecProcess(c, func(err error) tea.Msg { return editorDoneMsg{err} })
}

// editorArgs builds the command that opens file at line.
func editorArgs(editor, file string, line int) []string {
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	args := strings.Fields(editor)
	switch filepath.Base(args[0]) {
	case "code", "codium", "code-insiders", "cursor":
		has := false
		for _, a := range args[1:] {
			has = has || a == "-w" || a == "--wait"
		}
		if !has {
			args = append(args, "--wait")
		}
		return append(args, "--goto", file+":"+strconv.Itoa(line))
	case "subl", "zed":
		return append(args, file+":"+strconv.Itoa(line))
	default: // vi, vim, nvim, nano, emacs, micro, hx, kak, ...
		return append(args, "+"+strconv.Itoa(line), file)
	}
}

// statCmd reads the files' modification times.
func (m *Model) statCmd() tea.Cmd {
	if m.opts.NoPoll {
		return nil
	}
	paths := requestsAndFiles(m.root)
	return func() tea.Msg { return statFiles(paths) }
}

func (m *Model) pollCmd() tea.Cmd {
	if m.opts.NoPoll {
		return nil
	}
	paths := requestsAndFiles(m.root)
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return statFiles(paths) })
}

func statFiles(paths []string) statMsg {
	out := statMsg{}
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out[p] = fi.ModTime()
		} else {
			out[p] = time.Time{}
		}
	}
	return out
}

func sameTimes(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !w.Equal(v) {
			return false
		}
	}
	return true
}

// requestsAndFiles lists the file paths in the tree.
func requestsAndFiles(root *node) []string {
	var out []string
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			if c.kind == kindFile {
				out = append(out, c.path)
			}
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return out
}

// reload rebuilds the tree, keeping the selection and open folders.
func (m *Model) reload() {
	var selKey string
	closed := map[string]bool{}
	if n := m.selected(); n != nil {
		selKey = n.key()
	}
	if m.root != nil {
		var walk func(n *node)
		walk = func(n *node) {
			for _, c := range n.children {
				if c.kind != kindRequest && !c.open {
					closed[c.key()] = true
				}
				walk(c)
			}
		}
		walk(m.root)
	}
	files, err := m.opts.Files()
	m.treeErr = err
	m.root = buildTree(files)
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			if closed[c.key()] {
				c.open = false
			}
			walk(c)
		}
	}
	walk(m.root)
	m.rows = visible(m.root)
	m.cursor = 0
	for i, r := range m.rows {
		if r.key() == selKey {
			m.cursor = i
			break
		}
	}
	m.move(0)
}

// move moves the cursor by d rows and keeps it on screen.
func (m *Model) move(d int) {
	m.cursor = clamp(m.cursor+d, 0, len(m.rows)-1)
	h := m.treeHeight()
	if m.cursor < m.treeTop {
		m.treeTop = m.cursor
	}
	if m.cursor >= m.treeTop+h {
		m.treeTop = m.cursor - h + 1
	}
	m.treeTop = clamp(m.treeTop, 0, max0(len(m.rows)-h))
}

func (m *Model) toParent() {
	n := m.selected()
	if n == nil {
		return
	}
	for i := m.cursor - 1; i >= 0; i-- {
		if m.rows[i].depth < n.depth {
			m.move(i - m.cursor)
			return
		}
	}
}

// showSelected shows the selected request's last result, if any.
func (m *Model) showSelected() {
	n := m.selected()
	if n == nil || n.kind != kindRequest {
		return
	}
	if res, ok := m.results[n.key()]; ok {
		m.shown = &res
	} else {
		m.shown = nil
	}
	m.refreshResponse()
}

func (m *Model) selected() *node {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor]
}

// currentFile is the selected node's file, else the first file.
func (m *Model) currentFile() string {
	if n := m.selected(); n != nil && n.kind != kindDir {
		return n.path
	}
	if files := requestsAndFiles(m.root); len(files) > 0 {
		return files[0]
	}
	return ""
}

// envChoices lists "" (defaults only) and the config's environments.
func (m *Model) envChoices() []string {
	return append([]string{""}, m.backend.Envs()...)
}

func (m *Model) setStatus(s string, isErr bool) { m.status, m.statusErr = s, isErr }

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
