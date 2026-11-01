package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/request"
	"jmpeax.com/sec/monica/pkg/script"
	"jmpeax.com/sec/monica/pkg/template"
)

// Severity tells whether a Problem fails validation.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Problem is one thing Validate found. Line is 0 when it is not tied to a
// line (and File may be empty when it is not tied to a file).
type Problem struct {
	File     string
	Line     int
	Severity Severity
	Msg      string
}

// String formats p as "file:line: message"; warnings say so.
func (p Problem) String() string {
	msg := p.Msg
	if p.Severity == SeverityWarning {
		msg = "warning: " + msg
	}
	switch {
	case p.File != "" && p.Line > 0:
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, msg)
	case p.File != "":
		return fmt.Sprintf("%s: %s", p.File, msg)
	default:
		return msg
	}
}

// Validation is the outcome of Validate.
type Validation struct {
	Files    int // .mon files checked
	Requests int // requests checked (those passing --filter)
	Problems []Problem
}

// Errors counts the problems that fail validation.
func (v *Validation) Errors() int {
	n := 0
	for _, p := range v.Problems {
		if p.Severity == SeverityError {
			n++
		}
	}
	return n
}

// setRe finds vars.set("name" / vars.set('name' / vars.set(`name` in
// script source: the names scripts may set at run time.
var setRe = regexp.MustCompile("vars\\.set\\(\\s*[\"'`]([A-Za-z_][A-Za-z0-9_.-]*)[\"'`]")

// scriptExts are the files a scripts.paths folder may hold modules in.
var scriptExts = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}

// vproject is a config as Validate sees it.
type vproject struct {
	p   *project // settings, root and scriptPaths; no client is used
	err error    // loading or resolving failed (already reported)
}

// validator holds the state of one Validate call.
type validator struct {
	r        *Runner // for --filter and monica.js discovery only; it sends nothing
	opts     *Opts
	out      *Validation
	projects map[string]*vproject // by config path; "" = no config
	runtime  map[string]bool      // names set by vars.set somewhere in the run
	data     map[string]bool      // --data columns
	scanned  map[string]bool      // script files already scanned or compiled
	reported map[string]bool      // problem lines already reported
	badFlags bool                 // --cert/--key/--cacert/--proxy failed CheckTransport
}

// Validate checks files the way RunFile would run them (see Opts: Env,
// Vars, ConfigPath, Filters, LookupEnv), without sending anything or
// running any script. It reports .mon syntax errors (including missing
// body and script files), monica.yml errors and unknown environments,
// unreadable TLS files and invalid proxies (config and Opts flags),
// script syntax errors (inline blocks, script files and the monica.js
// hooks that apply) and {{variables}} that can't be resolved. ${NAME}
// references missing from the environment and .env are warnings.
//
// Variables set at run time can't be known statically, so a variable
// counts as known when it is a column of rows (the --data file) or when
// any script of the run (request scripts, monica.js hooks, and modules in
// scripts.paths folders) contains vars.set("name" with a literal name
// (quoted with ", ' or `). Names built at run time (vars.set(key, ...))
// are not detected.
func Validate(files []string, opts *Opts, rows []Row) *Validation {
	v := &validator{
		r:        NewWithReporter(opts, nil),
		opts:     opts,
		out:      &Validation{},
		projects: map[string]*vproject{},
		runtime:  map[string]bool{},
		data:     map[string]bool{},
		scanned:  map[string]bool{},
		reported: map[string]bool{},
	}
	if err := opts.CheckTransport(); err != nil {
		v.add(Problem{Severity: SeverityError, Msg: err.Error()})
		v.badFlags = true
	}
	for _, row := range rows {
		for name := range row {
			v.data[name] = true
		}
	}

	// First pass: parse, compile scripts and collect vars.set names, so a
	// variable set by any script of the run is known to every request.
	type parsed struct {
		file string
		reqs []*request.Request
		vp   *vproject
	}
	var all []parsed
	for _, file := range files {
		v.out.Files++
		reqs, err := request.ParseMonFile(file)
		if err != nil {
			v.parseProblem(file, err)
			continue
		}
		vp := v.project(file)
		var selected []*request.Request
		for _, req := range reqs {
			if !v.r.selected(req) {
				continue
			}
			selected = append(selected, req)
			for _, s := range []*request.Script{req.PreScript, req.PostScript} {
				if s != nil {
					v.checkScript(s, s.File != file)
				}
			}
		}
		if len(selected) > 0 && vp.p != nil {
			for _, h := range v.r.hookChain(file, vp.p) {
				v.checkScriptFile(h.file)
			}
		}
		v.out.Requests += len(selected)
		all = append(all, parsed{file, selected, vp})
	}

	// Second pass: {{variables}}.
	for _, f := range all {
		if f.vp.err != nil {
			continue // settings unknown; the config problem is reported
		}
		for _, req := range f.reqs {
			if !req.Skip {
				v.checkVars(req, f.vp.p.settings)
			}
		}
	}
	return v.out
}

