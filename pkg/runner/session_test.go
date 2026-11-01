package runner_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

func TestRunRequestAt(t *testing.T) {
	rec := newRecorder(t)
	file := writeMon(t, `### a
GET `+rec.URL+`/a

> {% vars.set("token", "t-" + res.body.length) %}

### b
GET `+rec.URL+`/b?{{token}}

### c
GET `+rec.URL+`/c
`)
	c := &collect{}
	r := runner.NewWithReporter(&runner.Opts{}, c)
	// Lines of the request lines: a=2, b=7, c=10.
	tests := []struct {
		line      int
		wantPaths []string
		wantErr   string
	}{
		{7, nil, "unknown variable"}, // b needs a's token, which isn't set yet
		{2, []string{"/a"}, ""},
		{7, []string{"/a", "/b"}, ""}, // vars.set from the earlier send applies
		{3, []string{"/a", "/b"}, ""}, // not a request line: nothing runs
		{10, []string{"/a", "/b", "/c"}, ""},
	}
	for i, tt := range tests {
		err := r.RunRequestAt(file, tt.line)
		if (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("step %d: err = %v, want %q", i, err, tt.wantErr)
		}
		if got := rec.seen(); !reflect.DeepEqual(got, tt.wantPaths) {
			t.Errorf("step %d: paths = %v, want %v", i, got, tt.wantPaths)
		}
	}
	if len(c.results) != 4 {
		t.Fatalf("results = %d, want 4", len(c.results))
	}
	last := c.results[3]
	if last.ResponseStatus != "HTTP/1.1 200 OK" || last.ResponseBody != "served /c" || last.ResponseHeaders["Content-Type"] == nil {
		t.Errorf("response parts = %q %q %v", last.ResponseStatus, last.ResponseBody, last.ResponseHeaders)
	}
	// RunFile afterwards runs everything again.
	if err := r.RunFile(file); err != nil {
		t.Fatal(err)
	}
	if n := len(rec.seen()); n != 6 {
		t.Errorf("RunFile after RunRequestAt sent %d in total, want 6", n)
	}
}

func TestVariables(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "monica.yml"), []byte(`version: 1
defaults:
  vars:
    base: http://x
    token: ${TOK}
    shared: from-config
envs:
  dev:
    vars:
      base: http://dev
`), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.mon")
	rec := newRecorder(t)
	if err := os.WriteFile(file, []byte("GET "+rec.URL+"\n\n> {% vars.set(\"later\", \"set by a script with \" + vars.get(\"token\")) %}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runner.NewWithReporter(&runner.Opts{
		Env:       "dev",
		Vars:      map[string]string{"shared": "from-flag"},
		LookupEnv: func(name string) (string, bool) { return "s3cret", name == "TOK" },
	}, &collect{})
	if err := r.RunFile(file); err != nil {
		t.Fatal(err)
	}
	got, err := r.Variables(file)
	if err != nil {
		t.Fatal(err)
	}
	want := []runner.Variable{
		{"base", "http://dev", "config"},
		{"later", "set by a script with ****", "vars.set"},
		{"shared", "from-flag", "--var"},
		{"token", "****", "config"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Variables =\n%v\nwant\n%v", got, want)
	}
}
