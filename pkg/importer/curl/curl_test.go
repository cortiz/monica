package curl

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/request"
)

// TestGolden converts each testdata/*.curl command and compares the .mon
// text with testdata/*.mon, then checks the .mon file parses.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*.curl")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".curl")
		t.Run(name, func(t *testing.T) {
			cmd, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			res, err := ParseCommand(string(cmd))
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := importer.WriteMon(&buf, res.Requests); err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(strings.TrimSuffix(file, ".curl") + ".mon")
			if err != nil {
				t.Fatal(err)
			}
			if buf.String() != string(want) {
				t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
			}
			// Body and multipart files are read at parse time: give them stand-ins.
			dir := t.TempDir()
			for _, line := range strings.Split(buf.String(), "\n") {
				if path, ok := strings.CutPrefix(line, "< "); ok {
					path = filepath.Join(dir, path)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			mon := filepath.Join(dir, "out.mon")
			if err := os.WriteFile(mon, buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := request.ParseMonFile(mon); err != nil {
				t.Errorf("output does not parse: %v", err)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      []string
		wantWarns int
	}{
		{"plain", "curl -X GET http://h", []string{"curl", "-X", "GET", "http://h"}, 0},
		{"single quotes", `curl 'a b' 'it''s'`, []string{"curl", "a b", "its"}, 0},
		{"double quotes", `curl "a \"b\" \\ \$x \n"`, []string{"curl", `a "b" \ $x \n`}, 0},
		{"ansi-c", `$'a\nb\t\'c\x41é\101'`, []string{"a\nb\t'cAéA"}, 0},
		{"continuation", "curl \\\n  -s \\\r\n  url", []string{"curl", "-s", "url"}, 0},
		{"windows caret", "curl ^\r\n  -s ^\n  url", []string{"curl", "-s", "url"}, 0},
		{"backslash escape", `a\ b c\"d`, []string{"a b", `c"d`}, 0},
		{"adjacent quotes join", `-H'X: '"$y"`, []string{"-HX: $y"}, 1},
		{"pipe ends command", "curl url | jq .", []string{"curl", "url"}, 1},
		{"comment", "curl url # fetch it", []string{"curl", "url"}, 0},
		{"hash inside word", "curl url#frag", []string{"curl", "url#frag"}, 0},
		{"variable", "curl $BASE/x", []string{"curl", "$BASE/x"}, 1},
		{"dollar alone", "curl 'a' $ b", []string{"curl", "a", "$", "b"}, 0},
		{"empty", "  \n", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warns, err := Split(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("words = %q, want %q", got, tt.want)
			}
			if len(warns) != tt.wantWarns {
				t.Errorf("warnings = %q, want %d", warns, tt.wantWarns)
			}
		})
	}
}

func TestSplitErrors(t *testing.T) {
	for _, in := range []string{`curl 'x`, `curl "x`, `curl $'x`} {
		if _, _, err := Split(in); err == nil {
			t.Errorf("Split(%q): expected an error", in)
		}
	}
}

func TestParse(t *testing.T) {
	type hdr = importer.Header
	h := func(name, value string) hdr { return hdr{Name: name, Value: value} }
	tests := []struct {
		name     string
		args     []string
		want     importer.Request
		warnings []string
	}{
		{
			name: "default GET with scheme added",
			args: []string{"curl", "example.com"},
			want: importer.Request{Method: "GET", URL: "http://example.com"},
		},
		{
			name: "combined short options",
			args: []string{"-sSLXDELETE", "-HAccept:text/plain", "https://h/x"},
			want: importer.Request{Method: "DELETE", URL: "https://h/x", Headers: []hdr{h("Accept", "text/plain")}},
		},
		{
			name: "-d strips line breaks, data-raw keeps them, joined with &",
			args: []string{"https://h", "-d", "a=1\n", "--data-raw", "b=2\n"},
			want: importer.Request{Method: "POST", URL: "https://h", Headers: []hdr{h("Content-Type", "application/x-www-form-urlencoded")}, Body: "a=1&b=2\n"},
		},
		{
			name: "explicit content type kept, -X wins over -d",
			args: []string{"-X", "PUT", "-H", "content-type: text/plain", "-d", "hi", "https://h"},
			want: importer.Request{Method: "PUT", URL: "https://h", Headers: []hdr{h("content-type", "text/plain")}, Body: "hi"},
		},
		{
			name: "-G appends to an existing query and keeps the fragment",
			args: []string{"-G", "https://h/s?x=1#top", "-d", "q=a b"},
			want: importer.Request{Method: "GET", URL: "https://h/s?x=1&q=a%20b#top"},
		},
		{
			name: "--url-query",
			args: []string{"https://h/s", "--url-query", "q=ü&x", "--url-query", "raw"},
			want: importer.Request{Method: "GET", URL: "https://h/s?q=%C3%BC%26x&raw"},
		},
		{
			name: "user, referer, bearer, empty header",
			args: []string{"https://h", "-u", "ada", "-e", "https://r/;auto", "--oauth2-bearer", "tok", "-H", "X-Empty;"},
			want: importer.Request{Method: "GET", URL: "https://h", Headers: []hdr{
				h("Referer", "https://r/"), h("Authorization", "Bearer tok"), h("X-Empty", ""), h("Authorization", "Basic YWRhOg=="),
			}},
			warnings: []string{"-u ada has no password (curl would prompt for it); converted with an empty one"},
		},
		{
			name: "ignored and unknown options",
			args: []string{"https://h", "-k", "-o", "out.txt", "--proxy", "http://p", "-H", "Accept:", "-b", "jar.txt", "--weird", "-Z"},
			want: importer.Request{Method: "GET", URL: "https://h"},
			warnings: []string{
				"-k: pass -k/--insecure to monica run, or set insecureSkipVerify: true in monica.yml",
				"option --proxy http://p ignored",
				`-H "Accept:" removes a header in curl; ignored`,
				"-b jar.txt reads cookies from a file; not converted",
				"option --weird ignored",
				"option -Z ignored",
			},
		},
		{
			name:     "-d @file warns about line breaks",
			args:     []string{"https://h", "-d", "@body.txt", "-H", "Content-Type: text/plain"},
			want:     importer.Request{Method: "POST", URL: "https://h", Headers: []hdr{h("Content-Type", "text/plain")}, BodyFile: "body.txt"},
			warnings: []string{"-d @body.txt: curl strips line breaks from the file; monica sends it as is (like --data-binary)"},
		},
		{
			name:     "braces warn",
			args:     []string{"https://h", "--data-raw", `{{x}}`},
			want:     importer.Request{Method: "POST", URL: "https://h", Headers: []hdr{h("Content-Type", "application/x-www-form-urlencoded")}, Body: "{{x}}"},
			warnings: []string{`the request contains "{{", which monica reads as a {{variable}}`},
		},
		{
			name: "-- ends options",
			args: []string{"-s", "--", "-not-an-option.example.com"},
			want: importer.Request{Method: "GET", URL: "http://-not-an-option.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Parse(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Requests) != 1 {
				t.Fatalf("got %d requests", len(res.Requests))
			}
			if !reflect.DeepEqual(res.Requests[0], tt.want) {
				t.Errorf("request =\n%#v\nwant\n%#v", res.Requests[0], tt.want)
			}
			if !reflect.DeepEqual(res.Warnings, tt.warnings) {
				t.Errorf("warnings = %q, want %q", res.Warnings, tt.warnings)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no URL", []string{"curl", "-s"}, "no URL"},
		{"missing value", []string{"https://h", "-H"}, "-H needs a value"},
		{"bad scheme", []string{"ftp://h/x"}, "unsupported URL scheme"},
		{"bad header", []string{"https://h", "-H", "nocolon"}, "invalid header"},
		{"header file", []string{"https://h", "-H", "@headers.txt"}, "reads headers from a file"},
		{"form and data", []string{"https://h", "-F", "a=1", "-d", "x"}, "cannot be combined"},
		{"form without =", []string{"https://h", "-F", "a"}, "invalid -F"},
		{"form stdin", []string{"https://h", "-F", "a=@-"}, "stdin"},
		{"bad max-time", []string{"https://h", "-m", "soon"}, "invalid -m"},
		{"file with other data", []string{"https://h", "-d", "@a", "-d", "b"}, "cannot be combined"},
		{"stdin data", []string{"https://h", "-d", "@-"}, "stdin"},
		{"upload and data", []string{"https://h", "-T", "f", "-d", "x"}, "cannot be combined"},
		{"urlencode file", []string{"https://h", "--data-urlencode", "name@file"}, "reads a file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
