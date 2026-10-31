package script

import (
	"errors"
	"testing"
	"time"
)

func TestRunawayScriptsFailCleanly(t *testing.T) {
	tests := []struct{ name, code, wantMsg string }{
		{"unbounded recursion", `function f() { return f() + 1 } f()`, "RangeError: Maximum call stack size exceeded"},
		{"cyclic object in console", `const a = {}; a.self = a; console.log(a)`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newTestEngine()
			start := time.Now()
			_, err := e.run(src(tt.code), nil, 0)
			if time.Since(start) > time.Second {
				t.Errorf("took %s; should fail fast, not at the timeout", time.Since(start))
			}
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var se *Error
			if !errors.As(err, &se) || se.Kind != KindException || se.Msg != tt.wantMsg {
				t.Fatalf("got %v, want exception %q", err, tt.wantMsg)
			}
		})
	}
}