// newlineRe matches a line break and the indentation around it.
var newlineRe = regexp.MustCompile(`\s*\n\s*`)

// add records p once, on one line.
func (v *validator) add(p Problem) {
	p.Msg = newlineRe.ReplaceAllString(strings.TrimSpace(p.Msg), " ")
	key := p.String()
	if v.reported[key] {
		return
	}
	v.reported[key] = true
	v.out.Problems = append(v.out.Problems, p)
}

// parseProblem reports a .mon file that does not parse.
func (v *validator) parseProblem(file string, err error) {
	var pe *request.ParseError
	if errors.As(err, &pe) && pe.File != "" {
		v.add(Problem{File: pe.File, Line: pe.Line, Severity: SeverityError, Msg: pe.Msg})
		return
	}
	v.add(Problem{File: file, Severity: SeverityError, Msg: err.Error()})
}

// configProblem reports a config error, without the path prefix the
// config package puts on its messages.
func (v *validator) configProblem(path string, err error) {
	msg := err.Error()
	if path != "" {
		msg = strings.TrimPrefix(msg, path+": ")
	}
	v.add(Problem{File: path, Severity: SeverityError, Msg: msg})
}

// project loads and resolves the config for file once, reporting its
// problems.
func (v *validator) project(file string) *vproject {
	path := v.opts.ConfigPath
	if path == "" {
		var err error
		if path, err = config.Find(filepath.Dir(file)); err != nil {
			v.add(Problem{File: file, Severity: SeverityError, Msg: err.Error()})
			return &vproject{err: err}
		}
	}
	if vp, ok := v.projects[path]; ok {
		return vp
	}
	vp := &vproject{}
	v.projects[path] = vp
	if path == "" {
		if v.opts.Env != "" {
			v.add(Problem{File: file, Severity: SeverityWarning, Msg: fmt.Sprintf("no %s found; ignoring environment %q", config.FileName, v.opts.Env)})
		}
		vp.p = &project{settings: config.Builtin()}
		v.checkTransport("", vp.p.settings)
		return vp
	}
	cfg, err := config.Load(path)
	if err != nil {
		vp.err = err
		v.configProblem(path, err)
		return vp
	}
	lookupEnv := v.opts.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	var missing []string
	settings, err := cfg.Resolve(v.opts.Env, func(name string) (string, bool) {
		if val, ok := lookupEnv(name); ok {
			return val, true
		}
		if val, ok := cfg.DotEnv[name]; ok {
			return val, true
		}
		missing = append(missing, name)
		return "", true // a warning, not a failure
	})
	if err != nil {
		vp.err = err
		v.configProblem(path, err)
		return vp
	}
	for _, name := range missing {
		v.add(Problem{File: path, Line: lineOf(path, "${"+name+"}"), Severity: SeverityWarning,
			Msg: fmt.Sprintf("${%s} is not set (in the environment or %s)", name, config.DotEnvFileName)})
	}
	vp.p = &project{settings: settings, root: filepath.Dir(path), scriptPaths: cfg.Scripts.Paths}
	v.checkTransport(path, settings)
	v.scanModules(vp.p)
	return vp
}

// checkTransport reads the project's TLS files and parses its proxy, with
// the --cert/--key/--cacert/--proxy flags applied over them, without
// building a transport or dialing. Bad flags are reported once, by
// Validate, not again for every project.
func (v *validator) checkTransport(path string, s *config.Resolved) {
	if v.badFlags {
		return
	}
	tc, err := v.opts.transportConfig(s)
	if err == nil {
		err = tc.Check()
	}
	if err != nil {
		v.configProblem(path, err)
	}
}

