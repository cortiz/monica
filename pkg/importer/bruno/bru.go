// Package bruno converts Bruno collections (a folder of .bru files) into a
// monica project (`monica import bruno`).
package bruno

import (
	"fmt"
	"regexp"
	"strings"
)

// File is a parsed .bru file: its blocks in source order.
type File struct {
	Blocks []*Block
}

// Block is one "name { ... }" (or "name [ ... ]") section.
type Block struct {
	Name string
	Line int // 1-based line of the block header
	// Text holds text blocks (bodies, scripts, tests, docs), with the
	// block's two-space indent removed.
	Text string
	// Pairs holds dictionary blocks (meta, headers, params, auth, vars,
	// assert, form bodies, ...).
	Pairs []Pair
	// List holds list blocks (vars:secret [ a, b ]).
	List []string
}

// Pair is a "key: value" line; "~key: value" is Disabled.
type Pair struct {
	Key, Value string
	Disabled   bool
}

// textBlocks hold free text rather than key: value lines.
var textBlocks = map[string]bool{
	"body:json": true, "body:text": true, "body:xml": true, "body:sparql": true,
	"body:graphql": true, "body:graphql:vars": true,
	"script:pre-request": true, "script:post-response": true, "tests": true, "docs": true,
}

var headerRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9:_-]*)\s*([{\[])\s*$`)

// Parse reads .bru text.
func Parse(src string) (*File, error) {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	f := &File{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := headerRe.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected a block such as \"meta {\", got %q", i+1, line)
		}
		b := &Block{Name: m[1], Line: i + 1}
		closing := "}"
		if m[2] == "[" {
			closing = "]"
		}
		var body []string
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimRight(lines[j], " \t") == closing {
				end = j
				break
			}
			body = append(body, lines[j])
		}
		if end < 0 {
			return nil, fmt.Errorf("line %d: block %s is not closed with %q", i+1, b.Name, closing)
		}
		var err error
		switch {
		case closing == "]":
			for _, l := range body {
				for _, item := range strings.Split(l, ",") {
					if item = strings.TrimSpace(item); item != "" {
						b.List = append(b.List, item)
					}
				}
			}
		case textBlocks[b.Name]:
			b.Text = dedent(body, 2)
		default:
			b.Pairs, err = pairs(body, i+2)
		}
		if err != nil {
			return nil, err
		}
		f.Blocks = append(f.Blocks, b)
		i = end
	}
	return f, nil
}

// pairs reads "key: value" lines; a value of ”' starts a multi-line value
// that ends at a ”' line.
func pairs(lines []string, firstLine int) ([]Pair, error) {
	var out []Pair
	for i := 0; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		p := Pair{}
		if strings.HasPrefix(l, "~") {
			p.Disabled, l = true, l[1:]
		}
		key, value, ok := strings.Cut(l, ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected \"key: value\", got %q", firstLine+i, l)
		}
		p.Key, p.Value = strings.TrimSpace(key), strings.TrimSpace(value)
		if len(p.Key) >= 2 && p.Key[0] == '"' && p.Key[len(p.Key)-1] == '"' {
			p.Key = p.Key[1 : len(p.Key)-1]
		}
		if p.Value == "'''" {
			var ml []string
			j := i + 1
			for ; j < len(lines) && strings.TrimSpace(lines[j]) != "'''"; j++ {
				ml = append(ml, lines[j])
			}
			if j == len(lines) {
				return nil, fmt.Errorf("line %d: multi-line value of %s is not closed with '''", firstLine+i, p.Key)
			}
			p.Value = dedent(ml, 4)
			i = j
		}
		out = append(out, p)
	}
	return out, nil
}

// dedent removes up to n leading spaces from each line and trailing blank
// lines.
func dedent(lines []string, n int) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		k := 0
		for k < n && k < len(l) && l[k] == ' ' {
			k++
		}
		out[i] = l[k:]
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n ")
}

// Block returns the first block named name, or nil.
func (f *File) Block(name string) *Block {
	if f == nil {
		return nil
	}
	for _, b := range f.Blocks {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// Get returns the value of the first enabled pair named key.
func (b *Block) Get(key string) string {
	if b == nil {
		return ""
	}
	for _, p := range b.Pairs {
		if p.Key == key && !p.Disabled {
			return p.Value
		}
	}
	return ""
}

// Enabled returns the block's enabled pairs (none for a nil block).
func (b *Block) Enabled() []Pair {
	if b == nil {
		return nil
	}
	var out []Pair
	for _, p := range b.Pairs {
		if !p.Disabled {
			out = append(out, p)
		}
	}
	return out
}
