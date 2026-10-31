package load

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"jmpeax.com/sec/monica/pkg/report"
)

// Stat aggregates the results of one request (by file and line) or, for
// Result.Total, of all of them.
type Stat struct {
	Name string // the request's label; "all" for the total
	File string
	Line int

	Requests, Failed uint64
	// Duration is the time of each request that got a response; the
	// phases break it down (see net.Timings).
	Duration, DNS, Connect, TLS, Waiting, Receiving Histogram
	BytesSent, BytesReceived                        int64
}

func (s *Stat) record(r report.Request) {
	s.Requests++
	if r.Failed() {
		s.Failed++
	}
	if r.Status == 0 {
		return // no response: nothing to time
	}
	s.Duration.Record(r.Duration)
	s.DNS.Record(r.Timings.DNS)
	s.Connect.Record(r.Timings.Connect)
	s.TLS.Record(r.Timings.TLS)
	s.Waiting.Record(r.Timings.Waiting)
	s.Receiving.Record(r.Timings.Receiving)
	s.BytesSent += r.BytesSent
	s.BytesReceived += r.BytesReceived
}

func (s *Stat) merge(o *Stat) {
	s.Requests += o.Requests
	s.Failed += o.Failed
	s.Duration.Merge(&o.Duration)
	s.DNS.Merge(&o.DNS)
	s.Connect.Merge(&o.Connect)
	s.TLS.Merge(&o.TLS)
	s.Waiting.Merge(&o.Waiting)
	s.Receiving.Merge(&o.Receiving)
	s.BytesSent += o.BytesSent
	s.BytesReceived += o.BytesReceived
}

// FailRate is Failed / Requests (0 without requests).
func (s *Stat) FailRate() float64 {
	if s.Requests == 0 {
		return 0
	}
	return float64(s.Failed) / float64(s.Requests)
}

type statKey struct {
	file string
	line int
}

// metrics collects one VU's results; VUs' metrics are merged at the end.
type metrics map[statKey]*Stat

func (m metrics) record(r report.Request) {
	k := statKey{r.File, r.Line}
	s, ok := m[k]
	if !ok {
		s = &Stat{Name: r.Name, File: r.File, Line: r.Line}
		m[k] = s
	}
	s.record(r)
}

func (m metrics) merge(o metrics) {
	for k, s := range o {
		if mine, ok := m[k]; ok {
			mine.merge(s)
			continue
		}
		cp := &Stat{Name: s.Name, File: s.File, Line: s.Line}
		cp.merge(s)
		m[k] = cp
	}
}

// stats returns the per-request stats in file and line order, and their
// total.
func (m metrics) stats() ([]Stat, Stat) {
	out := make([]Stat, 0, len(m))
	total := Stat{Name: "all"}
	for _, s := range m {
		out = append(out, *s)
		total.merge(s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, total
}

// writeTable prints one row per request and the total:
//
//	request  reqs  failed  rps   min    avg    p50    p90    p95    p99    max
//	login    1234  0       41.1  12ms   ...
func writeTable(b *strings.Builder, stats []Stat, total Stat, d time.Duration) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "request\treqs\tfailed\trps\tmin\tavg\tp50\tp90\tp95\tp99\tmax")
	row := func(s Stat) {
		h := &s.Duration
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Requests, s.Failed, rate(int64(s.Requests), d),
			fmtDur(h.Min()), fmtDur(h.Mean()), fmtDur(h.Quantile(0.5)), fmtDur(h.Quantile(0.9)),
			fmtDur(h.Quantile(0.95)), fmtDur(h.Quantile(0.99)), fmtDur(h.Max()))
	}
	for _, s := range stats {
		row(s)
	}
	if len(stats) > 1 {
		row(total)
	}
	_ = tw.Flush()

	if total.Duration.Count() == 0 {
		return
	}
	phase := func(name string, h *Histogram) string {
		return fmt.Sprintf("%s %s / %s", name, fmtDur(h.Mean()), fmtDur(h.Quantile(0.95)))
	}
	fmt.Fprintf(b, "phases (avg / p95)  %s · %s · %s · %s · %s\n",
		phase("dns", &total.DNS), phase("connect", &total.Connect), phase("tls", &total.TLS),
		phase("waiting", &total.Waiting), phase("receiving", &total.Receiving))
}

// fmtDur prints a latency compactly: 850µs, 12.3ms, 1.23s.
func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "0"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d/time.Microsecond)
	case d < 10*time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// fmtBytes prints a size in SI units: 512 B, 1.2 kB, 3.4 MB.
func fmtBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, i := float64(n), -1
	for v >= unit && i < 4 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %cB", v, "kMGTP"[i])
}
