package script

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

// hookEngine writes monica.js with src into a temp project and returns an
// engine rooted there plus the hook file's path.
func hookEngine(t *testing.T, src string) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, HookFileName)
	if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewModules(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Modules: m}), file
}

func TestHooks(t *testing.T) {
	e, file := hookEngine(t, `
const prefix = vars.get("prefix") || "";
module.exports = {
  beforeAll() { vars.set("all", prefix + "before " + monica.env); },
  beforeEach() {
    req.headers.set("X-Hook", "1");
    req.url += "?h=1";
    test("each sees req", () => expect(req.method).toBe("GET"));
  },
  afterEach() { test("each sees res", () => expect(res.status).toBe(201)); },
  afterAll() { vars.set("all", vars.get("all") + ", after"); },
  helper: 42, // other exports are ignored
};`)
	vars := mapVars{"prefix": "> "}
	ctx := Context{Env: "dev", Vars: vars}
	h, err := e.LoadHooks(file, ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range allHooks {
		if !h.Has(hook) {
			t.Errorf("Has(%s) = false", hook)
		}
	}
	if err := e.RunAll(h, BeforeAll, ctx); err != nil {
		t.Fatal(err)
	}
	req := &request.Request{Method: "GET", URL: "http://x/y", Headers: http.Header{}}
	results, err := e.RunBeforeEach(h, req, ctx)
	if err != nil || len(results) != 1 || !results[0].Passed {
		t.Fatalf("beforeEach: %v %+v", err, results)
	}
	if req.URL != "http://x/y?h=1" || req.Headers.Get("X-Hook") != "1" {
		t.Fatalf("beforeEach changes not written back: %s %v", req.URL, req.Headers)
	}
	res := &net.HTTPResponse{StatusCode: 200, Headers: http.Header{}}
	results, err = e.RunAfterEach(h, req, res, ctx)
	if err != nil || len(results) != 1 || results[0].Passed {
		t.Fatalf("afterEach: %v %+v", err, results)
	}
	if err := e.RunAll(h, AfterAll, ctx); err != nil {
		t.Fatal(err)
	}
	if vars["all"] != "> before dev, after" {
		t.Fatalf("vars = %v", vars)
	}
}

func TestHooksPartial(t *testing.T) {
	e, file := hookEngine(t, `exports.afterAll = () => {};`)
	ctx := Context{Vars: mapVars{}}
	h, err := e.LoadHooks(file, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Has(BeforeEach) || !h.Has(AfterAll) {
		t.Fatal("Has wrong")
	}
	req := &request.Request{Method: "GET", URL: "u", Headers: http.Header{}}
	if r, err := e.RunBeforeEach(h, req, ctx); err != nil || r != nil {
		t.Fatalf("missing hook should be a no-op: %v %v", r, err)
	}
	if err := e.RunAll(h, BeforeAll, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHookErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		run  Hook // "" = only load
		want string
		line int // of the *Error, 0 = not a script error
	}{
		{"not an object", `module.exports = 1;`, "", "module.exports must be an object of hook functions", 0},
		{"hook is not a function", `exports.beforeEach = "x";`, "", "beforeEach must be a function", 0},
		{"top-level throw", "\nthrow new Error('load');", "", "Error: load", 2},
		{"syntax error", "exports.a = (;", "", "SyntaxError", 0},
		{"beforeAll throws", "exports.beforeAll = () => {\n  throw new Error('nope');\n};", BeforeAll, "Error: nope", 2},
		{"test() in beforeAll", "exports.beforeAll = () => {\n  test('x', () => {});\n};", BeforeAll, "test() is not available in beforeAll", 2},
		{"expect in afterAll", "exports.afterAll = () => {\n  expect(1).toBe(2);\n};", AfterAll, "expected 1 to be 2", 2},
		{"req is not defined in beforeAll", "exports.beforeAll = () => req.url;", BeforeAll, "ReferenceError: req is not defined", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, file := hookEngine(t, tt.src)
			ctx := Context{Vars: mapVars{}}
			h, err := e.LoadHooks(file, ctx)
			if tt.run != "" {
				if err != nil {
					t.Fatal(err)
				}
				err = e.RunAll(h, tt.run, ctx)
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
			var se *Error
			if tt.line > 0 {
				if !errors.As(err, &se) || se.File != file || se.Line != tt.line {
					t.Fatalf("got %v, want an *Error at %s:%d", err, file, tt.line)
				}
			}
		})
	}
}

func TestHooksWithoutModules(t *testing.T) {
	_, err := New(Options{}).LoadHooks("monica.js", Context{})
	if err == nil || !strings.Contains(err.Error(), "hooks need require()") {
		t.Fatalf("got %v", err)
	}
}
