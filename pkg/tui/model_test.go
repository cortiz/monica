package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
	"jmpeax.com/sec/monica/pkg/script"
)

// fakeBackend records calls and answers from canned results.
type fakeBackend struct {
	mu      sync.Mutex
	env     string
	envs    []string
	sends   []string // "file:line"
	results map[int]report.Request
	block   chan struct{} // when set, Send waits for it or ctx
	vars    []runner.Variable
	loadRun func(ctx context.Context, progress func(load.Progress)) (string, error)
}

func (f *fakeBackend) Envs() []string { return f.envs }
func (f *fakeBackend) Env() string    { return f.env }
func (f *fakeBackend) SetEnv(name string) error {
	if name == "broken" {
		return errors.New("broken env")
	}
	f.env = name
	return nil
}

func (f *fakeBackend) Send(ctx context.Context, file string, line int) (report.Request, error) {
	f.mu.Lock()
	f.sends = append(f.sends, filepath.Base(file)+":"+itoa(line))
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return report.Request{File: file, Line: line, Error: "canceled"}, ctx.Err()
		}
	}
	res, ok := f.results[line]
	if !ok {
		res = report.Request{File: file, Line: line, Name: "r", Status: 200, StatusText: "OK", ResponseStatus: "HTTP/1.1 200 OK", ResponseBody: "{}"}
	}
	return res, nil
}

func (f *fakeBackend) Variables(string) ([]runner.Variable, error) { return f.vars, nil }

func (f *fakeBackend) Load(ctx context.Context, _ string, progress func(load.Progress)) (string, error) {
	return f.loadRun(ctx, progress)
}

func (f *fakeBackend) Close() error { return nil }

func itoa(n int) string { return strconv.Itoa(n) }

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// project writes a small collection and returns its files in run order.
func project(t *testing.T) (dir string, files []string) {
	t.Helper()
	dir = t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("api/users.mon", "### List users\n# @name list\nGET https://h/users\n\n### Create\nPOST https://h/users\nContent-Type: application/json\n\n{\"name\": \"ada\"}\n")
	b := write("health.mon", "GET https://h/health\n")
	c := write("broken.mon", "NOT A REQUEST\n")
	return dir, []string{a, b, c}
}

func newModel(t *testing.T, b Backend, files []string) *Model {
	t.Helper()
	m := New(b, Options{Files: func() ([]string, error) { return files, nil }, NoPoll: true, Log: NewLogBuffer(10)})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func press(m *Model, keys ...string) tea.Cmd {
	var last tea.Cmd
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "left":
			msg = tea.KeyMsg{Type: tea.KeyLeft}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, last = m.Update(msg)
	}
	return last
}

// run executes cmd (and batches) and feeds the messages back, except
// spinner ticks and quit.
func run(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			run(t, m, c)
		}
	case tea.QuitMsg:
	case spinner.TickMsg:
	default:
		_, next := m.Update(msg)
		run(t, m, next)
	}
}

func TestTreeAndSelection(t *testing.T) {
	_, files := project(t)
	m := newModel(t, &fakeBackend{}, files)
	var rows []string
	for _, r := range m.rows {
		rows = append(rows, strings.Repeat(" ", r.depth)+r.name)
	}
	want := []string{"api", " users.mon", "  list", "  Create", "health.mon", " GET https://h/health", "broken.mon"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %q\nwant  %q", rows, want)
	}
	view := m.View()
	for _, s := range []string{"▾ api/", "list", "Create", "broken.mon !", "Select a request"} {
		if !strings.Contains(view, s) {
			t.Errorf("view lacks %q:\n%s", s, view)
		}
	}
	// Moving onto a request shows its section of the file.
	press(m, "down", "down", "down")
	if n := m.selected(); n.name != "Create" {
		t.Fatalf("selected %q", n.name)
	}
	if v := m.View(); !strings.Contains(v, `{"name": "ada"}`) || !strings.Contains(v, "users.mon:6") {
		t.Errorf("request pane:\n%s", v)
	}
	// Folding: left on a request goes to its file, left again folds it.
	press(m, "left", "left")
	if len(m.rows) != 5 || m.selected().name != "users.mon" || m.selected().open {
		t.Errorf("after fold: %d rows, selected %q", len(m.rows), m.selected().name)
	}
	press(m, "enter")
	if len(m.rows) != 7 {
		t.Errorf("enter on a file unfolds it: %d rows", len(m.rows))
	}
	// A broken file shows its parse error.
	press(m, "G")
	if v := m.View(); !strings.Contains(v, "invalid") && !strings.Contains(v, "NOT A REQUEST") {
		t.Errorf("parse error not shown:\n%s", v)
	}
}

