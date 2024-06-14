package net_test

import (
	"testing"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

func TestHTTPRequest(t *testing.T) {
	request := &request.Request{
		Method: "GET",
		URL:    "https://httpbin.org/status/200",
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: "",
	}
	res := net.HTTPRequest(request)
	if res.StatusCode != 200 {
		t.Fatalf("Expected status code 200, got %d", res.StatusCode)
	}
	if len(res.Headers) == 0 {
		t.Fatalf("Expected headers to be returned")
	}
}
