package request_test

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestParseMonFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    []*request.Request
		wantErr string // substring of the error; empty means no error
	}{
		{
			name: "simple GET",
			file: "testdata/simple.mon",
			want: []*request.Request{{
				File:   "testdata/simple.mon",
				Line:   2,
				Method: "GET",
				URL:    "http://httpbin.org/status/200",
				Headers: http.Header{
					"Host":       {"httpbin.org"},
					"User-Agent": {"monica/0.1.0"},
				},
			}},
		},
		{
			name: "simple POST with JSON body",
			file: "testdata/simple_post.mon",
			want: []*request.Request{{
				File:   "testdata/simple_post.mon",
				Line:   2,
				Method: "POST",
				URL:    "http://httpbin.org/anything",
				Headers: http.Header{
					"Host":       {"httpbin.org"},
					"User-Agent": {"monica/0.1.0"},
				},
				Body: "{\n  \"key\": \"value\"\n}\n",
			}},
		},
		{
			name: "header split on first colon only",
			file: "testdata/header_colon_value.mon",
			want: []*request.Request{{
				File:   "testdata/header_colon_value.mon",
				Line:   2,
				Method: "GET",
				URL:    "http://example.com/",
				Headers: http.Header{
					"X-Foo":         {"a: b"},
					"X-Spaced":      {"padded value"},
					"Authorization": {"Bearer abc:def"},
				},
			}},
		},
		{
			name: "repeated headers survive",
			file: "testdata/repeated_headers.mon",
			want: []*request.Request{{
				File:   "testdata/repeated_headers.mon",
				Line:   2,
				Method: "GET",
				URL:    "http://example.com/",
				Headers: http.Header{
					"Accept": {"text/html", "application/json"},
					"Cookie": {"a=1", "b=2"},
				},
			}},
		},
		{
			name: "body kept verbatim",
			file: "testdata/body_exact.mon",
			want: []*request.Request{{
				File:    "testdata/body_exact.mon",
				Line:    2,
				Method:  "POST",
				URL:     "http://example.com/notes",
				Headers: http.Header{"Content-Type": {"text/markdown"}},
				Body:    "# Title\n\nline after blank\n\n",
			}},
		},
		{
			name: "request line with HTTP version",
			file: "testdata/request_line_version.mon",
			want: []*request.Request{{
				File:    "testdata/request_line_version.mon",
				Line:    2,
				Method:  "PUT",
				URL:     "https://example.com/items/1",
				Proto:   "HTTP/1.1",
				Headers: http.Header{"Content-Type": {"application/json"}},
				Body:    `{"id":1}`,
			}},
		},
		{
			name: "multiple requests with titles",
			file: "testdata/multiple_requests.mon",
			want: []*request.Request{
				{
					File:    "testdata/multiple_requests.mon",
					Line:    3,
					Title:   "List users",
					Method:  "GET",
					URL:     "http://example.com/users",
					Headers: http.Header{"Accept": {"application/json"}},
				},
				{
					File:    "testdata/multiple_requests.mon",
					Line:    7,
					Title:   "Create user",
					Method:  "POST",
					URL:     "http://example.com/users",
					Headers: http.Header{"Content-Type": {"application/json"}},
					Body:    "{\"name\":\"ada\"}\n",
				},
				{
					File:    "testdata/multiple_requests.mon",
					Line:    14,
					Title:   "Delete user",
					Method:  "DELETE",
					URL:     "http://example.com/users/1",
					Headers: http.Header{},
				},
			},
		},
		{
			name: "empty sections are skipped",
			file: "testdata/empty_sections.mon",
			want: []*request.Request{
				{
					File:    "testdata/empty_sections.mon",
					Line:    5,
					Method:  "GET",
					URL:     "http://example.com/a",
					Headers: http.Header{},
				},
				{
					File:    "testdata/empty_sections.mon",
					Line:    9,
					Method:  "GET",
					URL:     "http://example.com/b",
					Headers: http.Header{"X-A": {"1"}},
					Body:    "# a body line, not a comment\n",
				},
			},
		},
		{
			name: "metadata directives",
			file: "testdata/metadata.mon",
			want: []*request.Request{
				{
					File:    "testdata/metadata.mon",
					Line:    4,
					Title:   "Login",
					Name:    "login",
					Timeout: 5 * time.Second,
					Method:  "POST",
					URL:     "http://example.com/login",
					Headers: http.Header{"Content-Type": {"application/json"}},
					Body:    "{\"user\":\"ada\"}\n",
				},
				{
					File:       "testdata/metadata.mon",
					Line:       12,
					Skip:       true,
					SkipReason: "flaky upstream",
					Method:     "GET",
					URL:        "http://example.com/flaky",
					Headers:    http.Header{},
				},
				{
					File:    "testdata/metadata.mon",
					Line:    17,
					Name:    "list-users.v2",
					Skip:    true,
					Method:  "GET",
					URL:     "http://example.com/users",
					Headers: http.Header{},
				},
			},
		},
		{
			name: "body from file",
			file: "testdata/body_file.mon",
			want: []*request.Request{
				{
					File:     "testdata/body_file.mon",
					Line:     1,
					Method:   "POST",
					URL:      "http://example.com/users",
					Headers:  http.Header{"Content-Type": {"application/json"}},
					Body:     "{\"name\": \"ada\"}\n",
					BodyFile: "testdata/payloads/user.json",
				},
				{
					File:    "testdata/body_file.mon",
					Line:    7,
					Method:  "PUT",
					URL:     "http://example.com/users/1",
					Headers: http.Header{},
					Body:    "<./not-a-ref\n",
				},
				{
					File:    "testdata/body_file.mon",
					Line:    11,
					Method:  "POST",
					URL:     "http://example.com/notes",
					Headers: http.Header{},
					Body:    "< ./payloads/user.json\nand a second line\n",
				},
			},
		},
		{
			name:    "body file that does not exist",
			file:    "testdata/body_file_missing.mon",
			wantErr: "testdata/body_file_missing.mon:3: reading body file: open testdata/payloads/missing.json",
		},
		{
			name: "inline script blocks",
			file: "testdata/script_blocks.mon",
			want: []*request.Request{
				{
					File:    "testdata/script_blocks.mon",
					Line:    6,
					Title:   "Login",
					Name:    "login",
					Method:  "POST",
					URL:     "http://example.com/login",
					Headers: http.Header{"Content-Type": {"application/json"}},
					Body:    "{\"user\":\"ada\"}\n",
					PreScript: &request.Script{
						Source: "\n  vars.set(\"ts\", Date.now());\n",
						File:   "testdata/script_blocks.mon",
						Line:   3,
					},
					PostScript: &request.Script{
						Source: "\n  test(\"ok\", () => expect(res.status).toBe(200));\n  vars.set(\"token\", res.json().token);\n",
						File:   "testdata/script_blocks.mon",
						Line:   11,
					},
				},
				{
					File:       "testdata/script_blocks.mon",
					Line:       18,
					Method:     "GET",
					URL:        "http://example.com/me",
					Headers:    http.Header{"Authorization": {"Bearer x"}},
					PreScript:  &request.Script{Source: ` req.headers.set("X-Trace", "1") `, File: "testdata/script_blocks.mon", Line: 17},
					PostScript: &request.Script{Source: ` test("me", () => {}) `, File: "testdata/script_blocks.mon", Line: 20},
				},
				{
					File:    "testdata/script_blocks.mon",
					Line:    23,
					Method:  "GET",
					URL:     "http://example.com/quote",
					Headers: http.Header{},
					Body:    "> just a quote, not a script\n",
				},
			},
		},
		{
			name: "script files",
			file: "testdata/script_files.mon",
			want: []*request.Request{{
				File:       "testdata/script_files.mon",
				Line:       2,
				Method:     "GET",
				URL:        "http://example.com/users",
				Headers:    http.Header{},
				PreScript:  &request.Script{Source: "vars.set(\"ts\", Date.now());\n", File: "testdata/scripts/pre.js", Line: 1},
				PostScript: &request.Script{Source: "test(\"lists users\", () => {\n  expect(res.status).toBe(200);\n});\n", File: "testdata/scripts/users_test.js", Line: 1},
			}},
		},
		{
			name:    "script file that does not exist",
			file:    "testdata/script_file_missing.mon",
			wantErr: "testdata/script_file_missing.mon:2: reading post-response script: open testdata/scripts/missing.js",
		},
		{
			name:    "unterminated script block swallows separators",
			file:    "testdata/script_unterminated.mon",
			wantErr: "testdata/script_unterminated.mon:3: unterminated post-response script (missing \"%}\")",
		},
		{
			name:    "duplicate request name",
			file:    "testdata/duplicate_name.mon",
			wantErr: "testdata/duplicate_name.mon:4: duplicate request name \"a\" (first at line 1)",
		},
		{
			name:    "directive after the request line",
			file:    "testdata/directive_after_request_line.mon",
			wantErr: "testdata/directive_after_request_line.mon:2: directive \"# @name late\" must come before the request line",
		},
		{
			name:    "missing request line",
			file:    "testdata/missing_request_line.mon",
			wantErr: "testdata/missing_request_line.mon: missing request line",
		},
		{
			name:    "request line without URL",
			file:    "testdata/bad_request_line.mon",
			wantErr: "testdata/bad_request_line.mon:3: invalid request line",
		},
		{
			name:    "error in a later request reports its line",
			file:    "testdata/multi_request_error.mon",
			wantErr: "testdata/multi_request_error.mon:4: invalid request line",
		},
		{
			name:    "invalid method",
			file:    "testdata/invalid_method.mon",
			wantErr: "testdata/invalid_method.mon:2: invalid method",
		},
		{
			name:    "URL without scheme",
			file:    "testdata/invalid_url.mon",
			wantErr: "testdata/invalid_url.mon:2: invalid URL \"example.com/path\": scheme must be http or https",
		},
		{
			name:    "header without colon",
			file:    "testdata/invalid_header.mon",
			wantErr: "testdata/invalid_header.mon:3: invalid header",
		},
		{
			name:    "invalid HTTP version",
			file:    "testdata/invalid_version.mon",
			wantErr: "testdata/invalid_version.mon:2: invalid HTTP version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := request.ParseMonFile(tt.file)
			checkResult(t, got, err, tt.want, tt.wantErr)
			if err != nil {
				var pe *request.ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("expected *request.ParseError, got %T", err)
				}
			}
		})
	}
}