func TestSendShowsResponseTabs(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{results: map[int]report.Request{
		3: {File: files[0], Line: 3, Name: "list", Method: "GET", Status: 200, StatusText: "OK", Duration: 42 * time.Millisecond,
			ResponseStatus: "HTTP/1.1 200 OK", ResponseHeaders: map[string][]string{"Content-Type": {"application/json"}},
			ResponseBody: `{"users":[{"id":1,"name":"ada"}]}`,
			Timings:      timings(),
			Tests:        []script.TestResult{{Name: "ok", Passed: true}, {Name: "has bob", Passed: false, Error: "expected bob"}}},
	}}
	m := newModel(t, fb, files)
	press(m, "down", "down") // "list"
	run(t, m, press(m, "enter"))
	if strings.Join(fb.sends, ",") != "users.mon:3" {
		t.Fatalf("sends = %v", fb.sends)
	}
	v := m.View()
	for _, s := range []string{"✗ list  200 OK · 42ms · 1/2 tests", `"users": [`, `"name": "ada"`, "1 sent"} {
		if !strings.Contains(v, s) {
			t.Errorf("view lacks %q:\n%s", s, v)
		}
	}
	tests := []struct {
		key  string
		want []string
	}{
		{"2", []string{"HTTP/1.1 200 OK", "Content-Type: application/json"}},
		{"3", []string{"Waiting (TTFB)", "Total", "42ms"}},
		{"4", []string{"✓ ok", "✗ has bob", "expected bob"}},
		{"5", []string{"No log output yet"}},
		{"1", []string{`"id": 1`}},
	}
	for _, tt := range tests {
		press(m, tt.key)
		v := m.View()
		for _, s := range tt.want {
			if !strings.Contains(v, s) {
				t.Errorf("tab %s lacks %q:\n%s", tt.key, s, v)
			}
		}
	}
	// The tree marks the request as failed; moving away and back keeps the result.
	if !strings.Contains(m.View(), "✗ GET  list") {
		t.Errorf("tree mark missing:\n%s", m.View())
	}
	press(m, "down", "up")
	if m.shown == nil || m.shown.Name != "list" {
		t.Errorf("result not kept for the request")
	}
	press(m, "down")
	if m.shown != nil {
		t.Errorf("an unsent request shows a result")
	}
}

func timings() net.Timings {
	return net.Timings{Waiting: 30 * time.Millisecond, Receiving: 2 * time.Millisecond}
}

func TestSendWhileBusyAndCancel(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{block: make(chan struct{})}
	m := newModel(t, fb, files)
	press(m, "down", "down")
	cmd := press(m, "enter")
	if !m.busy {
		t.Fatal("not busy after enter")
	}
	if c := press(m, "enter"); c != nil || !strings.Contains(m.status, "a request is running") {
		t.Errorf("second send allowed (status %q)", m.status)
	}
	press(m, "esc") // cancels: the blocked Send returns
	run(t, m, cmd)
	if m.busy || m.shown == nil || m.shown.Error != "canceled" {
		t.Errorf("after cancel: busy %v, shown %+v", m.busy, m.shown)
	}
}

