package importer

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/request"
)

// TestWriteMonRoundTrip writes requests and parses them back.
func TestWriteMonRoundTrip(t *testing.T) {
	reqs := []Request{
		{Title: "Create", Name: "create", Comments: []string{"from curl", "two\nlines"}, Timeout: 1500 * time.Millisecond,
			Method: "POST", URL: "https://h/u", Headers: []Header{{"X-A", "1"}, {"X-A", "2"}, {"Empty", ""}},
			Body: "line1\n# not a comment\n\n> plain\n"},
		{Method: "GET", URL: "https://h/{{id}}", Body: "no newline"},
		{Method: "DELETE", URL: "https://h/last", Body: "kept\n\n"},
	}
	var buf bytes.Buffer
	if err := WriteMon(&buf, reqs); err != nil {
		t.Fatal(err)
	}
	got, err := request.ParseRequest(buf.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if len(got) != len(reqs) {
		t.Fatalf("parsed %d requests:\n%s", len(got), buf.String())
	}
	tests := []struct {
		i                        int
		title, name, method, url string
		body                     string
		timeout                  time.Duration
		headers                  map[string][]string
	}{
		{0, "Create", "create", "POST", "https://h/u", "line1\n# not a comment\n\n> plain\n", 1500 * time.Millisecond,
			map[string][]string{"X-A": {"1", "2"}, "Empty": {""}}},
		// Before a separator a body always ends in a line break.
		{1, "", "", "GET", "https://h/{{id}}", "no newline\n", 0, nil},
		{2, "", "", "DELETE", "https://h/last", "kept\n\n", 0, nil},
	}
	for _, tt := range tests {
		r := got[tt.i]
		if r.Title != tt.title || r.Name != tt.name || r.Method != tt.method || r.URL != tt.url || r.Body != tt.body || r.Timeout != tt.timeout {
			t.Errorf("request %d = %q %q %s %s body %q timeout %v", tt.i, r.Title, r.Name, r.Method, r.URL, r.Body, r.Timeout)
		}
		for name, values := range tt.headers {
			if g := r.Headers.Values(name); strings.Join(g, "|") != strings.Join(values, "|") {
				t.Errorf("request %d header %s = %q, want %q", tt.i, name, g, values)
			}
		}
	}
}

func TestWriteMonErrors(t *testing.T) {
	ok := Request{Method: "GET", URL: "https://h"}
	with := func(f func(*Request)) Request { r := ok; f(&r); return r }
	tests := []struct {
		name string
		req  Request
		want string
	}{
		{"bad method", with(func(r *Request) { r.Method = "GE T" }), "invalid method"},
		{"url with space", with(func(r *Request) { r.URL = "https://h/a b" }), "whitespace"},
		{"bad name", with(func(r *Request) { r.Name = "1x" }), "invalid name"},
		{"title newline", with(func(r *Request) { r.Title = "a\nb" }), "title"},
		{"header name", with(func(r *Request) { r.Headers = []Header{{"Bad Name", "x"}} }), "invalid header name"},
		{"header newline", with(func(r *Request) { r.Headers = []Header{{"X", "a\nb"}} }), "line break"},
		{"separator in body", with(func(r *Request) { r.Body = "a\n### b" }), "###"},
		{"script in body", with(func(r *Request) { r.Body = "> {% x %}" }), "script"},
		{"script file in body", with(func(r *Request) { r.Body = "a\n> ./t.js" }), "script"},
		{"file-like body", with(func(r *Request) { r.Body = "< x.json\n" }), "file path"},
		{"body and file", with(func(r *Request) { r.Body, r.BodyFile = "a", "b" }), "both"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteMon(&bytes.Buffer{}, []Request{tt.req})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestWriteMonTrailingBlankLines(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string // error substring; empty means ok
	}{
		{"one newline", "x\n", ""},
		{"no newline", "x", ""},
		{"blank line", "x\n\n", "last request"},
		{"spaces line", "x\n  \n", "last request"},
		{"crlf blank", "x\r\n\r\n", "last request"},
		{"only blank", "\n\n", "last request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := Request{Method: "POST", URL: "https://h/1", Body: tt.body}
			// As the last request, any body is fine.
			if err := WriteMon(&bytes.Buffer{}, []Request{first}); err != nil {
				t.Fatalf("last request: %v", err)
			}
			err := WriteMon(&bytes.Buffer{}, []Request{first, {Method: "GET", URL: "https://h/2"}})
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}
