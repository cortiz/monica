package script

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestTranspileLoader(t *testing.T) {
	tests := []struct {
		file, src string
		want      bool
	}{
		{"a.ts", "const a = 1", true},
		{"a.MTS", "", true},
		{"a.cts", "", true},
		{"a.mjs", "const a = 1", true},
		{"a.cjs", "export const a = 1", false},
		{"a.json", `{"export": 1}`, false},
		{"a.js", "import x from 'y'", true},
		{"a.js", "import {x} from 'y'", true},
		{"a.js", "import * as x from 'y'", true},
		{"a.js", `import "side-effect"`, true},
		{"a.js", "  export const a = 1", true},
		{"a.js", "export default 1", true},
		{"a.js", "export {a}", true},
		{"a.js", "export * from 'b'", true},
		{"t.mon", "\nimport { sha256 } from 'monica/crypto'", true},
		{"a.js", "exports.beforeAll = () => {}", false},
		{"a.js", "module.exports = { exportAll: 1 }", false},
		{"a.js", "const imported = import('x')", false},
		{"a.js", "import('x').then(f)", false},
		{"a.js", "if (import.meta) {}", false},
		{"a.js", "const importer = 1;\nimporter + 1", false},
		{"a.js", "// import x from 'y' in a comment", false},
	}
	for _, tt := range tests {
		t.Run(tt.file+" "+tt.src, func(t *testing.T) {
			if _, got := transpileLoader(tt.file, tt.src); got != tt.want {
				t.Fatalf("transpile = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequireESMAndTypeScript(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"ES module importing TypeScript", `console.log(require("./esm/main").result)`, "hi ada 3 4"},
		{"TypeScript default and named exports", `const u = require("./esm/util.ts"); console.log(u.default("x"), u.add({a: 5, b: 5}))`, "hi x 10"},
		{"x.js falls back to x.ts", `console.log(require("./esm/util.js").add({a: 1, b: 1}))`, "2"},
		{".mjs", `console.log(require("./esm/plain.mjs").answer)`, "42"},
		{"index.ts", `console.log(require("./esm/dir-ts").where)`, "index.ts"},
		{"ESM package in node_modules", `console.log(require("./esm/uses_pkg.mjs").name, require("esm-pkg").named)`, "esm-pkg 1"},
		{"newer syntax is lowered", `const b = require("./esm/throws"); try { b.boom({x: 7}) } catch (e) { console.log(e.message) }`, "ts boom 7"},
		{"inline script with import", "import { sha256 } from 'monica/crypto';\nconsole.log(sha256('abc').slice(0, 6));", "ba7816"},
		{"inline script with import of a TS module", "import greet from './esm/util';\nconsole.log(greet('bob'));", "hi bob"},
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

func TestTranspileErrors(t *testing.T) {
	tests := []struct {
		name   string
		script *request.Script
		kind   ErrorKind
		file   string
		line   int
		want   string
	}{
		{
			name:   "TypeScript script file: runtime error keeps its line",
			script: &request.Script{File: "tests/check.ts", Line: 1, Source: "const n: number = 1;\ninterface X { a: string }\n\nthrow new Error('at line 4');\n"},
			kind:   KindException, file: "tests/check.ts", line: 4, want: "Error: at line 4",
		},
		{
			name:   "inline block with import: lines are the .mon file's",
			script: &request.Script{File: filepath.Join(modRoot, "api.mon"), Line: 10, Source: "\nimport { md5 } from 'monica/crypto';\n\nthrow new Error(md5('a'));\n"},
			kind:   KindException, file: filepath.Join(modRoot, "api.mon"), line: 13, want: "Error: 0cc175b9c0f1b6a831c399e269772661",
		},
		{
			name:   "TypeScript syntax error",
			script: &request.Script{File: "tests/bad.ts", Line: 1, Source: "const a: number = 1;\nconst = 2;\n"},
			kind:   KindSyntax, file: "tests/bad.ts", line: 2, want: `SyntaxError: Expected identifier but found "="`,
		},
		{
			name:   "syntax error in an inline ES module block",
			script: &request.Script{File: "a.mon", Line: 5, Source: "\nimport x from 'y';\nconst = 1;\n"},
			kind:   KindSyntax, file: "a.mon", line: 7, want: "SyntaxError: ",
		},
		{
			name:   "module syntax error names the .ts file",
			script: modSrc(`require("./esm/bad")`),
			kind:   KindException, file: filepath.Join(modRoot, "api.mon"), line: 1, want: "bad.ts:2:14: SyntaxError: Expected identifier",
		},
		{
			name:   "top-level await is rejected",
			script: modSrc(`require("./esm/tla.mjs")`),
			kind:   KindException, file: filepath.Join(modRoot, "api.mon"), line: 1, want: "tla.mjs:1:1: SyntaxError: Top-level await",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newModEngine(t)
			_, err := e.run(tt.script, nil, 0)
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("got %v, want *Error", err)
			}
			if se.Kind != tt.kind || se.File != tt.file || se.Line != tt.line || !strings.Contains(se.Msg, tt.want) {
				t.Fatalf("got %s %s:%d %q, want %s %s:%d containing %q", se.Kind, se.File, se.Line, se.Msg, tt.kind, tt.file, tt.line, tt.want)
			}
		})
	}
}

func TestESMHooks(t *testing.T) {
	tests := []struct {
		name, src string
		line      int // of the beforeAll error
	}{
		{"named exports", "import { md5 } from 'monica/crypto';\n\nexport function beforeAll(): void {\n  throw new Error(md5('x'));\n}\n", 4},
		{"export default", "const msg: string = 'from default';\n\nexport default {\n  beforeAll() {\n    throw new Error(msg);\n  },\n};\n", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, file := hookEngine(t, "")
			// monica.js is JavaScript, but type annotations are a TS-only
			// feature: write plain ESM for the default case.
			src := strings.ReplaceAll(strings.ReplaceAll(tt.src, ": void", ""), ": string", "")
			if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx := Context{Vars: mapVars{}}
			h, err := e.LoadHooks(file, ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !h.Has(BeforeAll) {
				t.Fatal("beforeAll not found")
			}
			err = e.RunAll(h, BeforeAll, ctx)
			var se *Error
			if !errors.As(err, &se) || se.File != file || se.Line != tt.line {
				t.Fatalf("got %v, want an error at %s:%d", err, file, tt.line)
			}
		})
	}
}
