package request

import (
	"bufio"
	"fmt"
	"os"
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

func parse(file, raw string) (*Request, error) {
	logging.Log.Debug().Msg("Parsing request")
	request := Request{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			logging.Log.Trace().Msgf("Skipping comment: %s", line)
			continue
		}
		if request.Method == "" {
			if strings.TrimSpace(line) == "" {
				continue // blank lines before the request line
			}
			logging.Log.Trace().Msgf("Parsing request line: %s", line)
			if err := parseRequestLine(line, &request); err != nil {
				return nil, &ParseError{File: file, Line: lineNo, Msg: err.Error()}
			}
			continue
		}
		if request.Headers == nil {
			request.Headers = make(map[string]string)
			for line != "" {
				logging.Log.Trace().Msgf("Parsing request headers: %s", line)
				parseRequestHeader(line, &request)
				if !scanner.Scan() {
					break // EOF
				}
				lineNo++
				line = scanner.Text()
			}
			continue
		}
		request.Body = strings.Join([]string{request.Body, line}, "\n")
	}
	if err := scanner.Err(); err != nil {
		return nil, &ParseError{File: file, Line: lineNo, Msg: err.Error()}
	}
	if request.Method == "" {
		return nil, &ParseError{File: file, Msg: "missing request line (expected \"METHOD URL\")"}
	}
	return &request, nil
}

func parseRequestHeader(line string, r *Request) {
	parts := strings.Split(line, ": ")
	if len(parts) == 2 {
		r.Headers[parts[0]] = parts[1]
	}
}

func parseRequestLine(requestLine string, r *Request) error {
	parts := strings.Fields(requestLine)
	if len(parts) < 2 {
		return fmt.Errorf("invalid request line %q (expected \"METHOD URL\")", requestLine)
	}
	r.Method = parts[0]
	r.URL = parts[1]
	return nil
}
