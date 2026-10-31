package request

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jmpeax.com/sec/monica/internal/logging"
)

// ParseError reports a syntax problem in a .mon source, with its location.
type ParseError struct {
	File string // empty when parsing a raw string
	Line int    // 1-based; 0 when the error is not tied to a line
	Msg  string
	Err  error // underlying cause, if any (e.g. a body file that can't be read)
}

func (e *ParseError) Unwrap() error { return e.Err }

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
	stateAfterPost // after the post-response script: only comments/blank lines
)

// parser holds the state of one parse. A source is a sequence of sections
// separated by "###" lines; each section holds at most one request.
type parser struct {
	file  string
	state parseState
	reqs  []*Request
	names map[string]int // request name -> line of its @name

	// Current section.
	title    string
	meta     directives // collected before the request line
	pre      *Script    // pre-request script, attached at the request line
	preLine  int        // line declaring pre
	req      *Request   // nil until the request line is seen
	body     []string   // raw body lines, terminators included
	bodyLine int        // line number of body[0]

	script *openScript // inline script block being read, if any
}

// directives holds the "# @..." lines of a section.
type directives struct {
	seen       map[string]int // directive -> line, to reject repeats
	firstLine  int
	name       string
	timeout    time.Duration
	skip       bool
	skipReason string
	retries    int
	retryDelay time.Duration
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

		if p.script != nil { // inside "{% ... %}" every line is code
			if err := p.scriptText(lineNo, line); err != nil {
				return nil, err
			}
			continue
		}
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
	if p.script != nil {
		return nil, p.fail(p.script.line, "unterminated %s script (missing \"%%}\")", p.script.kind)
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
		if rest, ok := postScriptLine(line); ok {
			p.state = stateAfterPost
			return p.startScript(postScript, lineNo, rest)
		}
		if len(p.body) == 0 {
			p.bodyLine = lineNo
		}
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
		if rest, ok := scriptMarker(line, '<'); ok {
			return p.startScript(preScript, lineNo, rest)
		}
		req := &Request{
			File:       p.file,
			Line:       lineNo,
			Title:      p.title,
			Name:       p.meta.name,
			Timeout:    p.meta.timeout,
			Skip:       p.meta.skip,
			SkipReason: p.meta.skipReason,
			Retries:    p.meta.retries,
			RetryDelay: p.meta.retryDelay,
			PreScript:  p.pre,
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
		if rest, ok := postScriptLine(line); ok {
			p.state = stateAfterPost
			return p.startScript(postScript, lineNo, rest)
		}
		if err := parseHeader(line, p.req.Headers); err != nil {
			return p.fail(lineNo, "%v", err)
		}
	case stateAfterPost:
		if strings.TrimSpace(line) == "" {
			return nil
		}
		if rest, ok := postScriptLine(line); ok {
			return p.startScript(postScript, lineNo, rest) // reports the duplicate
		}
		return p.fail(lineNo, "unexpected text after the post-response script (start a new request with ###)")
	}
	return nil
}

// endSection finishes the current section. A body cut off by a separator or
// a post-response script loses its trailing blank lines (the newline ending
// its last line is kept); a body that runs to EOF is kept verbatim.
func (p *parser) endSection(bySeparator bool) error {
	if p.req == nil && p.meta.seen != nil {
		return p.fail(p.meta.firstLine, "directives without a request")
	}
	if p.req == nil && p.pre != nil {
		return p.fail(p.preLine, "pre-request script without a request")
	}
	if p.req != nil {
		body := p.body
		if bySeparator || p.req.PostScript != nil {
			body = trimTrailingBlank(body)
		}
		p.req.Body = strings.Join(body, "")
		if path, ok := bodyFileRef(body); ok {
			if err := p.loadBodyFile(p.req, path); err != nil {
				return err
			}
		}
		p.reqs = append(p.reqs, p.req)
	}
	p.state, p.title, p.meta, p.pre, p.preLine = stateRequestLine, "", directives{}, nil, 0
	p.req, p.body, p.bodyLine = nil, nil, 0
	return nil
}

// bodyFileRef reports whether the body is a single "< path" line (trailing
// blank lines aside) and returns the path.
func bodyFileRef(body []string) (string, bool) {
	lines := trimTrailingBlank(body)
	if len(lines) != 1 {
		return "", false
	}
	rest, ok := strings.CutPrefix(strings.TrimRight(lines[0], "\r\n"), "<")
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return "", false
	}
	path := strings.TrimSpace(rest)
	return path, path != ""
}

