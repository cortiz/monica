package script

import (
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // offered for APIs that still sign with it
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // offered for APIs that still sign with it
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"math/big"
	"net/url"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// registerBuiltins makes the monica/* modules available to require().
func registerBuiltins(reg *require.Registry) {
	reg.RegisterNativeModule("monica/crypto", loadCrypto)
	reg.RegisterNativeModule("monica/encoding", loadEncoding)
	reg.RegisterNativeModule("monica/random", loadRandom)
	reg.RegisterNativeModule("monica/jsonpath", loadJSONPath)
}

// native wraps a Go function as a module export; returned errors throw a
// TypeError named after the function.
func native(rt *goja.Runtime, name string, fn func(call goja.FunctionCall) (any, error)) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		v, err := fn(call)
		if err != nil {
			panic(rt.NewTypeError(fmt.Sprintf("%s: %v", name, err)))
		}
		if gv, ok := v.(goja.Value); ok {
			return gv
		}
		return rt.ToValue(v)
	}
}

// strArg returns argument i as a string; a missing (or undefined) one is
// an error unless def is given.
func strArg(call goja.FunctionCall, i int, def ...string) (string, error) {
	if i >= len(call.Arguments) || goja.IsUndefined(call.Arguments[i]) {
		if len(def) > 0 {
			return def[0], nil
		}
		return "", fmt.Errorf("missing argument %d", i+1)
	}
	return call.Arguments[i].String(), nil
}

// intArg returns argument i as an integer.
func intArg(call goja.FunctionCall, i int) (int64, error) {
	if i >= len(call.Arguments) || goja.IsUndefined(call.Arguments[i]) {
		return 0, fmt.Errorf("missing argument %d", i+1)
	}
	f := call.Arguments[i].ToFloat()
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<53 {
		return 0, fmt.Errorf("argument %d must be a safe integer, got %s", i+1, call.Arguments[i])
	}
	return int64(f), nil
}

// --- monica/crypto ---

var hashes = map[string]func() hash.Hash{
	"md5": md5.New, "sha1": sha1.New, "sha256": sha256.New, "sha512": sha512.New,
}

// digestEncode renders a digest as hex (default), base64 or base64url.
func digestEncode(sum []byte, enc string) (string, error) {
	switch enc {
	case "hex":
		return hex.EncodeToString(sum), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(sum), nil
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(sum), nil
	}
	return "", fmt.Errorf("unknown encoding %q (expected hex, base64 or base64url)", enc)
}

func loadCrypto(rt *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)
	for alg, newHash := range hashes {
		alg, newHash := alg, newHash
		_ = exports.Set(alg, native(rt, alg, func(call goja.FunctionCall) (any, error) {
			data, err := strArg(call, 0)
			if err != nil {
				return nil, err
			}
			enc, _ := strArg(call, 1, "hex")
			h := newHash()
			h.Write([]byte(data))
			return digestEncode(h.Sum(nil), enc)
		}))
	}
	_ = exports.Set("hmac", native(rt, "hmac", func(call goja.FunctionCall) (any, error) {
		alg, err := strArg(call, 0)
		if err != nil {
			return nil, err
		}
		newHash, ok := hashes[strings.ToLower(alg)]
		if !ok {
			return nil, fmt.Errorf("unknown algorithm %q (expected md5, sha1, sha256 or sha512)", alg)
		}
		key, err := strArg(call, 1)
		if err != nil {
			return nil, err
		}
		data, err := strArg(call, 2)
		if err != nil {
			return nil, err
		}
		enc, _ := strArg(call, 3, "hex")
		mac := hmac.New(newHash, []byte(key))
		mac.Write([]byte(data))
		return digestEncode(mac.Sum(nil), enc)
	}))
}

// --- monica/encoding ---

