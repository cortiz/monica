package runner

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/request"
	"jmpeax.com/sec/monica/pkg/script"
	"jmpeax.com/sec/monica/pkg/template"
)

// InputError marks a failure caused by the input itself (unreadable file,
// invalid syntax or config, unknown variable) rather than by sending the
// request.
type InputError struct {
	Err error
}

func (e *InputError) Error() string { return e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

// Runner runs .mon files and reports each request to a report.Reporter. It
// loads each monica.yml once and reuses its HTTP client across files.
// Variables set by scripts (vars.set) are visible to every later request of
// the run.
type Runner struct {
	opts     *Opts
	rep      report.Reporter
	results  []report.Request
	start    time.Time
	projects map[string]*project // by config path; "" = no config
	runtime  map[string]string   // vars.set values
	engine   *script.Engine      // created on first script
	mask     masker              // of the request being run, for console output
}

// project is a resolved config and what is built from it.
type project struct {
	settings      *config.Resolved
	client        *net.Client
	mask          masker
	scriptTimeout time.Duration // 0 = engine default
}

// New returns a Runner printing a console report to out.
func New(opts *Opts, out io.Writer) *Runner {
	c := report.NewConsole(out)
	c.Quiet = opts.Quiet
	return NewWithReporter(opts, c)
}

// NewWithReporter returns a Runner sending results to rep.
func NewWithReporter(opts *Opts, rep report.Reporter) *Runner {
	return &Runner{opts: opts, rep: rep, start: time.Now(), projects: map[string]*project{}, runtime: map[string]string{}}
}

// RunSingleFile runs one file with a fresh Runner and finishes the report.
func RunSingleFile(file string, opts *Opts, out io.Writer) error {
	r := New(opts, out)
	err := r.RunFile(file)
	if ferr := r.Finish(); ferr != nil && err == nil {
		err = ferr
	}
	return err
}

// Finish sends the run summary to the reporter.
func (r *Runner) Finish() error {
	return r.rep.Done(report.Summarize(r.results, time.Since(r.start)))
}

func (r *Runner) emit(res report.Request) {
	r.results = append(r.results, res)
	r.rep.Request(res)
}

// RunFile parses file and runs its requests in order (except those marked
// @skip), reporting each. A failed request does not stop the ones after it;
// all failures are returned joined. Read, parse and config failures are
// returned as *InputError before anything is sent; a request whose
// variables can't be expanded, or whose script doesn't compile, fails with
// an *InputError of its own.
func (r *Runner) RunFile(file string) error {
	reqs, err := request.ParseMonFile(file)
	if err != nil {
		r.emit(fileFailure(file, err))
		return &InputError{Err: err}
	}
	p, err := r.project(file)
	if err != nil {
		r.emit(fileFailure(file, err))
		return &InputError{Err: err}
	}
	var errs []error
	for _, req := range reqs {
		res, err := r.runRequest(file, req, p)
		r.emit(res)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fileFailure reports a file that could not be run at all.
func fileFailure(file string, err error) report.Request {
	res := report.Request{File: file, Name: filepath.Base(file), Error: err.Error()}
	var pe *request.ParseError
	if errors.As(err, &pe) {
		res.Line = pe.Line
	}
	return res
}

// runRequest runs one request: pre-request script, {{}} expansion, send,
// post-response script. It returns the (masked) report entry and the error,
// if any, that fails the run.
func (r *Runner) runRequest(file string, req *request.Request, p *project) (report.Request, error) {
	where := fmt.Sprintf("%s:%d", file, req.Line)
	out := report.Request{File: file, Line: req.Line, Name: req.Label(), Method: req.Method, URL: p.mask.String(req.URL)}
	if req.Skip {
		msg := fmt.Sprintf("%s: skipping %s", where, req.Label())
		if req.SkipReason != "" {
			msg += ": " + req.SkipReason
		}
		logging.Log.Info().Msg(msg)
		out.Skipped, out.SkipReason = true, req.SkipReason
		return out, nil
	}
	logging.Log.Info().Msgf("%s: %s", where, req.Label())
	r.mask = p.mask

	// fail records err in the report (prefixed with stage) and returns it
	// for the exit code, prefixed with the request's location.
	fail := func(stage string, err error) (report.Request, error) {
		prefix := ""
		if stage != "" {
			prefix = stage + ": "
		}
		out.Error = p.mask.String(prefix + err.Error())
		full := p.mask.Error(fmt.Errorf("%s: %s: %s%w", where, req.Label(), prefix, err))
		var se *script.Error
		if errors.As(err, &se) && se.Kind == script.KindSyntax {
			full = &InputError{Err: full}
		}
		return out, full
	}

	ctx := script.Context{Env: p.settings.Env, Vars: &runVars{r: r, s: p.settings}, Timeout: p.scriptTimeout}
	working := *req
	working.Headers = req.Headers.Clone()
	if req.PreScript != nil {
		results, err := r.scripts().RunPre(req.PreScript, &working, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil {
			return fail("pre-request script", err)
		}
	}
	rendered, err := r.render(&working, p.settings)
	if err != nil {
		out.Error = p.mask.String(err.Error())
		return out, &InputError{Err: p.mask.Error(err)}
	}
	out.Method, out.URL = rendered.Method, p.mask.String(rendered.URL)
	res, err := p.client.Do(rendered, r.opts.HeaderOnly)
	if err != nil {
		return fail("", err)
	}
	logging.Log.Debug().Msg(p.mask.String(fmt.Sprintf("%s %s -> %d in %s", rendered.Method, rendered.URL, res.StatusCode, res.Duration)))
	out.Status, out.StatusText, out.Duration = res.StatusCode, http.StatusText(res.StatusCode), res.Duration
	out.Response = p.mask.String(res.String())
	if req.PostScript != nil {
		results, err := r.scripts().RunPost(req.PostScript, rendered, res, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil {
			return fail("post-response script", err)
		}
	}
	if err := testFailures(out.Tests); err != nil {
		return out, fmt.Errorf("%s: %s: %w", where, req.Label(), err)
	}
	return out, nil
}

func maskTests(p *project, tests []script.TestResult) []script.TestResult {
	for i := range tests {
		tests[i].Name = p.mask.String(tests[i].Name)
		tests[i].Error = p.mask.String(tests[i].Error)
	}
	return tests
}

// testFailures summarizes failed tests as one error, or nil.
func testFailures(tests []script.TestResult) error {
	var failed []string
	for _, t := range tests {
		if !t.Passed {
			failed = append(failed, fmt.Sprintf("%q: %s", t.Name, t.Error))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d tests failed: %s", len(failed), len(tests), strings.Join(failed, "; "))
}

// scripts returns the run's script engine, creating it on first use.
// Console output is masked like everything else.
func (r *Runner) scripts() *script.Engine {
	if r.engine == nil {
		r.engine = script.New(script.Options{Console: func(level, msg string) {
			msg = "console." + level + ": " + r.mask.String(msg)
			switch level {
			case "warn":
				logging.Log.Warn().Msg(msg)
			case "error":
				logging.Log.Error().Msg(msg)
			default:
				logging.Log.Info().Msg(msg)
			}
		}})
	}
	return r.engine
}

// lookup resolves a variable: --var, then vars.set values, then config vars.
func (r *Runner) lookup(s *config.Resolved, name string) (string, bool) {
	if v, ok := r.opts.Vars[name]; ok {
		return v, true
	}
	if v, ok := r.runtime[name]; ok {
		return v, true
	}
	v, ok := s.Vars[name]
	return v, ok
}

// runVars is the script view of the run's variables.
type runVars struct {
	r *Runner
	s *config.Resolved
}

func (v *runVars) Get(name string) (string, bool) { return v.r.lookup(v.s, name) }
func (v *runVars) Set(name, value string)         { v.r.runtime[name] = value }

// render expands {{variables}} (see lookup) and adds the config's default
// headers the request doesn't set itself.
func (r *Runner) render(req *request.Request, s *config.Resolved) (*request.Request, error) {
	lookup := func(name string) (string, bool) { return r.lookup(s, name) }
	expand := func(text string) (string, error) { return template.Expand(text, lookup) }
	out, err := req.Render(expand)
	if err != nil {
		return nil, err
	}
	for name, value := range s.Headers {
		if out.Headers.Get(name) != "" {
			continue
		}
		v, err := expand(value)
		if err != nil {
			return nil, &request.ParseError{File: req.File, Line: req.Line, Msg: fmt.Sprintf("default header %s: %v", name, err), Err: err}
		}
		out.Headers.Set(name, v)
	}
	return out, nil
}

// project returns the resolved config for file: --config if given, else the
// nearest monica.yml above the file, else the built-in settings.
func (r *Runner) project(file string) (*project, error) {
	path := r.opts.ConfigPath
	if path == "" {
		var err error
		if path, err = config.Find(filepath.Dir(file)); err != nil {
			return nil, err
		}
	}
	if p, ok := r.projects[path]; ok {
		return p, nil
	}
	settings := config.Builtin()
	var scriptTimeout time.Duration
	if path == "" {
		if r.opts.Env != "" {
			logging.Log.Warn().Msgf("%s: no %s found; ignoring environment %q", file, config.FileName, r.opts.Env)
		}
	} else {
		logging.Log.Debug().Msgf("Using config %s", path)
		cfg, err := config.Load(path)
		if err != nil {
			return nil, err
		}
		if settings, err = cfg.Resolve(r.opts.Env, r.opts.LookupEnv); err != nil {
			return nil, err
		}
		scriptTimeout = cfg.Scripts.Timeout.Duration
	}
	p := &project{
		settings: settings,
		client: net.NewClient(net.Options{
			Timeout:            settings.Timeout,
			NoFollowRedirects:  !settings.FollowRedirects,
			InsecureSkipVerify: settings.InsecureSkipVerify,
		}),
		mask:          newMasker(settings.Secrets),
		scriptTimeout: scriptTimeout,
	}
	r.projects[path] = p
	return p, nil
}
