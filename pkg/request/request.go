package request

import (
	"net/http"
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
	Method     string
	URL        string
	// Proto is the HTTP version from the request line ("HTTP/1.1"), or empty
	// when the line has no version.
	Proto string
	// Headers keeps every value of repeated headers; keys are canonicalized.
	Headers http.Header
	// Body is everything after the blank line that ends the headers.
	Body string
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
