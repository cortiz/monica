package script

import (
	"regexp"
	"strings"
	"testing"
)

func TestBuiltinModules(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string // exact console output, or a regexp when wantRe
		re   bool
	}{
		// monica/crypto
		{"sha256 hex", `console.log(require("monica/crypto").sha256("abc"))`, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", false},
		{"sha256 base64", `console.log(require("monica/crypto").sha256("abc", "base64"))`, "ungWv48Bz+pBQUDeXa4iI7ADYaOWF3qctBD/YfIAFa0=", false},
		{"sha256 base64url", `console.log(require("monica/crypto").sha256("abc", "base64url"))`, "ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0", false},
		{"md5", `console.log(require("monica/crypto").md5("abc"))`, "900150983cd24fb0d6963f7d28e17f72", false},
		{"sha1", `console.log(require("monica/crypto").sha1("abc"))`, "a9993e364706816aba3e25717850c26c9cd0d89d", false},
		{"sha512", `console.log(require("monica/crypto").sha512("abc").slice(0, 16))`, "ddaf35a193617aba", false},
		{"hmac sha256", `console.log(require("monica/crypto").hmac("sha256", "key", "The quick brown fox jumps over the lazy dog"))`, "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8", false},
		{"hmac algorithm is case-insensitive", `const c = require("monica/crypto"); console.log(c.hmac("SHA256", "k", "d") === c.hmac("sha256", "k", "d"))`, "true", false},
		// monica/encoding
		{"base64 round trip (UTF-8)", `const e = require("monica/encoding"); const b = e.base64Encode("héllo?"); console.log(b, e.base64Decode(b))`, "aMOpbGxvPw== héllo?", false},
		{"base64Decode without padding", `console.log(require("monica/encoding").base64Decode("aMOpbGxvPw"))`, "héllo?", false},
		{"base64url", `const e = require("monica/encoding"); console.log(e.base64UrlEncode("ûÿ?>"), e.base64UrlDecode(e.base64UrlEncode("a?b")))`, "w7vDvz8- a?b", false},
		{"base64UrlDecode with padding", `console.log(require("monica/encoding").base64UrlDecode("YQ=="))`, "a", false},
		{"hex", `const e = require("monica/encoding"); console.log(e.hexEncode("hi"), e.hexDecode("6869"))`, "6869 hi", false},
		{"url", `const e = require("monica/encoding"); console.log(e.urlEncode("a b&c=d/é"), e.urlDecode("a+b%26c"))`, "a+b%26c%3Dd%2F%C3%A9 a b&c", false},
		// monica/random
		{"uuid v4", `console.log(require("monica/random").uuid())`, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`, true},
		{"int in range", `const r = require("monica/random"); let ok = true; for (let i = 0; i < 200; i++) { const n = r.int(-2, 2); ok = ok && Number.isInteger(n) && n >= -2 && n <= 2 } console.log(ok, r.int(7, 7))`, "true 7", false},
		{"string default alphabet", `console.log(require("monica/random").string(24))`, `^[A-Za-z0-9]{24}$`, true},
		{"string custom alphabet", `console.log(require("monica/random").string(5, "é"))`, "ééééé", false},
		// monica/jsonpath
		{"jsonpath value", `console.log(require("monica/jsonpath").value({a: {b: [1, {c: "x"}]}}, "$.a.b[1].c"))`, "x", false},
		{"jsonpath parses JSON strings", `console.log(require("monica/jsonpath").value('{"users": [{"id": 7}]}', "$.users[0].id"))`, "7", false},
		{"jsonpath no match is undefined", `console.log(require("monica/jsonpath").value({}, "$.missing"))`, "undefined", false},
		{"jsonpath returns the same objects", `const o = {a: {b: 1}}; console.log(require("monica/jsonpath").value(o, "$.a") === o.a)`, "true", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, out := newModEngine(t)
			if _, err := e.run(modSrc(tt.code), nil, 0); err != nil {
				t.Fatal(err)
			}
			if len(*out) != 1 {
				t.Fatalf("got %v", *out)
			}
			got := (*out)[0].msg
			if tt.re && !regexp.MustCompile(tt.want).MatchString(got) || !tt.re && got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuiltinModuleErrors(t *testing.T) {
	tests := []struct{ code, want string }{
		{`require("monica/crypto").sha256()`, "TypeError: sha256: missing argument 1"},
		{`require("monica/crypto").sha256("x", "base32")`, `sha256: unknown encoding "base32"`},
		{`require("monica/crypto").hmac("sha3", "k", "d")`, `hmac: unknown algorithm "sha3"`},
		{`require("monica/crypto").hmac("sha256", "k")`, "hmac: missing argument 3"},
		{`require("monica/encoding").base64Decode("***")`, "base64Decode: illegal base64 data"},
		{`require("monica/encoding").hexDecode("zz")`, "hexDecode: encoding/hex: invalid byte"},
		{`require("monica/encoding").urlDecode("%zz")`, "urlDecode: invalid URL escape"},
		{`require("monica/random").int(5, 1)`, "int: min 5 is greater than max 1"},
		{`require("monica/random").int(1.5, 2)`, "int: argument 1 must be a safe integer, got 1.5"},
		{`require("monica/random").string(-1)`, "string: length must be between 0 and"},
		{`require("monica/random").string(3, "")`, "string: alphabet is empty"},
		{`require("monica/jsonpath").query({})`, "query: expected (value, path)"},
		{`require("monica/jsonpath").query("{oops", "$")`, "query: value is a string but not valid JSON"},
		{`require("monica/jsonpath").query({}, "$[?(@.a)]")`, "filter and script expressions are not supported"},
		{`require("monica/nope")`, `require("monica/nope"): cannot find module`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			e, _ := newModEngine(t)
			_, err := e.run(modSrc(tt.code), nil, 0)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}
