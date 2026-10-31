package net

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

type HTTPResponse struct {
	Status     string
	StatusCode int
	Headers    http.Header
	Body       string
	// Duration is the time from sending the request until the body was read
	// (or until headers arrived when the body is skipped).
	Duration time.Duration
	// Timings break Duration down by phase.
	Timings Timings
	// BytesSent is the request body's size; BytesReceived the response
	// body's (0 when the body was skipped).
	BytesSent, BytesReceived int64
}

// String renders the status line, headers sorted by name (one line per
// value) and the body.
func (r *HTTPResponse) String() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(r.Status)
	b.WriteString("\n")
	names := make([]string, 0, len(r.Headers))
	for k := range r.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, v := range r.Headers[k] {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\n")
		}
	}
	b.WriteString(r.Body)
	b.WriteString("\n")
	return b.String()
}
