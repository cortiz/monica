package load

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Threshold is a pass/fail condition on the results, written
// "[request:]metric op value", e.g. "p95<300ms", "login:p(99.9)<=1s",
// "errors<1%" or "rps>=100".
type Threshold struct {
	Source string // as written
	// Request limits the check to the requests with this label (@name,
	// ### title or "METHOD URL"); "" means all requests.
	Request string
	// Metric is min, avg, med, max, p (with Percentile), errors or rps.
	Metric     string
	Percentile float64 // (0, 100], for p
	Op         string  // <, <=, > or >=
	// Value is nanoseconds for latency metrics, a fraction (0..1) for
	// errors and requests per second for rps.
	Value float64
}

// ParseThresholds parses each threshold; the error names the first invalid
// one.
func ParseThresholds(list []string) ([]Threshold, error) {
	out := make([]Threshold, 0, len(list))
	for _, s := range list {
		t, err := ParseThreshold(s)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// ParseThreshold parses "[request:]metric op value".
func ParseThreshold(s string) (Threshold, error) {
	t := Threshold{Source: s}
	fail := func(format string, args ...any) (Threshold, error) {
		return Threshold{}, fmt.Errorf("invalid threshold %q: %s", s, fmt.Sprintf(format, args...))
	}
	i := strings.IndexAny(s, "<>")
	if i < 0 {
		return fail("expected [request:]metric op value with op <, <=, > or >=, e.g. p95<300ms")
	}
	t.Op = s[i : i+1]
	if i+1 < len(s) && s[i+1] == '=' {
		t.Op += "="
	}
	left, right := strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+len(t.Op):])
	if strings.HasSuffix(left, "=") || strings.HasSuffix(left, "!") {
		return fail("invalid operator (expected <, <=, > or >=)")
	}
	if j := strings.LastIndex(left, ":"); j >= 0 {
		t.Request, left = strings.TrimSpace(left[:j]), strings.TrimSpace(left[j+1:])
		if t.Request == "" {
			return fail("empty request name before ':'")
		}
	}
	switch m := strings.ToLower(left); {
	case m == "min" || m == "avg" || m == "med" || m == "max" || m == "errors" || m == "rps":
		t.Metric = m
	case strings.HasPrefix(m, "p"):
		q := strings.TrimPrefix(m, "p")
		if strings.HasPrefix(q, "(") && strings.HasSuffix(q, ")") {
			q = q[1 : len(q)-1]
		}
		pct, err := strconv.ParseFloat(q, 64)
		if err != nil || pct <= 0 || pct > 100 {
			return fail("percentile must be between 0 and 100, e.g. p95 or p(99.9)")
		}
		t.Metric, t.Percentile = "p", pct
	case m == "":
		return fail("missing metric")
	default:
		return fail("unknown metric %q (expected min, avg, med, max, pNN, errors or rps)", left)
	}
	if strings.ContainsAny(right, "<>=") {
		return fail("more than one comparison")
	}
	switch t.Metric {
	case "errors":
		v, err := parseRate(right)
		if err != nil {
			return fail("%v", err)
		}
		t.Value = v
	case "rps":
		v, err := strconv.ParseFloat(right, 64)
		if err != nil || v < 0 {
			return fail("rps needs a non-negative number of requests per second, e.g. rps>100")
		}
		t.Value = v
	default:
		d, err := time.ParseDuration(right)
		if err != nil || d < 0 {
			return fail("%s needs a duration such as 300ms or 1.5s", left)
		}
		t.Value = float64(d)
	}
	return t, nil
}

// parseRate reads "1%" or a fraction such as "0.01".
func parseRate(s string) (float64, error) {
	pct := strings.HasSuffix(s, "%")
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "%")), 64)
	if pct {
		v /= 100
	}
	if err != nil || v < 0 || v > 1 {
		return 0, errors.New("errors needs a rate such as 1% or 0.01 (between 0 and 100%)")
	}
	return v, nil
}

// ThresholdResult is a checked threshold.
type ThresholdResult struct {
	Threshold
	Passed bool
	// Actual is the measured value as text ("p95=120ms"), or why it could
	// not be measured.
	Actual string
}

// Check evaluates thresholds against res. A threshold on a request that
// never ran (or got no response, for latency metrics) fails.
func Check(res Result, thresholds []Threshold) []ThresholdResult {
	out := make([]ThresholdResult, len(thresholds))
	for i, t := range thresholds {
		out[i] = t.check(res)
	}
	return out
}

func (t Threshold) check(res Result) ThresholdResult {
	r := ThresholdResult{Threshold: t}
	stat := res.Total
	if t.Request != "" {
		stat = Stat{Name: t.Request}
		for i := range res.Stats {
			if res.Stats[i].Name == t.Request {
				stat.merge(&res.Stats[i])
			}
		}
	}
	if stat.Requests == 0 {
		r.Actual = "no requests ran"
		if t.Request != "" {
			r.Actual = fmt.Sprintf("no requests named %q ran", t.Request)
		}
		return r
	}
	name := t.Metric
	var actual float64
	var text string
	h := &stat.Duration
	switch t.Metric {
	case "errors":
		actual = stat.FailRate()
		text = fmt.Sprintf("%.2f%%", 100*actual)
	case "rps":
		actual = 0
		if res.Duration > 0 {
			actual = float64(stat.Requests) / res.Duration.Seconds()
		}
		text = fmt.Sprintf("%.1f", actual)
	default:
		if h.Count() == 0 {
			r.Actual = "no responses to time"
			return r
		}
		var d time.Duration
		switch t.Metric {
		case "min":
			d = h.Min()
		case "avg":
			d = h.Mean()
		case "med":
			d = h.Quantile(0.5)
		case "max":
			d = h.Max()
		case "p":
			d = h.Quantile(t.Percentile / 100)
			name = "p" + strconv.FormatFloat(t.Percentile, 'f', -1, 64)
		}
		actual, text = float64(d), fmtDur(d)
	}
	r.Actual = name + "=" + text
	switch t.Op {
	case "<":
		r.Passed = actual < t.Value
	case "<=":
		r.Passed = actual <= t.Value
	case ">":
		r.Passed = actual > t.Value
	case ">=":
		r.Passed = actual >= t.Value
	}
	return r
}

// ThresholdReport prints one line per threshold, e.g.
//
//	thresholds
//	  ✓ p95<300ms  p95=120ms
//	  ✗ errors<1%  errors=3.20%
func ThresholdReport(results []ThresholdResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nthresholds\n")
	width := 0
	for _, r := range results {
		width = max(width, len(r.Source))
	}
	for _, r := range results {
		mark := "✓"
		if !r.Passed {
			mark = "✗"
		}
		fmt.Fprintf(&b, "  %s %-*s  %s\n", mark, width, r.Source, r.Actual)
	}
	return b.String()
}

// Failed counts the thresholds that did not pass.
func Failed(results []ThresholdResult) int {
	n := 0
	for _, r := range results {
		if !r.Passed {
			n++
		}
	}
	return n
}
