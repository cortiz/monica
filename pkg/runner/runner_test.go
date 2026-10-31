package runner_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

// recorder is a test server that records the paths it was asked for.
type recorder struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newRecorder(t *testing.T) *recorder {
	t.Helper()
	rec := &recorder{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		_, _ = w.Write([]byte("served " + r.URL.Path))
	}))
	t.Cleanup(rec.Close)
	return rec
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func writeMon(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.mon")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close() // connections are refused from now on
	return u
}

func TestRunSingleFile(t *testing.T) {
	srv := newRecorder(t)
	dead := closedURL(t)

	tests := []struct {
		name      string
		mon       string
		wantPaths []string
		wantOut   []string // substrings of stdout, in order
		wantErr   string   // substring; empty means success
		wantInput bool     // error must be *runner.InputError
	}{
		{
			name:      "requests run in file order",
			mon:       "GET " + srv.URL + "/one\n###\nGET " + srv.URL + "/two\n### third\nGET " + srv.URL + "/three\n",
			wantPaths: []string{"/one", "/two", "/three"},
			wantOut:   []string{"served /one", "served /two", "served /three"},
		},
		{
			name:      "failure does not stop later requests",
			mon:       "GET " + srv.URL + "/a\n### broken\nGET " + dead + "/x\n###\nGET " + srv.URL + "/b\n",
			wantPaths: []string{"/a", "/b"},
			wantOut:   []string{"served /a", "served /b"},
			wantErr:   "test.mon:3: broken: sending HTTP request",
		},
		{
			name:      "parse error sends nothing",
			mon:       "GET " + srv.URL + "/a\n###\nGET\n",
			wantPaths: nil,
			wantErr:   "test.mon:3: invalid request line",
			wantInput: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(srv.seen())
			var out bytes.Buffer
			err := runner.RunSingleFile(writeMon(t, tt.mon), &runner.Opts{}, &out)

			if got := srv.seen()[before:]; !equal(got, tt.wantPaths) {
				t.Errorf("server saw %v, want %v", got, tt.wantPaths)
			}
			rest := out.String()
			for _, s := range tt.wantOut {
				i := strings.Index(rest, s)
				if i < 0 {
					t.Fatalf("stdout missing %q (in order); got %q", s, out.String())
				}
				rest = rest[i+len(s):]
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
			if got := errors.As(err, &ie); got != tt.wantInput {
				t.Errorf("InputError = %v, want %v (err: %v)", got, tt.wantInput, err)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
