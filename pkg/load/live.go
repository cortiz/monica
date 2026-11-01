package load

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"jmpeax.com/sec/monica/pkg/report"
)

// DefaultProgressInterval is how often Options.Progress is called.
const DefaultProgressInterval = time.Second

// Progress is a snapshot of a running test.
type Progress struct {
	Elapsed time.Duration
	// Planned is the test's duration (its stages' total, or Duration); 0
	// when only iterations limit it.
	Planned time.Duration
	// ActiveVUs are running an iteration right now; VUs have been started.
	ActiveVUs, VUs int
	// Iterations (completed), Requests (excluding aborted ones) and Failed
	// are totals so far.
	Iterations, Requests, Failed int64
	// RPS and P95 cover the last interval only (P95 is 0 without
	// responses in it).
	RPS float64
	P95 time.Duration
}

// live holds what Progress reports while the test runs; unlike the per-VU
// metrics it is shared, so it is kept small.
type live struct {
	active           atomic.Int64
	requests, failed atomic.Int64

	mu       sync.Mutex
	interval Histogram // durations since the last snapshot
	reqs     int64     // requests since the last snapshot
}

func (l *live) record(r report.Request) {
	l.requests.Add(1)
	if r.Failed() {
		l.failed.Add(1)
	}
	l.mu.Lock()
	l.reqs++
	if r.Status != 0 {
		l.interval.Record(r.Duration)
	}
	l.mu.Unlock()
}

// take returns and resets the interval's request count and p95.
func (l *live) take() (int64, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n, p95 := l.reqs, l.interval.Quantile(0.95)
	l.reqs, l.interval = 0, Histogram{}
	return n, p95
}

// reportProgress calls opts.Progress every interval until done is closed.
func (t *test) reportProgress(start time.Time, planned time.Duration, done <-chan struct{}) {
	every := t.opts.ProgressInterval
	if every <= 0 {
		every = DefaultProgressInterval
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	last := start
	for {
		select {
		case <-done:
			return
		case now := <-tick.C:
			n, p95 := t.live.take()
			t.mu.Lock()
			vus := len(t.vus)
			t.mu.Unlock()
			p := Progress{
				Elapsed:    now.Sub(start),
				Planned:    planned,
				ActiveVUs:  int(t.live.active.Load()),
				VUs:        vus,
				Iterations: t.iterations.Load(),
				Requests:   t.live.requests.Load(),
				Failed:     t.live.failed.Load(),
				P95:        p95,
			}
			if d := now.Sub(last).Seconds(); d > 0 {
				p.RPS = float64(n) / d
			}
			last = now
			t.opts.Progress(p)
		}
	}
}

// String is the one-line live view, e.g.
// "running 12s/30s · VUs 8/10 · 1234 iterations · 2468 requests · 82.0 rps · p95 18.0ms · 0.12% failed".
func (p Progress) String() string {
	elapsed := p.Elapsed.Truncate(time.Second).String()
	if p.Planned > 0 {
		elapsed += "/" + p.Planned.String()
	}
	failed := "0 failed"
	if p.Requests > 0 {
		failed = fmt.Sprintf("%.2f%% failed", 100*float64(p.Failed)/float64(p.Requests))
	}
	return fmt.Sprintf("running %s · VUs %d/%d · %d %s · %d %s · %.1f rps · p95 %s · %s",
		elapsed, p.ActiveVUs, p.VUs, p.Iterations, plural(p.Iterations, "iteration"),
		p.Requests, plural(p.Requests, "request"), p.RPS, fmtDur(p.P95), failed)
}
