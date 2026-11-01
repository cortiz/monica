package load

import (
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/script"
)

func TestParseThreshold(t *testing.T) {
	ms := float64(time.Millisecond)
	tests := []struct {
		in   string
		want Threshold
	}{
		{"p95<300ms", Threshold{Metric: "p", Percentile: 95, Op: "<", Value: 300 * ms}},
		{" p(99.9) <= 1.5s ", Threshold{Metric: "p", Percentile: 99.9, Op: "<=", Value: 1500 * ms}},
		{"P100<1s", Threshold{Metric: "p", Percentile: 100, Op: "<", Value: 1000 * ms}},
		{"avg<200ms", Threshold{Metric: "avg", Op: "<", Value: 200 * ms}},
		{"med>=1ms", Threshold{Metric: "med", Op: ">=", Value: ms}},
		{"min>0s", Threshold{Metric: "min", Op: ">", Value: 0}},
		{"max<2s", Threshold{Metric: "max", Op: "<", Value: 2000 * ms}},
		{"errors<1%", Threshold{Metric: "errors", Op: "<", Value: 0.01}},
		{"errors<=0.05", Threshold{Metric: "errors", Op: "<=", Value: 0.05}},
		{"rps>=100", Threshold{Metric: "rps", Op: ">=", Value: 100}},
		{"login:p95<300ms", Threshold{Request: "login", Metric: "p", Percentile: 95, Op: "<", Value: 300 * ms}},
		{"GET http://a.example:8080/x : errors<1%", Threshold{Request: "GET http://a.example:8080/x", Metric: "errors", Op: "<", Value: 0.01}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseThreshold(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			tt.want.Source = tt.in
			if got != tt.want {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestParseThresholdErrors(t *testing.T) {
	tests := []struct{ in, wantErr string }{
		{"p95", "expected [request:]metric op value"},
		{"<300ms", "missing metric"},
		{"p0<1s", "percentile must be between 0 and 100"},
		{"p101<1s", "percentile must be between 0 and 100"},
		{"pxx<1s", "percentile must be between 0 and 100"},
		{"mean<1s", `unknown metric "mean"`},
		{"p95<300", "p95 needs a duration"},
		{"avg<-1s", "avg needs a duration"},
		{"errors<5", "errors needs a rate"},
		{"errors<101%", "errors needs a rate"},
		{"rps>lots", "rps needs a non-negative number"},
		{"p95<1s<2s", "more than one comparison"},
		{"p95=<1s", "invalid operator"},
		{"p95!<1s", "invalid operator"},
		{":p95<1s", "empty request name"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := ParseThreshold(tt.in)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), `"`+tt.in+`"`) {
				t.Fatalf("err = %v, want %q naming the threshold", err, tt.wantErr)
			}
		})
	}
	if _, err := ParseThresholds([]string{"p95<1s", "bad"}); err == nil || !strings.Contains(err.Error(), `"bad"`) {
		t.Fatalf("ParseThresholds: %v", err)
	}
}

func TestCheckThresholds(t *testing.T) {
	m := metrics{}
	for i := 1; i <= 100; i++ { // login: 1..100ms, one failure
		r := report.Request{File: "a.mon", Line: 1, Name: "login", Status: 200, Duration: time.Duration(i) * time.Millisecond}
		if i == 100 {
			r.Tests = []script.TestResult{{Name: "ok"}}
		}
		m.record(r)
	}
	for i := 0; i < 100; i++ { // users: never answered
		m.record(report.Request{File: "b.mon", Line: 1, Name: "users", Error: "connection refused"})
	}
	res := Result{Duration: 10 * time.Second}
	res.Stats, res.Total = m.stats()

	tests := []struct {
		threshold  string
		wantPassed bool
		wantActual string
	}{
		{"login:p95<300ms", true, "p95=9"},
		{"login:p95<90ms", false, "p95=9"},
		{"login:max<=100ms", true, "max=100.0ms"},
		{"login:min>1ms", false, "min=1.00ms"},
		{"login:avg<51ms", true, "avg=50.5ms"},
		{"login:med<60ms", true, "med="},
		{"login:errors<=1%", true, "errors=1.00%"},
		{"login:errors<1%", false, "errors=1.00%"},
		{"errors<10%", false, "errors=50.50%"},
		{"rps>=20", true, "rps=20.0"},
		{"login:rps>10", false, "rps=10.0"},
		{"users:p95<1s", false, "no responses to time"},
		{"nobody:p95<1s", false, `no requests named "nobody" ran`},
	}
	for _, tt := range tests {
		t.Run(tt.threshold, func(t *testing.T) {
			th, err := ParseThreshold(tt.threshold)
			if err != nil {
				t.Fatal(err)
			}
			got := Check(res, []Threshold{th})[0]
			if got.Passed != tt.wantPassed || !strings.HasPrefix(got.Actual, tt.wantActual) {
				t.Fatalf("passed=%v actual=%q, want %v %q", got.Passed, got.Actual, tt.wantPassed, tt.wantActual)
			}
		})
	}
	if got := Check(Result{}, []Threshold{{Metric: "errors", Op: "<", Value: 0.1}})[0]; got.Passed || got.Actual != "no requests ran" {
		t.Fatalf("empty result: %+v", got)
	}
}

func TestThresholdReport(t *testing.T) {
	if ThresholdReport(nil) != "" {
		t.Fatal("no thresholds, no report")
	}
	results := []ThresholdResult{
		{Threshold: Threshold{Source: "p95<300ms"}, Passed: true, Actual: "p95=120ms"},
		{Threshold: Threshold{Source: "errors<1%"}, Actual: "errors=3.20%"},
	}
	want := "\nthresholds\n  ✓ p95<300ms  p95=120ms\n  ✗ errors<1%  errors=3.20%\n"
	if got := ThresholdReport(results); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if Failed(results) != 1 {
		t.Fatal("Failed")
	}
}
