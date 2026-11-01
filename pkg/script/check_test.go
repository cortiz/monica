package script

import (
	"errors"
	"testing"

	"jmpeax.com/sec/monica/pkg/request"
)

func TestCompile(t *testing.T) {
	tests := []struct {
		name     string
		script   request.Script
		wantLine int // 0 = compiles
	}{
		{"valid js", request.Script{Source: "vars.set('a', 1);\n", File: "a.mon", Line: 3}, 0},
		{"not run", request.Script{Source: "throw new Error('boom');\n", File: "a.mon", Line: 1}, 0},
		{"js syntax error", request.Script{Source: "\nlet x = ;\n", File: "a.mon", Line: 4}, 5},
		{"valid ts", request.Script{Source: "const n: number = 1;\n", File: "t.ts", Line: 1}, 0},
		{"ts syntax error", request.Script{Source: "const n: number = ;\n", File: "t.ts", Line: 1}, 1},
		{"esm", request.Script{Source: "export const a = 1;\n", File: "m.js", Line: 1}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Compile(&tt.script)
			if tt.wantLine == 0 {
				if err != nil {
					t.Fatalf("Compile: %v", err)
				}
				return
			}
			var se *Error
			if !errors.As(err, &se) || se.Kind != KindSyntax {
				t.Fatalf("Compile = %v, want a syntax *Error", err)
			}
			if se.Line != tt.wantLine {
				t.Errorf("line = %d, want %d (%v)", se.Line, tt.wantLine, err)
			}
		})
	}
}