// lineOf is the 1-based line of the first occurrence of text in file, or 0.
func lineOf(file, text string) int {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	i := strings.Index(string(data), text)
	if i < 0 {
		return 0
	}
	return strings.Count(string(data[:i]), "\n") + 1
}

// scanModules collects vars.set names from the modules in the project's
// scripts.paths folders. They are not compiled: a run may never load them.
func (v *validator) scanModules(p *project) {
	for _, dir := range p.scriptPaths {
		dir = filepath.Join(p.root, dir)
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !scriptExts[strings.ToLower(filepath.Ext(path))] || v.scanned[path] {
				return nil
			}
			v.scanned[path] = true
			if data, err := os.ReadFile(path); err == nil {
				v.scan(string(data))
			}
			return nil
		})
	}
}

// scan records the variable names src sets with vars.set.
func (v *validator) scan(src string) {
	for _, m := range setRe.FindAllStringSubmatch(src, -1) {
		v.runtime[m[1]] = true
	}
}

// checkScript compiles s and scans it for vars.set. A script file (fromFile)
// shared by several requests is checked once.
func (v *validator) checkScript(s *request.Script, fromFile bool) {
	if fromFile {
		if v.scanned[s.File] {
			return
		}
		v.scanned[s.File] = true
	}
	v.scan(s.Source)
	if err := script.Compile(s); err != nil {
		var se *script.Error
		if errors.As(err, &se) {
			v.add(Problem{File: se.File, Line: se.Line, Severity: SeverityError, Msg: se.Msg})
			return
		}
		v.add(Problem{File: s.File, Line: s.Line, Severity: SeverityError, Msg: err.Error()})
	}
}

// checkScriptFile checks a monica.js hooks file.
func (v *validator) checkScriptFile(file string) {
	if v.scanned[file] {
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		v.scanned[file] = true
		v.add(Problem{File: file, Severity: SeverityError, Msg: err.Error()})
		return
	}
	v.checkScript(&request.Script{Source: strings.ReplaceAll(string(data), "\r\n", "\n"), File: file, Line: 1}, true)
}

// checkVars expands req's {{variables}} and the config's default headers
// it uses, reporting unknown names and template syntax errors. Values set
// at run time are unknown, so URL checks are skipped for requests using
// them.
func (v *validator) checkVars(req *request.Request, s *config.Resolved) {
	var unknown []string
	seen := map[string]bool{}
	dynamic := false
	lookup := func(name string) (string, bool) {
		if val, ok := v.opts.Vars[name]; ok {
			return val, true
		}
		if v.runtime[name] || v.data[name] {
			dynamic = true
			return "http://runtime.invalid", true
		}
		if val, ok := s.Vars[name]; ok {
			return val, true
		}
		dynamic = true
		if !seen[name] {
			seen[name] = true
			unknown = append(unknown, name)
		}
		return "http://unknown.invalid", true
	}
	var expandErr error
	expand := func(text string) (string, error) {
		out, err := template.Expand(text, lookup)
		if err != nil && expandErr == nil {
			expandErr = err
		}
		return out, err
	}
	_, renderErr := req.Render(expand)
	names := make([]string, 0, len(s.Headers))
	for name := range s.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if req.Headers.Get(name) != "" {
			continue
		}
		if _, err := expand(s.Headers[name]); err != nil && renderErr == nil {
			renderErr = fmt.Errorf("default header %s: %w", name, err)
		}
	}
	for _, name := range unknown {
		v.add(Problem{File: req.File, Line: req.Line, Severity: SeverityError, Msg: fmt.Sprintf("%s: unknown variable %q", req.Label(), name)})
	}
	if renderErr == nil || (expandErr == nil && dynamic) {
		return
	}
	msg := renderErr.Error()
	var pe *request.ParseError
	if errors.As(renderErr, &pe) {
		msg = pe.Msg
	}
	v.add(Problem{File: req.File, Line: req.Line, Severity: SeverityError, Msg: req.Label() + ": " + msg})
}
