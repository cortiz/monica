package script

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// jpStep is one JSONPath segment: a selector, applied to the current
// nodes (deep: to them and all their descendants).
type jpStep struct {
	deep     bool
	wildcard bool
	names    []string
	indexes  []int
	slice    *jpSlice
}

// jpSlice is [start:end]; nil bounds are open.
type jpSlice struct{ start, end *int }

// parseJSONPath parses the supported subset: $, .name, ['name'], [n]
// (negative counts from the end), [*], .*, ..name, ..*, [a:b] and unions
// such as [0,2] or ['a','b']. Filter and script expressions are rejected.
func parseJSONPath(path string) ([]jpStep, error) {
	bad := func(msg string, args ...any) error {
		return fmt.Errorf("invalid path %q: %s", path, fmt.Sprintf(msg, args...))
	}
	if !strings.HasPrefix(path, "$") {
		return nil, bad("must start with $")
	}
	var steps []jpStep
	for i := 1; i < len(path); {
		var st jpStep
		switch {
		case strings.HasPrefix(path[i:], ".."):
			st.deep = true
			i += 2
		case path[i] == '.':
			i++
		case path[i] == '[':
		default:
			return nil, bad("unexpected %q at offset %d", path[i], i)
		}
		if i < len(path) && path[i] == '[' {
			end, err := bracketEnd(path, i)
			if err != nil {
				return nil, bad("%v", err)
			}
			if err := st.parseBracket(path[i+1 : end]); err != nil {
				return nil, bad("%v", err)
			}
			i = end + 1
		} else {
			j := i
			for j < len(path) && path[j] != '.' && path[j] != '[' {
				j++
			}
			name := path[i:j]
			switch name {
			case "":
				return nil, bad("missing name at offset %d", i)
			case "*":
				st.wildcard = true
			default:
				st.names = []string{name}
			}
			i = j
		}
		steps = append(steps, st)
	}
	return steps, nil
}

// bracketEnd returns the index of the "]" closing the "[" at start,
// skipping quoted strings.
func bracketEnd(path string, start int) (int, error) {
	var quote byte
	for i := start + 1; i < len(path); i++ {
		c := path[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ']':
			return i, nil
		}
	}
	return 0, fmt.Errorf("unclosed [ at offset %d", start)
}

// parseBracket parses the inside of [...].
func (st *jpStep) parseBracket(in string) error {
	in = strings.TrimSpace(in)
	switch {
	case in == "*":
		st.wildcard = true
		return nil
	case in == "":
		return fmt.Errorf("empty []")
	case in[0] == '?' || in[0] == '(':
		return fmt.Errorf("filter and script expressions are not supported")
	}
	parts, err := splitUnion(in)
	if err != nil {
		return err
	}
	if len(parts) == 1 && !isQuoted(parts[0]) && strings.Contains(parts[0], ":") {
		return st.parseSlice(parts[0])
	}
	for _, p := range parts {
		if isQuoted(p) {
			name, err := unquote(p)
			if err != nil {
				return err
			}
			st.names = append(st.names, name)
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return fmt.Errorf("%q is not an index or a quoted name", p)
		}
		st.indexes = append(st.indexes, n)
	}
	if len(st.names) > 0 && len(st.indexes) > 0 {
		return fmt.Errorf("cannot mix names and indexes in [%s]", in)
	}
	return nil
}

func (st *jpStep) parseSlice(in string) error {
	bounds := strings.Split(in, ":")
	if len(bounds) != 2 {
		return fmt.Errorf("slice steps are not supported: [%s]", in)
	}
	st.slice = &jpSlice{}
	for i, b := range bounds {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		n, err := strconv.Atoi(b)
		if err != nil {
			return fmt.Errorf("invalid slice bound %q", b)
		}
		if i == 0 {
			st.slice.start = &n
		} else {
			st.slice.end = &n
		}
	}
	return nil
}

