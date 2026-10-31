package script

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

type mapVars map[string]string

func (m mapVars) Get(name string) (string, bool) { v, ok := m[name]; return v, ok }
func (m mapVars) Set(name, value string)         { m[name] = value }

func TestRunPreMutatesRequest(t *testing.T) {
	e, out := newTestEngine()
	req := &request.Request{
		Method: "GET", URL: "{{base}}/users", Body: "",
		Headers:  http.Header{"Accept": {"text/plain"}, "X-Drop": {"1"}},
		BodyFile: "",
	}
	vars := mapVars{"base": "http://a.example"}
	code := `
		console.log(req.method, req.url, req.headers.get("accept"), monica.env, vars.get("base"), vars.get("nope"), vars.has("base"));
		req.method = "POST";
		req.url = req.url + "?page=" + 2;
		req.headers.set("Accept", "application/json");
		req.headers.add("X-Multi", "a"); req.headers.add("X-Multi", "b");
		req.headers.delete("x-drop");
		req.body = JSON.stringify({n: 1});
		vars.set("token", 42);
	`
	if _, err := e.RunPre(src(code), req, Context{Env: "dev", Vars: vars}); err != nil {
		t.Fatal(err)
	}
	if got := (*out)[0].msg; got != "GET {{base}}/users text/plain dev http://a.example undefined true" {
		t.Fatalf("script saw %q", got)
	}
	want := &request.Request{
		Method: "POST", URL: "{{base}}/users?page=2", Body: `{"n":1}`,
		Headers: http.Header{"Accept": {"application/json"}, "X-Multi": {"a", "b"}},
	}
	if !reflect.DeepEqual(req, want) {
		t.Fatalf("got  %#v\nwant %#v", req, want)
	}
	if vars["token"] != "42" {
		t.Fatalf("vars.set not stored: %v", vars)
	}
}

func TestRunPreBodyFile(t *testing.T) {
	e, _ := newTestEngine()
	unchanged := &request.Request{Method: "POST", URL: "http://a.example/", Body: "file", BodyFile: "p.json", Headers: http.Header{}}
	if _, err := e.RunPre(src(`req.headers.set("X", "1")`), unchanged, Context{Vars: mapVars{}}); err != nil {
		t.Fatal(err)
	}
	if unchanged.BodyFile != "p.json" {
		t.Fatal("untouched body must keep its file origin")
	}
	changed := &request.Request{Method: "POST", URL: "http://a.example/", Body: "file", BodyFile: "p.json", Headers: http.Header{}}
	if _, err := e.RunPre(src(`req.body = "{{x}}"`), changed, Context{Vars: mapVars{}}); err != nil {
		t.Fatal(err)
	}
	if changed.BodyFile != "" || changed.Body != "{{x}}" {
		t.Fatalf("script-set body must be templated like an inline body: %#v", changed)
	}
}

func TestRunPost(t *testing.T) {
	e, out := newTestEngine()
	req := &request.Request{Method: "GET", URL: "http://a.example/u", Headers: http.Header{"Accept": {"application/json"}}}
	res := &net.HTTPResponse{
		StatusCode: 201,
		Headers:    http.Header{"Content-Type": {"application/json"}, "Set-Cookie": {"a=1", "b=2"}},
		Body:       `{"id": 7, "tags": ["x"]}`,
		Duration:   1500 * time.Microsecond,
	}
	vars := mapVars{}
	code := `
		const data = res.json();
		console.log(res.status, res.statusText, res.headers.get("content-type"), res.headers.get("set-cookie"),
			res.headers.get("nope"), res.headers.has("Set-Cookie"), data.id, data.tags[0], res.time, req.url,
			req.headers.get("accept"), res.headers.all()["Set-Cookie"].length, typeof res.body);
		vars.set("id", data.id);
	`
	if _, err := e.RunPost(src(code), req, res, Context{Vars: vars}); err != nil {
		t.Fatal(err)
	}
	want := "201 Created application/json a=1, b=2 null true 7 x 1.5 http://a.example/u application/json 2 string"
	if got := (*out)[0].msg; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if vars["id"] != "7" {
		t.Fatalf("vars = %v", vars)
	}
}

func TestAPIErrors(t *testing.T) {
	res := &net.HTTPResponse{StatusCode: 200, Body: "not json", Headers: http.Header{}}
	tests := []struct {
		name    string
		pre     bool
		code    string
		wantErr string
	}{
		{"req read-only in post", false, `req.headers.set("X", "1")`, "req is read-only in post-response scripts"},
		{"invalid json body", false, `res.json()`, "res.json(): body is not valid JSON"},
		{"res absent in pre", true, `res.status`, "res is not defined"},
		{"bad var name", true, `vars.set("1x", "v")`, `vars.set: invalid variable name "1x"`},
		{"missing argument", true, `vars.set("x")`, "vars.set: missing argument 2"},
		{"invalid method", true, `req.method = ""`, `invalid req.method ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEngine()
			req := &request.Request{Method: "GET", URL: "http://a.example/", Headers: http.Header{}}
			var err error
			if tt.pre {
				_, err = e.RunPre(src(tt.code), req, Context{Vars: mapVars{}})
			} else {
				_, err = e.RunPost(src(tt.code), req, res, Context{Vars: mapVars{}})
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}
