package runner_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/runner"
)

// apiServer is a tiny login API: POST /login returns a token, GET /me
// requires it, everything else echoes method, path, headers and body.
func apiServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token": "tok-123"}`))
		case "/me":
			if r.Header.Get("Authorization") != "Bearer tok-123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"name": "ada"}`))
		default:
			_, _ = fmt.Fprintf(w, "%s %s x-pre=%s body=%s", r.Method, r.URL.RequestURI(), r.Header.Get("X-Pre"), body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logging.Log
	logging.Log = zerolog.New(&buf)
	t.Cleanup(func() { logging.Log = prev })
	return &buf
}

func TestRunScripts(t *testing.T) {
	srv, _ := apiServer(t)
	tests := []struct {
		name      string
		mon       string
		opts      runner.Opts
		wantOut   []string
		wantErr   string
		wantInput bool
		wantLogs  string
	}{
		{
			name: "post script chains a token into the next request",
			mon: "POST " + srv.URL + "/login\n\n> {%\n  vars.set(\"token\", res.json().token);\n%}\n" +
				"###\nGET " + srv.URL + "/me\nAuthorization: Bearer {{token}}\n",
			wantOut: []string{`{"token": "tok-123"}`, `{"name": "ada"}`},
		},
		{
			name: "pre script runs before templating and can mutate req",
			mon: "< {%\n  vars.set(\"page\", 1 + 1);\n  req.method = \"PUT\";\n  req.url += \"?p={{page}}\";\n" +
				"  req.headers.set(\"X-Pre\", monica.env || \"none\");\n  req.body = \"n={{page}}\";\n%}\nGET " + srv.URL + "/echo\n",
			wantOut: []string{"PUT /echo?p=2 x-pre=none body=n=2"},
		},
		{
			name:    "--var wins over vars.set",
			mon:     "< {% vars.set(\"page\", \"script\") %}\nGET " + srv.URL + "/echo?p={{page}}\n",
			opts:    runner.Opts{Vars: map[string]string{"page": "cli"}},
			wantOut: []string{"GET /echo?p=cli"},
		},
		{
			name: "failed tests fail the request",
			mon: "GET " + srv.URL + "/echo\n> {%\n  test(\"ok\", () => expect(res.status).toBe(200));\n" +
				"  test(\"wrong\", () => expect(res.status).toBe(201));\n%}\n",
			wantOut: []string{"GET /echo"},
			wantErr: `test.mon:1: GET ` + srv.URL + `/echo: 1 of 2 tests failed: "wrong": expected 200 to be 201`,
		},
		{
			name:    "passing tests",
			mon:     "GET " + srv.URL + "/echo\n> {% test(\"ok\", () => expect(res.body).toContain(\"/echo\")) %}\n",
			wantOut: []string{"GET /echo"},
		},
		{
			name:     "console output goes to the log",
			mon:      "GET " + srv.URL + "/echo\n> {% console.log(\"status\", res.status) %}\n",
			wantLogs: "console.log: status 200",
		},
		{
			name:    "pre script error: request not sent, run fails",
			mon:     "< {% throw new Error(\"nope\") %}\nGET " + srv.URL + "/never\n###\nGET " + srv.URL + "/after\n",
			wantOut: []string{"GET /after"},
			wantErr: "test.mon:2: GET " + srv.URL + "/never: pre-request script: ",
		},
		{
			name:      "syntax error is an input error with the script's line",
			mon:       "GET " + srv.URL + "/echo\n\n> {%\n  const x = ;\n%}\n",
			wantOut:   []string{"GET /echo"},
			wantErr:   "test.mon:4:",
			wantInput: true,
		},

		{
			name:    "post script error fails the request after printing it",
			mon:     "GET " + srv.URL + "/echo\n> {% res.json() %}\n",
			wantOut: []string{"GET /echo"},
			wantErr: "test.mon:2:22: TypeError: res.json(): body is not valid JSON (SyntaxError: invalid character",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			var out bytes.Buffer
			opts := tt.opts
			err := runner.RunSingleFile(writeMon(t, tt.mon), &opts, &out)

			rest := out.String()
			for _, s := range tt.wantOut {
				i := strings.Index(rest, s)
				if i < 0 {
					t.Fatalf("stdout missing %q (in order); got %q", s, out.String())
				}
				rest = rest[i+len(s):]
			}
			if !strings.Contains(logs.String(), tt.wantLogs) {
				t.Errorf("logs %q do not contain %q", logs.String(), tt.wantLogs)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
			var ie *runner.InputError
			if errors.As(err, &ie) != tt.wantInput {
				t.Errorf("InputError = %v, want %v", !tt.wantInput, tt.wantInput)
			}
		})
	}
}

func TestVarsSharedAcrossFiles(t *testing.T) {
	srv, seen := apiServer(t)
	dir := t.TempDir()
	login := write(t, dir, "1-login.mon", "POST "+srv.URL+"/login\n> {% vars.set(\"token\", res.json().token) %}\n")
	me := write(t, dir, "2-me.mon", "GET "+srv.URL+"/me\nAuthorization: Bearer {{token}}\n")

	var out bytes.Buffer
	r := runner.New(&runner.Opts{}, &out)
	for _, f := range []string{login, me} {
		if err := r.RunFile(f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if !strings.Contains(out.String(), `{"name": "ada"}`) {
		t.Fatalf("token from the first file was not used by the second: %q (server saw %v)", out.String(), *seen)
	}
}

func TestConsoleOutputIsMasked(t *testing.T) {
	srv, _ := apiServer(t)
	dir := t.TempDir()
	write(t, dir, "monica.yml", "version: 1\ndefaults:\n  vars:\n    key: ${KEY}\n")
	mon := write(t, dir, "t.mon", "GET "+srv.URL+"/echo\n> {% console.log(\"key is\", vars.get(\"key\")) %}\n")
	logs := captureLogs(t)
	opts := &runner.Opts{LookupEnv: func(n string) (string, bool) { return "hunter2", n == "KEY" }}
	if err := runner.RunSingleFile(mon, opts, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "hunter2") || !strings.Contains(logs.String(), "key is ****") {
		t.Fatalf("console output not masked: %s", logs.String())
	}
}

func TestScriptTimeoutFromConfig(t *testing.T) {
	srv, seen := apiServer(t)
	dir := t.TempDir()
	write(t, dir, "monica.yml", "version: 1\nscripts:\n  timeout: 100ms\n")
	mon := write(t, dir, "t.mon", "< {% while (true) {} %}\nGET "+srv.URL+"/never\n")
	start := time.Now()
	err := runner.RunSingleFile(mon, &runner.Opts{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "t.mon:1: script timed out after 100ms") {
		t.Fatalf("expected a timeout at t.mon:1, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("scripts.timeout not applied (took %s)", time.Since(start))
	}
	if len(*seen) != 0 {
		t.Fatalf("request must not be sent, server saw %v", *seen)
	}
}
