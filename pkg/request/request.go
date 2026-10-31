package request

import "net/http"

type Request struct {
	// File is the .mon file the request came from (empty for raw input) and
	// Line the 1-based line of its request line.
	File string
	Line int
	// Title is the text after the "###" separator that opens the request's
	// section, if any.
	Title  string
	Method string
	URL    string
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

// Label is a short human-readable name for the request: its title when it
// has one, otherwise "METHOD URL".
func (r Request) Label() string {
	if r.Title != "" {
		return r.Title
	}
	return r.String()
}
