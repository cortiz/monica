package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
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
	projects map[string]*project       // by config path; "" = no config
	runtime  map[string]string         // vars.set values
	engines  map[string]*script.Engine // by module root; created on first script
	mask     masker                    // of the request being run, for console output
	bailed   bool                      // --bail tripped: run nothing more
	filters  []*regexp.Regexp          // compiled Opts.Filters
	onlyLine int                       // RunRequestAt: run only this request line

	transports *net.Transports // see pool

	iteration int // 1-based --data row being run; 0 without --data
	row       Row // its variables

	chains    map[string][]*hookFile // monica.js chain by folder and root
	hookFiles map[string]*hookFile   // by absolute path; nil = none there
	entered   []*hookFile            // beforeAll done, in order: afterAll pending
}

// project is a resolved config and what is built from it.
type project struct {
	settings      *config.Resolved
	client        *net.Client
	mask          masker
	scriptTimeout time.Duration // 0 = engine default
	// root is the require() root: the config's folder; "" without a config
	// (then each .mon file's folder). scriptPaths are scripts.paths.
	root        string
	scriptPaths []string
}

// New returns a Runner printing a console report to out.
func New(opts *Opts, out io.Writer) *Runner {
	c := report.NewConsole(out)
	c.Quiet = opts.Quiet
	return NewWithReporter(opts, c)
}

// NewWithReporter returns a Runner sending results to rep.
func NewWithReporter(opts *Opts, rep report.Reporter) *Runner {
	r := &Runner{opts: opts, rep: rep, start: time.Now(), projects: map[string]*project{}, runtime: map[string]string{}, engines: map[string]*script.Engine{},
		chains: map[string][]*hookFile{}, hookFiles: map[string]*hookFile{}}
	for _, glob := range opts.Filters {
		r.filters = append(r.filters, globRe(glob))
	}
	return r
}

// RunSingleFile runs one file with a fresh Runner and finishes the report.
func RunSingleFile(file string, opts *Opts, out io.Writer) error {
	r := New(opts, out)
	err := errors.Join(r.RunFile(file), r.AfterAll())
	if ferr := r.Finish(); ferr != nil && err == nil {
		err = ferr
	}
	return err
}

// Finish runs pending afterAll hooks (if AfterAll was not called) and sends
// the run summary to the reporter. It returns the reporter's error; hook
// failures are in the report.
func (r *Runner) Finish() error {
	_ = r.AfterAll()
	if r.transports != nil {
		r.transports.CloseIdle()
	}
	return r.rep.Done(report.Summarize(r.results, time.Since(r.start)))
}

// SetIteration starts iteration n (1-based) of a data-driven run: row's
// variables apply to the requests run next. Values a script set with
// vars.set for one of row's names are dropped, so each row starts from its
// own data; other vars.set values carry over.
func (r *Runner) SetIteration(n int, row Row) {
	r.iteration, r.row = n, row
	for name := range row {
		delete(r.runtime, name)
	}
}

func (r *Runner) emit(res report.Request) {
	res.Iteration = r.iteration
	if !r.opts.NoSummary {
		r.results = append(r.results, res)
	}
	r.rep.Request(res)
	if r.opts.Bail && res.Failed() && !r.bailed {
		r.bailed = true
		logging.Log.Warn().Msgf("%s:%d: stopping at the first failure (--bail)", res.File, res.Line)
	}
}

// ctx is Opts.Context, or a context that is never done.
func (r *Runner) ctx() context.Context {
	if r.opts.Context != nil {
		return r.opts.Context
	}
	return context.Background()
}

// Bailed reports whether --bail stopped the run; later RunFile calls do
// nothing.
func (r *Runner) Bailed() bool { return r.bailed }

// selected reports whether req passes the --filter globs.
func (r *Runner) selected(req *request.Request) bool {
	if r.onlyLine > 0 {
		return req.Line == r.onlyLine
	}
	if len(r.filters) == 0 {
		return true
	}
	for _, re := range r.filters {
		for _, name := range []string{req.Name, req.Title, req.String()} {
			if name != "" && re.MatchString(name) {
				return true
			}
		}
	}
	return false
}

// globRe compiles a glob where * matches any text (including "/") and ?
// one character; everything else is literal.
func globRe(glob string) *regexp.Regexp {
	q := regexp.QuoteMeta(glob)
	q = strings.ReplaceAll(q, `\*`, ".*")
	q = strings.ReplaceAll(q, `\?`, ".")
	return regexp.MustCompile("^" + q + "$")
}

