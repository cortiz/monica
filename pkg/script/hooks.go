package script

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dop251/goja"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

// HookFileName is the per-folder hooks module.
const HookFileName = "monica.js"

// Hook names a monica.js export.
type Hook string

const (
	BeforeAll  Hook = "beforeAll"
	BeforeEach Hook = "beforeEach"
	AfterEach  Hook = "afterEach"
	AfterAll   Hook = "afterAll"
)

var allHooks = []Hook{BeforeAll, BeforeEach, AfterEach, AfterAll}

// Hooks are the hook functions a monica.js module exports.
type Hooks struct {
	file  string // as given to LoadHooks, for messages
	abs   string // module path, as goja names its frames
	funcs map[Hook]goja.Callable
}

// Has reports whether the module exports hook h.
func (h *Hooks) Has(hook Hook) bool { return h.funcs[hook] != nil }

// LoadHooks loads the monica.js module at file through require() (so it
// may require other modules and is the same instance scripts get) and
// checks its exports. Its top-level code runs with vars and monica.
func (e *Engine) LoadHooks(file string, ctx Context) (*Hooks, error) {
	if e.require == nil {
		return nil, fmt.Errorf("%s: hooks need require() (no Modules configured)", file)
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	h := &Hooks{file: file, abs: abs, funcs: map[Hook]goja.Callable{}}
	var exports goja.Value
	_, err = e.exec(h.script(), e.allGlobals(ctx), ctx.Timeout, func() error {
		v, err := e.require.Require(abs)
		if err != nil && !isJSError(err) {
			return fmt.Errorf("%s", moduleErrorText(err))
		}
		exports = v
		return err
	})
	if err != nil {
		return nil, h.locate(err)
	}
	obj, ok := exports.(*goja.Object)
	if !ok {
		return nil, fmt.Errorf("%s: module.exports must be an object of hook functions", file)
	}
	for _, name := range allHooks {
		v := obj.Get(string(name))
		if v == nil || goja.IsUndefined(v) {
			continue
		}
		fn, ok := goja.AssertFunction(v)
		if !ok {
			return nil, fmt.Errorf("%s: %s must be a function", file, name)
		}
		h.funcs[name] = fn
	}
	return h, nil
}

// RunAll runs beforeAll or afterAll (if exported) with vars, monica,
// console, require and expect; test() is not available there.
func (e *Engine) RunAll(h *Hooks, hook Hook, ctx Context) error {
	if !h.Has(hook) {
		return nil
	}
	g := e.allGlobals(ctx)
	g["test"] = func(goja.FunctionCall) goja.Value {
		e.throw("test() is not available in %s; use expect() to fail the hook", hook)
		return nil
	}
	_, err := e.exec(h.script(), g, ctx.Timeout, func() error {
		_, err := h.funcs[hook](goja.Undefined())
		return err
	})
	return h.locate(err)
}

// RunBeforeEach runs beforeEach (if exported) like a pre-request script:
// changes to req are written back. It returns the test() results.
func (e *Engine) RunBeforeEach(h *Hooks, req *request.Request, ctx Context) ([]TestResult, error) {
	if !h.Has(BeforeEach) {
		return nil, nil
	}
	r := e.newReq(req, true)
	results, err := e.exec(h.script(), e.globals(ctx, r.obj, nil), ctx.Timeout, func() error {
		_, err := h.funcs[BeforeEach](goja.Undefined())
		return err
	})
	if err != nil {
		return results, h.locate(err)
	}
	return results, r.writeBack(req)
}

// RunAfterEach runs afterEach (if exported) like a post-response script.
func (e *Engine) RunAfterEach(h *Hooks, req *request.Request, res *net.HTTPResponse, ctx Context) ([]TestResult, error) {
	if !h.Has(AfterEach) {
		return nil, nil
	}
	results, err := e.exec(h.script(), e.globals(ctx, e.newReq(req, false).obj, e.newRes(res)), ctx.Timeout, func() error {
		_, err := h.funcs[AfterEach](goja.Undefined())
		return err
	})
	return results, h.locate(err)
}

// allGlobals are the globals of module top-level code and the *All hooks.
func (e *Engine) allGlobals(ctx Context) map[string]any {
	g := e.globals(ctx, nil, nil)
	delete(g, "req")
	return g
}

// script stands for the hook module in error locations: goja names the
// module's frames by its absolute path.
func (h *Hooks) script() *request.Script { return &request.Script{File: h.abs, Line: 1} }

// locate reports script errors at the hook file as given, not its
// absolute path.
func (h *Hooks) locate(err error) error {
	var se *Error
	if errors.As(err, &se) && se.File == h.abs {
		se.File = h.file
		se.Column = moduleColumn(se.Line, se.Column)
	}
	return err
}

// isJSError reports whether err comes from running JS (exception, timeout,
// stack overflow) rather than from loading the module.
func isJSError(err error) bool {
	var (
		ex   *goja.Exception
		intr *goja.InterruptedError
		so   *goja.StackOverflowError
	)
	return errors.As(err, &ex) || errors.As(err, &intr) || errors.As(err, &so)
}
