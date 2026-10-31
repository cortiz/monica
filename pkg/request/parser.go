package request

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

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

// ParseRequest parses a raw HTTP request and returns a Request object.
func ParseRequest(raw string) (*Request, error) {
	return parse("", raw)
}

// ParseMonFile reads and parses a .mon file.
func ParseMonFile(file string) (*Request, error) {
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
)

// parse reads, in order: comments/blank lines, the request line, header lines
// (comments allowed) up to the first blank line, then the body verbatim.
func parse(file, raw string) (*Request, error) {
	logging.Log.Debug().Msg("Parsing request")
	req := &Request{}
	state := stateRequestLine
	fail := func(line int, format string, args ...any) error {
		return &ParseError{File: file, Line: line, Msg: fmt.Sprintf(format, args...)}
	}

	rest := raw
	for lineNo := 1; rest != ""; lineNo++ {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		line = strings.TrimSuffix(line, "\r")

		if strings.HasPrefix(line, "#") {
			logging.Log.Trace().Msgf("Skipping comment: %s", line)
			continue
		}
		switch state {
		case stateRequestLine:
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := parseRequestLine(line, req); err != nil {
				return nil, fail(lineNo, "%v", err)
			}
			req.Headers = make(http.Header)
			state = stateHeaders
		case stateHeaders:
			if strings.TrimSpace(line) == "" {
				req.Body = rest
				return req, nil
			}
			if err := parseHeader(line, req.Headers); err != nil {
				return nil, fail(lineNo, "%v", err)
			}
		}
	}
	if state == stateRequestLine {
		return nil, fail(0, "missing request line (expected \"METHOD URL [HTTP/x.y]\")")
	}
	return req, nil // headers ran to EOF: no body
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