// splitUnion splits on commas outside quotes and trims each part.
func splitUnion(in string) ([]string, error) {
	var parts []string
	var quote byte
	last := 0
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case quote != 0 && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ',':
			parts = append(parts, strings.TrimSpace(in[last:i]))
			last = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated string in [%s]", in)
	}
	parts = append(parts, strings.TrimSpace(in[last:]))
	for _, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("empty entry in [%s]", in)
		}
	}
	return parts, nil
}

func isQuoted(s string) bool {
	return len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0]
}

// unquote strips the quotes and resolves \\, \' and \" escapes.
func unquote(s string) (string, error) {
	body := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			if i+1 == len(body) {
				return "", fmt.Errorf("dangling \\ in %s", s)
			}
			i++
		}
		b.WriteByte(body[i])
	}
	return b.String(), nil
}

// jsonPath evaluates path against doc and returns the matches in document
// order. Matches are the values themselves, not copies.
func jsonPath(rt *goja.Runtime, doc goja.Value, path string) ([]goja.Value, error) {
	steps, err := parseJSONPath(path)
	if err != nil {
		return nil, err
	}
	nodes := []goja.Value{doc}
	for _, st := range steps {
		if st.deep {
			var all []goja.Value
			for _, n := range nodes {
				all = descendants(rt, n, all)
			}
			nodes = all
		}
		var next []goja.Value
		for _, n := range nodes {
			next = st.apply(rt, n, next)
		}
		nodes = next
	}
	return nodes, nil
}

// children returns an object's own enumerable values or an array's
// elements; nil for anything else.
func children(rt *goja.Runtime, v goja.Value) []goja.Value {
	obj, ok := v.(*goja.Object)
	if !ok {
		return nil
	}
	if _, isFunc := goja.AssertFunction(obj); isFunc {
		return nil
	}
	if elems, ok := arrayElems(obj); ok {
		return elems
	}
	var out []goja.Value
	for _, k := range obj.Keys() {
		out = append(out, obj.Get(k))
	}
	return out
}

// arrayElems returns the elements of a JS array.
func arrayElems(obj *goja.Object) ([]goja.Value, bool) {
	if obj.ClassName() != "Array" {
		return nil, false
	}
	n := int(obj.Get("length").ToInteger())
	out := make([]goja.Value, n)
	for i := range out {
		out[i] = obj.Get(strconv.Itoa(i))
	}
	return out, true
}

// descendants appends v and everything below it, pre-order.
func descendants(rt *goja.Runtime, v goja.Value, out []goja.Value) []goja.Value {
	out = append(out, v)
	for _, c := range children(rt, v) {
		out = descendants(rt, c, out)
	}
	return out
}

func (st *jpStep) apply(rt *goja.Runtime, v goja.Value, out []goja.Value) []goja.Value {
	if st.wildcard {
		return append(out, children(rt, v)...)
	}
	obj, ok := v.(*goja.Object)
	if !ok {
		return out
	}
	elems, isArray := arrayElems(obj)
	switch {
	case len(st.names) > 0:
		if isArray {
			return out
		}
		for _, name := range st.names {
			if hasOwn(obj, name) {
				out = append(out, obj.Get(name))
			}
		}
	case !isArray:
	case st.slice != nil:
		start, end := 0, len(elems)
		if st.slice.start != nil {
			start = clampIndex(*st.slice.start, len(elems))
		}
		if st.slice.end != nil {
			end = clampIndex(*st.slice.end, len(elems))
		}
		for i := start; i < end; i++ {
			out = append(out, elems[i])
		}
	default:
		for _, i := range st.indexes {
			if i < 0 {
				i += len(elems)
			}
			if i >= 0 && i < len(elems) {
				out = append(out, elems[i])
			}
		}
	}
	return out
}

// clampIndex maps a slice bound (negative = from the end) into [0, n].
func clampIndex(i, n int) int {
	if i < 0 {
		i += n
	}
	return max(0, min(i, n))
}

func hasOwn(obj *goja.Object, name string) bool {
	for _, k := range obj.Keys() {
		if k == name {
			return true
		}
	}
	return false
}
