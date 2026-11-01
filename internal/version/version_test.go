package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string, ok bool) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Version: v}}, ok
		}
	}
	tests := []struct {
		name, ldflag string
		info         func() (*debug.BuildInfo, bool)
		want         string
	}{
		{"ldflags win", "1.4.0", info("v9.9.9", true), "1.4.0"},
		{"ldflags v prefix dropped", "v1.4.0-rc.1", info("", false), "1.4.0-rc.1"},
		{"module version", "", info("v0.3.1", true), "0.3.1"},
		{"devel build", "", info("(devel)", true), "dev"},
		{"no build info", "", info("", false), "dev"},
		{"non-numeric v kept", "vnext", info("", false), "vnext"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(tt.ldflag, tt.info); got != tt.want {
				t.Errorf("resolve = %q, want %q", got, tt.want)
			}
		})
	}
}
