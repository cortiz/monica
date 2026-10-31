package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// Console prints a human-readable report as results arrive.
type Console struct {
	w io.Writer
	// Quiet prints only the status lines, errors, tests and summary: no
	// response headers or bodies.
	Quiet bool
}

// NewConsole returns a Console writing to w.
func NewConsole(w io.Writer) *Console { return &Console{w: w} }

const (
	markPass = "✓"
	markFail = "✗"
	markSkip = "-"
)

// Request prints, for example:
//
//	✓ login  POST https://api.example.com/login → 200 OK  42ms  (api.mon:3)
//	<response>
//	    ✓ returns a token
//	    ✗ is fast
//	        expected 612 to be less than 500
func (c *Console) Request(r Request) {
	where := fmt.Sprintf("(%s:%d)", r.File, r.Line)
	if r.Iteration > 0 {
		where = fmt.Sprintf("(%s:%d, row %d)", r.File, r.Line, r.Iteration)
	}
	if r.Skipped {
		line := fmt.Sprintf("%s %s  skipped", markSkip, r.Name)
		if r.SkipReason != "" {
			line += ": " + r.SkipReason
		}
		c.printf("%s  %s\n", line, where)
		return
	}
	mark := markPass
	if r.Failed() {
		mark = markFail
	}
	head := fmt.Sprintf("%s %s", mark, r.Name)
	if req := r.Method + " " + r.URL; r.Method != "" && r.Name != req {
		head += "  " + req
	}
	if r.Status > 0 {
		head += fmt.Sprintf(" → %d %s  %s", r.Status, r.StatusText, formatDuration(r.Duration))
	}
	if r.Attempts > 1 {
		head += fmt.Sprintf("  %d attempts", r.Attempts)
	}
	c.printf("%s  %s\n", head, where)
	if r.Error != "" {
		c.printf("    %s %s\n", markFail, indent(r.Error, "      "))
	}
	if !c.Quiet && r.Response != "" {
		c.printf("%s\n", r.Response)
	}
	for _, t := range r.Tests {
		if t.Passed {
			c.printf("    %s %s\n", markPass, t.Name)
			continue
		}
		c.printf("    %s %s\n        %s\n", markFail, t.Name, indent(t.Error, "        "))
	}
}

// Done prints the summary line, e.g.
// "3 requests: 2 passed, 1 failed · 5 tests: 4 passed, 1 failed · 1.2s".
func (c *Console) Done(s Summary) error {
	parts := []string{fmt.Sprintf("%d %s: %d passed, %d failed", s.Requests, plural(s.Requests, "request"), s.Passed, s.Failed)}
	if s.Skipped > 0 {
		parts[0] += fmt.Sprintf(", %d skipped", s.Skipped)
	}
	if s.Tests > 0 {
		parts = append(parts, fmt.Sprintf("%d %s: %d passed, %d failed", s.Tests, plural(s.Tests, "test"), s.TestsPassed, s.TestsFailed))
	}
	parts = append(parts, formatDuration(s.Duration))
	_, err := fmt.Fprintf(c.w, "\n%s\n", strings.Join(parts, " · "))
	return err
}

// printf writes to the report; write errors surface from Done.
func (c *Console) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(c.w, format, args...)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// indent indents every line after the first, for multi-line messages.
func indent(s, prefix string) string {
	return strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n"+prefix)
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < 10*time.Millisecond:
		return d.Round(100 * time.Microsecond).String()
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	default:
		return d.Round(10 * time.Millisecond).String()
	}
}
