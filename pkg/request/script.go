package request

import "strings"

// Script is JavaScript attached to a request. Scripts are only parsed for now;
// they run once the script engine lands (roadmap phase 3).
type Script struct {
	// Source is the code, verbatim, with "\n" line endings.
	Source string
	// File and Line locate the first line of Source, so a JS error on line n
	// maps to File:Line+n-1. For an inline block File is the .mon file and
	// Line the line holding "{%".
	File string
	Line int
}

type scriptKind int

const (
	preScript  scriptKind = iota // "< ..." before the request line
	postScript                   // "> ..." after the headers or body
)

func (k scriptKind) String() string {
	if k == preScript {
		return "pre-request"
	}
	return "post-response"
}

// openScript is an inline "{% ... %}" block still being read.
type openScript struct {
	kind scriptKind
	line int // line holding "{%"
	src  strings.Builder
}

// scriptMarker reports whether line starts with marker ('<' or '>') and
// returns the text after it, leading whitespace removed.
func scriptMarker(line string, marker byte) (string, bool) {
	if line == "" || line[0] != marker {
		return "", false
	}
	return strings.TrimLeft(line[1:], " \t"), true
}

// postScriptLine reports whether a line after the request line opens a
// post-response script. Other lines starting with '>' are ordinary body text.
func postScriptLine(line string) (string, bool) {
	rest, ok := scriptMarker(line, '>')
	if !ok || !strings.HasPrefix(rest, "{%") {
		return "", false
	}
	return rest, true
}

// startScript handles a line opening a script; rest is the text after the
// marker.
func (p *parser) startScript(kind scriptKind, lineNo int, rest string) error {
	if p.hasScript(kind) {
		return p.fail(lineNo, "duplicate %s script", kind)
	}
	after, ok := strings.CutPrefix(rest, "{%")
	if !ok {
		return p.fail(lineNo, "invalid %s script %q (expected \"{%% ... %%}\")", kind, rest)
	}
	p.script = &openScript{kind: kind, line: lineNo}
	return p.scriptText(lineNo, after)
}

// scriptText adds one line of the open block, closing it at "%}".
func (p *parser) scriptText(lineNo int, text string) error {
	s := p.script
	end := strings.Index(text, "%}")
	if end < 0 {
		s.src.WriteString(text)
		s.src.WriteString("\n")
		return nil
	}
	if strings.TrimSpace(text[end+2:]) != "" {
		return p.fail(lineNo, "unexpected text after \"%%}\"")
	}
	s.src.WriteString(text[:end])
	p.script = nil
	p.setScript(s.kind, s.line, &Script{Source: s.src.String(), File: p.file, Line: s.line})
	return nil
}

func (p *parser) hasScript(kind scriptKind) bool {
	if kind == preScript {
		return p.pre != nil
	}
	return p.req.PostScript != nil
}

// setScript stores a finished script; line is where it was declared.
func (p *parser) setScript(kind scriptKind, line int, s *Script) {
	if kind == preScript {
		p.pre, p.preLine = s, line
		return
	}
	p.req.PostScript = s
}
