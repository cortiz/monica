package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func runInitCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	initForce, initBaseURL = false, "https://httpbin.org"
	for _, name := range []string{"force", "base-url"} {
		initCmd.Flags().Lookup(name).Changed = false
	}
	return runCLI(t, args...)
}

// TestInitThenRun scaffolds a project and runs its example against a
// stand-in for httpbin.
func TestInitThenRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed any
		_ = json.Unmarshal(body, &parsed)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"url": r.URL.String(), "json": parsed})
	}))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "api")
	code, out, errOut := runInitCLI(t, "init", dir, "--base-url", srv.URL)
	if code != ExitOK {
		t.Fatalf("init exit %d: %s", code, errOut)
	}
	for _, want := range []string{"created     " + filepath.Join(dir, "monica.yml"), "created     " + filepath.Join(dir, ".gitignore")} {
		if !strings.Contains(out, want) {
			t.Errorf("init output lacks %q:\n%s", want, out)
		}
	}

	code, out, errOut = runCLI(t, "run", "-q", "-s", filepath.Join(dir, "example.mon"))
	if code != ExitOK {
		t.Fatalf("run exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "2 requests: 2 passed") || !strings.Contains(out, "2 tests: 2 passed") {
		t.Errorf("run output:\n%s", out)
	}

	code, out, _ = runInitCLI(t, "init", dir)
	if code != ExitOK || !strings.Contains(out, "skipped     "+filepath.Join(dir, "monica.yml")+" (exists; --force to overwrite)") {
		t.Errorf("second init exit %d:\n%s", code, out)
	}
}

func TestInitErrors(t *testing.T) {
	dir := t.TempDir()
	if code, _, errOut := runInitCLI(t, "init", dir, "--base-url", "localhost"); code != ExitUsage || !strings.Contains(errOut, "invalid base URL") {
		t.Errorf("bad base URL: exit %d, %s", code, errOut)
	}
	if code, _, _ := runInitCLI(t, "init", dir, "extra"); code != ExitUsage {
		t.Errorf("two args: exit %d", code)
	}
}
