package runner

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/net"
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

// Runner runs .mon files. It loads each monica.yml once and reuses its
// HTTP client across files. Variables set by scripts (vars.set) are visible
// to every later request of the run.
type Runner struct {
	opts     *Opts
	out      io.Writer
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

// New returns a Runner writing responses to out.
func New(opts *Opts, out io.Writer) *Runner {
	return &Runner{opts: opts, out: out, projects: map[string]*project{}, runtime: map[string]string{}}
}

// RunSingleFile runs one file with a fresh Runner.
func RunSingleFile(file string, opts *Opts, out io.Writer) error {
	return New(opts, out).RunFile(file)
}

// RunFile parses file, sends its requests in order (except those marked
// @skip) and writes each response to out. A failed request does not stop the
// ones after it; all failures are returned joined. Read, parse and config
// failures are returned as *InputError before anything is sent; a request
// whose variables can't be expanded fails with an *InputError of its own.
func (r *Runner) RunFile(file string) error {
	reqs, err := request.ParseMonFile(file)
	if err != nil {
		return &InputError{Err: err}
	}
	p, err := r.project(file)
	if err != nil {
		return &InputError{Err: err}
	}
	var errs []error
	for _, req := range reqs {
		where := fmt.Sprintf("%s:%d", file, req.Line)
		if req.Skip {
			msg := fmt.Sprintf("%s: skipping %s", where, req.Label())
			if req.SkipReason != "" {
				msg += ": " + req.SkipReason
			}
			logging.Log.Info().Msg(msg)
			continue
		}
		logging.Log.Info().Msgf("%s: %s", where, req.Label())
		r.mask = p.mask
		ctx := script.Context{Env: p.settings.Env, Vars: &runVars{r: r, s: p.settings}, Timeout: p.scriptTimeout}
		working := *req
		working.Headers = req.Headers.Clone()
		if req.PreScript != nil {
			if err := r.scripts().RunPre(req.PreScript, &working, ctx); err != nil {
				errs = append(errs, scriptFailure(p, fmt.Errorf("%s: %s: pre-request script: %w", where, req.Label(), err)))
				continue
			}
		}
		rendered, err := r.render(&working, p.settings)
		if err != nil {
			errs = append(errs, &InputError{Err: p.mask.Error(err)})
			continue
		}
		res, err := p.client.Do(rendered, r.opts.HeaderOnly)
		if err != nil {
			errs = append(errs, p.mask.Error(fmt.Errorf("%s: %s: %w", where, req.Label(), err)))
			continue
		}
		logging.Log.Debug().Msg(p.mask.String(fmt.Sprintf("%s %s -> %d in %s", rendered.Method, rendered.URL, res.StatusCode, res.Duration)))
		if _, err := fmt.Fprintln(r.out, p.mask.String(res.String())); err != nil {
			return err
		}
		if req.PostScript != nil {
			if err := r.scripts().RunPost(req.PostScript, rendered, res, ctx); err != nil {
				errs = append(errs, scriptFailure(p, fmt.Errorf("%s: %s: post-response script: %w", where, req.Label(), err)))
			}
		}
	}
	return errors.Join(errs...)
}

// scriptFailure masks a script error; a script that doesn't compile is an
// input error (exit 2), anything else fails the request (exit 1).
func scriptFailure(p *project, err error) error {
	err = p.mask.Error(err)
	var se *script.Error
	if errors.As(err, &se) && se.Kind == script.KindSyntax {
		return &InputError{Err: err}
	}
	return err
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
