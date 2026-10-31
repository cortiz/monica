package request_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestParseMonFile(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		wantMethod  string
		wantURL     string
		wantHeaders map[string]string
		wantBody    string
		wantErr     string // substring of the error; empty means no error
	}{
		{
			name:       "simple GET",
			file:       "testdata/simple.mon",
			wantMethod: "GET",
			wantURL:    "http://httpbin.org/status/200",
			wantHeaders: map[string]string{
				"Host":       "httpbin.org",
				"User-Agent": "monica/0.1.0",
			},
		},
		{
			name:    "missing request line",
			file:    "testdata/missing_request_line.mon",
			wantErr: "testdata/missing_request_line.mon: missing request line",
		},
		{
			name:    "bad request line reports file and line",
			file:    "testdata/bad_request_line.mon",
			wantErr: "testdata/bad_request_line.mon:3: invalid request line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := request.ParseMonFile(tt.file)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got request %+v", tt.wantErr, req)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				var pe *request.ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("expected *request.ParseError, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertRequest(t, req, tt.wantMethod, tt.wantURL, tt.wantHeaders, tt.wantBody)
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
		name        string
		raw         string
		wantMethod  string
		wantURL     string
		wantHeaders map[string]string
		wantBody    string
		wantErr     string
	}{
		{
			name: "headers and multi-line body",
			raw: `GET https://www.google.com HTTP/1.1
Host: www.google.com
User-Agent: monica/0.1.0

multi
line
body
`,
			wantMethod: "GET",
			wantURL:    "https://www.google.com",
			wantHeaders: map[string]string{
				"Host":       "www.google.com",
				"User-Agent": "monica/0.1.0",
			},
			// Leading "\n" is a known join bug, fixed in roadmap task 0.2.
			wantBody: "\nmulti\nline\nbody",
		},
		{
			name:    "empty input",
			raw:     "",
			wantErr: "missing request line",
		},
		{
			name:    "request line without URL",
			raw:     "# comment\nGET\n",
			wantErr: "line 2: invalid request line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := request.ParseRequest(tt.raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertRequest(t, req, tt.wantMethod, tt.wantURL, tt.wantHeaders, tt.wantBody)
		})
	}
}

func assertRequest(t *testing.T, req *request.Request, method, url string, headers map[string]string, body string) {
	t.Helper()
	if req == nil {
		t.Fatal("request is nil")
	}
	if req.Method != method {
		t.Errorf("Method = %q, want %q", req.Method, method)
	}
	if req.URL != url {
		t.Errorf("URL = %q, want %q", req.URL, url)
	}
	if len(req.Headers) != len(headers) {
		t.Errorf("Headers = %v, want %v", req.Headers, headers)
	}
	for k, v := range headers {
		if got := req.Headers[k]; got != v {
			t.Errorf("Headers[%q] = %q, want %q", k, got, v)
		}
	}
	if req.Body != body {
		t.Errorf("Body = %q, want %q", req.Body, body)
	}
}
