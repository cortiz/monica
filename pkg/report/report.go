// Package report renders run results: a console view for people and
// machine formats for CI. Reporters consume one Request result per request,
// as the runner finishes it, then a Summary.
package report

import (
	"errors"
	"fmt"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/script"
)

// Request is the result of one request. All text is already masked.
type Request struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Name is the request's label: @name, else ### title, else "METHOD URL".
	Name string `json:"name"`
	// Iteration is the 1-based --data row the request ran for; 0 without
	// --data.
	Iteration  int           `json:"iteration,omitempty"`
	Method     string        `json:"method"`
	URL        string        `json:"url"` // as sent, once expanded
	Status     int           `json:"status,omitempty"`
	StatusText string        `json:"statusText,omitempty"`
	Duration   time.Duration `json:"-"`
	Skipped    bool          `json:"skipped,omitempty"`
	SkipReason string        `json:"skipReason,omitempty"`
	// Attempts is how many times the request was sent under @retry; 0 when
	// it was sent (at most) once.
	Attempts int `json:"attempts,omitempty"`
	// Error is why the request failed before or after sending (input,
	// connection or script error); empty otherwise. Failed tests are in Tests.
	Error string              `json:"error,omitempty"`
	Tests []script.TestResult `json:"tests,omitempty"`
	// Response is the printable response (status line, headers, body).
	Response string `json:"-"`
	// Timings, BytesSent and BytesReceived describe the exchange, for load
	// metrics; zero when nothing was received.
	Timings                  net.Timings `json:"-"`
	BytesSent, BytesReceived int64       `json:"-"`
}

// rowSuffix is " [row N]" for a --data iteration, else "".
func (r Request) rowSuffix() string {
	if r.Iteration == 0 {
		return ""
	}
	return fmt.Sprintf(" [row %d]", r.Iteration)
}

// Failed reports whether the request errored or has a failed test.
func (r Request) Failed() bool {
	if r.Error != "" {
		return true
	}
	for _, t := range r.Tests {
		if !t.Passed {
			return true
		}
	}
	return false
}

// Summary counts a run's results.
type Summary struct {
	Requests    int           `json:"requests"`
	Passed      int           `json:"passed"`
	Failed      int           `json:"failed"`
	Skipped     int           `json:"skipped"`
	Tests       int           `json:"tests"`
	TestsPassed int           `json:"testsPassed"`
	TestsFailed int           `json:"testsFailed"`
	Duration    time.Duration `json:"-"`
}

// Summarize counts results; d is the run's wall time.
func Summarize(results []Request, d time.Duration) Summary {
	s := Summary{Requests: len(results), Duration: d}
	for _, r := range results {
		switch {
		case r.Skipped:
			s.Skipped++
		case r.Failed():
			s.Failed++
		default:
			s.Passed++
		}
		for _, t := range r.Tests {
			s.Tests++
			if t.Passed {
				s.TestsPassed++
			} else {
				s.TestsFailed++
			}
		}
	}
	return s
}

// Reporter receives results as the run progresses.
type Reporter interface {
	// Request is called once per request, in run order.
	Request(r Request)
	// Done is called once at the end; machine reporters write here.
	Done(s Summary) error
}

// Multi fans results out to several reporters.
func Multi(reporters ...Reporter) Reporter { return multi(reporters) }

type multi []Reporter

func (m multi) Request(r Request) {
	for _, rep := range m {
		rep.Request(r)
	}
}

func (m multi) Done(s Summary) error {
	var errs []error
	for _, rep := range m {
		errs = append(errs, rep.Done(s))
	}
	return errors.Join(errs...)
}
