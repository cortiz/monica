package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"testing"
	"time"
)

func TestJSON(t *testing.T) {
	var buf bytes.Buffer
	j := NewJSON(&buf)
	for _, r := range sample {
		j.Request(r)
	}
	if err := j.Done(Summarize(sample, 2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Summary struct {
			Requests, Passed, Failed, Skipped, Tests, TestsPassed, TestsFailed int
			DurationMS                                                         float64 `json:"durationMs"`
		} `json:"summary"`
		Requests []map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	s := doc.Summary
	if s.Requests != 4 || s.Failed != 3 || s.Skipped != 1 || s.Tests != 3 || s.TestsFailed != 2 || s.DurationMS != 2500 {
		t.Fatalf("summary = %+v", s)
	}
	login := doc.Requests[0]
	checks := map[string]any{
		"name": "login", "file": "api.mon", "line": float64(3), "method": "POST", "status": float64(200),
		"statusText": "OK", "durationMs": float64(42), "failed": true,
	}
	for k, want := range checks {
		if login[k] != want {
			t.Errorf("requests[0].%s = %v, want %v", k, login[k], want)
		}
	}
	tests := login["tests"].([]any)
	if len(tests) != 2 || tests[1].(map[string]any)["error"] != "expected 612 to be less than 500" {
		t.Errorf("tests = %v", tests)
	}
	if _, ok := login["response"]; ok {
		t.Error("response bodies must not be in the JSON report")
	}
	if doc.Requests[2]["skipped"] != true || doc.Requests[2]["skipReason"] != "flaky upstream" {
		t.Errorf("skipped request = %v", doc.Requests[2])
	}
	if doc.Requests[1]["error"] != "sending HTTP request: connection refused" {
		t.Errorf("error request = %v", doc.Requests[1])
	}
}

func TestJUnit(t *testing.T) {
	results := append([]Request{}, sample...)
	results = append(results, Request{File: "other.mon", Line: 1, Name: "ping", Method: "GET", URL: "u", Status: 204, Duration: 5 * time.Millisecond})
	var buf bytes.Buffer
	j := NewJUnit(&buf)
	for _, r := range results {
		j.Request(r)
	}
	if err := j.Done(Summarize(results, 2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	want := `<?xml version="1.0" encoding="UTF-8"?>
<testsuites name="monica" tests="6" failures="2" errors="1" skipped="1" time="2.500">
  <testsuite name="api.mon" tests="5" failures="2" errors="1" skipped="1" time="0.043">
    <testcase name="returns a token" classname="api.mon:3 login" time="0.042"></testcase>
    <testcase name="is fast" classname="api.mon:3 login" time="0.042">
      <failure message="expected 612 to be less than 500">expected 612 to be less than 500</failure>
    </testcase>
    <testcase name="GET https://api.example.com/me" classname="api.mon:9 GET https://api.example.com/me" time="0.000">
      <error message="sending HTTP request: connection refused">sending HTTP request: connection refused</error>
    </testcase>
    <testcase name="slow" classname="api.mon:12 slow" time="0.000">
      <skipped message="flaky upstream"></skipped>
    </testcase>
    <testcase name="deep" classname="api.mon:15 users" time="0.002">
      <failure message="expected values to be deeply equal">expected values to be deeply equal&#xA;  expected: [1]&#xA;  received: []</failure>
    </testcase>
  </testsuite>
  <testsuite name="other.mon" tests="1" failures="0" errors="0" skipped="0" time="0.005">
    <testcase name="ping" classname="other.mon:1 ping" time="0.005"></testcase>
  </testsuite>
</testsuites>
`
	if buf.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), want)
	}
	var v any
	if err := xml.Unmarshal(buf.Bytes(), &v); err != nil {
		t.Fatalf("not well-formed XML: %v", err)
	}
}
