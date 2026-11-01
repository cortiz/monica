// Package version reports Monica's version.
package version

import "runtime/debug"

// Version is set at build time by goreleaser, the Makefile and the Docker
// build:
//
//	go build -ldflags "-X jmpeax.com/sec/monica/internal/version.Version=1.2.3"
//
// Without it, Get falls back to the module version (go install ...@v1.2.3)
// and then to "dev".
var Version = ""

// Get returns the version without a leading "v" ("1.2.3", or "dev").
func Get() string {
	return resolve(Version, debug.ReadBuildInfo)
}

func resolve(v string, info func() (*debug.BuildInfo, bool)) string {
	if v == "" {
		if bi, ok := info(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	if len(v) > 1 && v[0] == 'v' && v[1] >= '0' && v[1] <= '9' {
		v = v[1:]
	}
	if v == "" {
		return "dev"
	}
	return v
}

// UserAgent is the User-Agent Monica sends when a request sets none.
func UserAgent() string { return "monica/" + Get() }
