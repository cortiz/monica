package runner_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunWithConfig(t *testing.T) {
	// The server echoes what the runner's templating and defaults produced.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/item/followed", http.StatusFound)
			return
		}
		_, _ = fmt.Fprintf(w, "path=%s default=%s ua=%s token=%s",
			r.URL.Path, r.Header.Get("X-Default"), r.Header.Get("User-Agent"), r.Header.Get("X-Token"))
	}))
	defer srv.Close()

	yml := `version: 1
defaults:
  headers:
    User-Agent: custom/1
    X-Default: d-{{id}}
  vars:
    baseUrl: ` + srv.URL + `
envs:
  dev:
    headers:
      X-Token: ${TOKEN}
    vars:
      id: dev-1
  noredirect:
    followRedirects: false
    vars:
      id: nr
`
	lookupEnv := func(name string) (string, bool) {
		if name == "TOKEN" {
			return "t0k", true
		}
		return "", false
	}
	tests := []struct {
		name       string
		mon        string
		opts       runner.Opts
		configPath bool // pass the config with --config instead of discovery
		wantOut    string
		wantErr    string
		wantInput  bool
	}{
		{
			name:    "env vars, default headers and ${NAME}",
			mon:     "GET {{baseUrl}}/item/{{ id }}\n",
			opts:    runner.Opts{Env: "dev"},
			wantOut: "path=/item/dev-1 default=d-dev-1 ua=custom/1 token=t0k",
		},
		{
			name:    "--var beats config vars",
			mon:     "GET {{baseUrl}}/item/{{id}}\n",
			opts:    runner.Opts{Env: "dev", Vars: map[string]string{"id": "cli"}},
			wantOut: "path=/item/cli default=d-cli",
		},
		{
			name:    "request header beats default header",
			mon:     "GET {{baseUrl}}/item/{{id}}\nX-Default: mine\n",
			opts:    runner.Opts{Env: "dev"},
			wantOut: "default=mine",
		},
		{
			name:       "explicit config path",
			mon:        "GET {{baseUrl}}/item/{{id}}\n",
			opts:       runner.Opts{Env: "dev"},
			configPath: true,
			wantOut:    "path=/item/dev-1",
		},
		{
			name:    "followRedirects false returns the 302",
			mon:     "GET {{baseUrl}}/redirect\n",
			opts:    runner.Opts{Env: "noredirect"},
			wantOut: "302 Found",
		},
		{
			name:      "unknown variable is an input error",
			mon:       "GET {{baseUrl}}/item/{{id}}\n",
			wantErr:   `test.mon:1: url: unknown variable "id"`,
			wantInput: true,
		},
		{
			name:      "unknown environment is an input error",
			mon:       "GET {{baseUrl}}/\n",
			opts:      runner.Opts{Env: "qa"},
			wantErr:   `unknown environment "qa" (defined: dev, noredirect)`,
			wantInput: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			opts := tt.opts
			opts.LookupEnv = lookupEnv
			var mon string
			if tt.configPath {
				opts.ConfigPath = write(t, root, "elsewhere/monica.yml", yml)
				mon = write(t, root, "requests/test.mon", tt.mon)
			} else {
				write(t, root, "monica.yml", yml)
				mon = write(t, root, "api/test.mon", tt.mon) // found by walking up
			}
			var out bytes.Buffer
			err := runner.RunSingleFile(mon, &opts, &out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v does not contain %q", err, tt.wantErr)
				}
				var ie *runner.InputError
				if errors.As(err, &ie) != tt.wantInput {
					t.Fatalf("InputError = %v, want %v", !tt.wantInput, tt.wantInput)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("stdout %q does not contain %q", out.String(), tt.wantOut)
			}
		})
	}
}

func TestRunConfigErrorSendsNothing(t *testing.T) {
	srv := newRecorder(t)
	root := t.TempDir()
	write(t, root, "monica.yml", "version: 1\ndefaults:\n  timout: 5s\n")
	mon := write(t, root, "test.mon", "GET "+srv.URL+"/x\n")
	err := runner.RunSingleFile(mon, &runner.Opts{}, &bytes.Buffer{})
	var ie *runner.InputError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "field timout not found") {
		t.Fatalf("expected config InputError, got %v", err)
	}
	if got := srv.seen(); len(got) != 0 {
		t.Fatalf("nothing should be sent, server saw %v", got)
	}
}
