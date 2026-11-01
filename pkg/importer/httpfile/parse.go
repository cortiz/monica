// Package httpfile converts JetBrains HTTP Client and VS Code REST Client
// files (.http, .rest) into a monica project.
package httpfile

import (
	"regexp"
	"strconv"
	"strings"

	"jmpeax.com/sec/monica/pkg/importer"
)

// File is a parsed .http file.
type File struct {
	// Vars are the file variables ("@name = value"), in source order.
	Vars []Var
	// Requests are the requests, in source order.
	Requests []*Request
}

// Var is a file variable.
type Var struct {
	Name, Value string
	Line        int
}

// Directive is a "# @key value" comment.
type Directive struct {
	Key, Value string
	Line       int
}

// Script is a "< {% %}" / "> {% %}" block (Code) or a "< path" / "> path"
// reference (File).
type Script struct {
	Code, File string
	Line       int
}

// Request is one request of a .http file, as written.
type Request struct {
	Line       int // of the request line
	Title      string
	Comments   []string
	Directives []Directive
	Pre        []Script
	Method     string // as written ("" when the line is only a URL)
	URL        string // with continuation lines joined
	Version    string // "HTTP/1.1", ...
	Headers    []importer.Header
	Body       string // trailing blank lines dropped
	BodyLine   int
	Post       []Script
	// Redirects are ">> path" / ">>! path" output redirections.
	Redirects []string
	// Problems are lines that could not be read, as "line N: ..." text.
	Problems []string
}

var (
	fileVarRe   = regexp.MustCompile(`^@([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s?(.*)$`)
	directiveRe = regexp.MustCompile(`^@([A-Za-z][A-Za-z0-9_-]*)(?:\s*=\s*|\s+|$)(.*)$`)
	headerRe    = regexp.MustCompile(`^([!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+)\s*:\s?(.*)$`)
)

// Parse reads .http text. It does not fail: lines it cannot read are listed
// in the requests' Problems.
func Parse(text string) *File {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	f := &File{}
	p := &parser{f: f, lines: lines}
	p.run()
	return f
}

type parser struct {
	f     *File
	lines []string
	i     int
}

// comment returns the text of a "#" or "//" comment line.
func comment(line string) (string, bool) {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "###"):
		return "", false
	case strings.HasPrefix(t, "#"):
		return strings.TrimSpace(t[1:]), true
	case strings.HasPrefix(t, "//"):
		return strings.TrimSpace(t[2:]), true
	}
	return "", false
}

func isSeparator(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), "###") }

func (p *parser) run() {
	title := ""
	for p.i < len(p.lines) {
		line := p.lines[p.i]
		if isSeparator(line) {
			title = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			p.i++
		}
		p.section(title)
		title = ""
	}
}

// section reads up to the next "###" line.
func (p *parser) section(title string) {
	r := &Request{Title: title}
	// Before the request line: comments, directives, file variables, pre-request scripts.
	for ; p.i < len(p.lines); p.i++ {
		line := p.lines[p.i]
		t := strings.TrimSpace(line)
		if isSeparator(line) {
			return
		}
		if t == "" {
			continue
		}
		if c, ok := comment(line); ok {
			if m := directiveRe.FindStringSubmatch(c); m != nil {
				r.Directives = append(r.Directives, Directive{Key: m[1], Value: strings.TrimSpace(m[2]), Line: p.i + 1})
			} else {
				r.Comments = append(r.Comments, c)
			}
			continue
		}
		if m := fileVarRe.FindStringSubmatch(t); m != nil {
			p.f.Vars = append(p.f.Vars, Var{Name: m[1], Value: strings.TrimSpace(m[2]), Line: p.i + 1})
			continue
		}
		if strings.HasPrefix(t, "<") {
			if s, ok := p.script(r, t[1:]); ok {
				r.Pre = append(r.Pre, s)
				continue
			}
		}
		break
	}
	if p.i >= len(p.lines) || isSeparator(p.lines[p.i]) {
		return // no request in this section
	}
	r.Line = p.i + 1
	r.Method, r.URL, r.Version = requestLine(strings.TrimSpace(p.lines[p.i]))
	p.i++
	// URL continuation lines: indented, starting with ?, & or /.
	for ; p.i < len(p.lines); p.i++ {
		line := p.lines[p.i]
		t := strings.TrimSpace(line)
		if t == "" || line == t || !strings.ContainsAny(t[:1], "?&/") {
			break
		}
		r.URL += t
	}
	p.f.Requests = append(p.f.Requests, r)
	// Headers.
	for ; p.i < len(p.lines); p.i++ {
		line := p.lines[p.i]
		t := strings.TrimSpace(line)
		if isSeparator(line) {
			return
		}
		if t == "" {
			p.i++
			break
		}
		if p.post(r, t) {
			p.after(r)
			return
		}
		if _, ok := comment(line); ok {
			continue
		}
		if m := headerRe.FindStringSubmatch(t); m != nil {
			r.Headers = append(r.Headers, importer.Header{Name: m[1], Value: strings.TrimSpace(m[2])})
			continue
		}
		r.Problems = append(r.Problems, lineProblem(p.i+1, "not a header", t))
	}
	// Body.
	var body []string
	for ; p.i < len(p.lines); p.i++ {
		line := p.lines[p.i]
		if isSeparator(line) {
			break
		}
		if p.post(r, strings.TrimSpace(line)) {
			p.after(r)
			break
		}
		if len(body) == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		if len(body) == 0 {
			r.BodyLine = p.i + 1
		}
		body = append(body, line)
	}
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	r.Body = strings.Join(body, "\n")
}

