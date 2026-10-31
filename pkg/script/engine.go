// Package script runs the JavaScript attached to requests with goja.
package script

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dop251/goja"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/request"
)

// ConsoleFunc receives console output: level is "log", "info", "warn" or
// "error".
type ConsoleFunc func(level, msg string)

// Options configure an Engine.
type Options struct {
	// Console receives console.* output; nil logs it.
	Console ConsoleFunc
}

// Engine runs scripts in one goja runtime. A runtime is not safe for
// concurrent use: use one Engine per goroutine.
//
// The sandbox is plain ECMAScript plus the globals the engine installs
// (console, and per run req, res, vars, monica, test, expect); there is no
// require, file system, network or process access.
type Engine struct {
	rt       *goja.Runtime
	programs map[*request.Script]*goja.Program
	console  ConsoleFunc
	results  []TestResult // test() outcomes of the current run
}

// maxCallStack bounds JS recursion so runaway scripts fail fast with a
// RangeError instead of growing until the timeout.
const maxCallStack = 10000

// New returns an Engine with console installed.
func New(opts Options) *Engine {
	rt := goja.New()
	rt.SetMaxCallStackSize(maxCallStack)
	e := &Engine{rt: rt, programs: map[*request.Script]*goja.Program{}, console: opts.Console}
	if e.console == nil {
		e.console = logConsole
	}
	e.installConsole()
	return e
}

func logConsole(level, msg string) {
	switch level {
	case "warn":
		logging.Log.Warn().Msg(msg)
	case "error":
		logging.Log.Error().Msg(msg)
	default:
		logging.Log.Info().Msg(msg)
	}
}

func (e *Engine) installConsole() {
	console := e.rt.NewObject()
	for _, level := range []string{"log", "info", "warn", "error"} {
		level := level
		_ = console.Set(level, func(call goja.FunctionCall) goja.Value {
			parts := make([]string, len(call.Arguments))
			for i, arg := range call.Arguments {
				parts[i] = e.format(arg)
			}
			e.console(level, strings.Join(parts, " "))
			return goja.Undefined()
		})
	}
	_ = e.rt.Set("console", console)
}

// format renders a console argument: objects as JSON, the rest as String().
func (e *Engine) format(v goja.Value) string {
	if obj, ok := v.(*goja.Object); ok {
		if _, isFunc := goja.AssertFunction(obj); !isFunc {
			if b, err := json.Marshal(obj.Export()); err == nil {
				return string(b)
			}
		}
	}
	return v.String()
}

// compile compiles s once. The source is prefixed with blank lines so goja's
// line numbers are the script's lines in its file, and wrapped in a function
// so top-level declarations don't leak between scripts (the wrapper sits on
// the script's first line and only shifts that line's columns).
func (e *Engine) compile(s *request.Script) (*goja.Program, error) {
	if p, ok := e.programs[s]; ok {
		return p, nil
	}
	pad := ""
	if s.Line > 1 {
		pad = strings.Repeat("\n", s.Line-1)
	}
	name := s.File
	if name == "" {
		name = "script"
	}
	p, err := goja.Compile(name, pad+"(function(){"+s.Source+"\n})();", false)
	if err != nil {
		return nil, err
	}
	e.programs[s] = p
	return p, nil
}

// run executes s with globals (plus test and expect) visible for this run
// only, interrupting it after timeout (DefaultTimeout when zero). It returns
// the test() results recorded before it finished; failures are *Error.
func (e *Engine) run(s *request.Script, globals map[string]any, timeout time.Duration) ([]TestResult, error) {
	e.results = nil
	prog, err := e.compile(s)
	if err != nil {
		return nil, toError(s, err)
	}
	all := e.installTesting()
	for name, v := range globals {
		all[name] = v
	}
	globals = all
	for name, v := range globals {
		if err := e.rt.Set(name, v); err != nil {
			return nil, err
		}
	}
	defer func() {
		for name := range globals {
			_ = e.rt.GlobalObject().Delete(name)
		}
	}()
	stop := e.interruptAfter(timeout)
	_, err = e.rt.RunProgram(prog)
	stop()
	return e.results, toError(s, err)
}
