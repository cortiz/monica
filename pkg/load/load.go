// Package load runs .mon files as a load test: a fixed number of virtual
// users (VUs) each loop over the files until a duration or a total number of
// iterations is reached.
package load

import (
	"context"
	"errors"
	"fmt"
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
	// VUs is the number of virtual users running at once (at least 1).
	VUs int
	// Duration stops VUs from starting new iterations once it has passed;
	// 0 means no time limit (Iterations must then be set).
	Duration time.Duration
	// Iterations is the total number of iterations, shared by all VUs; 0
	// means as many as fit in Duration.
	Iterations int
	// GracefulStop is how long iterations running when Duration ends may
	// continue before their requests are aborted. Interrupted iterations
	// are counted, not reported.
	GracefulStop time.Duration
	// Files are the .mon files of one iteration, in run order.
	Files []string
	// Run is the runner configuration each VU starts from; Context,
	// NoSummary and VU are set per VU.
	Run runner.Opts
}

// Result summarizes a load test.
type Result struct {
	VUs int
	// Iterations completed, and those cut short by the stop.
	Iterations, Interrupted int64
	// Requests that ran (skipped ones excluded), and how many failed.
	Requests, Failed int64
	Duration         time.Duration
}

func (o *Options) validate() error {
	switch {
	case o.VUs < 1:
		return fmt.Errorf("vus must be at least 1, got %d", o.VUs)
	case o.Duration < 0:
		return fmt.Errorf("duration must not be negative, got %s", o.Duration)
	case o.Iterations < 0:
		return fmt.Errorf("iterations must not be negative, got %d", o.Iterations)
	case o.Duration == 0 && o.Iterations == 0:
		return errors.New("set a duration or a number of iterations")
	case len(o.Files) == 0:
		return errors.New("no .mon files to run")
	}
	return nil
}

// Run runs the load test until Duration or Iterations is reached, ctx is
// done, or a VU hits an input error (unreadable file, invalid syntax or
// config, unknown variable), which would fail every iteration the same
// way. It returns the result so far and, in the last two cases, ctx's error
// or the *runner.InputError.
func Run(ctx context.Context, opts Options) (Result, error) {
	if err := opts.validate(); err != nil {
		return Result{}, err
	}
	// iterCtx ends when no new iteration may start; hardCtx when running
	// requests are aborted.
	hardCtx, hardStop := context.WithCancel(ctx)
	defer hardStop()
	iterCtx, iterStop := context.WithCancel(hardCtx)
	defer iterStop()
	if opts.Duration > 0 {
		t1 := time.AfterFunc(opts.Duration, iterStop)
		t2 := time.AfterFunc(opts.Duration+opts.GracefulStop, hardStop)
		defer t1.Stop()
		defer t2.Stop()
	}

	t := &test{opts: opts, hard: hardCtx}
	var abortOnce sync.Once
	abort := func(err error) {
		abortOnce.Do(func() {
			t.inputErr = err
			hardStop()
		})
	}

	start := time.Now()
	var wg sync.WaitGroup
	for vu := 1; vu <= opts.VUs; vu++ {
		wg.Add(1)
		go func(vu int) {
			defer wg.Done()
			t.runVU(vu, iterCtx, abort)
		}(vu)
	}
	wg.Wait()

	res := Result{
		VUs:         opts.VUs,
		Iterations:  t.iterations.Load(),
		Interrupted: t.interrupted.Load(),
		Requests:    t.requests.Load(),
		Failed:      t.failed.Load(),
		Duration:    time.Since(start),
	}
	if t.inputErr != nil {
		return res, t.inputErr
	}
	return res, ctx.Err()
}

type test struct {
	opts     Options
	hard     context.Context
	inputErr error // set once, before hard is cancelled

	claimed                 atomic.Int64 // iterations started (with Iterations)
	iterations, interrupted atomic.Int64
	requests, failed        atomic.Int64
}

// runVU loops over the files with its own runner (own script runtimes and
// vars) until iterCtx ends or the shared iterations run out.
func (t *test) runVU(vu int, iterCtx context.Context, abort func(error)) {
	ropts := t.opts.Run
	ropts.Context, ropts.NoSummary, ropts.VU = t.hard, true, vu
	sk := &sink{t: t}
	r := runner.NewWithReporter(&ropts, sk)
	defer func() {
		if err := r.AfterAll(); err != nil {
			logging.Log.Warn().Msgf("VU %d: %v", vu, err)
		}
	}()
	for n := 1; iterCtx.Err() == nil; n++ {
		if t.opts.Iterations > 0 && t.claimed.Add(1) > int64(t.opts.Iterations) {
			return
		}
		r.SetIteration(n, nil)
		sk.ran = false
		for _, file := range t.opts.Files {
			err := r.RunFile(file)
			if t.hard.Err() != nil {
				break
			}
			var ie *runner.InputError
			if errors.As(err, &ie) {
				abort(ie)
				break
			}
			if err != nil {
				logging.Log.Debug().Msgf("VU %d: %v", vu, err)
			}
		}
		if t.hard.Err() != nil {
			t.interrupted.Add(1)
			return
		}
		if !sk.ran {
			// Everything was filtered out or @skip: looping would only spin.
			abort(&runner.InputError{Err: errors.New("an iteration ran no requests (all filtered out by --filter or marked @skip)")})
			return
		}
		t.iterations.Add(1)
	}
}

// sink counts one VU's results into the test's totals.
type sink struct {
	t   *test
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
	s.t.requests.Add(1)
	if r.Failed() {
		s.t.failed.Add(1)
	}
}

func (s *sink) Done(report.Summary) error { return nil }

// Summary is the end-of-test report, e.g.
//
//	monica load: 10 VUs, 30s
//	iterations  1234 completed (41.1/s), 2 interrupted
//	requests    2468 (82.2/s), 3 failed (0.12%)
//	duration    30.02s
func (r Result) Summary(opts Options) string {
	var b strings.Builder
	limit := opts.Duration.String()
	if opts.Iterations > 0 {
		limit = fmt.Sprintf("%d %s", opts.Iterations, plural(int64(opts.Iterations), "iteration"))
		if opts.Duration > 0 {
			limit += ", max " + opts.Duration.String()
		}
	}
	fmt.Fprintf(&b, "monica load: %d %s, %s\n", r.VUs, plural(int64(r.VUs), "VU"), limit)
	fmt.Fprintf(&b, "iterations  %d completed (%s/s)", r.Iterations, rate(r.Iterations, r.Duration))
	if r.Interrupted > 0 {
		fmt.Fprintf(&b, ", %d interrupted", r.Interrupted)
	}
	fmt.Fprintf(&b, "\nrequests    %d (%s/s), %d failed", r.Requests, rate(r.Requests, r.Duration), r.Failed)
	if r.Requests > 0 {
		fmt.Fprintf(&b, " (%.2f%%)", 100*float64(r.Failed)/float64(r.Requests))
	}
	fmt.Fprintf(&b, "\nduration    %s\n", r.Duration.Round(10*time.Millisecond))
	return b.String()
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
