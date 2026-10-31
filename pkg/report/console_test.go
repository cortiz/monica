package report

import (
	"bytes"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/script"
)

var sample = []Request{
	{
		File: "api.mon", Line: 3, Name: "login", Method: "POST", URL: "https://api.example.com/login",
		Status: 200, StatusText: "OK", Duration: 42 * time.Millisecond,
		Response: "\nHTTP/1.1 200 OK\nContent-Type: application/json\n{\"token\":\"****\"}\n",
		Tests: []script.TestResult{
			{Name: "returns a token", Passed: true},
			{Name: "is fast", Error: "expected 612 to be less than 500"},
		},
	},
	{
		File: "api.mon", Line: 9, Name: "GET https://api.example.com/me", Method: "GET", URL: "https://api.example.com/me",
		Error: "sending HTTP request: connection refused",
	},
	{File: "api.mon", Line: 12, Name: "slow", Skipped: true, SkipReason: "flaky upstream"},
	{
		File: "api.mon", Line: 15, Name: "users", Method: "GET", URL: "https://api.example.com/users",
		Status: 200, StatusText: "OK", Duration: 1500 * time.Microsecond, Response: "\nHTTP/1.1 200 OK\n[]\n",
		Tests: []script.TestResult{{Name: "deep", Error: "expected values to be deeply equal\n  expected: [1]\n  received: []"}},
	},
}

func TestConsole(t *testing.T) {
	tests := []struct {
		name  string
		quiet bool
		want  string
	}{
		{
			name: "full",
			want: `✗ login  POST https://api.example.com/login → 200 OK  42ms  (api.mon:3)

HTTP/1.1 200 OK
Content-Type: application/json
{"token":"****"}

    ✓ returns a token
    ✗ is fast
        expected 612 to be less than 500
✗ GET https://api.example.com/me  (api.mon:9)
    ✗ sending HTTP request: connection refused
- slow  skipped: flaky upstream  (api.mon:12)
✗ users  GET https://api.example.com/users → 200 OK  1.5ms  (api.mon:15)

HTTP/1.1 200 OK
[]

    ✗ deep
        expected values to be deeply equal
          expected: [1]
          received: []

4 requests: 0 passed, 3 failed, 1 skipped · 3 tests: 1 passed, 2 failed · 2.5s
`,
		},
		{
			name:  "quiet hides responses",
			quiet: true,
			want: `✗ login  POST https://api.example.com/login → 200 OK  42ms  (api.mon:3)
    ✓ returns a token
    ✗ is fast
        expected 612 to be less than 500
✗ GET https://api.example.com/me  (api.mon:9)
    ✗ sending HTTP request: connection refused
- slow  skipped: flaky upstream  (api.mon:12)
✗ users  GET https://api.example.com/users → 200 OK  1.5ms  (api.mon:15)
    ✗ deep
        expected values to be deeply equal
          expected: [1]
          received: []

4 requests: 0 passed, 3 failed, 1 skipped · 3 tests: 1 passed, 2 failed · 2.5s
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := NewConsole(&buf)
			c.Quiet = tt.quiet
			for _, r := range sample {
				c.Request(r)
			}
			if err := c.Done(Summarize(sample, 2500*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if buf.String() != tt.want {
				t.Fatalf("got:\n%s\nwant:\n%s", buf.String(), tt.want)
			}
		})
	}
}

func TestSummaryLine(t *testing.T) {
	var buf bytes.Buffer
	ok := []Request{{Name: "a", Method: "GET", URL: "u", Status: 200}}
	if err := NewConsole(&buf).Done(Summarize(ok, 80*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if want := "\n1 request: 1 passed, 0 failed · 80ms\n"; buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestSummarize(t *testing.T) {
	got := Summarize(sample, time.Second)
	want := Summary{Requests: 4, Passed: 0, Failed: 3, Skipped: 1, Tests: 3, TestsPassed: 1, TestsFailed: 2, Duration: time.Second}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

type recorder struct {
	got  []Request
	done *Summary
}

func (r *recorder) Request(q Request) { r.got = append(r.got, q) }
func (r *recorder) Done(s Summary) error {
	r.done = &s
	return nil
}

func TestMulti(t *testing.T) {
	a, b := &recorder{}, &recorder{}
	m := Multi(a, b)
	m.Request(sample[0])
	if err := m.Done(Summary{Requests: 1}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*recorder{a, b} {
		if len(r.got) != 1 || r.done == nil || r.done.Requests != 1 {
			t.Fatalf("reporter not fed: %+v", r)
		}
	}
}
