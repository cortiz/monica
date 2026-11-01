package tui

import (
	"context"
	"errors"
	"net/http/cookiejar"
	"sort"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

// SessionOptions configure NewSession.
type SessionOptions struct {
	// Run is the runner configuration (env, --var, config, insecure, ...).
	Run runner.Opts
	// NoCookies turns the session's cookie jar off.
	NoCookies bool
	// ConfigPath is the monica.yml whose environments are offered; "" means
	// none (only the defaults).
	ConfigPath string
	// LoadOptions builds the load test settings (load: in monica.yml); nil
	// disables load tests.
	LoadOptions func() (load.Options, []load.Threshold, error)
}

// Session is the Backend over a runner: one Runner per environment, so
// vars.set values and cookies carry over between sends, as in one
// `monica run`.
type Session struct {
	opts   SessionOptions
	envs   []string
	runner *runner.Runner
	run    runner.Opts // the runner's options; Context is set per send
	last   *collector
}

// collector keeps the results a send produced.
type collector struct{ results []report.Request }

func (c *collector) Request(r report.Request)  { c.results = append(c.results, r) }
func (c *collector) Done(report.Summary) error { return nil }

// NewSession starts a session in opts.Run.Env.
func NewSession(opts SessionOptions) (*Session, error) {
	s := &Session{opts: opts}
	if opts.ConfigPath != "" {
		cfg, err := config.Load(opts.ConfigPath)
		if err != nil {
			return nil, err
		}
		for name := range cfg.Envs {
			s.envs = append(s.envs, name)
		}
		sort.Strings(s.envs)
	}
	if err := s.start(opts.Run.Env); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) start(env string) error {
	run := s.opts.Run
	run.Env = env
	if !s.opts.NoCookies {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return err
		}
		run.CookieJar = jar
	}
	s.run = run
	s.last = &collector{}
	s.runner = runner.NewWithReporter(&s.run, s.last)
	return nil
}

// Envs implements Backend.
func (s *Session) Envs() []string { return s.envs }

// Env implements Backend.
func (s *Session) Env() string { return s.run.Env }

// SetEnv implements Backend.
func (s *Session) SetEnv(name string) error {
	if name != "" && !contains(s.envs, name) {
		return errors.New("unknown environment " + name)
	}
	_ = s.runner.Finish() // afterAll hooks of the old session
	return s.start(name)
}

// Send implements Backend.
func (s *Session) Send(ctx context.Context, file string, line int) (report.Request, error) {
	s.run.Context = ctx
	defer func() { s.run.Context = nil }()
	s.last.results = nil
	err := s.runner.RunRequestAt(file, line)
	if n := len(s.last.results); n > 0 {
		return s.last.results[n-1], err
	}
	if err == nil {
		err = errors.New("the request is no longer in the file (reload with r)")
	}
	return report.Request{File: file, Line: line, Error: err.Error()}, err
}

// Variables implements Backend.
func (s *Session) Variables(file string) ([]runner.Variable, error) {
	return s.runner.Variables(file)
}

// Load implements Backend.
func (s *Session) Load(ctx context.Context, file string, progress func(load.Progress)) (string, error) {
	if s.opts.LoadOptions == nil {
		return "", errors.New("load tests are not available")
	}
	opts, thresholds, err := s.opts.LoadOptions()
	if err != nil {
		return "", err
	}
	opts.Files = []string{file}
	opts.Run = s.opts.Run
	opts.Run.Env = s.run.Env
	opts.Progress = progress
	res, err := load.Run(ctx, opts)
	if err != nil && !errors.Is(err, context.Canceled) {
		return "", err
	}
	summary := res.Summary(opts) + load.ThresholdReport(load.Check(res, thresholds))
	if err != nil {
		summary += "\n(stopped early)\n"
	}
	return summary, nil
}

// Close implements Backend.
func (s *Session) Close() error { return s.runner.Finish() }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
