// Package load runs .mon files as a load test. Virtual users (VUs) run
// iterations (one pass over the files) in one of four ways: a fixed number
// of VUs looping, a VU count ramped through stages, iterations started at a
// constant rate, or at a rate ramped through stages.
package load

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

// DefaultGracefulStop is how long iterations still running at the end of
// the duration may take before their requests are aborted.
const DefaultGracefulStop = 30 * time.Second

// Options configure a load test.
type Options struct {
	// VUs is the number of virtual users looping at once; with Stages, the
	// count the ramp starts from (may be 0); with Rate, the most VUs
	// running iterations at once (at least 1).
	VUs int
	// Rate, when positive, starts iterations at this many per second (with
	// Stages: starts the ramp from it) on free VUs, however long each
	// takes; an iteration due while all VUs are busy is dropped.
	Rate float64
	// Stages ramp the VU count, or with Rate the rate, linearly to each
	// target in turn; their total is the test's duration.
	Stages []Stage
	// Duration stops new iterations once it has passed; 0 means no time
	// limit (Iterations or Stages must then be set). Not with Stages.
	Duration time.Duration
	// Iterations is the total number of iterations, shared by all VUs; 0
	// means as many as fit in the duration.
	Iterations int
	// GracefulStop is how long iterations running when the duration ends
	// may continue before their requests are aborted. Interrupted
	// iterations are counted, not reported.
	GracefulStop time.Duration
	// Files are the .mon files of one iteration, in run order.
	Files []string
	// Run is the runner configuration each VU starts from; Context,
	// NoSummary and VU are set per VU.
	Run runner.Opts
	// Progress, when set, is called every ProgressInterval (default 1s)
	// while the test runs, from its own goroutine.
	Progress         func(Progress)
	ProgressInterval time.Duration
}

// Result summarizes a load test.
type Result struct {
	// VUs is how many VUs were started (the most at once).
	VUs int
	// Iterations completed, those cut short by the stop and, in rate mode,
	// those dropped because no VU was free.
	Iterations, Interrupted, Dropped int64
	// Requests that ran (skipped ones excluded), and how many failed.
	Requests, Failed int64
	Duration         time.Duration
	// Stats has one entry per request (file and line order); Total sums
	// them. Requests aborted by the stop are not included.
	Stats []Stat
	Total Stat
}

func (o *Options) validate() error {
	switch {
	case o.Rate < 0 || math.IsNaN(o.Rate) || math.IsInf(o.Rate, 0):
		return fmt.Errorf("rate must be a positive number of iterations per second, got %g", o.Rate)
	case o.VUs < 0 || (o.VUs == 0 && (len(o.Stages) == 0 || o.Rate > 0)):
		return fmt.Errorf("vus must be at least 1, got %d", o.VUs)
	case o.Duration < 0:
		return fmt.Errorf("duration must not be negative, got %s", o.Duration)
	case o.Iterations < 0:
		return fmt.Errorf("iterations must not be negative, got %d", o.Iterations)
	case len(o.Stages) > 0 && o.Duration > 0:
		return errors.New("set either a duration or stages (stages set the duration)")
	case o.Duration == 0 && o.Iterations == 0 && len(o.Stages) == 0:
		return errors.New("set a duration, stages or a number of iterations")
	case len(o.Files) == 0:
		return errors.New("no .mon files to run")
	}
	for i, st := range o.Stages {
		if st.Duration <= 0 || st.Target < 0 || math.IsNaN(st.Target) || math.IsInf(st.Target, 0) {
			return fmt.Errorf("stage %d: needs a positive duration and a non-negative target", i+1)
		}
	}
	if o.Rate == 0 && len(o.Stages) > 0 && o.maxVUs() < 1 {
		return errors.New("stages never reach 1 VU")
	}
	return nil
}

// maxVUs is how many VUs the test may need.
func (o *Options) maxVUs() int {
	if o.Rate > 0 || len(o.Stages) == 0 {
		return o.VUs
	}
	most := float64(o.VUs)
	for _, st := range o.Stages {
		most = math.Max(most, st.Target)
	}
	return vuCount(most)
}

// vuCount is how many VUs a ramped value means: rounded to the nearest
// whole VU, so on a ramp from 0 to N, VU i joins once the ramp passes
// i-0.5 (not only at the very end of the stage, which could race the stop).
func vuCount(v float64) int { return int(math.Floor(v + 0.5 + 1e-9)) }

