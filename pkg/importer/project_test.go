package importer

import (
	"encoding/json"
	"testing"
)

func TestJSString(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`say "hi"`, `"say \"hi\""`},
		{`back\slash`, `"back\\slash"`},
		{"line\nbreak\ttab\rcr", `"line\nbreak\ttab\rcr"`},
		{"bell\x07del\x7f", `"bell\u0007del\u007f"`},
		{"js\u2028line\u2029sep", "\"js\\u2028line\\u2029sep\""},
		{`{{apiUser}} <b>&`, `"{{apiUser}} <b>&"`},
	}
	for _, tt := range tests {
		got := JSString(tt.in)
		if got != tt.want {
			t.Errorf("JSString(%q) = %s, want %s", tt.in, got, tt.want)
		}
		// Each result is also a JSON string that decodes back to the input.
		var back string
		if err := json.Unmarshal([]byte(got), &back); err != nil || back != tt.in {
			t.Errorf("JSString(%q) does not round-trip: %v %q", tt.in, err, back)
		}
	}
}

func TestGraphQLBody(t *testing.T) {
	got := GraphQLBody("query { a(x: \"<1>\") }", `{ "x": 1 }`)
	if want := `{"query":"query { a(x: \"<1>\") }","variables":{"x":1}}`; got != want {
		t.Errorf("GraphQLBody = %s, want %s", got, want)
	}
}
