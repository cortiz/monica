package runner

import (
	"context"

	"jmpeax.com/sec/monica/internal/config"
)

type Opts struct {
	HeaderOnly bool
	// Quiet hides response headers and bodies in the console report.
	Quiet bool
	// Bail stops the run after the first failed request (or file).
	Bail bool
	// Filters are globs (* = any text, ? = one character); when set, only
	// requests whose @name, ### title or "METHOD URL" matches one of them run.
	Filters []string
	// Env selects an environment from monica.yml ("" = defaults only).
	Env string
	// Vars are --var overrides; they win over config variables.
	Vars map[string]string
	// ConfigPath is an explicit config file; "" means look for monica.yml
	// above each .mon file.
	ConfigPath string
	// LookupEnv reads OS environment variables for ${NAME}; nil means
	// os.LookupEnv.
	LookupEnv config.LookupFunc
	// Context, when set, stops the run once it is done: the request being
	// sent is aborted and no further request starts. nil means never.
	Context context.Context
	// NoSummary keeps no per-request results for Finish's summary, so a long
	// run (monica load) does not grow; the reporter still gets every result.
	NoSummary bool
	// VU is monica.vu in scripts: the 1-based virtual user under monica
	// load, 0 otherwise.
	VU int
}