// Run runs the load test until its duration, stages or iterations are
// done, ctx is done, or a VU hits an input error (unreadable file, invalid
// syntax or config, unknown variable), which would fail every iteration the
// same way. It returns the result so far and, in the last two cases, ctx's
// error or the *runner.InputError.
func Run(ctx context.Context, opts Options) (Result, error) {
	if err := opts.validate(); err != nil {
		return Result{}, err
	}
	duration := opts.Duration
	if len(opts.Stages) > 0 {
		duration = stagesDuration(opts.Stages)
	}
	// iterCtx ends when no new iteration may start; hardCtx when running
	// requests are aborted.
	hardCtx, hardStop := context.WithCancel(ctx)
	defer hardStop()
	iterCtx, iterStop := context.WithCancel(hardCtx)
	defer iterStop()
	if duration > 0 {
		t1 := time.AfterFunc(duration, iterStop)
		t2 := time.AfterFunc(duration+opts.GracefulStop, hardStop)
		defer t1.Stop()
		defer t2.Stop()
	}

	t := &test{opts: opts, hard: hardCtx, iter: iterCtx, stopIter: iterStop}
	t.abort = func(err error) {
		t.abortOnce.Do(func() {
			t.inputErr = err
			hardStop()
		})
	}

	start := time.Now()
	if opts.Progress != nil {
		done := make(chan struct{})
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			t.reportProgress(start, duration, done)
		}()
		defer func() { close(done); <-stopped }()
	}
	switch {
	case opts.Rate > 0:
		t.runRate(start)
	case len(opts.Stages) > 0:
		t.runRamping(start)
	default:
		t.runFixed()
	}
	t.wg.Wait()

	all := metrics{}
	for _, v := range t.vus {
		all.merge(v.sk.m)
	}
	res := Result{
		VUs:         len(t.vus),
		Iterations:  t.iterations.Load(),
		Interrupted: t.interrupted.Load(),
		Dropped:     t.dropped.Load(),
		Duration:    time.Since(start),
	}
	res.Stats, res.Total = all.stats()
	res.Requests, res.Failed = int64(res.Total.Requests), int64(res.Total.Failed)
	if t.inputErr != nil {
		return res, t.inputErr
	}
	return res, ctx.Err()
}

type test struct {
	opts       Options
	hard, iter context.Context
	stopIter   func() // ends iter: no more iterations start
	abort      func(error)
	abortOnce  sync.Once
	inputErr   error // set once, before hard is cancelled
	wg         sync.WaitGroup

	mu   sync.Mutex
	vus  []*vu // every VU started
	live live  // for Progress

	claimed                          atomic.Int64 // iterations started (with Iterations)
	iterations, interrupted, dropped atomic.Int64
}

// claim reserves one of the shared iterations; false when they ran out.
// Taking the last one ends the test's iteration phase, so waiting VUs and
// schedulers stop.
func (t *test) claim() bool {
	if t.opts.Iterations == 0 {
		return true
	}
	n := t.claimed.Add(1)
	if n >= int64(t.opts.Iterations) {
		t.stopIter()
	}
	return n <= int64(t.opts.Iterations)
}

// runFixed starts opts.VUs VUs that loop until the end.
func (t *test) runFixed() {
	for i := 1; i <= t.opts.VUs; i++ {
		t.spawn(func(v *vu) {
			for t.iter.Err() == nil && t.claim() && v.iterate() {
			}
		})
	}
}

// runRamping starts every VU the stages need; VU i runs iterations only
// while the ramped count is at least i, finishing its current iteration
// when the count drops below it.
func (t *test) runRamping(start time.Time) {
	segs := segments(float64(t.opts.VUs), t.opts.Stages)
	g := newGauge()
	update := func() { g.set(vuCount(valueAt(segs, time.Since(start)))) }
	update()
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				update()
			case <-t.iter.Done():
				return
			}
		}
	}()
	for i := 1; i <= t.opts.maxVUs(); i++ {
		i := i
		t.spawnLazy(func() bool {
			// Create VU i only once the ramp first reaches it.
			for {
				n, changed := g.get()
				if n >= i {
					return true
				}
				select {
				case <-changed:
				case <-t.iter.Done():
					return false
				}
			}
		}, func(v *vu) {
			for t.iter.Err() == nil {
				if n, changed := g.get(); n < i {
					select {
					case <-changed:
					case <-t.iter.Done():
					}
					continue
				}
				if !t.claim() || !v.iterate() {
					return
				}
			}
		})
	}
}

