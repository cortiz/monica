package script

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/dop251/goja"
)

// TestResult is the outcome of one test() call.
type TestResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	// Error is the failure message (an assertion message, or "TypeError: ..."
	// for other exceptions); empty when Passed.
	Error string `json:"error,omitempty"`
}

// installTesting defines test() and expect() for one run; results are
// appended to e.results.
func (e *Engine) installTesting() map[string]any {
	return map[string]any{
		"test":   e.jsTest,
		"expect": e.jsExpect,
	}
}

// jsTest runs fn and records whether it threw. Uncatchable errors (timeout,
// stack overflow) are not test failures: they abort the script.
func (e *Engine) jsTest(call goja.FunctionCall) goja.Value {
	name := e.argString(call, "test", 0)
	fn, ok := goja.AssertFunction(call.Argument(1))
	if !ok {
		e.throw("test(%q): second argument must be a function", name)
	}
	_, err := fn(goja.Undefined())
	if err == nil {
		e.results = append(e.results, TestResult{Name: name, Passed: true})
		return goja.Undefined()
	}
	ex, ok := err.(*goja.Exception)
	if !ok {
		panic(err) // interrupt or stack overflow: propagate
	}
	e.results = append(e.results, TestResult{Name: name, Error: e.failureMessage(ex.Value())})
	return goja.Undefined()
}

// failureMessage is an assertion's message, or "Name: message" for other
// thrown values.
func (e *Engine) failureMessage(v goja.Value) string {
	if obj, ok := v.(*goja.Object); ok && obj.Get("name") != nil && obj.Get("name").String() == "AssertionError" {
		return obj.Get("message").String()
	}
	return v.String()
}

// fail throws an AssertionError with msg.
func (e *Engine) fail(msg string) {
	ctor := e.rt.Get("Error")
	obj, err := e.rt.New(ctor, e.rt.ToValue(msg))
	if err != nil {
		panic(err)
	}
	_ = obj.Set("name", "AssertionError")
	panic(obj)
}

// matcher checks actual against args; it returns whether it passed and the
// messages for a failure without and with .not.
type matcher func(actual goja.Value, args []goja.Value) (pass bool, msg, notMsg string)

// jsExpect returns the matcher object for actual, with .not negating each.
func (e *Engine) jsExpect(call goja.FunctionCall) goja.Value {
	actual := call.Argument(0)
	pos, neg := e.rt.NewObject(), e.rt.NewObject()
	for name, m := range e.matchers() {
		name, m := name, m
		_ = pos.Set(name, func(c goja.FunctionCall) goja.Value {
			if pass, msg, _ := m(actual, c.Arguments); !pass {
				e.fail(msg)
			}
			return goja.Undefined()
		})
		_ = neg.Set(name, func(c goja.FunctionCall) goja.Value {
			if pass, _, notMsg := m(actual, c.Arguments); pass {
				e.fail(notMsg)
			}
			return goja.Undefined()
		})
	}
	_ = pos.Set("not", neg)
	return pos
}

func arg(args []goja.Value, i int) goja.Value {
	if i < len(args) {
		return args[i]
	}
	return goja.Undefined()
}

// verdict builds the standard "expected A [not] to VERB B" messages.
func (e *Engine) verdict(pass bool, actual goja.Value, verb string, expected ...goja.Value) (bool, string, string) {
	tail := ""
	if len(expected) > 0 {
		tail = " " + e.show(expected[0])
	}
	a := e.show(actual)
	return pass, fmt.Sprintf("expected %s to %s%s", a, verb, tail), fmt.Sprintf("expected %s not to %s%s", a, verb, tail)
}

