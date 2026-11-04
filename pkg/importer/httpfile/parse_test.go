package httpfile

import (
	"reflect"
	"testing"

	"jmpeax.com/sec/monica/pkg/importer"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []*Request
		vars []Var
	}{
		{
			name: "URL only",
			in:   "https://example.com/\n",
			want: []*Request{{Line: 1, URL: "https://example.com/"}},
		},
		{
			name: "titles, comments, directives, version",
			in: "### First one\n# plain comment\n// @name first\n# @timeout = 5\nGET https://h/a HTTP/1.1\nAccept: */*\n\n" +
				"###\nPOST https://h/b\n",
			want: []*Request{
				{Line: 5, Title: "First one", Comments: []string{"plain comment"},
					Directives: []Directive{{"name", "first", 3}, {"timeout", "5", 4}},
					Method:     "GET", URL: "https://h/a", Version: "HTTP/1.1", Headers: []importer.Header{{Name: "Accept", Value: "*/*"}}},
				{Line: 9, Method: "POST", URL: "https://h/b"},
			},
		},
		{
			name: "query continuation lines and comments among headers",
			in:   "GET https://h/a\n    ?x=1\n    &y={{y}}\n  /more\n# note\nX-A: 1\n// other\nX-B:2\n",
			want: []*Request{{Line: 1, Method: "GET", URL: "https://h/a?x=1&y={{y}}/more",
				Headers: []importer.Header{{Name: "X-A", Value: "1"}, {Name: "X-B", Value: "2"}}}},
		},
		{
			name: "file variables",
			in:   "@host = example.com\n@empty =\n@url={{host}}/v1\n\nGET https://{{host}}\n",
			vars: []Var{{"host", "example.com", 1}, {"empty", "", 2}, {"url", "{{host}}/v1", 3}},
			want: []*Request{{Line: 5, Method: "GET", URL: "https://{{host}}"}},
		},
		{
			name: "body kept verbatim, trailing blank lines dropped, CRLF",
			in:   "POST https://h\r\nContent-Type: text/plain\r\n\r\n\r\n# not a comment\r\n> quoted text\r\n  indented\r\n\r\n\r\n###\r\n",
			want: []*Request{{Line: 1, Method: "POST", URL: "https://h", Headers: []importer.Header{{Name: "Content-Type", Value: "text/plain"}},
				Body: "# not a comment\n> quoted text\n  indented", BodyLine: 5}},
		},
		{
			name: "scripts inline, multi-line and files; redirections",
			in: "< {% request.variables.set(\"a\", 1) %}\n< ./pre.js\nPOST https://h\n\n{\"a\": 1}\n\n" +
				"> {%\n    client.test(\"t\", () => {\n        client.assert(true);\n    });\n%}\n> ./post.js\n>> out.json\n>>! out2.json\n// done\n",
			want: []*Request{{Line: 3, Method: "POST", URL: "https://h", Body: `{"a": 1}`, BodyLine: 5,
				Pre: []Script{{Code: `request.variables.set("a", 1)`, Line: 1}, {File: "./pre.js", Line: 2}},
				Post: []Script{
					{Code: "client.test(\"t\", () => {\n    client.assert(true);\n});", Line: 7},
					{File: "./post.js", Line: 12},
				},
				Redirects: []string{">> out.json", ">>! out2.json"}}},
		},
		{
			name: "handler right after the headers",
			in:   "GET https://h\nAccept: */*\n> {% client.log(1) %}\n",
			want: []*Request{{Line: 1, Method: "GET", URL: "https://h", Headers: []importer.Header{{Name: "Accept", Value: "*/*"}},
				Post: []Script{{Code: "client.log(1)", Line: 3}}}},
		},
		{
			name: "problems",
			in:   "GET https://h\nnot a header line\n\nbody\n> {% x %}\nstray text\n",
			want: []*Request{{Line: 1, Method: "GET", URL: "https://h", Body: "body", BodyLine: 4,
				Post:     []Script{{Code: "x", Line: 5}},
				Problems: []string{"line 2: not a header: not a header line", "line 6: text after the response handler: stray text"}}},
		},
		{
			name: "unclosed {% block takes the rest of the file and is reported",
			in:   "GET https://h/a\n\n> {%\n  client.log(1)\n###\nGET https://h/b\n",
			want: []*Request{{Line: 1, Method: "GET", URL: "https://h/a",
				Post:     []Script{{Code: "  client.log(1)\n###\nGET https://h/b", Line: 3}},
				Problems: []string{"line 3: {% block has no closing %}; it takes the rest of the file: > {%"}}},
		},
		{
			name: "sections without a request are skipped",
			in:   "### only comments\n# nothing here\n\n### real\nDELETE https://h/1\n",
			want: []*Request{{Line: 5, Title: "real", Method: "DELETE", URL: "https://h/1"}},
		},
		{
			name: "spaces inside variables stay in the URL",
			in:   "GET {{ base }}/a {{$random.integer(1, 2)}}\n",
			want: []*Request{{Line: 1, Method: "GET", URL: "{{ base }}/a {{$random.integer(1, 2)}}"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Parse(tt.in)
			if !reflect.DeepEqual(f.Requests, tt.want) {
				t.Errorf("requests:\n got %#v\nwant %#v", f.Requests, tt.want)
				for i := range f.Requests {
					t.Logf("got[%d] = %+v", i, *f.Requests[i])
				}
			}
			if !reflect.DeepEqual(f.Vars, tt.vars) {
				t.Errorf("vars = %#v, want %#v", f.Vars, tt.vars)
			}
		})
	}
}

func TestRequestLine(t *testing.T) {
	tests := []struct{ in, method, url, version string }{
		{"GET https://h/a", "GET", "https://h/a", ""},
		{"https://h/a HTTP/1.1", "", "https://h/a", "HTTP/1.1"},
		{"/relative", "", "/relative", ""},
		{"{{base}}/x", "", "{{base}}/x", ""},
		{"post\thttps://h  HTTP/2", "post", "https://h", "HTTP/2"},
		{"GRAPHQL https://h/graphql", "GRAPHQL", "https://h/graphql", ""},
	}
	for _, tt := range tests {
		m, u, v := requestLine(tt.in)
		if m != tt.method || u != tt.url || v != tt.version {
			t.Errorf("requestLine(%q) = %q, %q, %q", tt.in, m, u, v)
		}
	}
}
