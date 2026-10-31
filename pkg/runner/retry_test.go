package runner_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

// flakyServer answers 500 to the first fails requests for each path, then
// 200. It counts requests per path.
func flakyServer(t *testing.T, fails int) (*httptest.Server, func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		n := hits[r.URL.Path]
		mu.Unlock()
		if n <= fails {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = w.Write([]byte(strconv.Itoa(n)))
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
}

const okTest = "> {% test(\"ok\", () => expect(res.status).toBe(200)) %}\n"

func TestRetry(t *testing.T) {
	tests := []struct {
		name         string
		mon          string // %s is the server URL
		fails        int
		wantSent     int
		wantAttempts int // in the report; 0 = sent once
		wantErr      string
		wantInput    bool
	}{
		{name: "passes on the last retry", mon: "# @retry 2\nGET %s/x\n" + okTest, fails: 2, wantSent: 3, wantAttempts: 3},
		{name: "stops retrying once it passes", mon: "# @retry 5 1ms\nGET %s/x\n" + okTest, fails: 1, wantSent: 2, wantAttempts: 2},
		{name: "fails after the retries", mon: "# @retry 2\nGET %s/x\n" + okTest, fails: 9, wantSent: 3, wantAttempts: 3, wantErr: "1 of 1 tests failed"},
		{name: "without @retry: one attempt", mon: "GET %s/x\n" + okTest, fails: 1, wantSent: 1, wantErr: "1 of 1 tests failed"},
		{name: "script error is retried", mon: "# @retry 1\nGET %s/x\n> {% if (res.status !== 200) throw new Error(\"boom\") %}\n", fails: 1, wantSent: 2, wantAttempts: 2},
		{name: "a plain 500 without tests is not a failure", mon: "# @retry 3\nGET %s/x\n", fails: 9, wantSent: 1},
		{name: "unknown variable is not retried", mon: "# @retry 3\nGET %s/x?{{nope}}\n", wantSent: 0, wantErr: "nope", wantInput: true},
		{name: "script syntax error is not retried", mon: "# @retry 3\n< {% ) %}\nGET %s/x\n", wantSent: 0, wantErr: "SyntaxError", wantInput: true},
		{
			name: "pre-request script reruns on each attempt",
			mon: "# @retry 2\n< {% vars.set(\"n\", Number(vars.get(\"n\") || 0) + 1) %}\nGET %s/x?n={{n}}\n" +
				"> {% test(\"ok\", () => expect(res.status).toBe(200)); test(\"n\", () => expect(vars.get(\"n\")).toBe(\"3\")) %}\n",
			fails: 2, wantSent: 3, wantAttempts: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sent := flakyServer(t, tt.fails)
			mon := write(t, t.TempDir(), "r.mon", strings.ReplaceAll(tt.mon, "%s", srv.URL))
			rec := &reportRecorder{}
			logs := captureLogs(t)
			r := runner.NewWithReporter(&runner.Opts{}, rec)
			err := r.RunFile(mon)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
			var ie *runner.InputError
			if errors.As(err, &ie) != tt.wantInput {
				t.Errorf("InputError = %v, want %v", !tt.wantInput, tt.wantInput)
			}
			if got := sent("/x"); got != tt.wantSent {
				t.Errorf("sent %d times, want %d", got, tt.wantSent)
			}
			if len(rec.got) != 1 {
				t.Fatalf("want one report entry, got %+v", rec.got)
			}
			res := rec.got[0]
			if res.Attempts != tt.wantAttempts {
				t.Errorf("Attempts = %d, want %d", res.Attempts, tt.wantAttempts)
			}
			// Only the last attempt's tests are reported.
			if tt.wantAttempts > 0 && len(res.Tests) > 2 {
				t.Errorf("tests from earlier attempts leaked: %+v", res.Tests)
			}
			if retries := strings.Count(logs.String(), "failed; retrying"); tt.wantAttempts > 0 && retries != tt.wantAttempts-1 {
				t.Errorf("logged %d retries, want %d: %s", retries, tt.wantAttempts-1, logs)
			}
		})
	}
}

func TestReqSkip(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string // %s is the server URL
		wantSent   int
		wantReason string
		wantSkip   []bool // per report entry; nil = one skipped entry
	}{
		{
			name:       "pre-request script",
			files:      map[string]string{"a.mon": "< {% if (!vars.has(\"token\")) req.skip(\"no token\") %}\nGET %s/x\n> {% throw new Error(\"post must not run\") %}\n"},
			wantReason: "no token",
		},
		{
			name: "beforeEach hook",
			files: map[string]string{
				"monica.yml": "version: 1\n",
				"monica.js":  "module.exports = { beforeEach() { req.skip(\"hook\") }, afterEach() { throw new Error(\"afterEach must not run\") } };\n",
				"a.mon":      "< {% throw new Error(\"pre must not run\") %}\nGET %s/x\n",
			},
			wantReason: "hook",
		},
		{
			name:     "not called: sent",
			files:    map[string]string{"a.mon": "< {% if (false) req.skip() %}\nGET %s/x\n"},
			wantSent: 1,
			wantSkip: []bool{false},
		},
		{
			// The first request is sent; the second skips itself once and is
			// not retried; the third has no script and is sent.
			name: "per request, not retried",
			files: map[string]string{"a.mon": "# @retry 3\n< {% if (vars.get(\"seen\")) req.skip(); vars.set(\"seen\", \"1\") %}\nGET %s/x\n\n###\n" +
				"# @retry 3\n< {% if (vars.get(\"seen\")) req.skip(); vars.set(\"seen\", \"1\") %}\nGET %s/x\n\n###\nGET %s/x\n"},
			wantSent: 2,
			wantSkip: []bool{false, true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, sent := flakyServer(t, 0)
			dir := t.TempDir()
			for name, content := range tt.files {
				write(t, dir, name, strings.ReplaceAll(content, "%s", srv.URL))
			}
			rec := &reportRecorder{}
			r := runner.NewWithReporter(&runner.Opts{}, rec)
			err := errors.Join(r.RunFile(dir+"/a.mon"), r.AfterAll())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := sent("/x"); got != tt.wantSent {
				t.Errorf("sent %d times, want %d", got, tt.wantSent)
			}
			if tt.wantSkip == nil {
				if len(rec.got) != 1 || !rec.got[0].Skipped || rec.got[0].SkipReason != tt.wantReason {
					t.Fatalf("report = %+v, want skipped with %q", rec.got, tt.wantReason)
				}
				return
			}
			if len(rec.got) != len(tt.wantSkip) {
				t.Fatalf("got %d report entries, want %d", len(rec.got), len(tt.wantSkip))
			}
			for i, want := range tt.wantSkip {
				if rec.got[i].Skipped != want || rec.got[i].Attempts != 0 {
					t.Errorf("entry %d: Skipped=%v Attempts=%d, want skipped=%v, sent once", i, rec.got[i].Skipped, rec.got[i].Attempts, want)
				}
			}
		})
	}
}