// RunFile parses file and runs its requests in order (except those marked
// @skip), reporting each. A failed request does not stop the ones after it;
// all failures are returned joined. Read, parse and config failures are
// returned as *InputError before anything is sent; a request whose
// variables can't be expanded, or whose script doesn't compile, fails with
// an *InputError of its own.
func (r *Runner) RunFile(file string) error {
	if r.bailed {
		return nil
	}
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
		if r.bailed {
			break
		}
		if err := r.ctx().Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if !r.selected(req) {
			logging.Log.Debug().Msgf("%s:%d: %s does not match --filter", file, req.Line, req.Label())
			continue
		}
		var chain []*hookFile
		if !req.Skip {
			chain = r.hookChain(file, p)
			if err := r.enterHooks(chain); err != nil {
				errs = append(errs, err)
				if r.bailed {
					break
				}
			}
		}
		res, err := r.runRequest(file, req, p, chain)
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

// runRequest runs one request (see attempt), sending it again up to
// @retry times while it fails. It returns the (masked) report entry of the
// last attempt and the error, if any, that fails the run. A request under a
// failed monica.js is skipped.
func (r *Runner) runRequest(file string, req *request.Request, p *project, chain []*hookFile) (report.Request, error) {
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
	if h := blockedBy(chain); h != nil {
		out.Skipped, out.SkipReason = true, "hooks in "+h.file+" failed"
		logging.Log.Warn().Msgf("%s: skipping %s: %s", where, req.Label(), out.SkipReason)
		return out, nil
	}
	logging.Log.Info().Msgf("%s: %s", where, req.Label())
	r.mask = p.mask
	attempts := 1 + req.Retries
	for n := 1; ; n++ {
		res, err := r.attempt(file, where, req, p, chain, out)
		if n > 1 {
			res.Attempts = n
		}
		if err == nil || n == attempts || !retryable(err) || r.ctx().Err() != nil {
			return res, err
		}
		msg := fmt.Sprintf("%s: %s: attempt %d of %d failed; retrying", where, req.Label(), n, attempts)
		if req.RetryDelay > 0 {
			msg += " in " + req.RetryDelay.String()
		}
		logging.Log.Warn().Msg(msg)
		select {
		case <-time.After(req.RetryDelay):
		case <-r.ctx().Done():
			return res, err
		}
	}
}

// retryable reports whether a failed attempt is worth repeating: input
// errors (unknown variable, script syntax error) fail the same way again.
func retryable(err error) bool {
	var ie *InputError
	return !errors.As(err, &ie)
}

// attempt sends req once: beforeEach hooks, pre-request script, {{}}
// expansion, send, post-response script, afterEach hooks (innermost first).
// out is the report entry to fill in. A beforeEach hook or pre-request
// script that calls req.skip() skips the request.
func (r *Runner) attempt(file, where string, req *request.Request, p *project, chain []*hookFile, out report.Request) (report.Request, error) {
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

	ctx := r.hookContext(p)
	working := *req
	working.Headers = req.Headers.Clone()
	// skipped reports a req.skip() call from stage.
	skipped := func(stage string) (report.Request, error) {
		out.Skipped, out.SkipReason = true, p.mask.String(working.SkipReason)
		msg := fmt.Sprintf("%s: skipping %s (req.skip() in %s)", where, req.Label(), stage)
		if out.SkipReason != "" {
			msg += ": " + out.SkipReason
		}
		logging.Log.Info().Msg(msg)
		return out, nil
	}
	for _, h := range chain {
		results, err := h.engine.RunBeforeEach(h.hooks, &working, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil {
			return fail("beforeEach hook ("+h.file+")", err)
		}
		if working.Skip {
			return skipped("beforeEach hook (" + h.file + ")")
		}
	}
	if req.PreScript != nil {
		results, err := r.scripts(file, p).RunPre(req.PreScript, &working, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil {
			return fail("pre-request script", err)
		}
		if working.Skip {
			return skipped("pre-request script")
		}
	}
	rendered, err := r.render(&working, p.settings)
	if err != nil {
		out.Error = p.mask.String(err.Error())
		return out, &InputError{Err: p.mask.Error(err)}
	}
	out.Method, out.URL = rendered.Method, p.mask.String(rendered.URL)
	res, err := p.client.DoContext(r.ctx(), rendered, r.opts.HeaderOnly)
	if err != nil {
		return fail("", err)
	}
	logging.Log.Debug().Msg(p.mask.String(fmt.Sprintf("%s %s -> %d in %s", rendered.Method, rendered.URL, res.StatusCode, res.Duration)))
	out.Status, out.StatusText, out.Duration = res.StatusCode, http.StatusText(res.StatusCode), res.Duration
	out.Timings, out.BytesSent, out.BytesReceived = res.Timings, res.BytesSent, res.BytesReceived
	out.Response = p.mask.String(res.String())
	out.ResponseStatus, out.ResponseBody = res.Status, p.mask.String(res.Body)
	out.ResponseHeaders = make(map[string][]string, len(res.Headers))
	for name, values := range res.Headers {
		for _, v := range values {
			out.ResponseHeaders[name] = append(out.ResponseHeaders[name], p.mask.String(v))
		}
	}
	// afterEach hooks run even when the post-response script failed; the
	// first error is the one reported.
	var postErr error
	var postStage string
	if req.PostScript != nil {
		results, err := r.scripts(file, p).RunPost(req.PostScript, rendered, res, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil {
			postErr, postStage = err, "post-response script"
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		h := chain[i]
		results, err := h.engine.RunAfterEach(h.hooks, rendered, res, ctx)
		out.Tests = append(out.Tests, maskTests(p, results)...)
		if err != nil && postErr == nil {
			postErr, postStage = err, "afterEach hook ("+h.file+")"
		}
	}
	if postErr != nil {
		return fail(postStage, postErr)
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

// scripts returns the script engine for file's module root, creating it on
// first use. Engines share the run's vars; each has its own require() root
// and module instances. Console output is masked like everything else.
func (r *Runner) scripts(file string, p *project) *script.Engine {
	root := p.root
	if root == "" {
		root = filepath.Dir(file)
	}
	if e, ok := r.engines[root]; ok {
		return e
	}
	modules, err := script.NewModules(root, p.scriptPaths)
	if err != nil {
		// The root or a scripts.paths folder is unreadable: run without require().
		logging.Log.Warn().Msgf("%s: require() disabled: %v", file, err)
	}
	e := script.New(script.Options{Modules: modules, Console: func(level, msg string) {
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
	r.engines[root] = e
	return e
}

// lookup resolves a variable: --var, then vars.set values, then the
// --data row, then config vars.
func (r *Runner) lookup(s *config.Resolved, name string) (string, bool) {
	if v, ok := r.opts.Vars[name]; ok {
		return v, true
	}
	if v, ok := r.runtime[name]; ok {
		return v, true
	}
	if v, ok := r.row[name]; ok {
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

// pool is Opts.Transports, or the runner's own pool: projects with the
// same TLS and proxy settings share a transport, and certificate files
// are read once per run.
func (r *Runner) pool() *net.Transports {
	if r.opts.Transports != nil {
		return r.opts.Transports
	}
	if r.transports == nil {
		r.transports = net.NewTransports(net.TransportOptions{})
	}
	return r.transports
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
	var root string
	var scriptPaths []string
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
		root, scriptPaths = filepath.Dir(path), cfg.Scripts.Paths
	}
	tc, err := r.opts.transportConfig(settings)
	if err != nil {
		return nil, err
	}
	secrets := append(append([]string(nil), settings.Secrets...), config.ProxySecrets(tc.Proxy)...)
	mask := newMasker(secrets)
	if tc.Proxy != "" {
		// RedactProxy hides the whole user info; the masker is not used
		// here, so no credential flows into the log (CodeQL
		// go/clear-text-logging).
		logging.Log.Debug().Msg("Using proxy " + config.RedactProxy(tc.Proxy))
	}
	// Certificate files are read here, once per transport, before any
	// request of the project is sent.
	client, err := net.New(net.Options{
		Timeout:            settings.Timeout,
		NoFollowRedirects:  !settings.FollowRedirects,
		InsecureSkipVerify: tc.InsecureSkipVerify,
		ClientCert:         tc.ClientCert,
		ClientKey:          tc.ClientKey,
		CACert:             tc.CACert,
		Proxy:              tc.Proxy,
		Transports:         r.pool(),
		Jar:                r.opts.CookieJar,
	})
	if err != nil {
		return nil, mask.Error(err)
	}
	p := &project{
		settings:      settings,
		client:        client,
		mask:          mask,
		scriptTimeout: scriptTimeout,
		root:          root,
		scriptPaths:   scriptPaths,
	}
	r.projects[path] = p
	return p, nil
}
