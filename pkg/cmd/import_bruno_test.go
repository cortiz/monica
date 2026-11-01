package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runBrunoCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	brunoOut, brunoForce = "", false
	for _, name := range []string{"out", "force"} {
		importBrunoCmd.Flags().Lookup(name).Changed = false
	}
	return runCLI(t, args...)
}

// TestImportBrunoThenRun imports the fixture collection and runs it
// against the same stand-in API as the Postman fixture: bru/req/res
// scripts, assert blocks, vars:post-response chaining, folder and
// collection hooks, auth and bodies all have to work.
func TestImportBrunoThenRun(t *testing.T) {
	srv := shopServer(t)
	defer srv.Close()
	src, err := filepath.Abs("../importer/bruno/testdata/shop")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "shop")
	code, _, stderr := runBrunoCLI(t, "import", "bruno", src, "-o", out)
	if code != ExitOK || !strings.Contains(stderr, "wrote 7 requests") {
		t.Fatalf("import: exit %d\n%s", code, stderr)
	}
	for k, v := range map[string]string{"API_PASS": "s3cret", "API_KEY": "k-123", "PASSWORD": "pw", "TRACE_ID": "trace-1"} {
		t.Setenv(k, v)
	}
	chdir(t, out)
	code, stdout, stderr := runCLI(t, "run", "-q", "-r", "3", "--env", "dev", "--var", "baseUrl="+srv.URL)
	if code != ExitOK {
		t.Errorf("run: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"✓ logIn", "✓ res.status: eq 200", "✓ res.body.user.name: eq ada", "✓ returns the user",
		"✓ listProducts", "✓ res.body.items[1].name: eq Mug", "✓ page of products", "✓ json response",
		"✓ getProduct", "✓ createOrder", "✓ created", "✓ uploadInvoice", "✓ searchGraphQL", "✓ ping", "✓ pong", "✓ no server error",
		"7 requests: 7 passed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run output lacks %q\n%s", want, stdout)
		}
	}
}

func TestImportBrunoErrors(t *testing.T) {
	dir := t.TempDir()
	notColl := filepath.Join(dir, "plain")
	if err := os.Mkdir(notColl, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "x.bru")
	if err := os.WriteFile(file, []byte("meta {\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "bruno.json"), []byte(`{"name":"b","type":"collection"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "r.bru"), []byte("get {\n  url: http://h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"import", "bruno", filepath.Join(dir, "missing")}, "no such file"},
		{[]string{"import", "bruno", file}, "is not a folder"},
		{[]string{"import", "bruno", notColl}, "no bruno.json"},
		{[]string{"import", "bruno", broken, "-o", filepath.Join(dir, "out")}, "r.bru: line 1: block get is not closed"},
		{[]string{"import", "bruno", broken, "-o", broken, "-f"}, "must not be the collection folder"},
	}
	for _, tt := range tests {
		code, _, stderr := runBrunoCLI(t, tt.args...)
		if code != ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, %s", tt.args, code, stderr)
		}
	}
}
