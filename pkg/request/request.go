package request

import "net/http"

type Request struct {
	Method string
	URL    string
	// Proto is the HTTP version from the request line ("HTTP/1.1"), or empty
	// when the line has no version.
	Proto string
	// Headers keeps every value of repeated headers; keys are canonicalized.
	Headers http.Header
	// Body is everything after the blank line that ends the headers, verbatim.
	Body string
}

func (r Request) String() string {
	return r.Method + " " + r.URL
}
