package script

import (
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

type logged struct{ level, msg string }

func newTestEngine() (*Engine, *[]logged) {
	var out []logged
	e := New(Options{Console: func(level, msg string) { out = append(out, logged{level, msg}) }})
	return e, &out
}

func src(code string) *request.Script { return &request.Script{Source: code, File: "t.mon", Line: 1} }

func TestSandbox(t *testing.T) {
	e, out := newTestEngine()
	code := `console.log([typeof require, typeof process, typeof fetch, typeof module, typeof setTimeout, typeof XMLHttpRequest].join(","))`
	if err := e.run(src(code), nil); err != nil {
		t.Fatal(err)
	}
	if want := "undefined,undefined,undefined,undefined,undefined,undefined"; (*out)[0].msg != want {
		t.Fatalf("sandbox exposes host APIs: %q", (*out)[0].msg)
	}
}

func TestConsole(t *testing.T) {
	e, out := newTestEngine()
	code := `console.log("a", 1, true, null, undefined); console.info({k: [1, "x"]}); console.warn("w"); console.error("e", function f() {})`
	if err := e.run(src(code), nil); err != nil {
		t.Fatal(err)
	}
	want := []logged{
		{"log", "a 1 true null undefined"},
		{"info", `{"k":[1,"x"]}`},
		{"warn", "w"},
		{"error", "e function f() {}"},
	}
	if len(*out) != len(want) {
		t.Fatalf("got %v", *out)
	}
	for i := range want {
		if (*out)[i] != want[i] {
			t.Errorf("console[%d] = %v, want %v", i, (*out)[i], want[i])
		}
	}
}

func TestScriptsAreIsolated(t *testing.T) {
	e, out := newTestEngine()
	// Same top-level names in two scripts must not collide.
	for _, code := range []string{`const x = 1; let y = 2; console.log(x + y)`, `const x = 10; let y = 20; console.log(x + y)`} {
		if err := e.run(src(code), nil); err != nil {
			t.Fatal(err)
		}
	}
	if (*out)[0].msg != "3" || (*out)[1].msg != "30" {
		t.Fatalf("got %v", *out)
	}
}

func TestGlobalsArePerRun(t *testing.T) {
	e, out := newTestEngine()
	if err := e.run(src(`console.log(typeof thing, thing.v)`), map[string]any{"thing": map[string]any{"v": 7}}); err != nil {
		t.Fatal(err)
	}
	if err := e.run(src(`console.log(typeof thing)`), nil); err != nil {
		t.Fatal(err)
	}
	if (*out)[0].msg != "object 7" || (*out)[1].msg != "undefined" {
		t.Fatalf("got %v", *out)
	}
}

func TestCompileCacheAndLineNumbers(t *testing.T) {
	e, _ := newTestEngine()
	s := &request.Script{Source: "\n\n  null.boom;\n", File: "api.mon", Line: 10}
	err := e.run(s, nil)
	// "{%" is on line 10, so the third source line is line 12 of api.mon.
	if err == nil || !strings.Contains(err.Error(), "api.mon:12:") {
		t.Fatalf("error %v should point at api.mon:12", err)
	}
	p1 := e.programs[s]
	_ = e.run(s, nil)
	if e.programs[s] != p1 || len(e.programs) != 1 {
		t.Fatal("script was compiled twice")
	}
}
