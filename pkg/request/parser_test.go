package request_test

import (
	"errors"
	"io/fs"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestParseMonFile(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    *request.Request
		wantErr string // substring of the error; empty means no error
	}{
		{
			name: "simple GET",
			file: "testdata/simple.mon",
			want: &request.Request{
				Method: "GET",
				URL:    "http://httpbin.org/status/200",
				Headers: http.Header{
					"Host":       {"httpbin.org"},
					"User-Agent": {"monica/0.1.0"},
				},
			},
		},
		{
			name: "simple POST with JSON body",
			file: "testdata/simple_post.mon",
			want: &request.Request{
				Method: "POST",
				URL:    "http://httpbin.org/anything",
				Headers: http.Header{
					"Host":       {"httpbin.org"},
					"User-Agent": {"monica/0.1.0"},
				},
				Body: "{\n  \"key\": \"value\"\n}\n",
			},
		},
		{
			name: "header split on first colon only",
			file: "testdata/header_colon_value.mon",
			want: &request.Request{
				Method: "GET",
				URL:    "http://example.com/",
				Headers: http.Header{
					"X-Foo":         {"a: b"},
					"X-Spaced":      {"padded value"},
					"Authorization": {"Bearer abc:def"},
				},
			},
		},
		{
			name: "repeated headers survive",
			file: "testdata/repeated_headers.mon",
			want: &request.Request{
				Method: "GET",
				URL:    "http://example.com/",
				Headers: http.Header{
					"Accept": {"text/html", "application/json"},
					"Cookie": {"a=1", "b=2"},
				},
			},
		},
		{
			name: "body kept verbatim",
			file: "testdata/body_exact.mon",
			want: &request.Request{
				Method:  "POST",
				URL:     "http://example.com/notes",
				Headers: http.Header{"Content-Type": {"text/markdown"}},
				Body:    "# Title\n\nline after blank\n\n",
			},
		},
		{
			name: "request line with HTTP version",
			file: "testdata/request_line_version.mon",
			want: &request.Request{
				Method:  "PUT",
				URL:     "https://example.com/items/1",
				Proto:   "HTTP/1.1",
				Headers: http.Header{"Content-Type": {"application/json"}},
				Body:    `{"id":1}`,
			},
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
		t.Fatalf("expected error, got request %+v", req)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist, got %v", err)
	}
	if !strings.Contains(err.Error(), "testdata/does_not_exist.mon") {
		t.Fatalf("error %q should name the file", err)
	}
}

func TestParseRequest(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *request.Request
		wantErr string
	}{
		{
			name: "headers and multi-line body",
			raw:  "GET https://www.google.com HTTP/1.1\nHost: www.google.com\nUser-Agent: monica/0.1.0\n\nmulti\nline\nbody\n",
			want: &request.Request{
				Method: "GET",
				URL:    "https://www.google.com",
				Proto:  "HTTP/1.1",
				Headers: http.Header{
					"Host":       {"www.google.com"},
					"User-Agent": {"monica/0.1.0"},
				},
				Body: "multi\nline\nbody\n",
			},
		},
		{
			name: "CRLF line endings",
			raw:  "POST http://example.com/ HTTP/1.1\r\nX-A: 1\r\n\r\nbody\r\n",
			want: &request.Request{
				Method:  "POST",
				URL:     "http://example.com/",
				Proto:   "HTTP/1.1",
				Headers: http.Header{"X-A": {"1"}},
				Body:    "body\r\n",
			},
		},
		{
			name: "no headers, no trailing newline",
			raw:  "DELETE http://example.com/x",
			want: &request.Request{Method: "DELETE", URL: "http://example.com/x", Headers: http.Header{}},
		},
		{
			name: "comments and blank lines before request line, comment among headers",
			raw:  "\n# a\n\nGET http://example.com/\n# skipped\nX-A: 1\n",
			want: &request.Request{Method: "GET", URL: "http://example.com/", Headers: http.Header{"X-A": {"1"}}},
		},
		{
			name: "lowercase header names are canonicalized",
			raw:  "GET http://example.com/\ncontent-type: text/plain\n",
			want: &request.Request{Method: "GET", URL: "http://example.com/", Headers: http.Header{"Content-Type": {"text/plain"}}},
		},
		{
			name: "empty header value",
			raw:  "GET http://example.com/\nX-Empty:\n",
			want: &request.Request{Method: "GET", URL: "http://example.com/", Headers: http.Header{"X-Empty": {""}}},
		},
		{name: "empty input", raw: "", wantErr: "missing request line"},
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

func checkResult(t *testing.T, got *request.Request, err error, want *request.Request, wantErr string) {
	t.Helper()
	if wantErr != "" {
		if err == nil {
			t.Fatalf("expected error containing %q, got request %+v", wantErr, got)
		}
		if !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("error %q does not contain %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}
