package script

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestScriptErrors(t *testing.T) {
	tests := []struct {
		name     string
		script   *request.Script
		timeout  time.Duration
		wantKind ErrorKind
		wantFile string
		wantLine int
		wantMsg  string
	}{
		{
			name:     "syntax error on a later line of an inline block",
			script:   &request.Script{Source: "\n  const ok = 1;\n  foo(;\n", File: "api.mon", Line: 3},
			wantKind: KindSyntax, wantFile: "api.mon", wantLine: 5, wantMsg: "SyntaxError: Unexpected token ;",
		},
		{
			name:     "thrown Error",
			script:   &request.Script{Source: "\n  throw new Error(\"bad status\");\n", File: "api.mon", Line: 7},
			wantKind: KindException, wantFile: "api.mon", wantLine: 8, wantMsg: "Error: bad status",
		},
		{
			name:     "thrown from a nested function reports the throw site",
			script:   &request.Script{Source: "\nfunction check() {\n  throw new TypeError(\"nope\");\n}\ncheck();\n", File: "api.mon", Line: 1},
			wantKind: KindException, wantFile: "api.mon", wantLine: 3, wantMsg: "TypeError: nope",
		},
		{
			name:     "thrown string",
			script:   &request.Script{Source: " throw \"boom\" ", File: "api.mon", Line: 2},
			wantKind: KindException, wantFile: "api.mon", wantLine: 2, wantMsg: "boom",
		},
		{
			name:     "script file",
			script:   &request.Script{Source: "const a = 1;\nundefinedFn();\n", File: "lib/check.js", Line: 1},
			wantKind: KindException, wantFile: "lib/check.js", wantLine: 2, wantMsg: "ReferenceError: undefinedFn is not defined",
		},
		{
			name:     "timeout",
			script:   &request.Script{Source: " while (true) {} ", File: "api.mon", Line: 4},
			timeout:  50 * time.Millisecond,
			wantKind: KindTimeout, wantFile: "api.mon", wantLine: 4, wantMsg: "script timed out after 50ms",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEngine()
			req := &request.Request{Method: "GET", URL: "http://a.example/", Headers: http.Header{}}
			_, err := e.RunPre(tt.script, req, Context{Vars: mapVars{}, Timeout: tt.timeout})
			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("expected *script.Error, got %T: %v", err, err)
			}
			if se.Kind != tt.wantKind || se.File != tt.wantFile || se.Line != tt.wantLine {
				t.Fatalf("got %s at %s:%d (%v), want %s at %s:%d", se.Kind, se.File, se.Line, err, tt.wantKind, tt.wantFile, tt.wantLine)
			}
			if tt.wantMsg != "" && se.Msg != tt.wantMsg {
				t.Fatalf("msg = %q, want %q", se.Msg, tt.wantMsg)
			}

			// The engine stays usable after any failure, timeouts included.
			if _, err := e.RunPre(src(`req.headers.set("X-After", "ok")`), req, Context{Vars: mapVars{}}); err != nil {
				t.Fatalf("engine unusable after %s: %v", tt.wantKind, err)
			}
		})
	}
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		err  Error
		want string
	}{
		{Error{File: "a.mon", Line: 3, Column: 5, Msg: "m"}, "a.mon:3:5: m"},
		{Error{File: "a.mon", Line: 3, Msg: "m"}, "a.mon:3: m"},
		{Error{File: "a.mon", Msg: "m"}, "a.mon: m"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}
