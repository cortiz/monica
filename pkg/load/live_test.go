package load

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/script"
)

func TestProgress(t *testing.T) {
	srv := newConcurrencyServer(t, 20*time.Millisecond)
	file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
	var mu sync.Mutex
	var got []Progress
	res, err := Run(context.Background(), Options{
		VUs: 3, Duration: 500 * time.Millisecond, GracefulStop: time.Second, Files: []string{file},
		ProgressInterval: 100 * time.Millisecond,
		Progress: func(p Progress) {
			mu.Lock()
			got = append(got, p)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) < 2 || len(got) > 7 {
		t.Fatalf("got %d snapshots in 500ms at 100ms", len(got))
	}
	for i, p := range got {
		if p.Planned != 500*time.Millisecond || p.VUs != 3 || p.ActiveVUs < 0 || p.ActiveVUs > 3 {
			t.Fatalf("snapshot %d: %+v", i, p)
		}
		if i > 0 && (p.Elapsed <= got[i-1].Elapsed || p.Requests < got[i-1].Requests || p.Iterations < got[i-1].Iterations) {
			t.Fatalf("snapshot %d went backwards: %+v after %+v", i, p, got[i-1])
		}
	}
	mid := got[1]
	// 3 VUs × ~20ms requests ≈ 150 rps; allow slow machines.
	if mid.RPS < 10 || mid.RPS > 200 || mid.P95 < 20*time.Millisecond || mid.ActiveVUs == 0 || mid.Failed != 0 {
		t.Fatalf("mid-test snapshot: %+v", mid)
	}
	if last := got[len(got)-1]; last.Requests > res.Requests {
		t.Fatalf("progress counted %d requests, result %d", last.Requests, res.Requests)
	}
}

func TestLiveTake(t *testing.T) {
	var l live
	l.record(report.Request{Status: 200, Duration: 10 * time.Millisecond})
	l.record(report.Request{Error: "refused"})
	l.record(report.Request{Status: 500, Duration: 30 * time.Millisecond, Tests: []script.TestResult{{Name: "x"}}})
	n, p95 := l.take()
	if n != 3 || p95 < 29*time.Millisecond || l.requests.Load() != 3 || l.failed.Load() != 2 {
		t.Fatalf("n=%d p95=%s requests=%d failed=%d", n, p95, l.requests.Load(), l.failed.Load())
	}
	if n, p95 := l.take(); n != 0 || p95 != 0 {
		t.Fatalf("take must reset the interval: %d %s", n, p95)
	}
}

func TestJSONReport(t *testing.T) {
	m := metrics{}
	for i := 1; i <= 10; i++ {
		m.record(report.Request{File: "a.mon", Line: 3, Name: "login", Status: 200, Duration: time.Duration(i) * time.Millisecond, BytesReceived: 100, BytesSent: 5})
	}
	m.record(report.Request{File: "a.mon", Line: 9, Name: "users", Error: "refused"})
	res := Result{VUs: 4, Iterations: 10, Interrupted: 1, Dropped: 2, Duration: 2 * time.Second}
	res.Stats, res.Total = m.stats()
	th := []ThresholdResult{{Threshold: Threshold{Source: "p95<1s"}, Passed: true, Actual: "p95=10.0ms"}, {Threshold: Threshold{Source: "errors<1%"}, Actual: "errors=9.09%"}}
	opts := Options{VUs: 5, Rate: 20, Stages: []Stage{{time.Second, 40}}}
	data, err := res.JSON(opts, th, true)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	get := func(path ...any) any {
		var v any = doc
		for _, k := range path {
			switch k := k.(type) {
			case string:
				v = v.(map[string]any)[k]
			case int:
				v = v.([]any)[k]
			}
		}
		return v
	}
	checks := []struct {
		path []any
		want any
	}{
		{[]any{"config", "vus"}, 5.0},
		{[]any{"config", "rate"}, 20.0},
		{[]any{"config", "stages", 0, "durationMs"}, 1000.0},
		{[]any{"config", "stages", 0, "target"}, 40.0},
		{[]any{"aborted"}, true},
		{[]any{"durationMs"}, 2000.0},
		{[]any{"vus"}, 4.0},
		{[]any{"iterations"}, 10.0},
		{[]any{"interrupted"}, 1.0},
		{[]any{"dropped"}, 2.0},
		{[]any{"passed"}, false},
		{[]any{"total", "name"}, "all"},
		{[]any{"total", "requests"}, 11.0},
		{[]any{"total", "failed"}, 1.0},
		{[]any{"total", "rps"}, 5.5},
		{[]any{"total", "bytesReceived"}, 1000.0},
		{[]any{"total", "latency", "count"}, 10.0},
		{[]any{"total", "latency", "min"}, 1.0},
		{[]any{"total", "latency", "avg"}, 5.5},
		{[]any{"total", "latency", "max"}, 10.0},
		{[]any{"requests", 0, "name"}, "login"},
		{[]any{"requests", 0, "line"}, 3.0},
		{[]any{"requests", 1, "failRate"}, 1.0},
		{[]any{"requests", 1, "latency", "count"}, 0.0},
		{[]any{"thresholds", 1, "threshold"}, "errors<1%"},
		{[]any{"thresholds", 1, "passed"}, false},
		{[]any{"thresholds", 1, "actual"}, "errors=9.09%"},
	}
	for _, c := range checks {
		if got := get(c.path...); got != c.want {
			t.Errorf("%v = %v, want %v", c.path, got, c.want)
		}
	}
	if p95 := get("total", "latency", "p95").(float64); p95 < 9.8 || p95 > 10 {
		t.Errorf("p95 = %v", p95)
	}
	if !strings.Contains(string(data), "\n  \"config\": {") {
		t.Error("expected indented JSON")
	}
	// No thresholds: passed, empty list (not null).
	data, _ = Result{}.JSON(Options{VUs: 1, Iterations: 1}, nil, false)
	if !strings.Contains(string(data), `"thresholds": []`) || !strings.Contains(string(data), `"passed": true`) || !strings.Contains(string(data), `"requests": []`) {
		t.Errorf("empty report: %s", data)
	}
}

func TestProgressString(t *testing.T) {
	tests := []struct {
		p    Progress
		want string
	}{
		{
			Progress{Elapsed: 12345 * time.Millisecond, Planned: 30 * time.Second, ActiveVUs: 8, VUs: 10, Iterations: 1234, Requests: 2468, Failed: 3, RPS: 82, P95: 18 * time.Millisecond},
			"running 12s/30s · VUs 8/10 · 1234 iterations · 2468 requests · 82.0 rps · p95 18.0ms · 0.12% failed",
		},
		{
			Progress{Elapsed: 900 * time.Millisecond, ActiveVUs: 1, VUs: 1, Iterations: 1, Requests: 1},
			"running 0s · VUs 1/1 · 1 iteration · 1 request · 0.0 rps · p95 0 · 0.00% failed",
		},
		{
			Progress{Elapsed: 2 * time.Second},
			"running 2s · VUs 0/0 · 0 iterations · 0 requests · 0.0 rps · p95 0 · 0 failed",
		},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("got  %q\nwant %q", got, tt.want)
		}
	}
}
