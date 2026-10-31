package script

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/dop251/goja"
	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

// Vars is the variable store scripts read and write with vars.get/set/has.
type Vars interface {
	Get(name string) (string, bool)
	Set(name, value string)
}

// Context is the run state a script can see.
type Context struct {
	Env  string // monica.env; "" when no environment is selected
	Vars Vars
	// Timeout bounds each script run; zero means DefaultTimeout.
	Timeout time.Duration
}

// RunPre runs a pre-request script. Changes to req.method, req.url,
// req.headers and req.body are written back to req. It returns the test()
// results recorded by the script.
func (e *Engine) RunPre(s *request.Script, req *request.Request, ctx Context) ([]TestResult, error) {
	r := e.newReq(req, true)
	results, err := e.run(s, e.globals(ctx, r.obj, nil), ctx.Timeout)
	if err != nil {
		return results, err
	}
	return results, r.writeBack(req)
}

// RunPost runs a post-response script with res set and req read-only, and
// returns the test() results it recorded.
func (e *Engine) RunPost(s *request.Script, req *request.Request, res *net.HTTPResponse, ctx Context) ([]TestResult, error) {
	return e.run(s, e.globals(ctx, e.newReq(req, false).obj, e.newRes(res)), ctx.Timeout)
}

func (e *Engine) globals(ctx Context, req, res *goja.Object) map[string]any {
	monica := e.rt.NewObject()
	_ = monica.Set("env", ctx.Env)
	g := map[string]any{"req": req, "vars": e.newVars(ctx.Vars), "monica": monica}
	if res != nil {
		g["res"] = res
	}
	return g
}

func (e *Engine) throw(format string, args ...any) {
	panic(e.rt.NewTypeError(fmt.Sprintf(format, args...)))
}

// argString returns argument i as a string, throwing if it is missing.
func (e *Engine) argString(call goja.FunctionCall, fn string, i int) string {
	if i >= len(call.Arguments) || goja.IsUndefined(call.Arguments[i]) {
		e.throw("%s: missing argument %d", fn, i+1)
	}
	return call.Arguments[i].String()
}

type reqObject struct {
	obj     *goja.Object
	headers http.Header
}

// newReq exposes method, url and body as plain properties and headers with
// get/set/add/delete/has. Without mutable, setters throw.
func (e *Engine) newReq(req *request.Request, mutable bool) *reqObject {
	r := &reqObject{obj: e.rt.NewObject(), headers: req.Headers.Clone()}
	if r.headers == nil {
		r.headers = http.Header{}
	}
	_ = r.obj.Set("method", req.Method)
	_ = r.obj.Set("url", req.URL)
	_ = r.obj.Set("body", req.Body)

	h := e.headerReader(r.headers)
	write := func(fn string, op func(call goja.FunctionCall)) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			if !mutable {
				e.throw("req.headers.%s: req is read-only in post-response scripts", fn)
			}
			op(call)
			return goja.Undefined()
		}
	}
	_ = h.Set("set", write("set", func(c goja.FunctionCall) {
		r.headers.Set(e.argString(c, "req.headers.set", 0), e.argString(c, "req.headers.set", 1))
	}))
	_ = h.Set("add", write("add", func(c goja.FunctionCall) {
		r.headers.Add(e.argString(c, "req.headers.add", 0), e.argString(c, "req.headers.add", 1))
	}))
	_ = h.Set("delete", write("delete", func(c goja.FunctionCall) {
		r.headers.Del(e.argString(c, "req.headers.delete", 0))
	}))
	_ = r.obj.Set("headers", h)
	return r
}

// writeBack copies the script's changes into req.
func (r *reqObject) writeBack(req *request.Request) error {
	method := r.obj.Get("method").String()
	if method == "" || strings.ContainsAny(method, " \t\r\n") {
		return fmt.Errorf("pre-request script set an invalid req.method %q", method)
	}
	req.Method = method
	req.URL = r.obj.Get("url").String()
	if body := r.obj.Get("body").String(); body != req.Body {
		req.Body, req.BodyFile = body, "" // a script-set body is no longer the file's
	}
	req.Headers = r.headers
	return nil
}

// headerReader returns an object with get (values joined by ", ", null
// when absent), has and all (name -> array of values).
func (e *Engine) headerReader(h http.Header) *goja.Object {
	o := e.rt.NewObject()
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		vs := h.Values(e.argString(call, "headers.get", 0))
		if len(vs) == 0 {
			return goja.Null()
		}
		return e.rt.ToValue(strings.Join(vs, ", "))
	})
	_ = o.Set("has", func(call goja.FunctionCall) goja.Value {
		return e.rt.ToValue(len(h.Values(e.argString(call, "headers.has", 0))) > 0)
	})
	_ = o.Set("all", func(goja.FunctionCall) goja.Value {
		all := map[string]any{}
		for k, vs := range h {
			list := make([]any, len(vs))
			for i, v := range vs {
				list[i] = v
			}
			all[k] = list
		}
		return e.rt.ToValue(all)
	})
	return o
}

// newRes exposes status, statusText, headers, body, json() and time (ms).
func (e *Engine) newRes(res *net.HTTPResponse) *goja.Object {
	o := e.rt.NewObject()
	_ = o.Set("status", res.StatusCode)
	_ = o.Set("statusText", http.StatusText(res.StatusCode))
	_ = o.Set("headers", e.headerReader(res.Headers))
	_ = o.Set("body", res.Body)
	_ = o.Set("time", float64(res.Duration.Microseconds())/1000)
	_ = o.Set("json", func(goja.FunctionCall) goja.Value {
		parse, _ := goja.AssertFunction(e.rt.Get("JSON").ToObject(e.rt).Get("parse"))
		v, err := parse(goja.Undefined(), e.rt.ToValue(res.Body))
		if err != nil {
			reason := err.Error()
			if ex, ok := err.(*goja.Exception); ok {
				reason = ex.Value().String() // without the native stack
			}
			e.throw("res.json(): body is not valid JSON (%s)", reason)
		}
		return v
	})
	return o
}

var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// newVars exposes get (undefined when unset), set (value converted to a
// string) and has.
func (e *Engine) newVars(vars Vars) *goja.Object {
	o := e.rt.NewObject()
	_ = o.Set("get", func(call goja.FunctionCall) goja.Value {
		if v, ok := vars.Get(e.argString(call, "vars.get", 0)); ok {
			return e.rt.ToValue(v)
		}
		return goja.Undefined()
	})
	_ = o.Set("has", func(call goja.FunctionCall) goja.Value {
		_, ok := vars.Get(e.argString(call, "vars.has", 0))
		return e.rt.ToValue(ok)
	})
	_ = o.Set("set", func(call goja.FunctionCall) goja.Value {
		name := e.argString(call, "vars.set", 0)
		if !varNameRe.MatchString(name) {
			e.throw("vars.set: invalid variable name %q", name)
		}
		vars.Set(name, e.argString(call, "vars.set", 1))
		return goja.Undefined()
	})
	return o
}
