package script

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

const jpDoc = `{
  "store": {
    "book": [
      {"title": "A", "price": 8, "tags": ["x"]},
      {"title": "B", "price": 12},
      {"title": "C", "price": 9, "isbn": "123"}
    ],
    "bicycle": {"color": "red", "price": 19},
    "odd key": {"it's": 1}
  },
  "price": 1
}`

func TestJSONPath(t *testing.T) {
	tests := []struct {
		path string
		want string // JSON of the match list
	}{
		{"$", ""}, // whole document; checked separately below
		{"$.store.bicycle.color", `["red"]`},
		{"$['store']['bicycle']['color']", `["red"]`},
		{`$["store"].bicycle["color"]`, `["red"]`},
		{"$.store.book[0].title", `["A"]`},
		{"$.store.book[-1].title", `["C"]`},
		{"$.store.book[5]", `[]`},
		{"$.store.book[*].title", `["A","B","C"]`},
		{"$.store.book.*.price", `[8,12,9]`},
		{"$.store.book[0,2].title", `["A","C"]`},
		{"$.store.book[1:].title", `["B","C"]`},
		{"$.store.book[:2].title", `["A","B"]`},
		{"$.store.book[-2:].title", `["B","C"]`},
		{"$.store.book[0:100].price", `[8,12,9]`},
		{"$.store.bicycle['color','price']", `["red",19]`},
		{"$..price", `[1,8,12,9,19]`},
		{"$.store..title", `["A","B","C"]`},
		{"$..book[1].title", `["B"]`},
		{"$..isbn", `["123"]`},
		{"$.store['odd key']['it\\'s']", `[1]`},
		{"$.store.*.color", `["red"]`},
		{"$.missing.deeper", `[]`},
		{"$.store.book.title", `[]`}, // names don't apply to arrays
		{"$.store.bicycle[0]", `[]`}, // indexes don't apply to objects
		{"$.price.toString", `[]`},   // only own properties
		{"$.store.book[0].tags[0]", `["x"]`},
	}
	rt := goja.New()
	parse, _ := goja.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
	v, err := parse(goja.Undefined(), rt.ToValue(jpDoc))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := jsonPath(rt, v, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(got) != 1 || got[0] != v {
					t.Fatalf("$ should match the document itself, got %v", got)
				}
				return
			}
			list := make([]any, len(got))
			for i, g := range got {
				list[i] = g.Export()
			}
			b, _ := json.Marshal(list)
			if string(b) != tt.want {
				t.Fatalf("got %s, want %s", b, tt.want)
			}
		})
	}
}

func TestJSONPathKeyOrder(t *testing.T) {
	// Wildcards follow the document's key order, not sorted order.
	rt := goja.New()
	v, err := rt.RunString(`({z: 1, a: 2, m: 3})`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonPath(rt, v, "$.*")
	if err != nil {
		t.Fatal(err)
	}
	var vals []int64
	for _, g := range got {
		vals = append(vals, g.ToInteger())
	}
	if len(vals) != 3 || vals[0] != 1 || vals[1] != 2 || vals[2] != 3 {
		t.Fatalf("got %v, want [1 2 3]", vals)
	}
}

func TestJSONPathInvalid(t *testing.T) {
	tests := []struct{ path, want string }{
		{"store.book", "must start with $"},
		{"$.", "missing name"},
		{"$..", "missing name"},
		{"$.a[", "unclosed ["},
		{"$.a[]", "empty []"},
		{"$.a[?(@.price < 10)]", "filter and script expressions are not supported"},
		{"$.a[(@.length-1)]", "filter and script expressions are not supported"},
		{"$.a[x]", `"x" is not an index or a quoted name`},
		{"$.a[0:2:1]", "slice steps are not supported"},
		{"$.a[0,'b']", "cannot mix names and indexes"},
		{"$.a['b]", "unclosed ["},
		{"$.a[1,]", "empty entry"},
		{"$x", `unexpected 'x'`},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, err := parseJSONPath(tt.path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestJSONPathLimits(t *testing.T) {
	tests := []struct {
		name, setup, path, want string // want: count of matches, or error text
	}{
		{"cycle in recursive descent", "const o = {a: {x: 1}}; o.self = o; o.a.up = o;", "$..x", "1"},
		{"cycle under a wildcard", "const o = {k: [1]}; o.k.push(o);", "$..*", "3"},
		{"shared objects are visited each time", "const s = {x: 1}; const o = {a: s, b: s};", "$..x", "2"},
		{"huge sparse array", "const o = {a: new Array(4e9)};", "$..x", "visits more than 1000000 values"},
		{"index into a huge array", "const o = {a: new Array(4e9)}; o.a[3] = 'v';", "$.a[3]", "1"},
		{"slice of a huge array", "const o = {a: new Array(4e9)};", "$.a[:]", "visits more than 1000000 values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := goja.New()
			v, err := rt.RunString(tt.setup + " o")
			if err != nil {
				t.Fatal(err)
			}
			got, err := jsonPath(rt, v, tt.path)
			if err != nil {
				if !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("got error %v, want %q", err, tt.want)
				}
				return
			}
			if n := strconv.Itoa(len(got)); n != tt.want {
				t.Fatalf("got %s matches, want %s", n, tt.want)
			}
		})
	}
}
