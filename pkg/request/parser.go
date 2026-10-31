package request

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"jmpeax.com/sec/monica/internal/logging"
)

// ParseError reports a syntax problem in a .mon source, with its location.
type ParseError struct {
	File string // empty when parsing a raw string
	Line int    // 1-based; 0 when the error is not tied to a line
	Msg  string
}

func (e *ParseError) Error() string {
	switch {
	case e.File != "" && e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	case e.File != "":
		return fmt.Sprintf("%s: %s", e.File, e.Msg)
	case e.Line > 0:
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	default:
		return e.Msg
	}
}

// ParseRequest parses raw .mon text and returns its requests in source order.
func ParseRequest(raw string) ([]*Request, error) {
	return parse("", raw)
}

// ParseMonFile reads and parses a .mon file and returns its requests in
// source order.
func ParseMonFile(file string) ([]*Request, error) {
	logging.Log.Debug().Msgf("Parsing file: %s", file)
	monFile, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading mon file: %w", err)
	}
	return parse(file, string(monFile))
}

type parseState int

const (
	stateRequestLine parseState = iota
	stateHeaders
	stateBody
)

// parser holds the state of one parse. A source is a sequence of sections
// separated by "###" lines; each section holds at most one request.
type parser struct {
	file  string
	state parseState
	reqs  []*Request
	names map[string]int // request name -> line of its @name

	// Current section.
	title string
	meta  directives // collected before the request line
	req   *Request   // nil until the request line is seen
	body  []string   // raw body lines, terminators included
}

// directives holds the "# @..." lines of a section.
type directives struct {
	seen       map[string]int // directive -> line, to reject repeats
	firstLine  int
	name       string
	timeout    time.Duration
	skip       bool
	skipReason string
}

// parse reads each section in turn: comments/blank lines, the request line,
// header lines (comments allowed) up to the first blank line, then the body
// verbatim up to the next separator or EOF.
func parse(file, raw string) ([]*Request, error) {
	logging.Log.Debug().Msg("Parsing request")
	p := &parser{file: file, names: map[string]int{}}
	for i, rawLine := range strings.SplitAfter(raw, "\n") {
		if rawLine == "" {
			continue // after a trailing "\n"
		}
		lineNo := i + 1
		line := strings.TrimSuffix(strings.TrimSuffix(rawLine, "\n"), "\r")

		if strings.HasPrefix(line, "###") {
			if err := p.endSection(true); err != nil {
				return nil, err
			}
			p.title = strings.TrimSpace(strings.TrimLeft(line, "#"))
			continue
		}
		if err := p.line(lineNo, line, rawLine); err != nil {
			return nil, err
		}
	}
	if err := p.endSection(false); err != nil {
		return nil, err
	}
	if len(p.reqs) == 0 {
		return nil, p.fail(0, "missing request line (expected \"METHOD URL [HTTP/x.y]\")")
	}
	return p.reqs, nil
}

func (p *parser) fail(line int, format string, args ...any) error {
	return &ParseError{File: p.file, Line: line, Msg: fmt.Sprintf(format, args...)}
}

// line handles one non-separator line of the current section.
func (p *parser) line(lineNo int, line, rawLine string) error {
	if p.state == stateBody {
		p.body = append(p.body, rawLine)
		return nil
	}
	if text, ok := directiveText(line); ok {
		if p.state != stateRequestLine {
			return p.fail(lineNo, "directive %q must come before the request line", line)
		}
		return p.directive(lineNo, text)
	}
	if strings.HasPrefix(line, "#") {
		logging.Log.Trace().Msgf("Skipping comment: %s", line)
		return nil
	}
	switch p.state {
	case stateRequestLine:
		if strings.TrimSpace(line) == "" {
			return nil
		}
		req := &Request{
			File:       p.file,
			Line:       lineNo,
			Title:      p.title,
			Name:       p.meta.name,
			Timeout:    p.meta.timeout,
			Skip:       p.meta.skip,
			SkipReason: p.meta.skipReason,
			Headers:    make(http.Header),
		}
		if err := parseRequestLine(line, req); err != nil {
			return p.fail(lineNo, "%v", err)
		}
		p.req = req
		p.state = stateHeaders
	case stateHeaders:
		if strings.TrimSpace(line) == "" {
			p.state = stateBody
			return nil
		}
		if err := parseHeader(line, p.req.Headers); err != nil {
			return p.fail(lineNo, "%v", err)
		}
	}
	return nil
}

