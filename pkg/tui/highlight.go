package tui

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	jsonKey    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1d4ed8", Dark: "#7aa2f7"})
	jsonString = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#9ece6a"})
	jsonNumber = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#ff9e64"})
	jsonLit    = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#7e22ce", Dark: "#bb9af7"})
)

// prettyBody indents a JSON body and colors it when color is set; other
// bodies are returned as they are. ok reports whether it was JSON.
func prettyBody(body string, color bool) (out string, ok bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') || !json.Valid([]byte(trimmed)) {
		return body, false
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
		return body, false
	}
	if !color {
		return buf.String(), true
	}
	return highlightJSON(buf.String()), true
}

// highlightJSON colors the tokens of valid JSON text: keys, strings,
// numbers and true/false/null.
func highlightJSON(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(s) {
				b.WriteString(s[i:])
				return b.String()
			}
			tok := s[i : j+1]
			k := j + 1
			for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
				k++
			}
			if k < len(s) && s[k] == ':' {
				b.WriteString(jsonKey.Render(tok))
			} else {
				b.WriteString(jsonString.Render(tok))
			}
			i = j + 1
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(s) && strings.IndexByte("0123456789.eE+-", s[j]) >= 0 {
				j++
			}
			b.WriteString(jsonNumber.Render(s[i:j]))
			i = j
		case c == 't' || c == 'f' || c == 'n':
			j := i
			for j < len(s) && s[j] >= 'a' && s[j] <= 'z' {
				j++
			}
			b.WriteString(jsonLit.Render(s[i:j]))
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}
