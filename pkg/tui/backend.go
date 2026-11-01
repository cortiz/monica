package tui

import (
	"context"

	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

// Backend does the work behind the TUI. The TUI only calls it from
// commands, one at a time; tests use a fake.
type Backend interface {
	// Envs lists the environments of the project's monica.yml.
	Envs() []string
	// Env is the selected environment ("" for defaults only).
	Env() string
	// SetEnv selects an environment and starts a new session: variables
	// set by scripts and cookies are dropped.
	SetEnv(name string) error
	// Send runs one request (by file and request line) in the session and
	// returns its result; err is the run error, if any (also in the
	// result).
	Send(ctx context.Context, file string, line int) (report.Request, error)
	// Variables lists what a request in file would see, secrets masked.
	Variables(file string) ([]runner.Variable, error)
	// Load runs a load test of file with the project's load settings,
	// calling progress while it runs, and returns the text summary.
	Load(ctx context.Context, file string, progress func(load.Progress)) (string, error)
	// Close ends the session (afterAll hooks).
	Close() error
}
