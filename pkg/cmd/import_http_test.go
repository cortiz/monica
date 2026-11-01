package cmd

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func runHTTPImportCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	httpOut, httpForce = "", false
	for _, name := range []string{"out", "force"} {
		importHTTPCmd.Flags().Lookup(name).Changed = false
	}
	return runCLI(t, args...)
}

// httpFileServer stands in for the API of the .http fixtures in
// pkg/importer/httpfile/testdata. Every request it rejects is listed in
// the returned func's result, so a test sees what the server disliked even
// when no script checks the status.
func httpFileServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu       sync.Mutex
		rejected []string
	)
	reject := func(w http.ResponseWriter, r *http.Request, why string) {
		mu.Lock()
		rejected = append(rejected, r.Method+" "+r.URL.String()+": "+why)
		mu.Unlock()
		http.Error(w, why, http.StatusBadRequest)
	}
	checks := map[string]*regexp.Regexp{
		"X-Uuid":      regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`),
		"X-Guid":      regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`),
		"X-Ts":        regexp.MustCompile(`^\d{10}$`),
		"X-Yesterday": regexp.MustCompile(`^\d{10}$`),
		"X-Iso":       regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`),
		"X-Date":      regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`),
		"X-Int":       regexp.MustCompile(`^[1-9]$`),
		"X-Rand":      regexp.MustCompile(`^[1-9]$`),
		"X-Hex":       regexp.MustCompile(`^[0-9a-f]{8}$`),
		"X-Local":     regexp.MustCompile(`^\d\d-\d\d-\d{4}$`),
		"X-Rfc":       regexp.MustCompile(`^[A-Z][a-z]{2}, \d\d [A-Z][a-z]{2} \d{4} \d\d:\d\d:\d\d GMT$`),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := r.Header.Get("Authorization") == "Bearer tok-1"
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch {
		case r.URL.Path == "/login":
			var in struct{ User, Password string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.User != "ada" || in.Password != "pw" {
				reject(w, r, "bad login")
				return
			}
			w.Header().Set("X-Session", "s-9")
			_, _ = io.WriteString(w, `{"token":"tok-1"}`)
		case r.URL.Path == "/v1/products":
			q := r.URL.Query()
			if !bearer || r.Header.Get("X-Api-Key") != "k-123" || q.Get("limit") != "2" || q.Get("currency") != "EUR" {
				reject(w, r, "bad products query")
				return
			}
			if s := r.Header.Get("X-Session"); s != "" && s != "s-9" {
				reject(w, r, "bad session "+s)
				return
			}
			_, _ = io.WriteString(w, `{"items":[{"name":"Pen"},{"name":"Mug"}]}`)
		case r.URL.Path == "/orders":
			var in struct {
				Product int
				User    *string
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			ref := r.Header.Get("X-Ref")
			if !bearer || in.Product != 42 || ref == "" || strings.Contains(ref, "{{") || (in.User != nil && *in.User != "ada") {
				reject(w, r, "bad order")
				return
			}
			w.Header().Set("Location", "/orders/1")
			w.Header().Add("X-Tag", "a")
			w.Header().Add("X-Tag", "b")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "ref": ref})
		case r.URL.Path == "/orders/1/invoice":
			f, _, err := r.FormFile("file")
			if !bearer || err != nil || r.FormValue("note") != "paid" {
				reject(w, r, "bad invoice upload")
				return
			}
			data, _ := io.ReadAll(f)
			_ = json.NewEncoder(w).Encode(map[string]int{"size": len(data)})
		case r.URL.Path == "/dynamic":
			if id := r.URL.Query().Get("id"); id != "" && !checks["X-Uuid"].MatchString(id) {
				reject(w, r, "bad id "+id)
				return
			}
			for name, re := range checks {
				if v := r.Header.Get(name); v != "" && !re.MatchString(v) {
					reject(w, r, name+": "+v)
					return
				}
			}
			if r.Header.Get("X-Client") == "" && r.Header.Get("X-Uuid") == "" {
				reject(w, r, "no default X-Client header")
				return
			}
			_, _ = io.WriteString(w, `{}`)
		default:
			reject(w, r, "unknown path")
		}
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), rejected...)
	}
}

