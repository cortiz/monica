package request

import (
	"fmt"
	"mime"
	"os"
	"strings"
)

// BodyPart is a piece of a multipart body: text, sent after {{variable}}
// expansion, or the contents of a file (File set), sent as is.
type BodyPart struct {
	Text string
	File string
}

// multipartBoundary returns the boundary of a literal multipart/*
// Content-Type header, or "" when the request has none.
func multipartBoundary(req *Request) (string, bool, error) {
	ct := req.Headers.Get("Content-Type")
	if ct == "" || strings.Contains(ct, "{{") {
		return "", false, nil
	}
	mt, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mt, "multipart/") {
		return "", false, nil //nolint:nilerr // not a multipart type we can read: the body is sent as written
	}
	b := params["boundary"]
	if b == "" {
		return "", true, fmt.Errorf("%s body needs a boundary parameter in Content-Type", mt)
	}
	return b, true, nil
}

// parseMultipart turns a multipart body written in the .mon file (any line
// endings) into parts with CRLF line endings: "--boundary" lines open
// parts, "--boundary--" closes the body, and a part whose content is a
// single "< path" line gets that file's contents. It returns the body
// with files inlined (unexpanded) and its parts. Errors carry the 0-based
// body line they refer to.
func (p *parser) parseMultipart(boundary string, body []string) (string, []BodyPart, int, error) {
	lines := make([]string, len(body))
	for i, l := range body {
		lines[i] = strings.TrimRight(l, "\r\n")
	}
	open, closing := "--"+boundary, "--"+boundary+"--"
	isDelim := func(l string) string {
		switch strings.TrimRight(l, " \t") {
		case open:
			return open
		case closing:
			return closing
		}
		return ""
	}
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i == len(lines) {
		return "", nil, 0, nil
	}
	if isDelim(lines[i]) != open {
		return "", nil, i, fmt.Errorf("multipart body must start with a %q line", open)
	}
	var (
		parts []BodyPart
		text  strings.Builder
	)
	flushText := func() {
		if text.Len() > 0 {
			parts = append(parts, BodyPart{Text: text.String()})
			text.Reset()
		}
	}
	closed := false
	for i < len(lines) && !closed {
		// lines[i] is an opening delimiter.
		text.WriteString(open + "\r\n")
		start := i
		i++
		j := i
		for j < len(lines) && isDelim(lines[j]) == "" {
			j++
		}
		if j == len(lines) {
			return "", nil, start, fmt.Errorf("multipart body has no closing %q line", closing)
		}
		part := lines[i:j]
		h := 0
		for h < len(part) && part[h] != "" {
			h++
		}
		if h == len(part) {
			return "", nil, start, fmt.Errorf("multipart part needs a blank line after its headers")
		}
		for _, hl := range part[:h] {
			text.WriteString(hl + "\r\n")
		}
		text.WriteString("\r\n")
		content := part[h+1:]
		if path, ok := bodyFileRef(withNewlines(content)); ok {
			resolved := p.resolve(path)
			data, err := os.ReadFile(resolved)
			if err != nil {
				return "", nil, i + h + 1, fmt.Errorf("reading multipart file: %w", err)
			}
			flushText()
			parts = append(parts, BodyPart{Text: string(data), File: resolved})
		} else {
			text.WriteString(strings.Join(content, "\r\n"))
		}
		text.WriteString("\r\n")
		i = j
		if isDelim(lines[j]) == closing {
			text.WriteString(closing + "\r\n")
			closed = true
			i = j + 1
		}
	}
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return "", nil, i, fmt.Errorf("unexpected text after the closing %q line", closing)
		}
	}
	flushText()
	var full strings.Builder
	for _, part := range parts {
		full.WriteString(part.Text)
	}
	return full.String(), parts, 0, nil
}

func withNewlines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l + "\n"
	}
	return out
}