func TestEnvPicker(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{envs: []string{"broken", "dev", "prod"}, env: "dev"}
	m := newModel(t, fb, files)
	press(m, "down", "down")
	run(t, m, press(m, "enter"))
	press(m, "e")
	if m.overlay != overlayEnv || m.envCursor != 2 {
		t.Fatalf("overlay %v cursor %d", m.overlay, m.envCursor)
	}
	if v := m.View(); !strings.Contains(v, "(defaults)") || !strings.Contains(v, "dev  (current)") {
		t.Errorf("env overlay:\n%s", v)
	}
	run(t, m, press(m, "down", "enter"))
	if fb.env != "prod" || m.overlay != overlayNone || len(m.results) != 0 || !strings.Contains(m.View(), "env prod") {
		t.Errorf("env = %q, overlay %v, results %d\n%s", fb.env, m.overlay, len(m.results), m.View())
	}
	press(m, "e")
	run(t, m, press(m, "up", "up", "enter")) // "broken"
	if fb.env != "prod" || !m.statusErr || !strings.Contains(m.status, "broken env") {
		t.Errorf("failed switch: env %q status %q", fb.env, m.status)
	}
	press(m, "e", "esc")
	if m.overlay != overlayNone {
		t.Error("esc does not close the picker")
	}

	// While a switch is in flight, sends and load tests wait.
	press(m, "e")
	cmd := press(m, "up", "enter") // prod -> dev
	if cmd == nil || !m.switching {
		t.Fatal("switch not marked in flight")
	}
	if c := press(m, "enter"); c != nil || !strings.Contains(m.status, "switching environments") {
		t.Errorf("send allowed during a switch (status %q)", m.status)
	}
	run(t, m, cmd)
	if m.switching {
		t.Error("switch flag not cleared")
	}
}

func TestVariablesPanel(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{vars: []runner.Variable{{Name: "baseUrl", Value: "https://h", Source: "config"}, {Name: "token", Value: "****", Source: "config"}}}
	m := newModel(t, fb, files)
	press(m, "down", "down")
	run(t, m, press(m, "v"))
	v := m.View()
	for _, s := range []string{"Variables", "users.mon", "baseUrl = https://h", "token   = ****", "(config)"} {
		if !strings.Contains(v, s) {
			t.Errorf("vars overlay lacks %q:\n%s", s, v)
		}
	}
	press(m, "esc")
	if m.overlay != overlayNone {
		t.Error("esc does not close")
	}
}

func TestHistory(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{results: map[int]report.Request{
		3: {Name: "list", Status: 200, StatusText: "OK", ResponseStatus: "HTTP/1.1 200 OK", ResponseBody: "first"},
		6: {Name: "Create", Status: 201, StatusText: "Created", ResponseStatus: "HTTP/1.1 201 Created", ResponseBody: "second"},
	}}
	m := newModel(t, fb, files)
	m.now = func() time.Time { return time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC) }
	press(m, "down", "down")
	run(t, m, press(m, "enter"))
	press(m, "down")
	run(t, m, press(m, "enter"))
	press(m, "H")
	v := m.View()
	if i, j := strings.Index(v, "Create  201"), strings.Index(v, "list  200"); i < 0 || j < 0 || i > j {
		t.Errorf("history not newest first:\n%s", v)
	}
	press(m, "down", "enter") // the older one
	if m.shown == nil || m.shown.ResponseBody != "first" || m.overlay != overlayNone {
		t.Errorf("history entry not shown: %+v", m.shown)
	}
}