// copyFixture copies a fixture folder, pointing http://localhost:1 at the
// stand-in server.
func copyFixture(t *testing.T, src, host string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p) //nolint:gosec // test fixtures
		if err != nil {
			return err
		}
		return os.WriteFile(target, []byte(strings.ReplaceAll(string(data), "localhost:1", host)), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// TestImportHTTPThenRun imports the JetBrains and VS Code fixtures and runs
// them against a stand-in API: env files, private values, scripts through
// the client/response shim, request variables, dynamic variables, body
// files and multipart all have to work.
func TestImportHTTPThenRun(t *testing.T) {
	srv, rejected := httpFileServer(t)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	fixtures, err := filepath.Abs("../importer/httpfile/testdata")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"DEV_PASSWORD": "pw", "PROD_PASSWORD": "unused", "API_KEY": "k-123", "API_PASSWORD": "pw"} {
		t.Setenv(k, v)
	}
	tests := []struct {
		fixture, env string
		requests     int
		want         []string
	}{
		{"jetbrains", "dev", 5, []string{
			"✓ logIn", "✓ logged in", "✓ listProducts", "✓ two products", "✓ createOrder", "✓ created",
			"✓ uploadInvoice", "✓ uploaded", "✓ globals", "✓ dynamicValues", "✓ dynamic values accepted",
		}},
		{"vscode", "local", 4, []string{"✓ login", "✓ products", "✓ createOrder", "✓ dynamicValues"}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			src := copyFixture(t, filepath.Join(fixtures, tt.fixture), host)
			out := filepath.Join(t.TempDir(), "project")
			code, _, stderr := runHTTPImportCLI(t, "import", "http", src, "-o", out)
			if code != ExitOK || !strings.Contains(stderr, "wrote "+strconv.Itoa(tt.requests)+" requests") {
				t.Fatalf("import: exit %d\n%s", code, stderr)
			}
			chdir(t, out)
			code, stdout, stderr := runCLI(t, "run", "-q", "-r", "3", "--env", tt.env)
			if code != ExitOK {
				t.Errorf("run: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			want := append(tt.want, strconv.Itoa(tt.requests)+" requests: "+strconv.Itoa(tt.requests)+" passed")
			for _, w := range want {
				if !strings.Contains(stdout, w) {
					t.Errorf("run output lacks %q\n%s", w, stdout)
				}
			}
			if r := rejected(); len(r) > 0 {
				t.Errorf("the server rejected:\n%s", strings.Join(r, "\n"))
			}
		})
	}
}

func TestImportHTTPSingleFileDefaultOut(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "My API.http"), []byte("GET https://example.com/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	chdir(t, work)
	code, _, stderr := runHTTPImportCLI(t, "import", "http", filepath.Join(dir, "My API.http"))
	if code != ExitOK || !strings.Contains(stderr, "wrote 1 requests") {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "my-api", "My API.mon")); err != nil {
		t.Error(err)
	}
	// A second import into the same folder needs --force.
	code, _, stderr = runHTTPImportCLI(t, "import", "http", filepath.Join(dir, "My API.http"))
	if code != ExitUsage || !strings.Contains(stderr, "--force") {
		t.Errorf("second import: exit %d\n%s", code, stderr)
	}
	if code, _, stderr = runHTTPImportCLI(t, "import", "http", filepath.Join(dir, "My API.http"), "-f"); code != ExitOK {
		t.Errorf("forced import: exit %d\n%s", code, stderr)
	}
}

func TestImportHTTPErrors(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.http")
	if err := os.WriteFile(file, []byte("GET https://example.com/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"import", "http", filepath.Join(dir, "missing.http")}, "no such file"},
		{[]string{"import", "http", empty, "-o", filepath.Join(dir, "out")}, "no .http or .rest files"},
		{[]string{"import", "http", file, "-o", dir, "-f"}, "must not be the folder"},
		{[]string{"import", "http"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		code, _, stderr := runHTTPImportCLI(t, tt.args...)
		if code != ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit %d, %s", tt.args, code, stderr)
		}
	}
}