func lineProblem(n int, what, text string) string {
	if len(text) > 60 {
		text = text[:60] + "…"
	}
	return "line " + strconv.Itoa(n) + ": " + what + ": " + text
}

// post reads a response handler ("> {% %}", "> path") or an output
// redirection (">> path", ">>! path") at the current line; it reports
// whether the line was one.
func (p *parser) post(r *Request, t string) bool {
	switch {
	case strings.HasPrefix(t, ">>"):
		r.Redirects = append(r.Redirects, t)
		return true
	case strings.HasPrefix(t, ">"):
		rest := strings.TrimSpace(t[1:])
		if !strings.HasPrefix(rest, "{%") && (rest == "" || strings.ContainsAny(rest, " \t") || !looksLikePath(rest)) {
			return false
		}
		if s, ok := p.script(r, t[1:]); ok {
			r.Post = append(r.Post, s)
			return true
		}
	}
	return false
}

// looksLikePath tells a "> ./handler.js" line from body text starting with ">".
func looksLikePath(s string) bool {
	return strings.HasSuffix(s, ".js") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "{{")
}

// after reads the lines after a response handler, up to the next "###":
// more handlers, redirections, comments and blank lines.
func (p *parser) after(r *Request) {
	for p.i++; p.i < len(p.lines); p.i++ {
		line := p.lines[p.i]
		t := strings.TrimSpace(line)
		if isSeparator(line) {
			return
		}
		if t == "" {
			continue
		}
		if _, ok := comment(line); ok {
			continue
		}
		if p.post(r, t) {
			continue
		}
		r.Problems = append(r.Problems, lineProblem(p.i+1, "text after the response handler", t))
	}
}

// script reads "{% ... %}" (possibly over several lines) or a path, from the
// text after "<" / ">" on the current line; p.i ends on the block's last line.
// A block without "%}" runs to the end of the file (swallowing any later
// requests), which is reported on r.
func (p *parser) script(r *Request, rest string) (Script, bool) {
	rest = strings.TrimSpace(rest)
	line := p.i + 1
	if !strings.HasPrefix(rest, "{%") {
		if rest == "" || strings.HasPrefix(rest, "@") {
			return Script{}, false
		}
		return Script{File: rest, Line: line}, true
	}
	rest = rest[2:]
	if end := strings.Index(rest, "%}"); end >= 0 {
		return Script{Code: strings.TrimSpace(rest[:end]), Line: line}, true
	}
	var code []string
	if strings.TrimSpace(rest) != "" {
		code = append(code, rest)
	}
	closed := false
	for p.i++; p.i < len(p.lines); p.i++ {
		l := p.lines[p.i]
		if end := strings.Index(l, "%}"); end >= 0 {
			if strings.TrimSpace(l[:end]) != "" {
				code = append(code, l[:end])
			}
			closed = true
			break
		}
		code = append(code, l)
	}
	if !closed {
		r.Problems = append(r.Problems, lineProblem(line, "{% block has no closing %}; it takes the rest of the file", strings.TrimSpace(p.lines[line-1])))
	}
	return Script{Code: dedent(code), Line: line}, true
}

// dedent removes the indentation common to the non-blank lines.
func dedent(lines []string) string {
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if len(l) >= common && common > 0 {
			l = l[common:]
		}
		out[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(out, "\n"), "\n")
}

var versionRe = regexp.MustCompile(`^HTTP/\d(\.\d)?$`)

// requestLine splits "METHOD URL [HTTP/x]"; the method is "" when the line
// starts with the URL.
func requestLine(line string) (method, url, version string) {
	fields := splitFields(line)
	if n := len(fields); n > 1 && versionRe.MatchString(fields[n-1]) {
		version = fields[n-1]
		fields = fields[:n-1]
	}
	if len(fields) == 0 {
		return "", "", version
	}
	first := fields[0]
	if len(fields) == 1 || strings.Contains(first, "://") || strings.HasPrefix(first, "/") || strings.HasPrefix(first, "{{") {
		return "", strings.Join(fields, " "), version
	}
	return first, strings.Join(fields[1:], " "), version
}

// splitFields splits on spaces and tabs outside {{ }}.
func splitFields(line string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(line); i++ {
		switch {
		case strings.HasPrefix(line[i:], "{{"):
			depth++
			cur.WriteString("{{")
			i++
		case strings.HasPrefix(line[i:], "}}") && depth > 0:
			depth--
			cur.WriteString("}}")
			i++
		case (line[i] == ' ' || line[i] == '\t') && depth == 0:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(line[i])
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
