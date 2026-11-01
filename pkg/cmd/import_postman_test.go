package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runPostmanCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	postmanEnvs, postmanOut, postmanForce = nil, "", false
	for _, name := range []string{"env", "out", "force"} {
		importPostmanCmd.Flags().Lookup(name).Changed = false
	}
	return runCLI(t, args...)
}

// shopServer stands in for the API of testdata/shop.postman_collection.json.
func shopServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Client") != "monica-import" { // set by the collection's pre-request hook
			http.Error(w, "missing X-Client", http.StatusBadRequest)
			return
		}
		basicOK := func() bool {
			u, p, ok := r.BasicAuth()
			return ok && u == "ada" && p == "s3cret"
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			var in struct{ User, Password string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.User != "ada" || in.Password != "pw" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"token":"tok-1","user":{"id":7,"name":"ada"}}`)
		case "/products":
			if !basicOK() {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"items":[{"name":"Pen","price":2},{"name":"Mug","price":9}]}`)
		case "/products/42":
			if r.URL.Query().Get("api_key") != "k-123" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, `{"id":42}`)
		case "/orders":
			if !basicOK() || r.FormValue("note") != "gift wrap & card" || r.FormValue("product") != "42" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"ref": r.FormValue("ref")})
		case "/invoices":
			f, _, err := r.FormFile("file")
			if !basicOK() || err != nil || r.FormValue("orderId") != "7" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(f)
			_ = json.NewEncoder(w).Encode(map[string]string{"size": string(rune('0' + len(data)))})
		case "/graphql":
			if r.Header.Get("Authorization") != "Bearer tok-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `{"data":{"search":[{"id":1}]}}`)
		default:
			_, _ = io.WriteString(w, "pong")
		}
	}))
}

// TestImportPostmanThenRun imports the fixture collection and runs it:
// pm scripts, chaining through pm.environment / collectionVariables,
// folder and collection hooks, auth and bodies all have to work.
func TestImportPostmanThenRun(t *testing.T) {
	srv := shopServer(t)
	defer srv.Close()
	fixtures, err := filepath.Abs("../importer/postman/testdata")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "shop")
	code, _, stderr := runPostmanCLI(t, "import", "postman", filepath.Join(fixtures, "shop.postman_collection.json"),
		"--env", filepath.Join(fixtures, "dev.postman_environment.json"), "-o", out)
	if code != ExitOK || !strings.Contains(stderr, "wrote 7 requests") || !strings.Contains(stderr, "IMPORT_REPORT.md") {
		t.Fatalf("import: exit %d\n%s", code, stderr)
	}

	// What the report asks for: the invoice file and the secrets.
	if err := os.MkdirAll(filepath.Join(out, "products-orders/files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "products-orders/files/invoice.pdf"), []byte("%PDF-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"API_SECRET": "unused-secret", "API_PASS": "s3cret", "API_KEY": "k-123", "PASSWORD": "pw"} {
		t.Setenv(k, v)
	}
	chdir(t, out)
	code, stdout, stderr := runCLI(t, "run", "-q", "-r", "3", "--env", "shop-dev", "--var", "baseUrl="+srv.URL)
	for _, want := range []string{
		"✓ logIn", "✓ listProducts", "✓ getProduct", "✓ createOrder", "✓ uploadInvoice", "✓ searchGraphQL",
		"✗ pingRandomFirstName", // {{$randomFirstName}} has no monica equivalent (in the report)
		"7 requests: 6 passed, 1 failed",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("run output lacks %q\nstdout:\n%s\nstderr:\n%s", want, stdout, stderr)
		}
	}
	if code != ExitUsage || !strings.Contains(stdout+stderr, `unknown built-in "$randomFirstName"`) {
		t.Errorf("exit %d (want 2 for the unknown dynamic variable)", code)
	}
	// Tests from scripts and hooks ran: login's two, the folder's and collection's afterEach.
	if !strings.Contains(stdout, "tests: ") || strings.Contains(stdout, " 0 tests") {
		t.Errorf("no tests ran:\n%s", stdout)
	}
}

func TestImportPostmanErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"info": {"name": "x", "schema": "https://schema.getpostman.com/json/collection/v1.0.0/collection.json"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"import", "postman", filepath.Join(dir, "missing.json")}, "no such file"},
		{[]string{"import", "postman", bad}, "unsupported collection schema"},
		{[]string{"import", "postman"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		code, _, stderr := runPostmanCLI(t, tt.args...)
		if code != ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, %s", tt.args, code, stderr)
		}
	}
}
