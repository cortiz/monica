package request_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
	"jmpeax.com/sec/monica/pkg/template"
)

func expander(vars map[string]string) func(string) (string, error) {
	return func(s string) (string, error) {
		return template.Expand(s, func(name string) (string, bool) {
			v, ok := vars[name]
			return v, ok
		})
	}
}

func TestRender(t *testing.T) {
	reqs, err := request.ParseMonFile("testdata/templated.mon")
	if err != nil {
		t.Fatalf("templated URL must parse: %v", err)
	}
	src := reqs[0]
	vars := map[string]string{"baseUrl": "https://api.example.com", "id": "7", "token": "abc"}

	got, err := src.Render(expander(vars))
	if err != nil {
		t.Fatal(err)
	}
	want := *src
	want.URL = "https://api.example.com/users/7"
	want.Headers = http.Header{"Authorization": {"Bearer abc"}, "X-Static": {"plain"}}
	want.Body = "{\"id\": \"7\"}\n"
	if !reflect.DeepEqual(got, &want) {
		t.Fatalf("got  %#v\nwant %#v", got, &want)
	}
	if src.URL != "{{baseUrl}}/users/{{ id }}" || src.Headers.Get("Authorization") != "Bearer {{token}}" {
		t.Fatalf("Render modified the source request: %#v", src)
	}
}

func TestRenderErrors(t *testing.T) {
	reqs, err := request.ParseMonFile("testdata/templated.mon")
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]string{"baseUrl": "https://api.example.com", "id": "7", "token": "abc"}
	without := func(key string) map[string]string {
		m := map[string]string{}
		for k, v := range full {
			if k != key {
				m[k] = v
			}
		}
		return m
	}
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"unknown var in url", without("baseUrl"), `testdata/templated.mon:2: url: unknown variable "baseUrl"`},
		{"unknown var in header", without("token"), `testdata/templated.mon:2: header Authorization: unknown variable "token"`},
		{"expanded url invalid", map[string]string{"baseUrl": "api.example.com", "id": "7", "token": "abc"}, `testdata/templated.mon:2: url: invalid URL "api.example.com/users/7": scheme must be http or https`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reqs[0].Render(expander(tt.vars))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
			var pe *request.ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *request.ParseError, got %T", err)
			}
		})
	}

	body, err := request.ParseRequest("POST http://a.example/\n\n{{nope}}\n")
	if err != nil {
		t.Fatal(err)
	}
	var uv *template.UnknownVarError
	if _, err := body[0].Render(expander(nil)); !errors.As(err, &uv) || !strings.Contains(err.Error(), "line 1: body:") {
		t.Fatalf("expected body UnknownVarError at line 1, got %v", err)
	}
}

func TestRenderSkipsBodyFile(t *testing.T) {
	payload := filepath.Join(t.TempDir(), "raw.txt")
	if err := os.WriteFile(payload, []byte("{{not a template}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	reqs, err := request.ParseRequest("POST http://a.example/\n\n< " + payload + "\n")
	if err != nil {
		t.Fatal(err)
	}
	got, err := reqs[0].Render(expander(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "{{not a template}}" {
		t.Fatalf("body file must be sent as is, got %q", got.Body)
	}
}