// runRate starts iterations on schedule, each on a free VU; VUs are
// created as needed, up to opts.VUs.
func (t *test) runRate(start time.Time) {
	next := arrivals(segments(t.opts.Rate, t.opts.Stages))
	jobs := make(chan struct{})
	defer close(jobs) // idle VUs stop
	started := 0
	timer := time.NewTimer(0)
	defer timer.Stop()
	for k := int64(0); ; k++ {
		offset, ok := next(k)
		if !ok {
			return
		}
		if wait := time.Until(start.Add(offset)); wait > 0 {
			timer.Reset(wait)
			select {
			case <-timer.C:
			case <-t.iter.Done():
				return
			}
		}
		if t.iter.Err() != nil || !t.claim() {
			return
		}
		select {
		case jobs <- struct{}{}: // an idle VU took it
			continue
		default:
		}
		if started == t.opts.VUs {
			t.dropped.Add(1)
			continue
		}
		started++
		t.spawn(func(v *vu) {
			if !v.iterate() {
				return
			}
			for range jobs {
				if !v.iterate() {
					return
				}
			}
		})
	}
}

// spawn starts a VU goroutine running loop.
func (t *test) spawn(loop func(v *vu)) {
	t.spawnLazy(func() bool { return true }, loop)
}

// spawnLazy starts a goroutine that creates its VU once ready returns true
// (never, if it returns false) and then runs loop.
func (t *test) spawnLazy(ready func() bool, loop func(v *vu)) {
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		if !ready() {
			return
		}
		v := t.newVU()
		defer v.close()
		loop(v)
	}()
}

// vu is one virtual user: its own runner (script runtimes, vars, hooks)
// and metrics.
type vu struct {
	t  *test
	id int
	r  *runner.Runner
	sk *sink
	n  int // iterations started
}

func (t *test) newVU() *vu {
	t.mu.Lock()
	v := &vu{t: t, id: len(t.vus) + 1}
	t.vus = append(t.vus, v)
	t.mu.Unlock()
	ropts := t.opts.Run
	ropts.Context, ropts.NoSummary, ropts.VU = t.hard, true, v.id
	v.sk = &sink{t: t, m: metrics{}}
	v.r = runner.NewWithReporter(&ropts, v.sk)
	return v
}

func (v *vu) close() {
	if err := v.r.AfterAll(); err != nil {
		logging.Log.Warn().Msgf("VU %d: %v", v.id, err)
	}
}

// iterate runs one iteration; false means the test is over for this VU
// (aborted or failed with an input error).
func (v *vu) iterate() bool {
	t := v.t
	t.live.active.Add(1)
	defer t.live.active.Add(-1)
	v.n++
	v.r.SetIteration(v.n, nil)
	v.sk.ran = false
	for _, file := range t.opts.Files {
		err := v.r.RunFile(file)
		if t.hard.Err() != nil {
			break
		}
		var ie *runner.InputError
		if errors.As(err, &ie) {
			t.abort(ie)
			break
		}
		if err != nil {
			logging.Log.Debug().Msgf("VU %d: %v", v.id, err)
		}
	}
	if t.hard.Err() != nil {
		t.interrupted.Add(1)
		return false
	}
	if !v.sk.ran {
		// Everything was filtered out or @skip: looping would only spin.
		t.abort(&runner.InputError{Err: errors.New("an iteration ran no requests (all filtered out by --filter or marked @skip)")})
		return false
	}
	t.iterations.Add(1)
	return true
}

// gauge is an int that goroutines can wait on to change.
type gauge struct {
	mu      sync.Mutex
	n       int
	changed chan struct{}
}

func newGauge() *gauge { return &gauge{changed: make(chan struct{})} }

func (g *gauge) set(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n != g.n {
		g.n = n
		close(g.changed)
		g.changed = make(chan struct{})
	}
}

// get returns the value and a channel closed on the next change.
func (g *gauge) get() (int, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.n, g.changed
}

