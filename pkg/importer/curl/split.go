package curl

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// cmdStyle matches the marks of a Windows cmd command line: "^" line
// continuations or ^" quotes (Chrome's "Copy as cURL (cmd)").
var cmdStyle = regexp.MustCompile(`\^\r?\n|\^"`)

// Split breaks a shell command line into words the way a POSIX shell would
// for a typical "copy as cURL" command: single and double quotes, $'...'
// (ANSI-C) quotes, backslash escapes and backslash-newline continuations
// (also "^"-newline from Windows cmd). A command with cmd marks ("^" at a
// line end, or ^") is split the way cmd and curl.exe would (see splitCmd). Variables and command substitutions
// are not expanded; they are kept as text and reported in warnings. An
// unquoted |, ;, &, < or > ends the command; the rest is reported.
func Split(line string) (words, warnings []string, err error) {
	if cmdStyle.MatchString(line) {
		return splitCmd(line)
	}
	var (
		cur     strings.Builder
		inWord  bool
		r       = []rune(line)
		warnVar bool
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\':
			if i+1 < len(r) && (r[i+1] == '\n' || (r[i+1] == '\r' && i+2 < len(r) && r[i+2] == '\n')) {
				if r[i+1] == '\r' {
					i++
				}
				i++
				continue
			}
			if i+1 == len(r) {
				continue
			}
			i++
			cur.WriteRune(r[i])
			inWord = true
		case c == '^' && i+1 < len(r) && (r[i+1] == '\n' || r[i+1] == '\r'):
			for i+1 < len(r) && (r[i+1] == '\r' || r[i+1] == '\n') {
				i++
			}
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		case c == '\'':
			end := indexRune(r, i+1, '\'')
			if end < 0 {
				return nil, nil, errors.New("unterminated ' quote")
			}
			cur.WriteString(string(r[i+1 : end]))
			inWord = true
			i = end
		case c == '$' && i+1 < len(r) && r[i+1] == '\'':
			s, n, err := ansiC(r[i+2:])
			if err != nil {
				return nil, nil, err
			}
			cur.WriteString(s)
			inWord = true
			i += 1 + n
		case c == '"':
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				switch {
				case r[j] == '\\' && j+1 < len(r) && strings.ContainsRune("$`\"\\\n", r[j+1]):
					j++
					if r[j] != '\n' {
						cur.WriteRune(r[j])
					}
				case r[j] == '$' || r[j] == '`':
					warnVar = warnVar || isExpansion(r, j)
					cur.WriteRune(r[j])
				default:
					cur.WriteRune(r[j])
				}
			}
			if j == len(r) {
				return nil, nil, errors.New("unterminated \" quote")
			}
			inWord = true
			i = j
		case c == '#' && !inWord:
			end := indexRune(r, i, '\n')
			if end < 0 {
				end = len(r)
			}
			i = end - 1
		case strings.ContainsRune("|;&<>", c):
			flush()
			rest := strings.TrimSpace(string(r[i:]))
			warnings = append(warnings, fmt.Sprintf("ignored the rest of the command line: %q", rest))
			return words, appendVarWarning(warnings, warnVar), nil
		default:
			if c == '$' || c == '`' {
				warnVar = warnVar || isExpansion(r, i)
			}
			cur.WriteRune(c)
			inWord = true
		}
	}
	flush()
	return words, appendVarWarning(warnings, warnVar), nil
}

func appendVarWarning(warnings []string, warn bool) []string {
	if warn {
		warnings = append(warnings, "shell variables and command substitutions ($NAME, $(...), `...`) were not expanded; replace them with {{variables}}")
	}
	return warnings
}

// isExpansion reports whether r[i] ('$' or '`') starts a shell expansion.
func isExpansion(r []rune, i int) bool {
	if r[i] == '`' {
		return true
	}
	if i+1 >= len(r) {
		return false
	}
	n := r[i+1]
	return n == '{' || n == '(' || n == '_' || (n >= 'A' && n <= 'Z') || (n >= 'a' && n <= 'z')
}

func indexRune(r []rune, from int, c rune) int {
	for i := from; i < len(r); i++ {
		if r[i] == c {
			return i
		}
	}
	return -1
}

