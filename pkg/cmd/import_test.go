package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runImportCLI(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	importOut, importForce, importName = "", false, ""
	for _, name := range []string{"out", "force", "name"} {
		importCurlCmd.Flags().Lookup(name).Changed = false
	}
	rootCmd.SetIn(strings.NewReader(stdin))
	t.Cleanup(func() { rootCmd.SetIn(nil) })
	return runCLI(t, args...)
}

func TestImportCurl(t *testing.T) {
	const want = "POST https://h/users\nContent-Type: application/x-www-form-urlencoded\n\na=1"
	tests := []struct {
		name  string
		stdin string
		args  []string
		want  string
	}{
		{"one argument", "", []string{"import", "curl", "curl https://h/users -d a=1"}, want},
		{"stdin", "curl https://h/users \\\n  -d a=1\n", []string{"import", "curl"}, want},
		{"stdin dash", "curl https://h/users -d a=1", []string{"import", "curl", "-"}, want},
		{"after --", "", []string{"import", "curl", "--", "curl", "https://h/users", "-d", "a=1"}, want},
		{"words without --", "", []string{"import", "curl", "curl", "https://h/users"}, "GET https://h/users\n"},
		{"name", "", []string{"import", "curl", "--name", "get", "curl https://h/a https://h/b"},
			"# @name get\nGET https://h/a\n\n###\n# @name get2\nGET https://h/b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runImportCLI(t, tt.stdin, tt.args...)
			if code != ExitOK || out != tt.want {
				t.Errorf("exit %d\nstdout %q\nwant   %q\nstderr %s", code, out, tt.want, errOut)
			}
		})
	}
}

func TestImportCurlOut(t *testing.T) {
	out := filepath.Join(t.TempDir(), "a.mon")
	code, stdout, stderr := runImportCLI(t, "", "import", "curl", "-o", out, "curl -k https://h/x")
	if code != ExitOK || stdout != "" {
		t.Fatalf("exit %d stdout %q stderr %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "warning: -k:") || !strings.Contains(stderr, "wrote "+out) {
		t.Errorf("stderr: %s", stderr)
	}
	if b, _ := os.ReadFile(out); string(b) != "GET https://h/x\n" {
		t.Errorf("file = %q", b)
	}

	code, _, stderr = runImportCLI(t, "", "import", "curl", "-o", out, "curl https://h/y")
	if code != ExitUsage || !strings.Contains(stderr, "exists (use --force") {
		t.Errorf("existing file: exit %d, %s", code, stderr)
	}
	code, _, _ = runImportCLI(t, "", "import", "curl", "-f", "-o", out, "curl https://h/y")
	if b, _ := os.ReadFile(out); code != ExitOK || string(b) != "GET https://h/y\n" {
		t.Errorf("--force: exit %d, file %q", code, b)
	}
}

func TestImportCurlErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no url", []string{"import", "curl", "curl -s"}, "curl: no URL"},
		{"bad quote", []string{"import", "curl", "curl 'https://h"}, "unterminated"},
		{"unwritable body", []string{"import", "curl", "curl https://h --data-raw '### x'"}, "###"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := runImportCLI(t, "", tt.args...)
			if code != ExitUsage || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit %d, stderr %s", code, stderr)
			}
		})
	}
}