func TestParseMonFileMissingFile(t *testing.T) {
	req, err := request.ParseMonFile("testdata/does_not_exist.mon")
	if err == nil {
		t.Fatalf("expected error, got requests %+v", req)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
	if !strings.Contains(err.Error(), "testdata/does_not_exist.mon") {
		t.Fatalf("error %q should name the file", err)
	}
}

func TestParseRequest(t *testing.T) {
	// one builds the expected result for a single request on line 1.
	one := func(r request.Request) []*request.Request {
		if r.Line == 0 {
			r.Line = 1
		}
		return []*request.Request{&r}
	}
	tests := []struct {
		name    string
		raw     string
		want    []*request.Request
		wantErr string
	}{
		{
			name: "headers and multi-line body",
			raw:  "GET https://www.google.com HTTP/1.1\nHost: www.google.com\nUser-Agent: monica/0.1.0\n\nmulti\nline\nbody\n",
			want: one(request.Request{
				Method: "GET",
				URL:    "https://www.google.com",
				Proto:  "HTTP/1.1",
				Headers: http.Header{
					"Host":       {"www.google.com"},
					"User-Agent": {"monica/0.1.0"},
				},
				Body: "multi\nline\nbody\n",
			}),
		},
		{
			name: "CRLF line endings",
			raw:  "POST http://example.com/ HTTP/1.1\r\nX-A: 1\r\n\r\nbody\r\n",
			want: one(request.Request{
				Method:  "POST",
				URL:     "http://example.com/",
				Proto:   "HTTP/1.1",
				Headers: http.Header{"X-A": {"1"}},
				Body:    "body\r\n",
			}),
		},
		{
			name: "no headers, no trailing newline",
			raw:  "DELETE http://example.com/x",
			want: one(request.Request{Method: "DELETE", URL: "http://example.com/x", Headers: http.Header{}}),
		},
		{
			name: "comments and blank lines before request line, comment among headers",
			raw:  "\n# a\n\nGET http://example.com/\n# skipped\nX-A: 1\n",
			want: one(request.Request{Line: 4, Method: "GET", URL: "http://example.com/", Headers: http.Header{"X-A": {"1"}}}),
		},
		{
			name: "lowercase header names are canonicalized",
			raw:  "GET http://example.com/\ncontent-type: text/plain\n",
			want: one(request.Request{Method: "GET", URL: "http://example.com/", Headers: http.Header{"Content-Type": {"text/plain"}}}),
		},
		{
			name: "empty header value",
			raw:  "GET http://example.com/\nX-Empty:\n",
			want: one(request.Request{Method: "GET", URL: "http://example.com/", Headers: http.Header{"X-Empty": {""}}}),
		},
		{
			name: "separator ends headers without a body",
			raw:  "GET http://a.example/\nX-A: 1\n###\nGET http://b.example/\n",
			want: []*request.Request{
				{Line: 1, Method: "GET", URL: "http://a.example/", Headers: http.Header{"X-A": {"1"}}},
				{Line: 4, Method: "GET", URL: "http://b.example/", Headers: http.Header{}},
			},
		},
		{
			name: "separator trims trailing blank lines of the body, CRLF",
			raw:  "POST http://a.example/\r\n\r\nbody\r\n\r\n \r\n### next\r\nGET http://b.example/\r\n",
			want: []*request.Request{
				{Line: 1, Method: "POST", URL: "http://a.example/", Headers: http.Header{}, Body: "body\r\n"},
				{Line: 7, Title: "next", Method: "GET", URL: "http://b.example/", Headers: http.Header{}},
			},
		},
		{
			name: "title text is trimmed, extra hashes allowed",
			raw:  "####   Spaced title  \nGET http://a.example/\n",
			want: one(request.Request{Line: 2, Title: "Spaced title", Method: "GET", URL: "http://a.example/", Headers: http.Header{}}),
		},
		{
			name: "title does not leak into an untitled section",
			raw:  "### first\n###\nGET http://a.example/\n",
			want: one(request.Request{Line: 3, Method: "GET", URL: "http://a.example/", Headers: http.Header{}}),
		},
		{
			name: "tab after directive key",
			raw:  "#\t@timeout\t250ms\nGET http://a.example/\n",
			want: one(request.Request{Line: 2, Timeout: 250 * time.Millisecond, Method: "GET", URL: "http://a.example/", Headers: http.Header{}}),
		},
		{
			name: "directive-like line in the body is body text",
			raw:  "POST http://a.example/\n\n# @name body\n",
			want: one(request.Request{Method: "POST", URL: "http://a.example/", Headers: http.Header{}, Body: "# @name body\n"}),
		},
		{
			name: "title and name together",
			raw:  "### title\n# @name n1\nGET http://a.example/\n",
			want: one(request.Request{Line: 3, Title: "title", Name: "n1", Method: "GET", URL: "http://a.example/", Headers: http.Header{}}),
		},
		{
			name: "script block with CRLF is normalized to LF",
			raw:  "< {%\r\na();\r\n%}\r\nGET http://a.example/\r\n",
			want: one(request.Request{
				Line: 4, Method: "GET", URL: "http://a.example/", Headers: http.Header{},
				PreScript: &request.Script{Source: "\na();\n", Line: 1},
			}),
		},
		{
			name: "### inside a block is code, comments after the post script are fine",
			raw:  "GET http://a.example/\n> {%\n### not a separator\n%}\n# note\n\n",
			want: one(request.Request{
				Method: "GET", URL: "http://a.example/", Headers: http.Header{},
				PostScript: &request.Script{Source: "\n### not a separator\n", Line: 2},
			}),
		},
		{
			name: "body from file followed by a post script",
			raw:  "POST http://a.example/\n\n< testdata/payloads/user.json\n\n> {% t() %}\n",
			want: one(request.Request{
				Method: "POST", URL: "http://a.example/", Headers: http.Header{},
				Body: "{\"name\": \"ada\"}\n", BodyFile: "testdata/payloads/user.json",
				PostScript: &request.Script{Source: " t() ", Line: 5},
			}),
		},
		{
			name: "body line starting with > that is not a .js file is body text",
			raw:  "POST http://a.example/\n\n> notes.txt\n>  two words.js\n",
			want: one(request.Request{Method: "POST", URL: "http://a.example/", Headers: http.Header{}, Body: "> notes.txt\n>  two words.js\n"}),
		},
		{name: "pre-request file that is not .js", raw: "< ./pre.txt\nGET http://a.example/\n", wantErr: "line 1: invalid pre-request script \"./pre.txt\""},
		{name: "inline and file post scripts together", raw: "GET http://a.example/\n> {% a %}\n> testdata/scripts/pre.js\n", wantErr: "line 3: duplicate post-response script"},
		{name: "text after closing %}", raw: "< {% a() %} b\nGET http://a.example/\n", wantErr: "line 1: unexpected text after \"%}\""},
		{name: "pre-request line without a block", raw: "< nope\nGET http://a.example/\n", wantErr: "line 1: invalid pre-request script \"nope\""},
		{name: "duplicate pre-request script", raw: "< {% a %}\n< {% b %}\nGET http://a.example/\n", wantErr: "line 2: duplicate pre-request script"},
		{name: "duplicate post-response script", raw: "GET http://a.example/\n> {% a %}\n> {% b %}\n", wantErr: "line 3: duplicate post-response script"},
		{name: "text after the post-response script", raw: "GET http://a.example/\n> {% a %}\nX-A: 1\n", wantErr: "line 3: unexpected text after the post-response script"},
		{name: "pre-request script without a request", raw: "< {% a %}\n###\nGET http://a.example/\n", wantErr: "line 1: pre-request script without a request"},
		{name: "unterminated pre-request script", raw: "< {%\na()\n", wantErr: "line 1: unterminated pre-request script"},
		{name: "unknown directive", raw: "# @retry 3\nGET http://a.example/\n", wantErr: "line 1: unknown directive @retry"},
		{name: "empty directive", raw: "# @\nGET http://a.example/\n", wantErr: "line 1: empty directive"},
		{name: "missing name", raw: "# @name\nGET http://a.example/\n", wantErr: "line 1: invalid @name \"\""},
		{name: "name starting with digit", raw: "# @name 1st\nGET http://a.example/\n", wantErr: "line 1: invalid @name \"1st\""},
		{name: "name with space", raw: "# @name a b\nGET http://a.example/\n", wantErr: "line 1: invalid @name \"a b\""},
		{name: "timeout without unit", raw: "# @timeout 5\nGET http://a.example/\n", wantErr: "line 1: invalid @timeout \"5\""},
		{name: "negative timeout", raw: "# @timeout -1s\nGET http://a.example/\n", wantErr: "line 1: invalid @timeout \"-1s\""},
		{name: "repeated directive", raw: "# @timeout 1s\n# @timeout 2s\nGET http://a.example/\n", wantErr: "line 2: duplicate @timeout (first at line 1)"},
		{name: "directives without a request", raw: "# @name orphan\n###\nGET http://a.example/\n", wantErr: "line 1: directives without a request"},
		{name: "directives at EOF", raw: "GET http://a.example/\n###\n# @skip\n", wantErr: "line 3: directives without a request"},
		{
			name: "templated URL with spaces inside braces",
			raw:  "GET {{ baseUrl }}/users/{{id}} HTTP/1.1\n",
			want: one(request.Request{Method: "GET", URL: "{{ baseUrl }}/users/{{id}}", Proto: "HTTP/1.1", Headers: http.Header{}}),
		},
		{name: "empty input", raw: "", wantErr: "missing request line"},
		{name: "only separators", raw: "###\n### title\n", wantErr: "missing request line"},
		{name: "request line without URL", raw: "# comment\nGET\n", wantErr: "line 2: invalid request line"},
		{name: "too many request line parts", raw: "GET http://example.com/ HTTP/1.1 extra\n", wantErr: "line 1: invalid request line"},
		{name: "URL without host", raw: "GET http:///path\n", wantErr: "missing host"},
		{name: "unsupported scheme", raw: "GET ftp://example.com/\n", wantErr: "scheme must be http or https"},
		{name: "header with empty name", raw: "GET http://example.com/\n: value\n", wantErr: "line 2: invalid header name"},
		{name: "header name with space", raw: "GET http://example.com/\nX Foo: 1\n", wantErr: "line 2: invalid header name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := request.ParseRequest(tt.raw)
			checkResult(t, got, err, tt.want, tt.wantErr)
		})
	}
}

