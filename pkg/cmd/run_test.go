package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMon writes a .mon file into dir and returns its path.
func writeMon(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	// Flags are package-level; reset them so tests don't leak into each other.
	singleFile, recursiveLevel = "", 1
	envName, varFlags, configPath = "", nil, ""
	for _, name := range []string{"env", "var", "config", "single", "recursive"} {
		if f := run.Flags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	code = execute(rootCmd, args, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunExitCodes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer ok.Close()

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close() // connections to it are refused

	dir := t.TempDir()
	good := writeMon(t, dir, "good.mon", "GET "+ok.URL+"\n\n")
	unreachable := writeMon(t, dir, "unreachable.mon", "GET "+closedURL+"\n\n")
	badLine := writeMon(t, dir, "bad.mon", "# comment\nGET\n")
	missing := filepath.Join(dir, "missing.mon")

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"success", []string{"run", "-s", good}, ExitOK, "hello", ""},
		{"missing file", []string{"run", "-s", missing}, ExitUsage, "", "missing.mon: no such file or directory"},
		{"bad request line", []string{"run", "-s", badLine}, ExitUsage, "", "bad.mon:2: invalid request line"},
		{"request fails", []string{"run", "-s", unreachable}, ExitFailed, "", "sending HTTP request"},
		{"unknown flag", []string{"run", "--nope"}, ExitUsage, "", "unknown flag: --nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout %q does not contain %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}

func TestRunAllFilesWorstExitCode(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	tests := []struct {
		name     string
		files    map[string]string
		wantCode int
	}{
		{"no files", nil, ExitOK},
		{"all ok", map[string]string{"a.mon": "GET " + ok.URL + "\n"}, ExitOK},
		{"one request fails", map[string]string{
			"a.mon": "GET " + ok.URL + "\n",
			"b.mon": "GET " + closedURL + "\n",
		}, ExitFailed},
		{"input error outranks request failure", map[string]string{
			"a.mon": "GET " + closedURL + "\n",
			"b.mon": "GET\n",
		}, ExitUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeMon(t, dir, name, content)
			}
			chdir(t, dir)
			code, _, stderr := runCLI(t, "run")
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
		})
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestRunConfigFlags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("path=" + r.URL.Path))
	}))
	defer srv.Close()

	dir := t.TempDir()
	yml := "version: 1\ndefaults:\n  vars:\n    baseUrl: " + srv.URL + "\n    id: default\nenvs:\n  dev:\n    vars:\n      id: dev\n  prod:\n    vars:\n      id: prod\n"
	writeMon(t, dir, "monica.yml", yml)
	other := t.TempDir()
	writeMon(t, other, "custom.yml", strings.Replace(yml, "id: default", "id: custom", 1))
	mon := writeMon(t, dir, "req.mon", "GET {{baseUrl}}/{{id}}\n")

	tests := []struct {
		name       string
		monicaEnv  string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"defaults", "", []string{"run", "-s", mon}, ExitOK, "path=/default", ""},
		{"--env", "", []string{"run", "-s", mon, "--env", "dev"}, ExitOK, "path=/dev", ""},
		{"-e short flag", "", []string{"run", "-s", mon, "-e", "prod"}, ExitOK, "path=/prod", ""},
		{"MONICA_ENV default", "prod", []string{"run", "-s", mon}, ExitOK, "path=/prod", ""},
		{"--env beats MONICA_ENV", "prod", []string{"run", "-s", mon, "--env", "dev"}, ExitOK, "path=/dev", ""},
		{"--var beats env vars, last wins", "", []string{"run", "-s", mon, "-e", "dev", "--var", "id=a", "--var", "id=b=c"}, ExitOK, "path=/b=c", ""},
		{"--config", "", []string{"run", "-s", mon, "--config", filepath.Join(other, "custom.yml")}, ExitOK, "path=/custom", ""},
		{"unknown env", "", []string{"run", "-s", mon, "--env", "qa"}, ExitUsage, "", `unknown environment "qa"`},
		{"bad --var", "", []string{"run", "-s", mon, "--var", "novalue"}, ExitUsage, "", `invalid --var "novalue"`},
		{"missing --config", "", []string{"run", "-s", mon, "-c", filepath.Join(other, "nope.yml")}, ExitUsage, "", "reading config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MONICA_ENV", tt.monicaEnv)
			code, stdout, stderr := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.wantCode, stderr)
			}
			if !strings.Contains(stdout, tt.wantStdout) {
				t.Errorf("stdout %q does not contain %q", stdout, tt.wantStdout)
			}
			if !strings.Contains(stderr, tt.wantStderr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantStderr)
			}
		})
	}
}
