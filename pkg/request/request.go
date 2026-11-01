package request

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Request struct {
	// File is the .mon file the request came from (empty for raw input) and
	// Line the 1-based line of its request line.
	File string
	Line int
	// Title is the text after the "###" separator that opens the request's
	// section, if any.
	Title string
	// Name comes from "# @name"; unique within a file, used for chaining.
	Name string
	// Timeout comes from "# @timeout"; zero means the client default.
	Timeout time.Duration
	// Skip is set by "# @skip [reason]": the request is parsed but not sent.
	Skip       bool
	SkipReason string
	// Retries and RetryDelay come from "# @retry <count> [delay]": a failed
	// request is sent again up to Retries more times, RetryDelay apart.
	Retries    int
	RetryDelay time.Duration
	Method     string
	URL        string
	// Proto is the HTTP version from the request line ("HTTP/1.1"), or empty
	// when the line has no version.
	Proto string
	// Headers keeps every value of repeated headers; keys are canonicalized.
	Headers http.Header
	// Body is everything after the blank line that ends the headers, or the
	// contents of BodyFile when the body is a "< path" line.
	Body string
	// BodyFile is the resolved path of a "< path" body, or empty.
	BodyFile string
	// Parts is set for a multipart body (a literal multipart/* Content-Type
	// with a boundary): Body split into text, expanded by Render, and file
	// contents, sent as is. Body holds the parts joined, unexpanded.
	Parts []BodyPart
	// PreScript runs before the request is sent ("< {% ... %}" before the
	// request line); PostScript runs on the response ("> {% ... %}" after the
	// headers or body). Either may be nil.
	PreScript  *Script
	PostScript *Script
}

func (r Request) String() string {
	return r.Method + " " + r.URL
}

// Label is a short human-readable name for the request: its @name, else its
// title, else "METHOD URL".
func (r Request) Label() string {
	if r.Name != "" {
		return r.Name
	}
	if r.Title != "" {
		return r.Title
	}
	return r.String()
}

// Render returns a copy of r with expand applied to the URL, every header
// value and the body (a body read from a file is sent as is), then checks the
// expanded URL. Errors are *ParseError at the request line.
func (r *Request) Render(expand func(string) (string, error)) (*Request, error) {
	fail := func(where string, err error) error {
		return &ParseError{File: r.File, Line: r.Line, Msg: fmt.Sprintf("%s: %v", where, err), Err: err}
	}
	out := *r
	var err error
	if out.URL, err = expand(r.URL); err != nil {
		return nil, fail("url", err)
	}
	if err := validateURL(out.URL); err != nil {
		return nil, fail("url", err)
	}
	out.Headers = make(http.Header, len(r.Headers))
	for name, values := range r.Headers {
		for _, v := range values {
			ev, err := expand(v)
			if err != nil {
				return nil, fail("header "+name, err)
			}
			out.Headers[name] = append(out.Headers[name], ev)
		}
	}
	switch {
	case len(r.Parts) > 0:
		var b strings.Builder
		for _, part := range r.Parts {
			text := part.Text
			if part.File == "" {
				if text, err = expand(text); err != nil {
					return nil, fail("body", err)
				}
			}
			b.WriteString(text)
		}
		out.Body = b.String()
	case r.BodyFile == "":
		if out.Body, err = expand(r.Body); err != nil {
			return nil, fail("body", err)
		}
	}
	return &out, nil
}
