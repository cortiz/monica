// Package importer converts requests from other tools into .mon files.
// Each source (curl, later Postman and Bruno) builds Requests; WriteMon
// turns them into .mon text.
package importer

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// Header is one header line; Requests keep headers in source order.
type Header struct {
	Name, Value string
}

// Request is a request to write as .mon text.
type Request struct {
	// Title follows the "###" separator; Name becomes "# @name".
	Title string
	Name  string
	// Comments are written as "# ..." lines before the request line.
	Comments []string
	Timeout  time.Duration
	Method   string
	URL      string
	Headers  []Header
	// Body is written verbatim. BodyFile, when set, is written as a
	// "< path" body instead.
	Body     string
	BodyFile string
}

var (
	nameRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	methodRe = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// WriteMon writes reqs as one .mon file, separated by "###" lines. It
// fails when a request cannot be expressed in the format (for example a
// body line that would start a new request or a script).
func WriteMon(w io.Writer, reqs []Request) error {
	var b strings.Builder
	for i, r := range reqs {
		if err := check(r, i == len(reqs)-1); err != nil {
			return fmt.Errorf("request %d (%s %s): %w", i+1, r.Method, r.URL, err)
		}
		if i > 0 || r.Title != "" {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.TrimRight("### "+r.Title, " ") + "\n")
		}
		for _, c := range r.Comments {
			for _, line := range strings.Split(c, "\n") {
				b.WriteString(strings.TrimRight("# "+line, " ") + "\n")
			}
		}
		if r.Name != "" {
			b.WriteString("# @name " + r.Name + "\n")
		}
		if r.Timeout > 0 {
			b.WriteString("# @timeout " + r.Timeout.String() + "\n")
		}
		b.WriteString(r.Method + " " + r.URL + "\n")
		for _, h := range r.Headers {
			b.WriteString(h.Name + ": " + h.Value + "\n")
		}
		switch {
		case r.BodyFile != "":
			b.WriteString("\n< " + r.BodyFile + "\n")
		case r.Body != "":
			b.WriteString("\n" + r.Body)
			if i < len(reqs)-1 && !strings.HasSuffix(r.Body, "\n") {
				b.WriteString("\n")
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// check reports what WriteMon cannot write for r; last is set for the
// file's last request.
func check(r Request, last bool) error {
	if !methodRe.MatchString(r.Method) {
		return fmt.Errorf("invalid method %q", r.Method)
	}
	if r.URL == "" || strings.ContainsAny(r.URL, " \t\r\n") {
		return fmt.Errorf("URL %q is empty or contains whitespace", r.URL)
	}
	if r.Name != "" && !nameRe.MatchString(r.Name) {
		return fmt.Errorf("invalid name %q", r.Name)
	}
	if strings.ContainsAny(r.Title, "\r\n") {
		return fmt.Errorf("title contains a line break")
	}
	for _, h := range r.Headers {
		if !methodRe.MatchString(h.Name) {
			return fmt.Errorf("invalid header name %q", h.Name)
		}
		if strings.ContainsAny(h.Value, "\r\n") {
			return fmt.Errorf("header %s contains a line break", h.Name)
		}
	}
	if r.BodyFile != "" {
		if r.Body != "" {
			return fmt.Errorf("both a body and a body file")
		}
		if strings.ContainsAny(r.BodyFile, "\r\n") {
			return fmt.Errorf("body file path contains a line break")
		}
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(r.Body, "\r\n", "\n"), "\n")
	if !last {
		// Before a "###" separator the parser drops a body's trailing blank lines.
		content := lines
		if n := len(content); n > 0 && content[n-1] == "" {
			content = content[:n-1]
		}
		if n := len(content); n > 0 && strings.TrimSpace(content[n-1]) == "" {
			return fmt.Errorf("a body ending in blank lines can only be the file's last request")
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "###") {
			return fmt.Errorf("a body line starts with ###, which would start a new request")
		}
		if strings.HasPrefix(line, ">") {
			rest := strings.TrimSpace(line[1:])
			if strings.HasPrefix(rest, "{%") || (strings.HasSuffix(rest, ".js") && !strings.ContainsAny(rest, " \t")) {
				return fmt.Errorf("a body line %q would be read as a post-response script", line)
			}
		}
	}
	if body := strings.TrimRight(r.Body, "\r\n"); !strings.Contains(body, "\n") && (strings.HasPrefix(body, "< ") || strings.HasPrefix(body, "<\t")) {
		return fmt.Errorf("a one-line body starting with \"< \" would be read as a file path")
	}
	return nil
}
