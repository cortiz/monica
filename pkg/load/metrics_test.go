package load

import (
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/script"
)

func TestMetricsRecordMergeStats(t *testing.T) {
	ok := func(file string, line int, name string, d time.Duration) report.Request {
		return report.Request{File: file, Line: line, Name: name, Status: 200, Duration: d,
			Timings: net.Timings{Waiting: d / 2}, BytesSent: 10, BytesReceived: 100}
	}
	vu1, vu2 := metrics{}, metrics{}
	vu1.record(ok("b.mon", 1, "users", 20*time.Millisecond))
	vu1.record(ok("a.mon", 9, "login", 10*time.Millisecond))
	vu2.record(ok("a.mon", 9, "login", 30*time.Millisecond))
	vu2.record(report.Request{File: "a.mon", Line: 9, Name: "login", Error: "connection refused"}) // no response
	vu2.record(report.Request{File: "a.mon", Line: 2, Name: "login", Status: 500, Duration: time.Millisecond,
		Tests: []script.TestResult{{Name: "ok", Passed: false}}}) // same name, other request

	all := metrics{}
	all.merge(vu1)
	all.merge(vu2)
	stats, total := all.stats()
	if len(stats) != 3 || stats[0].Line != 2 || stats[1].Line != 9 || stats[2].File != "b.mon" {
		t.Fatalf("order: %+v", stats)
	}
	login := stats[1]
	if login.Requests != 3 || login.Failed != 1 || login.Duration.Count() != 2 {
		t.Fatalf("login: requests %d failed %d timed %d", login.Requests, login.Failed, login.Duration.Count())
	}
	if login.Duration.Mean() != 20*time.Millisecond || login.Waiting.Mean() != 10*time.Millisecond || login.BytesReceived != 200 {
		t.Fatalf("login timings: mean %s waiting %s bytes %d", login.Duration.Mean(), login.Waiting.Mean(), login.BytesReceived)
	}
	if total.Name != "all" || total.Requests != 5 || total.Failed != 2 || total.Duration.Count() != 4 || total.BytesSent != 30 {
		t.Fatalf("total: %+v", total)
	}
	if got := stats[0].FailRate(); got != 1 {
		t.Fatalf("fail rate %v", got)
	}
	// Merging must not alias the VU's histograms.
	vu1.record(ok("b.mon", 1, "users", time.Second))
	if s, _ := all.stats(); s[2].Duration.Count() != 1 {
		t.Fatal("merged stats share buckets with a VU")
	}
}

func TestFormat(t *testing.T) {
	durs := []struct {
		d    time.Duration
		want string
	}{
		{0, "0"}, {850 * time.Microsecond, "850µs"}, {1234 * time.Microsecond, "1.23ms"},
		{12345 * time.Microsecond, "12.3ms"}, {1234 * time.Millisecond, "1.23s"}, {90 * time.Second, "90.00s"},
	}
	for _, tt := range durs {
		if got := fmtDur(tt.d); got != tt.want {
			t.Errorf("fmtDur(%s) = %q, want %q", tt.d, got, tt.want)
		}
	}
	sizes := []struct {
		n    int64
		want string
	}{
		{0, "0 B"}, {999, "999 B"}, {1000, "1.0 kB"}, {1500, "1.5 kB"}, {12_345_678, "12.3 MB"}, {3e12, "3.0 TB"},
	}
	for _, tt := range sizes {
		if got := fmtBytes(tt.n); got != tt.want {
			t.Errorf("fmtBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestSummaryTable(t *testing.T) {
	m := metrics{}
	for i := 1; i <= 100; i++ {
		m.record(report.Request{File: "a.mon", Line: 1, Name: "login", Status: 200, Duration: time.Duration(i) * time.Millisecond, BytesReceived: 1000})
		m.record(report.Request{File: "a.mon", Line: 5, Name: "users", Status: 200, Duration: 2 * time.Millisecond, BytesSent: 10})
	}
	res := Result{VUs: 2, Iterations: 100, Duration: 10 * time.Second}
	res.Stats, res.Total = m.stats()
	res.Requests = int64(res.Total.Requests)
	got := res.Summary(Options{Iterations: 100})
	for _, want := range []string{
		"data        100.0 kB received, 1.0 kB sent\n",
		"request  reqs  failed  rps   min     avg     p50     p90     p95     p99     max\n",
		"login    100   0       10.0  1.00ms  50.5ms  ", // quantiles within 1.6%
		"users    100   0       10.0  2.00ms  2.00ms  2.00ms",
		"all      200   0       20.0  1.00ms",
		"phases (avg / p95)  dns 0 / 0 · connect 0 / 0 · tls 0 / 0 · waiting 0 / 0 · receiving 0 / 0\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary does not contain %q:\n%s", want, got)
		}
	}
	one := Result{VUs: 1, Iterations: 1, Duration: time.Second}
	one.Stats, one.Total = metrics{{"a.mon", 1}: {Name: "only", Requests: 1}}.stats()
	if s := one.Summary(Options{Iterations: 1}); strings.Contains(s, "\nall ") {
		t.Errorf("a single request needs no total row:\n%s", s)
	}
}
