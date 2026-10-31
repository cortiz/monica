package runner_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

// logHook is a monica.js that appends "<tag>.<hook>" to the "log" var for
// every hook.
func logHook(tag string) string {
	return `const log = (s) => vars.set("log", (vars.has("log") ? vars.get("log") + " " : "") + "` + tag + `." + s);
module.exports = {
  beforeAll: () => log("beforeAll"),
  beforeEach: () => log("beforeEach"),
  afterEach: () => log("afterEach"),
  afterAll: () => log("afterAll"),
};
`
}

// collect is a Reporter keeping every result.
type collect struct {
	results []report.Request
	summary report.Summary
}

func (c *collect) Request(r report.Request) { c.results = append(c.results, r) }
func (c *collect) Done(s report.Summary) error {
	c.summary = s
	return nil
}

// runTree writes files into a temp project and runs mons (in order) with
// one Runner, then AfterAll and Finish. It returns the reporter and the
// RunFile and AfterAll errors joined.
func runTree(t *testing.T, files map[string]string, opts runner.Opts, mons ...string) (*collect, error) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		write(t, dir, name, content)
	}
	rep := &collect{}
	r := runner.NewWithReporter(&opts, rep)
	var errs []error
	for _, m := range mons {
		errs = append(errs, r.RunFile(filepath.Join(dir, m)))
	}
	errs = append(errs, r.AfterAll())
	if err := r.Finish(); err != nil {
		t.Fatal(err)
	}
	return rep, errors.Join(errs...)
}

func TestHooksOrder(t *testing.T) {
	srv, _ := apiServer(t)
	logs := captureLogs(t)
	files := map[string]string{
		"monica.yml":      "version: 1\n",
		"monica.js":       logHook("root") + "module.exports.afterAll = () => { log('afterAll'); console.log('LOG=' + vars.get('log')); };\n",
		"api/monica.js":   logHook("api"),
		"api/a.mon":       "GET " + srv.URL + "/a1\n###\nGET " + srv.URL + "/a2\n",
		"b.mon":           "< {% vars.set('log', vars.get('log') + ' b.pre') %}\nGET " + srv.URL + "/b\n> {% vars.set('log', vars.get('log') + ' b.post') %}\n",
		"other/x.mon":     "# @skip\nGET " + srv.URL + "/x\n",
		"other/monica.js": "exports.beforeAll = () => { throw new Error('must not run for skipped requests'); };\n",
	}
	rep, err := runTree(t, files, runner.Opts{}, "api/a.mon", "b.mon", "other/x.mon")
	if err != nil {
		t.Fatal(err)
	}
	want := "LOG=root.beforeAll api.beforeAll " +
		"root.beforeEach api.beforeEach api.afterEach root.afterEach " +
		"root.beforeEach api.beforeEach api.afterEach root.afterEach " +
		"root.beforeEach b.pre b.post root.afterEach " +
		"api.afterAll root.afterAll"
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("hook order wrong:\nwant %s\nlogs %s", want, logs.String())
	}
	if s := rep.summary; s.Requests != 4 || s.Passed != 3 || s.Skipped != 1 || s.Failed != 0 {
		t.Fatalf("summary %+v", s)
	}
}

func TestHooksTouchRequests(t *testing.T) {
	srv, _ := apiServer(t)
	files := map[string]string{
		"monica.js": `module.exports = {
  beforeEach() { req.headers.set("X-Pre", "hooked " + vars.get("n")); },
  afterEach() { test("status ok", () => expect(res.status).toBe(200)); },
};`,
		"a.mon": "< {% vars.set('n', '7') %}\nGET " + srv.URL + "/echo\n",
	}
	rep, err := runTree(t, files, runner.Opts{}, "a.mon")
	if err != nil {
		t.Fatal(err)
	}
	r := rep.results[0]
	// beforeEach runs before the pre-request script, so n is unset there.
	if !strings.Contains(r.Response, "x-pre=hooked undefined") {
		t.Fatalf("beforeEach did not set the header: %q", r.Response)
	}
	if len(r.Tests) != 1 || !r.Tests[0].Passed || r.Tests[0].Name != "status ok" {
		t.Fatalf("afterEach tests not reported: %+v", r.Tests)
	}
}

