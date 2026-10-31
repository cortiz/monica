package script

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

const modRoot = "testdata/modules/project"

// newModEngine returns an engine whose require() is rooted at modRoot with
// shared/ as a scripts.paths folder.
func newModEngine(t *testing.T) (*Engine, *[]logged) {
	t.Helper()
	m, err := NewModules(modRoot, []string{"shared"})
	if err != nil {
		t.Fatal(err)
	}
	var out []logged
	e := New(Options{Modules: m, Console: func(level, msg string) { out = append(out, logged{level, msg}) }})
	return e, &out
}

// modSrc is an inline script of a .mon file in modRoot.
func modSrc(code string) *request.Script {
	return &request.Script{Source: code, File: filepath.Join(modRoot, "api.mon"), Line: 1}
}

func TestRequire(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(modRoot, "lib", "helpers.js"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		code string
		want string
	}{
		{"relative file with nested require", `const m = require("./lib/math"); console.log(m.add(1, 2), m.twice(4))`, "3 8"},
		{"explicit .js extension", `console.log(require("./lib/math.js").add(2, 2))`, "4"},
		{"json file", `console.log(require("./data.json").users[1].name)`, "grace"},
		{"folder index.js", `console.log(require("./lib/dir"))`, "from index.js"},
		{"bare name from scripts.paths", `console.log(require("greet")("ada"))`, "hello ada"},
		{"absolute path inside the root", `console.log(require(` + jsString(abs) + `).double(5))`, "10"},
		{"same instance on every require", `console.log(require("./lib/counter").next(), require("./lib/counter.js").next())`, "1 2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, out := newModEngine(t)
			if _, err := e.run(modSrc(tt.code), nil, 0); err != nil {
				t.Fatal(err)
			}
			if len(*out) != 1 || (*out)[0].msg != tt.want {
				t.Fatalf("got %v, want %q", *out, tt.want)
			}
		})
	}
}

func TestRequireErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		line int
		want []string
	}{
		{"missing file", `require("./lib/nope")`, 1, []string{`Error: require("./lib/nope"): cannot find module`}},
		{"missing package", `require("left-pad")`, 1, []string{`require("left-pad"): cannot find module`}},
		{"relative path traversal", "\nrequire('../outside/secret.js')", 2, []string{`require("../outside/secret.js")`, "outside the project root and scripts.paths"}},
		{"absolute path outside", `require("/etc/passwd")`, 1, []string{`/etc/passwd is outside the project root`}},
		{"traversal from inside a module", `require("./lib/escape")`, 1, []string{`require("../../outside/secret.js")`, "outside the project root"}},
		{"module throws when called", "const m = require('./lib/throws');\nm.boom()", 2, []string{"Error: boom from module"}},
		{"module syntax error", `require("./lib/bad_syntax")`, 1, []string{`require("./lib/bad_syntax"): `, "bad_syntax.js:2:12: SyntaxError: Unexpected token"}},
		{"missing argument", `require()`, 1, []string{"require: missing argument 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newModEngine(t)
			_, err := e.run(modSrc(tt.code), nil, 0)
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("got %v, want *Error", err)
			}
			if se.Kind != KindException || se.Line != tt.line || se.File != filepath.Join(modRoot, "api.mon") {
				t.Errorf("got %s %s:%d, want exception at api.mon:%d", se.Kind, se.File, se.Line, tt.line)
			}
			for _, w := range tt.want {
				if !strings.Contains(se.Msg, w) {
					t.Errorf("message %q does not contain %q", se.Msg, w)
				}
			}
		})
	}
}

func TestRequireSymlinkOutside(t *testing.T) {
	dir := t.TempDir()
	proj, out := filepath.Join(dir, "proj"), filepath.Join(dir, "out")
	for _, d := range []string{proj, out} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(out, "secret.js"), []byte(`module.exports = "secret"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "secret.js"), filepath.Join(proj, "link.js")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	m, err := NewModules(proj, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{Modules: m})
	s := &request.Script{Source: `require("./link.js")`, File: filepath.Join(proj, "a.mon"), Line: 1}
	_, err = e.run(s, nil, 0)
	if err == nil || !strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("got %v, want a links-outside error", err)
	}
}

func TestRequireScriptFileBase(t *testing.T) {
	// A script file's require() is relative to the script file, not the .mon.
	e, out := newModEngine(t)
	s := &request.Script{Source: `console.log(require("../lib/math").add(1, 1))`, File: filepath.Join(modRoot, "tests", "t.js"), Line: 1}
	if _, err := e.run(s, nil, 0); err != nil {
		t.Fatal(err)
	}
	if (*out)[0].msg != "2" {
		t.Fatalf("got %v", *out)
	}
}

func TestRequireSharedCompileCache(t *testing.T) {
	// Two engines on one Modules share compiled code, not module state.
	m, err := NewModules(modRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var got string
		e := New(Options{Modules: m, Console: func(_, msg string) { got = msg }})
		if _, err := e.run(modSrc(`console.log(require("./lib/counter").next())`), nil, 0); err != nil {
			t.Fatal(err)
		}
		if got != "1" {
			t.Fatalf("engine %d: counter = %s, want a fresh module instance", i, got)
		}
	}
}

func TestRequireTimeout(t *testing.T) {
	// Module top-level code runs inside the script's time budget.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spin.js"), []byte("for (;;) {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewModules(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := New(Options{Modules: m})
	s := &request.Script{Source: `require("./spin")`, File: filepath.Join(dir, "a.mon"), Line: 1}
	_, err = e.run(s, nil, 50e6)
	var se *Error
	if !errors.As(err, &se) || se.Kind != KindTimeout {
		t.Fatalf("got %v, want timeout", err)
	}
}

func jsString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