// endSection finishes the current section. A body cut off by a separator
// loses its trailing blank lines (the newline ending its last line is kept);
// a body that runs to EOF is kept verbatim.
func (p *parser) endSection(bySeparator bool) error {
	if p.req == nil && p.meta.seen != nil {
		return p.fail(p.meta.firstLine, "directives without a request")
	}
	if p.req != nil {
		body := p.body
		if bySeparator {
			body = trimTrailingBlank(body)
		}
		p.req.Body = strings.Join(body, "")
		p.reqs = append(p.reqs, p.req)
	}
	p.state, p.title, p.meta, p.req, p.body = stateRequestLine, "", directives{}, nil, nil
	return nil
}

// directiveText reports whether line is a "# @directive" line and returns
// the text after the "@".
func directiveText(line string) (string, bool) {
	rest, ok := strings.CutPrefix(line, "#")
	if !ok {
		return "", false
	}
	return strings.CutPrefix(strings.TrimLeft(rest, " \t"), "@")
}

// nameRe matches a request name: usable later as an identifier in scripts.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// directive parses one "@key [value]" directive into the section metadata.
func (p *parser) directive(lineNo int, text string) error {
	key, value := strings.TrimSpace(text), ""
	if i := strings.IndexAny(key, " \t"); i >= 0 {
		key, value = key[:i], strings.TrimSpace(key[i:])
	}
	m := &p.meta
	if prev, dup := m.seen["@"+key]; dup {
		return p.fail(lineNo, "duplicate @%s (first at line %d)", key, prev)
	}
	switch key {
	case "name":
		if !nameRe.MatchString(value) {
			return p.fail(lineNo, "invalid @name %q (expected letters, digits, '_', '-', '.', not starting with a digit)", value)
		}
		if prev, dup := p.names[value]; dup {
			return p.fail(lineNo, "duplicate request name %q (first at line %d)", value, prev)
		}
		p.names[value] = lineNo
		m.name = value
	case "timeout":
		d, err := time.ParseDuration(value)
		if err != nil || d <= 0 {
			return p.fail(lineNo, "invalid @timeout %q (expected a positive duration such as 5s or 500ms)", value)
		}
		m.timeout = d
	case "skip":
		m.skip, m.skipReason = true, value
	case "":
		return p.fail(lineNo, "empty directive")
	default:
		return p.fail(lineNo, "unknown directive @%s (known: @name, @timeout, @skip)", key)
	}
	if m.seen == nil {
		m.seen, m.firstLine = map[string]int{}, lineNo
	}
	m.seen["@"+key] = lineNo
	return nil
}

func trimTrailingBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// parseHeader splits "Name: value" on the first colon only.
func parseHeader(line string, h http.Header) error {
	name, value, ok := strings.Cut(line, ":")
	if !ok {
		return fmt.Errorf("invalid header %q (expected \"Name: value\")", line)
	}
	if !tokenRe.MatchString(name) {
		return fmt.Errorf("invalid header name %q", name)
	}
	h.Add(name, strings.TrimSpace(value))
	return nil
}

var (
	// tokenRe matches an RFC 9110 token (method and header names).
	tokenRe = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	protoRe = regexp.MustCompile(`^HTTP/[0-9](\.[0-9])?$`)
)

// parseRequestLine parses "METHOD URL [HTTP/x.y]".
func parseRequestLine(line string, r *Request) error {
	parts := strings.Fields(line)
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("invalid request line %q (expected \"METHOD URL [HTTP/x.y]\")", line)
	}
	method, rawURL := parts[0], parts[1]
	if !tokenRe.MatchString(method) {
		return fmt.Errorf("invalid method %q", method)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %v", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL %q: scheme must be http or https", rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid URL %q: missing host", rawURL)
	}
	if len(parts) == 3 {
		if !protoRe.MatchString(parts[2]) {
			return fmt.Errorf("invalid HTTP version %q (expected e.g. HTTP/1.1)", parts[2])
		}
		r.Proto = parts[2]
	}
	r.Method, r.URL = method, rawURL
	return nil
}
