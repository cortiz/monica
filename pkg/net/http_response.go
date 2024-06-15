package net

import "bytes"

type HTTPResponse struct {
	Status     string
	StatusCode int
	Headers    map[string]string
	Body       string
}

func (r *HTTPResponse) String() string {
	s := "\n"
	buff := bytes.NewBufferString(s)
	buff.WriteString(r.Status)
	buff.WriteString("\n")
	for k, v := range r.Headers {
		buff.WriteString(k)
		buff.WriteString(": ")
		buff.WriteString(v)
		buff.WriteString("\n")
	}
	buff.WriteString(r.Body)
	buff.WriteString("\n")
	return buff.String()
}
