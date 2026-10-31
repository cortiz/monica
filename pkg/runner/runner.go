package runner

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
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
// HTTP client across files.
type Runner struct {
	opts     *Opts
	out      io.Writer
	projects map[string]*project // by config path; "" = no config
}

// project is a resolved config and the client built from it.
type project struct {
	settings *config.Resolved
	client   *net.Client
}

// New returns a Runner writing responses to out.
func New(opts *Opts, out io.Writer) *Runner {
	return &Runner{opts: opts, out: out, projects: map[string]*project{}}
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
		if req.PreScript != nil || req.PostScript != nil {
			logging.Log.Warn().Msgf("%s: scripts are parsed but not run yet", where)
		}
		rendered, err := r.render(req, p.settings)
		if err != nil {
			errs = append(errs, &InputError{Err: err})
			continue
		}
		res, err := p.client.Do(rendered, r.opts.HeaderOnly)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", where, req.Label(), err))
			continue
		}
		if _, err := fmt.Fprintln(r.out, res); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// render expands {{variables}} (--var over config vars) and adds the
// config's default headers the request doesn't set itself.
func (r *Runner) render(req *request.Request, s *config.Resolved) (*request.Request, error) {
	lookup := func(name string) (string, bool) {
		if v, ok := r.opts.Vars[name]; ok {
			return v, true
		}
		v, ok := s.Vars[name]
		return v, ok
	}
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
	}
	p := &project{
		settings: settings,
		client: net.NewClient(net.Options{
			Timeout:            settings.Timeout,
			NoFollowRedirects:  !settings.FollowRedirects,
			InsecureSkipVerify: settings.InsecureSkipVerify,
		}),
	}
	r.projects[path] = p
	return p, nil
}