// resolve makes a path from the .mon source relative to the .mon file's
// directory (or the working directory for raw input).
func (p *parser) resolve(path string) string {
	if !filepath.IsAbs(path) && p.file != "" {
		return filepath.Join(filepath.Dir(p.file), path)
	}
	return path
}

// loadBodyFile replaces req's body with the contents of path.
func (p *parser) loadBodyFile(req *Request, path string) error {
	path = p.resolve(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return &ParseError{File: p.file, Line: p.bodyLine, Msg: fmt.Sprintf("reading body file: %v", err), Err: err}
	}
	req.Body, req.BodyFile = string(data), path
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
	case "retry":
		n, delay, err := parseRetry(value)
		if err != nil {
			return p.fail(lineNo, "invalid @retry %q (%v)", value, err)
		}
		m.retries, m.retryDelay = n, delay
	case "":
		return p.fail(lineNo, "empty directive")
	default:
		return p.fail(lineNo, "unknown directive @%s (known: @name, @timeout, @skip, @retry)", key)
	}
	if m.seen == nil {
		m.seen, m.firstLine = map[string]int{}, lineNo
	}
	m.seen["@"+key] = lineNo
	return nil
}

// MaxRetries bounds @retry so a typo can't stall a run.
const MaxRetries = 100

// parseRetry parses "<count> [delay]": count retries (1..MaxRetries) with an
// optional non-negative Go duration between attempts.
func parseRetry(value string) (int, time.Duration, error) {
	fields := strings.Fields(value)
	if len(fields) == 0 || len(fields) > 2 {
		return 0, 0, errors.New("expected a retry count and an optional delay, e.g. \"3 1s\"")
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil || n < 1 || n > MaxRetries {
		return 0, 0, fmt.Errorf("count must be a whole number from 1 to %d", MaxRetries)
	}
	var delay time.Duration
	if len(fields) == 2 {
		if delay, err = time.ParseDuration(fields[1]); err != nil || delay < 0 {
			return 0, 0, errors.New("delay must be a duration such as 500ms or 1s")
		}
	}
	return n, delay, nil
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
	parts := splitRequestLine(line)
	if len(parts) < 2 || len(parts) > 3 {
		return fmt.Errorf("invalid request line %q (expected \"METHOD URL [HTTP/x.y]\")", line)
	}
	method, rawURL := parts[0], parts[1]
	if !tokenRe.MatchString(method) {
		return fmt.Errorf("invalid method %q", method)
	}
	// A templated URL is checked once its {{variables}} are expanded.
	if !strings.Contains(rawURL, "{{") {
		if err := validateURL(rawURL); err != nil {
			return err
		}
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

// splitRequestLine splits on spaces and tabs, except inside {{ ... }} so a
// placeholder like "{{ id }}" stays part of the URL.
func splitRequestLine(line string) []string {
	var parts []string
	var cur strings.Builder
	inside := false
	for i := 0; i < len(line); i++ {
		switch {
		case !inside && strings.HasPrefix(line[i:], "{{"):
			inside = true
			cur.WriteString("{{")
			i++
		case inside && strings.HasPrefix(line[i:], "}}"):
			inside = false
			cur.WriteString("}}")
			i++
		case !inside && (line[i] == ' ' || line[i] == '\t'):
			if cur.Len() > 0 {
				parts = append(parts, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(line[i])
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// validateURL requires an absolute http(s) URL with a host.
func validateURL(rawURL string) error {
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
	return nil
}
