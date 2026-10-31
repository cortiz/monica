package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/script"
)

// hookFile is a folder's monica.js and its state in this run.
type hookFile struct {
	file   string // path as found, for messages
	engine *script.Engine
	p      *project
	hooks  *script.Hooks // nil until loaded, or when loading failed
	err    error         // why loading or beforeAll failed
}

// failed reports whether the hook file can't be used this run.
func (h *hookFile) failed() bool { return h.err != nil }

// hookChain returns the monica.js files that apply to file, outermost
// first: one in each folder from the project root (the .mon file's folder
// without a config) down to the file's folder. Files are looked up once.
func (r *Runner) hookChain(file string, p *project) []*hookFile {
	dir := filepath.Dir(file)
	root := p.root
	if root == "" {
		root = dir
	}
	key := dir + "\x00" + root
	if chain, ok := r.chains[key]; ok {
		return chain
	}
	absRoot, err1 := filepath.Abs(root)
	absDir, err2 := filepath.Abs(dir)
	var chain []*hookFile
	if err1 == nil && err2 == nil && within(absDir, absRoot) {
		for d, abs := dir, absDir; ; d, abs = filepath.Join(d, ".."), filepath.Dir(abs) {
			if h := r.hookFileAt(filepath.Join(d, script.HookFileName), filepath.Join(abs, script.HookFileName), file, p); h != nil {
				chain = append([]*hookFile{h}, chain...)
			}
			if abs == absRoot || abs == filepath.Dir(abs) {
				break
			}
		}
	}
	r.chains[key] = chain
	return chain
}

// hookFileAt returns the hook file at abs (named file in messages), or
// nil if there is none.
func (r *Runner) hookFileAt(file, abs, monFile string, p *project) *hookFile {
	if h, ok := r.hookFiles[abs]; ok {
		return h
	}
	var h *hookFile
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		h = &hookFile{file: filepath.Clean(file), engine: r.scripts(monFile, p), p: p}
	}
	r.hookFiles[abs] = h
	return h
}

// within reports whether the absolute path dir is root or below it.
func within(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// enterHooks loads each hook file of chain on its first use and runs its
// beforeAll. A failure is reported once, as its own result, and returned;
// the hook file then stays failed for the rest of the run.
func (r *Runner) enterHooks(chain []*hookFile) error {
	var errs []error
	for _, h := range chain {
		if h.hooks != nil || h.failed() {
			continue
		}
		r.mask = h.p.mask
		ctx := r.hookContext(h.p)
		hooks, err := h.engine.LoadHooks(h.file, ctx)
		stage := "loading hooks"
		if err == nil {
			stage = string(script.BeforeAll)
			err = h.engine.RunAll(hooks, script.BeforeAll, ctx)
		}
		if err != nil {
			h.err = err
			errs = append(errs, r.hookFailure(h, stage, err))
			continue
		}
		h.hooks = hooks
		r.entered = append(r.entered, h)
	}
	return errors.Join(errs...)
}

// blockedBy returns the first failed hook file of chain, or nil.
func blockedBy(chain []*hookFile) *hookFile {
	for _, h := range chain {
		if h.failed() {
			return h
		}
	}
	return nil
}

// AfterAll runs the afterAll hook of every monica.js used in the run,
// innermost and most recently entered first, reporting each failure as
// its own result. It runs once; later calls (and Finish) do nothing.
func (r *Runner) AfterAll() error {
	entered := r.entered
	r.entered = nil
	var errs []error
	for i := len(entered) - 1; i >= 0; i-- {
		h := entered[i]
		r.mask = h.p.mask
		if err := h.engine.RunAll(h.hooks, script.AfterAll, r.hookContext(h.p)); err != nil {
			errs = append(errs, r.hookFailure(h, string(script.AfterAll), err))
		}
	}
	return errors.Join(errs...)
}

// hookFailure emits a failed result for a hook file and returns the error.
func (r *Runner) hookFailure(h *hookFile, stage string, err error) error {
	line := 1
	var se *script.Error
	if errors.As(err, &se) && se.Line > 0 {
		line = se.Line
	}
	r.emit(report.Request{File: h.file, Line: line, Name: stage, Error: h.p.mask.String(err.Error())})
	return h.p.mask.Error(fmt.Errorf("%s: %s: %w", h.file, stage, err))
}

func (r *Runner) hookContext(p *project) script.Context {
	return script.Context{Env: p.settings.Env, Iteration: r.iteration, VU: r.opts.VU, Vars: &runVars{r: r, s: p.settings}, Timeout: p.scriptTimeout}
}
