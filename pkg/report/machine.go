package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"time"
)

// JSON writes all results as one JSON document when the run is done.
type JSON struct {
	w       io.Writer
	results []Request
}

// NewJSON returns a JSON reporter writing to w.
func NewJSON(w io.Writer) *JSON { return &JSON{w: w} }

func (j *JSON) Request(r Request) { j.results = append(j.results, r) }

type jsonRequest struct {
	Request
	DurationMS float64 `json:"durationMs"`
	Failed     bool    `json:"failed"`
}

type jsonSummary struct {
	Summary
	DurationMS float64 `json:"durationMs"`
}

// Done writes {"summary": {...}, "requests": [...]}.
func (j *JSON) Done(s Summary) error {
	doc := struct {
		Summary  jsonSummary   `json:"summary"`
		Requests []jsonRequest `json:"requests"`
	}{Summary: jsonSummary{s, ms(s.Duration)}, Requests: make([]jsonRequest, len(j.results))}
	for i, r := range j.results {
		doc.Requests[i] = jsonRequest{r, ms(r.Duration), r.Failed()}
	}
	enc := json.NewEncoder(j.w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// JUnit writes JUnit XML (one testsuite per .mon file) when the run is done.
// Each test() is a testcase; a request without tests is one testcase. A
// request error is an <error>, a failed test a <failure>, a skipped request
// <skipped/>.
type JUnit struct {
	w       io.Writer
	results []Request
}

// NewJUnit returns a JUnit reporter writing to w.
func NewJUnit(w io.Writer) *JUnit { return &JUnit{w: w} }

func (j *JUnit) Request(r Request) { j.results = append(j.results, r) }

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Time     string      `xml:"time,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr,omitempty"`
	Text    string `xml:",chardata"`
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

// cases turns one request into testcases.
func (r Request) cases() []junitCase {
	base := junitCase{Classname: fmt.Sprintf("%s:%d %s%s", r.File, r.Line, r.Name, r.rowSuffix()), Time: seconds(r.Duration)}
	switch {
	case r.Skipped:
		c := base
		c.Name = r.Name
		c.Skipped = &junitMessage{Message: r.SkipReason}
		return []junitCase{c}
	case len(r.Tests) == 0:
		c := base
		c.Name = r.Name
		if r.Error != "" {
			c.Error = &junitMessage{Message: firstLine(r.Error), Text: r.Error}
		}
		return []junitCase{c}
	}
	var out []junitCase
	for _, t := range r.Tests {
		c := base
		c.Name = t.Name
		if !t.Passed {
			c.Failure = &junitMessage{Message: firstLine(t.Error), Text: t.Error}
		}
		out = append(out, c)
	}
	if r.Error != "" { // e.g. the script threw after some tests ran
		c := base
		c.Name = r.Name
		c.Error = &junitMessage{Message: firstLine(r.Error), Text: r.Error}
		out = append(out, c)
	}
	return out
}

func firstLine(s string) string {
	for i, ch := range s {
		if ch == '\n' {
			return s[:i]
		}
	}
	return s
}

// Done writes the XML document.
func (j *JUnit) Done(s Summary) error {
	doc := junitSuites{Name: "monica", Time: seconds(s.Duration)}
	index := map[string]int{}
	var durations []time.Duration
	for _, r := range j.results {
		i, ok := index[r.File]
		if !ok {
			i = len(doc.Suites)
			index[r.File] = i
			doc.Suites = append(doc.Suites, junitSuite{Name: r.File})
			durations = append(durations, 0)
		}
		suite := &doc.Suites[i]
		durations[i] += r.Duration
		for _, c := range r.cases() {
			suite.Cases = append(suite.Cases, c)
			suite.Tests++
			switch {
			case c.Failure != nil:
				suite.Failures++
			case c.Error != nil:
				suite.Errors++
			case c.Skipped != nil:
				suite.Skipped++
			}
		}
	}
	for i, suite := range doc.Suites {
		doc.Suites[i].Time = seconds(durations[i])
		doc.Tests += suite.Tests
		doc.Failures += suite.Failures
		doc.Errors += suite.Errors
		doc.Skipped += suite.Skipped
	}
	if _, err := io.WriteString(j.w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(j.w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(j.w, "\n")
	return err
}