func TestParseBodyFileErrors(t *testing.T) {
	_, err := request.ParseMonFile("testdata/body_file_missing.mon")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestParseBodyFileAbsolutePath(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(payload, []byte("raw\x00bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		raw  string
	}{
		{"space after <", "POST http://a.example/\n\n< " + payload + "\n"},
		{"tab after <, CRLF, trailing blank lines", "POST http://a.example/\r\n\r\n<\t" + payload + "\r\n\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := request.ParseRequest(tt.raw)
			want := []*request.Request{{
				Line: 1, Method: "POST", URL: "http://a.example/", Headers: http.Header{},
				Body: "raw\x00bytes", BodyFile: payload,
			}}
			checkResult(t, got, err, want, "")
		})
	}
}

func TestParseScriptFileErrors(t *testing.T) {
	_, err := request.ParseMonFile("testdata/script_file_missing.mon")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
}

func TestParseScriptFileAbsolutePathCRLF(t *testing.T) {
	js := filepath.Join(t.TempDir(), "check.js")
	if err := os.WriteFile(js, []byte("a();\r\nb();\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := request.ParseRequest("GET http://a.example/\n\n>   " + js + "  \n")
	want := []*request.Request{{
		Line: 1, Method: "GET", URL: "http://a.example/", Headers: http.Header{},
		PostScript: &request.Script{Source: "a();\nb();\n", File: js, Line: 1},
	}}
	checkResult(t, got, err, want, "")
}

func TestLabel(t *testing.T) {
	tests := []struct {
		req  request.Request
		want string
	}{
		{request.Request{Method: "GET", URL: "http://a.example/"}, "GET http://a.example/"},
		{request.Request{Title: "List users", Method: "GET", URL: "http://a.example/"}, "List users"},
		{request.Request{Name: "list", Title: "List users", Method: "GET", URL: "http://a.example/"}, "list"},
	}
	for _, tt := range tests {
		if got := tt.req.Label(); got != tt.want {
			t.Errorf("Label() = %q, want %q", got, tt.want)
		}
	}
}

func checkResult(t *testing.T, got []*request.Request, err error, want []*request.Request, wantErr string) {
	t.Helper()
	if wantErr != "" {
		if err == nil {
			t.Fatalf("expected error containing %q, got requests %+v", wantErr, got)
		}
		if !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error %q does not contain %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("request %d:\ngot  %#v\nwant %#v", i, got[i], want[i])
		}
	}
}