// sink collects one VU's results.
type sink struct {
	t   *test
	m   metrics
	ran bool // a request ran in the current iteration
}

func (s *sink) Request(r report.Request) {
	if r.Skipped {
		return
	}
	s.ran = true
	if r.Error != "" && s.t.hard.Err() != nil {
		return // aborted by the stop, not a failure of the system under test
	}
	s.m.record(r)
	s.t.live.record(r)
}

func (s *sink) Done(report.Summary) error { return nil }

// Summary is the end-of-test report, e.g.
//
//	monica load: 10 VUs, 30s
//	iterations  1234 completed (41.1/s), 2 interrupted
//	requests    2468 (82.2/s), 3 failed (0.12%)
//	duration    30.02s
//
// followed by the per-request table.
func (r Result) Summary(opts Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, "monica load: %s\n", describe(opts, r.VUs))
	fmt.Fprintf(&b, "iterations  %d completed (%s/s)", r.Iterations, rate(r.Iterations, r.Duration))
	if r.Interrupted > 0 {
		fmt.Fprintf(&b, ", %d interrupted", r.Interrupted)
	}
	if r.Dropped > 0 {
		fmt.Fprintf(&b, ", %d dropped (no free VU)", r.Dropped)
	}
	fmt.Fprintf(&b, "\nrequests    %d (%s/s), %d failed", r.Requests, rate(r.Requests, r.Duration), r.Failed)
	if r.Requests > 0 {
		fmt.Fprintf(&b, " (%.2f%%)", 100*float64(r.Failed)/float64(r.Requests))
	}
	if r.Total.BytesReceived > 0 || r.Total.BytesSent > 0 {
		fmt.Fprintf(&b, "\ndata        %s received, %s sent", fmtBytes(r.Total.BytesReceived), fmtBytes(r.Total.BytesSent))
	}
	fmt.Fprintf(&b, "\nduration    %s\n", r.Duration.Round(10*time.Millisecond))
	if r.Total.Requests > 0 {
		b.WriteString("\n")
		writeTable(&b, r.Stats, r.Total, r.Duration)
	}
	return b.String()
}

// describe names the test's shape, e.g. "10 VUs, 30s", "ramping VUs
// 0 → 50 → 0 over 1m30s" or "20.0 iterations/s, 30s, up to 50 VUs (12
// used)".
func describe(o Options, started int) string {
	var parts []string
	switch {
	case o.Rate > 0 && len(o.Stages) > 0:
		parts = append(parts, "ramping rate "+ramp(o.Rate, o.Stages, "%.1f")+" iterations/s over "+stagesDuration(o.Stages).String())
	case o.Rate > 0:
		parts = append(parts, fmt.Sprintf("%.1f iterations/s", o.Rate))
	case len(o.Stages) > 0:
		parts = append(parts, "ramping VUs "+ramp(float64(o.VUs), o.Stages, "%g")+" over "+stagesDuration(o.Stages).String())
	default:
		parts = append(parts, fmt.Sprintf("%d %s", o.VUs, plural(int64(o.VUs), "VU")))
	}
	iters := fmt.Sprintf("%d %s", o.Iterations, plural(int64(o.Iterations), "iteration"))
	switch {
	case o.Iterations > 0 && len(o.Stages) > 0:
		parts = append(parts, "max "+iters)
	case o.Iterations > 0 && o.Duration > 0:
		parts = append(parts, iters+", max "+o.Duration.String())
	case o.Iterations > 0:
		parts = append(parts, iters)
	case o.Duration > 0:
		parts = append(parts, o.Duration.String())
	}
	if o.Rate > 0 {
		parts = append(parts, fmt.Sprintf("up to %d %s (%d used)", o.VUs, plural(int64(o.VUs), "VU"), started))
	}
	return strings.Join(parts, ", ")
}

// ramp writes start → target → … with format for each value.
func ramp(start float64, stages []Stage, format string) string {
	vals := []string{fmt.Sprintf(format, start)}
	for _, st := range stages {
		vals = append(vals, fmt.Sprintf(format, st.Target))
	}
	return strings.Join(vals, " → ")
}

func rate(n int64, d time.Duration) string {
	if d <= 0 {
		return "0.0"
	}
	return fmt.Sprintf("%.1f", float64(n)/d.Seconds())
}

func plural(n int64, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
