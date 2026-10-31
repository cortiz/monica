package runner

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

	"github.com/rs/zerolog"
	"jmpeax.com/sec/monica/internal/logging"
)

func TestMasker(t *testing.T) {
	m := newMasker([]string{"abc", "", "abcdef", "abc", "x.y"})
	tests := []struct{ in, want string }{
		{"no secrets here", "no secrets here"},
		{"token=abcdef&k=abc", "token=****&k=****"}, // longer secret masked whole
		{"x.y but not xzy", "**** but not xzy"},     // literal match, not a pattern
	}
	for _, tt := range tests {
		if got := m.String(tt.in); got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := newMasker(nil).String("abc"); got != "abc" {
		t.Errorf("empty masker changed text: %q", got)
	}

	base := &InputError{Err: errors.New("bad abc")}
	err := m.Error(fmt.Errorf("wrap: %w", base))
	var ie *InputError
	if err.Error() != "wrap: bad ****" || !errors.As(err, &ie) {
		t.Errorf("Error() = %q (InputError reachable: %v)", err, errors.As(err, &ie))
	}
}

func TestRunMasksSecrets(t *testing.T) {
	const secret = "s3cr3t-value"
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.Query().Get("key") + "|" + r.Header.Get("Authorization")
		_, _ = fmt.Fprintf(w, "echo key=%s auth=%s", r.URL.Query().Get("key"), r.Header.Get("Authorization"))
	}))
	defer srv.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	dir := t.TempDir()
	yml := "version: 1\ndefaults:\n  headers:\n    Authorization: Bearer ${API_KEY}\n  vars:\n    key: ${API_KEY}\n    base: " + srv.URL + "\n    dead: " + deadURL + "\n"
	if err := os.WriteFile(filepath.Join(dir, "monica.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	mon := filepath.Join(dir, "t.mon")
	content := "GET {{base}}/x?key={{key}}\n###\nGET {{dead}}/x?key={{key}}\n"
	if err := os.WriteFile(mon, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	prev := logging.Log
	logging.Log = zerolog.New(&logs).Level(zerolog.DebugLevel)
	t.Cleanup(func() { logging.Log = prev })

	var out bytes.Buffer
	opts := &Opts{LookupEnv: func(name string) (string, bool) { return secret, name == "API_KEY" }}
	err := RunSingleFile(mon, opts, &out)

	if received != secret+"|Bearer "+secret {
		t.Fatalf("server must receive the real secret, got %q", received)
	}
	if err == nil {
		t.Fatal("expected the request to the closed server to fail")
	}
	for what, text := range map[string]string{"stdout": out.String(), "error": err.Error(), "logs": logs.String()} {
		if strings.Contains(text, secret) {
			t.Errorf("%s leaks the secret: %s", what, text)
		}
	}
	if !strings.Contains(out.String(), "echo key=**** auth=Bearer ****") {
		t.Errorf("stdout not masked as expected: %q", out.String())
	}
	if !strings.Contains(logs.String(), "key=****") {
		t.Errorf("debug log should show the masked URL: %s", logs.String())
	}
}