// ansiC decodes the body of a $'...' string (r starts after the opening
// quote) and returns it with the number of runes consumed, closing quote
// included.
func ansiC(r []rune) (string, int, error) {
	var b strings.Builder
	for i := 0; i < len(r); i++ {
		c := r[i]
		if c == '\'' {
			return b.String(), i + 1, nil
		}
		if c != '\\' || i+1 == len(r) {
			b.WriteRune(c)
			continue
		}
		i++
		switch e := r[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'e', 'E':
			b.WriteByte(0x1b)
		case 'f':
			b.WriteByte('\f')
		case 'v':
			b.WriteByte('\v')
		case '\\', '\'', '"', '?':
			b.WriteRune(e)
		case 'x', 'u', 'U':
			width := map[rune]int{'x': 2, 'u': 4, 'U': 8}[e]
			j := i + 1
			for j < len(r) && j-i-1 < width && isHex(r[j]) {
				j++
			}
			if j == i+1 {
				b.WriteRune('\\')
				b.WriteRune(e)
				continue
			}
			if e == 'x' {
				v, _ := strconv.ParseUint(string(r[i+1:j]), 16, 8) // at most 2 hex digits
				b.WriteByte(uint8(v))
			} else {
				v, _ := strconv.ParseUint(string(r[i+1:j]), 16, 32)
				if v <= unicode.MaxRune {
					b.WriteRune(rune(v))
				} else {
					b.WriteRune(utf8.RuneError) // like bash, which prints nothing useful either
				}
			}
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(r) && j-i < 3 && r[j] >= '0' && r[j] <= '7' {
				j++
			}
			v, _ := strconv.ParseUint(string(r[i:j]), 8, 16) // up to \777
			v &= 0xFF                                        // bash keeps the low byte
			if v <= math.MaxUint8 {
				b.WriteByte(uint8(v))
			}
			i = j - 1
		default:
			b.WriteRune('\\')
			b.WriteRune(e)
		}
	}
	return "", 0, errors.New("unterminated $' quote")
}

func isHex(c rune) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// splitCmd splits a Windows cmd command line in two passes, as cmd and
// then curl.exe do: cmd removes "^" escapes outside its own "..." quotes
// (and "^" + line break joins lines), then the C runtime splits arguments
// on blanks outside "..." with its backslash rules (2n backslashes before
// " give n and a quote; 2n+1 give n and a literal "; "" inside quotes is
// a literal "). An unescaped |, &, < or > outside quotes ends the command.
func splitCmd(line string) (words, warnings []string, err error) {
	var pass1 strings.Builder
	r := []rune(line)
	quoted := false
	warnVar := false
loop:
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '"':
			quoted = !quoted
			pass1.WriteRune(c)
		case quoted:
			pass1.WriteRune(c)
		case c == '^':
			if i+1 == len(r) {
				continue
			}
			i++
			switch r[i] {
			case '\r':
				if i+1 < len(r) && r[i+1] == '\n' {
					i++
				}
			case '\n':
			default:
				pass1.WriteRune(r[i])
			}
		case strings.ContainsRune("|&<>", c):
			warnings = append(warnings, fmt.Sprintf("ignored the rest of the command line: %q", strings.TrimSpace(string(r[i:]))))
			break loop
		case c == '%':
			if j := indexRune(r, i+1, '%'); j > i+1 && !strings.ContainsAny(string(r[i+1:j]), " \t\r\n") {
				warnVar = true
			}
			pass1.WriteRune(c)
		default:
			pass1.WriteRune(c)
		}
	}
	if quoted {
		return nil, nil, errors.New("unterminated \" quote")
	}
	if warnVar {
		warnings = append(warnings, "cmd variables (%NAME%) were not expanded; replace them with {{variables}}")
	}

	var cur strings.Builder
	inWord, inQuote := false, false
	a := []rune(pass1.String())
	for i := 0; i < len(a); i++ {
		c := a[i]
		switch {
		case c == '\\':
			n := 0
			for i < len(a) && a[i] == '\\' {
				n++
				i++
			}
			if i < len(a) && a[i] == '"' {
				cur.WriteString(strings.Repeat("\\", n/2))
				if n%2 == 1 {
					cur.WriteRune('"')
				} else {
					i-- // the quote is handled on the next turn
				}
			} else {
				cur.WriteString(strings.Repeat("\\", n))
				i--
			}
			inWord = true
		case c == '"':
			if inQuote && i+1 < len(a) && a[i+1] == '"' {
				cur.WriteRune('"')
				i++
			} else {
				inQuote = !inQuote
			}
			inWord = true
		case !inQuote && (c == ' ' || c == '\t' || c == '\r' || c == '\n'):
			if inWord {
				words = append(words, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, cur.String())
	}
	return words, warnings, nil
}
