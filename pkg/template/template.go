// Package template expands {{name}} placeholders in request text.
package template

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Lookup returns the value of a user variable.
type Lookup func(name string) (string, bool)

// UnknownVarError reports a placeholder with no value.
type UnknownVarError struct {
	Name string
}

func (e *UnknownVarError) Error() string {
	return fmt.Sprintf("unknown variable %q", e.Name)
}

// Builtins are the "{{$name}}" placeholders; each call yields a fresh value.
var Builtins = map[string]func() (string, error){
	"$uuid":      uuidV4,
	"$timestamp": func() (string, error) { return strconv.FormatInt(time.Now().Unix(), 10), nil },
	"$randomInt": func() (string, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(1000))
		if err != nil {
			return "", err
		}
		return n.String(), nil
	},
}

var nameRe = regexp.MustCompile(`^\$?[A-Za-z_][A-Za-z0-9_.-]*$`)

// Expand replaces every {{name}} (spaces inside the braces allowed) in s.
// Names starting with "$" are built-ins; others come from lookup. Unknown
// names and unclosed "{{" are errors. There is no escaping and no recursion:
// substituted values are not expanded again.
func Expand(s string, lookup Lookup) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "{{")
		if start < 0 {
			b.WriteString(s)
			return b.String(), nil
		}
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			return "", fmt.Errorf("unclosed \"{{\" in %q", abbreviate(s[start:]))
		}
		name := strings.TrimSpace(s[start+2 : start+2+end])
		value, err := resolve(name, lookup)
		if err != nil {
			return "", err
		}
		b.WriteString(s[:start])
		b.WriteString(value)
		s = s[start+2+end+2:]
	}
}

func resolve(name string, lookup Lookup) (string, error) {
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("invalid variable name %q", name)
	}
	if strings.HasPrefix(name, "$") {
		fn, ok := Builtins[name]
		if !ok {
			return "", fmt.Errorf("unknown built-in %q (known: $uuid, $timestamp, $randomInt)", name)
		}
		return fn()
	}
	if lookup != nil {
		if v, ok := lookup(name); ok {
			return v, nil
		}
	}
	return "", &UnknownVarError{Name: name}
}

func abbreviate(s string) string {
	const max = 30
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// uuidV4 returns a random RFC 9562 version 4 UUID.
func uuidV4() (string, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", err
	}
	u[6] = u[6]&0x0f | 0x40 // version 4
	u[8] = u[8]&0x3f | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:]), nil
}