func TestHookFailures(t *testing.T) {
	srv, seen := apiServer(t)
	tests := []struct {
		name      string
		files     map[string]string
		wantErr   []string
		wantNames []string // result names in order
		wantSkip  string   // skip reason of the request
		wantSent  int
	}{
		{
			name: "beforeAll throws: reported once, folder's requests skipped, no afterAll",
			files: map[string]string{
				"monica.js": "exports.beforeAll = () => {\n  throw new Error('db down');\n};\nexports.afterAll = () => { throw new Error('must not run'); };\n",
				"a.mon":     "GET " + srv.URL + "/1\n###\nGET " + srv.URL + "/2\n",
			},
			wantErr:   []string{"monica.js: beforeAll: ", "monica.js:2:9: Error: db down"},
			wantNames: []string{"beforeAll", "GET ", "GET "},
			wantSkip:  "hooks in ",
		},
		{
			name: "monica.js does not load",
			files: map[string]string{
				"monica.js": "exports.beforeEach = 5;\n",
				"a.mon":     "GET " + srv.URL + "/1\n",
			},
			wantErr:   []string{"loading hooks", "beforeEach must be a function"},
			wantNames: []string{"loading hooks", "GET "},
			wantSkip:  "hooks in ",
		},
		{
			name: "afterAll throws: reported as its own failed result",
			files: map[string]string{
				"monica.js": "exports.afterAll = () => { throw new Error('cleanup failed'); };\n",
				"a.mon":     "GET " + srv.URL + "/1\n",
			},
			wantErr:   []string{"monica.js: afterAll: ", "Error: cleanup failed"},
			wantNames: []string{"GET ", "afterAll"},
			wantSent:  1,
		},
		{
			name: "beforeEach throws: request not sent",
			files: map[string]string{
				"monica.js": "exports.beforeEach = () => { throw new Error('no token'); };\n",
				"a.mon":     "GET " + srv.URL + "/1\n",
			},
			wantErr:   []string{"beforeEach hook (", "Error: no token"},
			wantNames: []string{"GET "},
		},
		{
			name: "afterEach runs after a failed post script; the script's error wins",
			files: map[string]string{
				"monica.js": "exports.afterEach = () => { throw new Error('after'); };\nexports.afterAll = () => { if (vars.get('ran') !== 'yes') throw new Error('afterEach did not run'); };\n",
				"a.mon":     "GET " + srv.URL + "/1\n> {% vars.set('ran', 'yes'); throw new Error('post') %}\n",
			},
			wantErr:   []string{"post-response script: ", "Error: post"},
			wantNames: []string{"GET "},
			wantSent:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*seen = nil
			rep, err := runTree(t, tt.files, runner.Opts{}, "a.mon")
			for _, w := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Fatalf("error %v does not contain %q", err, w)
				}
			}
			if strings.Contains(err.Error(), "must not run") || strings.Contains(err.Error(), "did not run") {
				t.Fatalf("unexpected hook behaviour: %v", err)
			}
			if len(rep.results) != len(tt.wantNames) {
				b, _ := json.Marshal(rep.results)
				t.Fatalf("got results %s, want names %v", b, tt.wantNames)
			}
			for i, w := range tt.wantNames {
				if !strings.HasPrefix(rep.results[i].Name, w) {
					t.Errorf("result %d is %q, want prefix %q", i, rep.results[i].Name, w)
				}
			}
			if tt.wantSkip != "" {
				last := rep.results[len(rep.results)-1]
				if !last.Skipped || !strings.Contains(last.SkipReason, tt.wantSkip) {
					t.Errorf("request not skipped: %+v", last)
				}
			}
			if len(*seen) != tt.wantSent {
				t.Errorf("server saw %v, want %d requests", *seen, tt.wantSent)
			}
			if rep.summary.Failed == 0 {
				t.Errorf("summary has no failure: %+v", rep.summary)
			}
		})
	}
}

func TestHooksWithoutConfigUseTheMonFolderOnly(t *testing.T) {
	srv, _ := apiServer(t)
	logs := captureLogs(t)
	files := map[string]string{
		"monica.js":     "exports.beforeAll = () => console.log('PARENT RAN');\n",
		"sub/monica.js": "exports.beforeAll = () => console.log('SUB RAN');\n",
		"sub/a.mon":     "GET " + srv.URL + "/1\n",
	}
	if _, err := runTree(t, files, runner.Opts{}, "sub/a.mon"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "SUB RAN") || strings.Contains(logs.String(), "PARENT RAN") {
		t.Fatalf("logs: %s", logs.String())
	}
}

func TestRunSingleFileFailsOnAfterAll(t *testing.T) {
	srv, _ := apiServer(t)
	dir := t.TempDir()
	write(t, dir, "monica.js", "exports.afterAll = () => { throw new Error('boom'); };\n")
	mon := write(t, dir, "a.mon", "GET "+srv.URL+"/1\n")
	err := runner.RunSingleFile(mon, &runner.Opts{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "afterAll: ") || !strings.Contains(err.Error(), "Error: boom") {
		t.Fatalf("got %v, want the afterAll failure", err)
	}
}

func TestHooksWithRunSingleFile(t *testing.T) {
	// RunSingleFile (no explicit AfterAll) still runs afterAll in Finish.
	srv, _ := apiServer(t)
	logs := captureLogs(t)
	dir := t.TempDir()
	write(t, dir, "monica.js", "exports.afterAll = () => console.log('AFTER ALL');\n")
	mon := write(t, dir, "a.mon", "GET "+srv.URL+"/1\n")
	if err := runner.RunSingleFile(mon, &runner.Opts{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "AFTER ALL") {
		t.Fatalf("afterAll did not run: %s", logs.String())
	}
}
