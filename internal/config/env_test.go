package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeEnv is a LookupFunc over a fixed map.
func fakeEnv(vars map[string]string) LookupFunc {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestResolveProject(t *testing.T) {
	c, err := Load("testdata/project/monica.yml")
	if err != nil {
		t.Fatal(err)
	}
	baseHeaders := map[string]string{"User-Agent": "monica-test", "X-Team": "core"}
	tests := []struct {
		name string
		env  string
		os   map[string]string
		want *Resolved
	}{
		{
			name: "no environment: built-ins + defaults + local defaults",
			want: &Resolved{
				Timeout:         20 * time.Second,
				FollowRedirects: true,
				Headers:         baseHeaders,
				Vars:            map[string]string{"apiVersion": "v2", "region": "local-eu"},
			},
		},
		{
			name: "dev: OS environment wins over .env",
			env:  "dev",
			os:   map[string]string{"DEV_TOKEN": "tok-from-os"},
			want: &Resolved{
				Env:             "dev",
				Timeout:         20 * time.Second,
				FollowRedirects: false,
				Headers:         map[string]string{"User-Agent": "monica-test", "X-Team": "core", "Authorization": "Bearer tok-from-os"},
				Vars: map[string]string{
					"apiVersion": "v2", "region": "local-eu",
					"baseUrl": "http://127.0.0.1:9000", // from monica.local.yml
					"pass":    "s3cr#t",                // from .env, quoted
				},
				Secrets: []string{"tok-from-os", "s3cr#t"},
			},
		},
		{
			name: "dev: .env fills what the OS environment lacks",
			env:  "dev",
			want: &Resolved{
				Env:             "dev",
				Timeout:         20 * time.Second,
				FollowRedirects: false,
				Headers:         map[string]string{"User-Agent": "monica-test", "X-Team": "core", "Authorization": "Bearer tok-from-dotenv"},
				Vars: map[string]string{
					"apiVersion": "v2", "region": "local-eu",
					"baseUrl": "http://127.0.0.1:9000", "pass": "s3cr#t",
				},
				Secrets: []string{"tok-from-dotenv", "s3cr#t"},
			},
		},
		{
			name: "prod: env values beat defaults, including local defaults",
			env:  "prod",
			want: &Resolved{
				Env:             "prod",
				Timeout:         10 * time.Second,
				FollowRedirects: true,
				Headers:         baseHeaders,
				Vars:            map[string]string{"apiVersion": "v2", "region": "us", "baseUrl": "https://api.example.com"},
			},
		},
		{
			name: "environment defined only in monica.local.yml",
			env:  "mine",
			want: &Resolved{
				Env:             "mine",
				Timeout:         20 * time.Second,
				FollowRedirects: true,
				Headers:         baseHeaders,
				Vars:            map[string]string{"apiVersion": "v2", "region": "local-eu", "baseUrl": "http://mine.local"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.Resolve(tt.env, fakeEnv(tt.os))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		name    string
		yml     string
		env     string
		wantErr string
	}{
		{"unknown environment", "version: 1\nenvs:\n  dev: {}\n  prod: {}\n", "qa", "unknown environment \"qa\" (defined: dev, prod)"},
		{"no environments defined", "version: 1\n", "dev", "unknown environment \"dev\" (no environments defined)"},
		{"unset reference", "version: 1\ndefaults:\n  vars:\n    x: a-${NOPE}-b\n", "", "vars.x: ${NOPE} is not set"},
		{"unset reference in header", "version: 1\nenvs:\n  dev:\n    headers:\n      X-Key: ${NOPE}\n", "dev", "headers.X-Key: ${NOPE} is not set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Load(writeFile(t, t.TempDir(), FileName, tt.yml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Resolve(tt.env, fakeEnv(nil))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestResolveExpansion(t *testing.T) {
	path := writeFile(t, t.TempDir(), FileName, "version: 1\ndefaults:\n  vars:\n    two: ${A}:${B}\n    literal: $A {A} $${A\n    empty: x${EMPTY}y\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve("", fakeEnv(map[string]string{"A": "1", "B": "2", "EMPTY": ""}))
	if err != nil {
		t.Fatal(err)
	}
	wantVars := map[string]string{"two": "1:2", "literal": "$A {A} $${A", "empty": "xy"}
	if !reflect.DeepEqual(got.Vars, wantVars) {
		t.Fatalf("vars = %v, want %v", got.Vars, wantVars)
	}
	// Empty values are never secrets (masking "" would be meaningless).
	if want := []string{"1", "2"}; !reflect.DeepEqual(got.Secrets, want) {
		t.Fatalf("secrets = %v, want %v", got.Secrets, want)
	}
}

func TestBuiltinWithoutDefaults(t *testing.T) {
	c, err := Load(writeFile(t, t.TempDir(), FileName, "version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Resolve("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Builtin()) {
		t.Fatalf("got %+v, want built-ins %+v", got, Builtin())
	}
}

func TestLocalAndDotEnvErrors(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{"local with bad version", map[string]string{LocalFileName: "version: 3\n"}, "monica.local.yml: unsupported version 3"},
		{"local with unknown key", map[string]string{LocalFileName: "defaults:\n  nope: 1\n"}, "monica.local.yml: yaml: unmarshal errors:\n  line 2: field nope not found"},
		{"bad .env line", map[string]string{DotEnvFileName: "OK=1\nnot a pair\n"}, ".env:2: invalid line"},
		{"bad .env key", map[string]string{DotEnvFileName: "1BAD=x\n"}, ".env:1: invalid line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, FileName, "version: 1\n")
			for name, content := range tt.files {
				writeFile(t, dir, name, content)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseDotEnv(t *testing.T) {
	data := strings.Join([]string{
		"# comment",
		"",
		"PLAIN=value",
		"  SPACED  =  padded  ",
		"export EXPORTED=yes",
		`DOUBLE="has # hash and spaces "`,
		`SINGLE='$NOT_EXPANDED'`,
		"TRAILING=v # comment",
		"HASH=a#b",
		"EMPTY=",
		"EQUALS=a=b",
		`HALF="unclosed`,
	}, "\n")
	got, err := parseDotEnv(".env", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"PLAIN": "value", "SPACED": "padded", "EXPORTED": "yes",
		"DOUBLE": "has # hash and spaces ", "SINGLE": "$NOT_EXPANDED",
		"TRAILING": "v", "HASH": "a#b", "EMPTY": "", "EQUALS": "a=b", "HALF": `"unclosed`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestLocalOverridesOrder(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, FileName, "version: 1\norder: [a.mon, b.mon]\n")
	writeFile(t, dir, LocalFileName, "order: [b.mon]\n")
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"b.mon"}; !reflect.DeepEqual(c.Order, want) {
		t.Fatalf("Order = %v, want %v", c.Order, want)
	}
}