func TestReloadKeepsSelectionAndFolds(t *testing.T) {
	dir, files := project(t)
	m := newModel(t, &fakeBackend{}, files)
	press(m, "down", "down", "down") // Create
	// Fold health.mon, then edit users.mon on disk: a request is added before Create.
	m.cursor = 4
	press(m, "space")
	m.cursor = 3
	if err := os.WriteFile(filepath.Join(dir, "api/users.mon"), []byte("### List users\n# @name list\nGET https://h/users\n\n### Search\nGET https://h/users?q=a\n\n### Create\nPOST https://h/users\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Update(statMsg{files[0]: time.Unix(1, 0)}) // first stat: remembers
	m.Update(statMsg{files[0]: time.Unix(2, 0)}) // changed: reloads
	if n := m.selected(); n.name != "Create" || n.req.Line != 9 {
		t.Errorf("selected %q line %d after reload", n.name, n.req.Line)
	}
	for _, r := range m.rows {
		if r.name == "health.mon" && r.open {
			t.Error("fold lost on reload")
		}
	}
	if !strings.Contains(m.status, "reloaded") {
		t.Errorf("status %q", m.status)
	}
}

func TestLoadDashboard(t *testing.T) {
	_, files := project(t)
	release := make(chan struct{})
	fb := &fakeBackend{loadRun: func(ctx context.Context, progress func(load.Progress)) (string, error) {
		progress(load.Progress{Elapsed: 5 * time.Second, Planned: 10 * time.Second, ActiveVUs: 3, VUs: 4, Iterations: 12, Requests: 24, Failed: 1, RPS: 4.8, P95: 18 * time.Millisecond})
		select {
		case <-release:
		case <-ctx.Done():
			return "stopped summary\n", nil
		}
		return "monica load: 4 VUs, 10s\nrequests 48\n", nil
	}}
	m := newModel(t, fb, files)
	press(m, "down")
	cmd := press(m, "L")
	if m.overlay != overlayLoad || !m.load.running {
		t.Fatalf("overlay %v running %v", m.overlay, m.load.running)
	}
	// First message: the progress tick.
	_, next := m.Update(cmd())
	v := m.View()
	for _, s := range []string{"Load test", "users.mon", "3 active / 4 started", "24 · 4.8 rps", "18ms", "1 (4.17%)", "rps ", "load test running"} {
		if !strings.Contains(v, s) {
			t.Errorf("dashboard lacks %q:\n%s", s, v)
		}
	}
	if c := press(m, "enter"); c != nil || m.overlay != overlayLoad {
		t.Error("keys other than esc do nothing while running")
	}
	close(release)
	run(t, m, next)
	if m.load.running || !strings.Contains(m.View(), "requests 48") {
		t.Errorf("summary not shown:\n%s", m.View())
	}
	press(m, "esc")
	if m.overlay != overlayNone {
		t.Error("esc does not close the finished dashboard")
	}
}

func TestLoadStopAndErrors(t *testing.T) {
	_, files := project(t)
	fb := &fakeBackend{loadRun: func(ctx context.Context, _ func(load.Progress)) (string, error) {
		<-ctx.Done()
		return "stopped summary\n", nil
	}}
	m := newModel(t, fb, files)
	press(m, "down")
	cmd := press(m, "L")
	press(m, "esc") // stops the running test
	run(t, m, cmd)
	if m.load.running || !strings.Contains(m.View(), "stopped summary") {
		t.Errorf("after stop:\n%s", m.View())
	}

	fb.loadRun = func(context.Context, func(load.Progress)) (string, error) { return "", errors.New("no load settings") }
	press(m, "esc")
	run(t, m, press(m, "L"))
	if !strings.Contains(m.View(), "no load settings") {
		t.Errorf("error not shown:\n%s", m.View())
	}
}

func TestEditorArgs(t *testing.T) {
	tests := []struct {
		editor string
		want   string
	}{
		{"vim", "vim +7 a.mon"},
		{"nvim -u NONE", "nvim -u NONE +7 a.mon"},
		{"code", "code --wait --goto a.mon:7"},
		{"code -w", "code -w --goto a.mon:7"},
		{"/usr/local/bin/subl", "/usr/local/bin/subl a.mon:7"},
	}
	for _, tt := range tests {
		if got := strings.Join(editorArgs(tt.editor, "a.mon", 7), " "); got != tt.want {
			t.Errorf("editorArgs(%q) = %q, want %q", tt.editor, got, tt.want)
		}
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	if got := strings.Join(editorArgs("", "a.mon", 1), " "); got != "nano +1 a.mon" {
		t.Errorf("$EDITOR: %q", got)
	}
}

func TestQuitAndHelp(t *testing.T) {
	_, files := project(t)
	m := newModel(t, &fakeBackend{}, files)
	press(m, "?")
	if !strings.Contains(m.View(), "load-test the selected file") {
		t.Errorf("help:\n%s", m.View())
	}
	press(m, "esc")
	if cmd := press(m, "q"); cmd == nil || cmd() != tea.Quit() {
		t.Error("q does not quit")
	}
}

func TestPrettyBody(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{`{"a":1,"b":[true,null,"x"]}`, "{\n  \"a\": 1,\n  \"b\": [\n    true,\n    null,\n    \"x\"\n  ]\n}", true},
		{"  [1]\n", "[\n  1\n]", true},
		{"plain text", "plain text", false},
		{`{"broken":`, `{"broken":`, false},
	}
	for _, tt := range tests {
		got, ok := prettyBody(tt.in, false)
		if got != tt.want || ok != tt.ok {
			t.Errorf("prettyBody(%q) = %q, %v", tt.in, got, ok)
		}
		if tt.ok {
			if colored := highlightJSON(got); stripANSI(colored) != got {
				t.Errorf("highlight changes the text: %q", stripANSI(colored))
			}
		}
	}
}
