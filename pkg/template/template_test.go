package template_test

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/template"
)

func lookup(vars map[string]string) template.Lookup {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestExpand(t *testing.T) {
	vars := lookup(map[string]string{
		"baseUrl": "http://a.example", "id": "42", "api.v": "v2", "nested": "{{id}}", "empty": "",
	})
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "no placeholders", in: "plain {text}", want: "plain {text}"},
		{name: "url", in: "{{baseUrl}}/users/{{id}}", want: "http://a.example/users/42"},
		{name: "spaces inside braces", in: "{{ id }}-{{\tid\t}}", want: "42-42"},
		{name: "dotted name", in: "/{{api.v}}/", want: "/v2/"},
		{name: "no recursion", in: "{{nested}}", want: "{{id}}"},
		{name: "empty value", in: "a{{empty}}b", want: "ab"},
		{name: "json body", in: `{"id": {{id}}, "obj": {"k": 1}}`, want: `{"id": 42, "obj": {"k": 1}}`},
		{name: "unknown variable", in: "{{baseUrl}}/{{nope}}", wantErr: `unknown variable "nope"`},
		{name: "unclosed", in: "x {{id", wantErr: `unclosed "{{" in "{{id"`},
		{name: "empty name", in: "{{ }}", wantErr: `invalid variable name ""`},
		{name: "invalid name", in: "{{a b}}", wantErr: `invalid variable name "a b"`},
		{name: "unknown built-in", in: "{{$nope}}", wantErr: `unknown built-in "$nope"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := template.Expand(tt.in, vars)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Expand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnknownVarError(t *testing.T) {
	_, err := template.Expand("{{missing}}", nil)
	var uv *template.UnknownVarError
	if !errors.As(err, &uv) || uv.Name != "missing" {
		t.Fatalf("expected UnknownVarError{missing}, got %v", err)
	}
}

func TestBuiltins(t *testing.T) {
	uuidRe := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	a, err := template.Expand("{{$uuid}}", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := template.Expand("{{ $uuid }}", nil)
	if !uuidRe.MatchString(a) || !uuidRe.MatchString(b) || a == b {
		t.Fatalf("bad or repeated v4 UUIDs: %q, %q", a, b)
	}

	before := time.Now().Unix()
	ts, err := template.Expand("{{$timestamp}}", nil)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || n < before || n > time.Now().Unix() {
		t.Fatalf("timestamp %q not within the test's run time", ts)
	}

	for i := 0; i < 50; i++ {
		s, err := template.Expand("{{$randomInt}}", nil)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := strconv.Atoi(s); err != nil || n < 0 || n > 999 {
			t.Fatalf("randomInt %q out of [0, 999]", s)
		}
	}
}

func TestBuiltinsNotShadowedByVars(t *testing.T) {
	got, err := template.Expand("{{$timestamp}}", lookup(map[string]string{"$timestamp": "x"}))
	if err != nil {
		t.Fatal(err)
	}
	if got == "x" {
		t.Fatal("a user variable must not override a built-in")
	}
}