func loadEncoding(rt *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)
	fns := map[string]func(string) (string, error){
		"base64Encode":    func(s string) (string, error) { return base64.StdEncoding.EncodeToString([]byte(s)), nil },
		"base64Decode":    decodeWith(base64.StdEncoding, base64.RawStdEncoding),
		"base64UrlEncode": func(s string) (string, error) { return base64.RawURLEncoding.EncodeToString([]byte(s)), nil },
		"base64UrlDecode": decodeWith(base64.RawURLEncoding, base64.URLEncoding),
		"hexEncode":       func(s string) (string, error) { return hex.EncodeToString([]byte(s)), nil },
		"hexDecode": func(s string) (string, error) {
			b, err := hex.DecodeString(s)
			return string(b), err
		},
		"urlEncode": func(s string) (string, error) { return url.QueryEscape(s), nil },
		"urlDecode": url.QueryUnescape,
	}
	for name, fn := range fns {
		name, fn := name, fn
		_ = exports.Set(name, native(rt, name, func(call goja.FunctionCall) (any, error) {
			s, err := strArg(call, 0)
			if err != nil {
				return nil, err
			}
			return fn(s)
		}))
	}
}

// decodeWith tries each encoding in turn (padded and unpadded variants).
func decodeWith(encs ...*base64.Encoding) func(string) (string, error) {
	return func(s string) (string, error) {
		var err error
		for _, enc := range encs {
			var b []byte
			if b, err = enc.DecodeString(s); err == nil {
				return string(b), nil
			}
		}
		return "", err
	}
}

// --- monica/random ---

const alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randInt returns a uniform random integer in [0, n).
func randInt(n int64) (int64, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0, err
	}
	return v.Int64(), nil
}

func loadRandom(rt *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)
	_ = exports.Set("uuid", native(rt, "uuid", func(goja.FunctionCall) (any, error) {
		var b [16]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		b[6] = b[6]&0x0f | 0x40 // version 4
		b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
		h := hex.EncodeToString(b[:])
		return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
	}))
	_ = exports.Set("int", native(rt, "int", func(call goja.FunctionCall) (any, error) {
		lo, err := intArg(call, 0)
		if err != nil {
			return nil, err
		}
		hi, err := intArg(call, 1)
		if err != nil {
			return nil, err
		}
		if lo > hi {
			return nil, fmt.Errorf("min %d is greater than max %d", lo, hi)
		}
		n, err := randInt(hi - lo + 1)
		return lo + n, err
	}))
	_ = exports.Set("string", native(rt, "string", func(call goja.FunctionCall) (any, error) {
		n, err := intArg(call, 0)
		if err != nil {
			return nil, err
		}
		if n < 0 || n > 1<<20 {
			return nil, fmt.Errorf("length must be between 0 and %d, got %d", 1<<20, n)
		}
		alphabet, _ := strArg(call, 1, alphanumeric)
		chars := []rune(alphabet)
		if len(chars) == 0 {
			return nil, fmt.Errorf("alphabet is empty")
		}
		out := make([]rune, n)
		for i := range out {
			j, err := randInt(int64(len(chars)))
			if err != nil {
				return nil, err
			}
			out[i] = chars[j]
		}
		return string(out), nil
	}))
}

// --- monica/jsonpath ---

func loadJSONPath(rt *goja.Runtime, module *goja.Object) {
	exports := module.Get("exports").(*goja.Object)
	// query returns every match, value the first (undefined when none).
	// A string argument is parsed as JSON first.
	run := func(name string, first bool) func(goja.FunctionCall) goja.Value {
		return native(rt, name, func(call goja.FunctionCall) (any, error) {
			if len(call.Arguments) < 2 {
				return nil, fmt.Errorf("expected (value, path)")
			}
			doc := call.Arguments[0]
			if goja.IsUndefined(doc) {
				return nil, fmt.Errorf("value is undefined")
			}
			if _, isStr := doc.Export().(string); isStr {
				parse, _ := goja.AssertFunction(rt.Get("JSON").ToObject(rt).Get("parse"))
				v, err := parse(goja.Undefined(), doc)
				if err != nil {
					return nil, fmt.Errorf("value is a string but not valid JSON")
				}
				doc = v
			}
			matches, err := jsonPath(rt, doc, call.Arguments[1].String())
			if err != nil {
				return nil, err
			}
			if first {
				if len(matches) == 0 {
					return goja.Undefined(), nil
				}
				return matches[0], nil
			}
			items := make([]any, len(matches))
			for i, m := range matches {
				items[i] = m
			}
			return rt.NewArray(items...), nil
		})
	}
	_ = exports.Set("query", run("query", false))
	_ = exports.Set("value", run("value", true))
}
