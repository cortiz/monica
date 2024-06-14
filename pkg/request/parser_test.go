package request_test

import (
	"jmpeax.com/sec/monica/pkg/request"
	"testing"
)

func TestParseMonFile(t *testing.T) {
	req := request.ParseMonFile("testdata/simple.mon")
	if req == nil {
		t.Fatal("Request is nil")
	}
	if req.Method != "GET" {
		t.Fatalf("Method is %s, expected GET", req.Method)
	}
	if req.URL != "http://httpbin.org/status/200" {
		t.Fatalf("URL is %s, expected http://httpbin.org/status/200", req.URL)
	}
	if len(req.Headers) != 2 {
		t.Fatalf("Headers length is %d, expected 2", len(req.Headers))
	}
	if req.Headers["Host"] != "httpbin.org" {
		t.Fatalf("Host header is %s, expected httpbin.org", req.Headers["Host"])
	}
	if req.Headers["User-Agent"] != "monica/0.1.0" {
		t.Fatalf("User-Agent header is %s, expected monica/0.1.0", req.Headers["User-Agent"])
	}
	if len(req.Body) != 0 {
		t.Fatalf("Body length is %d, expected 0", len(req.Body))
	}
}

func TestParseRequest(t *testing.T) {
	raw := `GET https://www.google.com HTTP/1.1
Host: www.google.com
User-Agent: monica/0.1.0

multi
line
body
`
	req := request.ParseRequest(raw)
	if req == nil {
		t.Fatal("Request is nil")
	}
	if req.Method != "GET" {
		t.Fatalf("Method is %s, expected GET", req.Method)
	}
	if req.URL != "https://www.google.com" {
		t.Fatalf("URL is %s, expected https://www.google.com", req.URL)
	}
	if len(req.Headers) != 2 {
		t.Fatalf("Headers length is %d, expected 2", len(req.Headers))
	}
	if req.Headers["Host"] != "www.google.com" {
		t.Fatalf("Host header is %s, expected www.google.com", req.Headers["Host"])
	}
	if req.Headers["User-Agent"] != "monica/0.1.0" {
		t.Fatalf("User-Agent header is %s, expected monica/0.1.0", req.Headers["User-Agent"])
	}
	if req.Body != "\nmulti\nline\nbody" {
		t.Fatalf("Body is %s, expected \nmulti\nline\nbody", req.Body)
	}
}