func (e *Engine) matchers() map[string]matcher {
	compare := func(verb string, ok func(a, b float64) bool) matcher {
		return func(actual goja.Value, args []goja.Value) (bool, string, string) {
			exp := arg(args, 0)
			if !isNumber(actual) || !isNumber(exp) {
				return false, fmt.Sprintf("expected %s to %s %s, but both must be numbers", e.show(actual), verb, e.show(exp)),
					fmt.Sprintf("expected %s not to %s %s, but both must be numbers", e.show(actual), verb, e.show(exp))
			}
			return e.verdict(ok(actual.ToFloat(), exp.ToFloat()), actual, verb, exp)
		}
	}
	return map[string]matcher{
		"toBe": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			return e.verdict(actual.SameAs(arg(args, 0)), actual, "be", arg(args, 0))
		},
		"toEqual": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			exp := arg(args, 0)
			pass := deepEqual(actual, exp, 0)
			return pass,
				fmt.Sprintf("expected values to be deeply equal\n  expected: %s\n  received: %s", e.show(exp), e.show(actual)),
				fmt.Sprintf("expected values not to be deeply equal\n  both: %s", e.show(actual))
		},
		"toContain": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			item := arg(args, 0)
			pass := false
			if obj, ok := actual.(*goja.Object); ok && obj.ClassName() == "Array" {
				for _, el := range arrayItems(obj) {
					if deepEqual(el, item, 0) {
						pass = true
						break
					}
				}
			} else if isString(actual) {
				pass = strings.Contains(actual.String(), item.String())
			}
			return e.verdict(pass, actual, "contain", item)
		},
		"toMatch": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			pattern := arg(args, 0)
			pass := false
			if re, ok := pattern.(*goja.Object); ok && re.ClassName() == "RegExp" {
				testFn, _ := goja.AssertFunction(re.Get("test"))
				if v, err := testFn(re, e.rt.ToValue(actual.String())); err == nil {
					pass = v.ToBoolean()
				}
			} else {
				pass = strings.Contains(actual.String(), pattern.String())
			}
			return e.verdict(pass, actual, "match", pattern)
		},
		"toBeLessThan":           compare("be less than", func(a, b float64) bool { return a < b }),
		"toBeLessThanOrEqual":    compare("be less than or equal to", func(a, b float64) bool { return a <= b }),
		"toBeGreaterThan":        compare("be greater than", func(a, b float64) bool { return a > b }),
		"toBeGreaterThanOrEqual": compare("be greater than or equal to", func(a, b float64) bool { return a >= b }),
		"toHaveProperty": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			path := arg(args, 0).String()
			v, found := property(actual, path)
			if len(args) < 2 {
				return e.verdict(found, actual, "have property", e.rt.ToValue(path))
			}
			exp := args[1]
			pass := found && deepEqual(v, exp, 0)
			return pass,
				fmt.Sprintf("expected %s to have property %q equal to %s, got %s", e.show(actual), path, e.show(exp), e.show(v)),
				fmt.Sprintf("expected %s not to have property %q equal to %s", e.show(actual), path, e.show(exp))
		},
		"toHaveLength": func(actual goja.Value, args []goja.Value) (bool, string, string) {
			exp := arg(args, 0)
			pass := false
			if !goja.IsUndefined(actual) && !goja.IsNull(actual) {
				if l := actual.ToObject(e.rt).Get("length"); l != nil && isNumber(l) {
					pass = l.ToFloat() == exp.ToFloat()
				}
			}
			return e.verdict(pass, actual, "have length", exp)
		},
		"toBeTruthy": func(actual goja.Value, _ []goja.Value) (bool, string, string) {
			return e.verdict(actual.ToBoolean(), actual, "be truthy")
		},
		"toBeFalsy": func(actual goja.Value, _ []goja.Value) (bool, string, string) {
			return e.verdict(!actual.ToBoolean(), actual, "be falsy")
		},
		"toBeNull": func(actual goja.Value, _ []goja.Value) (bool, string, string) {
			return e.verdict(goja.IsNull(actual), actual, "be null")
		},
		"toBeUndefined": func(actual goja.Value, _ []goja.Value) (bool, string, string) {
			return e.verdict(goja.IsUndefined(actual), actual, "be undefined")
		},
		"toBeDefined": func(actual goja.Value, _ []goja.Value) (bool, string, string) {
			return e.verdict(!goja.IsUndefined(actual), actual, "be defined")
		},
	}
}

// show renders a value for messages: strings quoted, objects as JSON.
func (e *Engine) show(v goja.Value) string {
	switch {
	case v == nil || goja.IsUndefined(v):
		return "undefined"
	case goja.IsNull(v):
		return "null"
	case isString(v):
		b, _ := json.Marshal(v.String())
		return string(b)
	}
	if obj, ok := v.(*goja.Object); ok && (obj.ClassName() == "RegExp" || obj.ClassName() == "Date") {
		return v.String()
	}
	return e.format(v)
}

func isString(v goja.Value) bool {
	_, ok := v.Export().(string)
	return ok && !isObject(v)
}

func isNumber(v goja.Value) bool {
	switch v.Export().(type) {
	case int64, float64:
		return !isObject(v)
	}
	return false
}

func isObject(v goja.Value) bool {
	_, ok := v.(*goja.Object)
	return ok
}

func arrayItems(obj *goja.Object) []goja.Value {
	n := int(obj.Get("length").ToInteger())
	items := make([]goja.Value, n)
	for i := 0; i < n; i++ {
		items[i] = obj.Get(fmt.Sprint(i))
	}
	return items
}

// property follows a dotted path ("a.b.0.c") of own properties from v.
func property(v goja.Value, path string) (goja.Value, bool) {
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(*goja.Object)
		if !ok || !hasKey(obj, key) {
			return goja.Undefined(), false
		}
		v = obj.Get(key)
	}
	return v, true
}

// deepEqual compares primitives with === (NaN equal to NaN) and arrays and
// plain objects structurally.
func deepEqual(a, b goja.Value, depth int) bool {
	if depth > 100 {
		return false
	}
	oa, aObj := a.(*goja.Object)
	ob, bObj := b.(*goja.Object)
	if !aObj || !bObj {
		if aObj != bObj {
			return false
		}
		if a.StrictEquals(b) {
			return true
		}
		fa, fb := a.ToFloat(), b.ToFloat()
		return isNumber(a) && isNumber(b) && math.IsNaN(fa) && math.IsNaN(fb)
	}
	if a.SameAs(b) {
		return true
	}
	_, aFn := goja.AssertFunction(a)
	_, bFn := goja.AssertFunction(b)
	if aFn || bFn {
		return false
	}
	if (oa.ClassName() == "Array") != (ob.ClassName() == "Array") {
		return false
	}
	if oa.ClassName() == "Array" {
		ia, ib := arrayItems(oa), arrayItems(ob)
		if len(ia) != len(ib) {
			return false
		}
		for i := range ia {
			if !deepEqual(ia[i], ib[i], depth+1) {
				return false
			}
		}
		return true
	}
	ka, kb := oa.Keys(), ob.Keys()
	if len(ka) != len(kb) {
		return false
	}
	for _, k := range ka {
		if !hasKey(ob, k) || !deepEqual(oa.Get(k), ob.Get(k), depth+1) {
			return false
		}
	}
	return true
}

func hasKey(obj *goja.Object, key string) bool {
	for _, k := range obj.Keys() {
		if k == key {
			return true
		}
	}
	return false
}
