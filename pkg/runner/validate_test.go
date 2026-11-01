package runner

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	dir := filepath.Join("testdata", "validate")
	in := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	noEnv := func(string) (string, bool) { return "", false }

	tests := []struct {
		name         string
		files        []string
		opts         Opts
		rows         []Row
		wantRequests int
		wantErrors   int
		want         []string // substrings of the problem lines, in order
	}{
		{name: "good project", files: []string{in("good", "api.mon")}, wantRequests: 2},
		{name: "good project, staging", files: []string{in("good", "api.mon")}, opts: Opts{Env: "staging"}, wantRequests: 2},
		{name: "unknown environment", files: []string{in("good", "api.mon")}, opts: Opts{Env: "nope"}, wantRequests: 2, wantErrors: 1,
			want: []string{`good/monica.yml: unknown environment "nope" (defined: staging)`}},
		{name: "bad syntax", files: []string{in("bad_syntax", "bad.mon")}, wantErrors: 1,
			want: []string{`bad_syntax/bad.mon:2: invalid request line "GET"`}},
		{name: "unknown variables", files: []string{in("unknown_var", "req.mon")}, wantRequests: 1, wantErrors: 2,
			want: []string{`unknown_var/req.mon:2: getUser: unknown variable "userId"`, `unknown_var/req.mon:2: getUser: unknown variable "token"`}},
		{name: "variables from --var and --data", files: []string{in("unknown_var", "req.mon")}, wantRequests: 1,
			opts: Opts{Vars: map[string]string{"token": "t"}}, rows: []Row{{"userId": "1"}}},
		{name: "variable set by vars.set in another file", files: []string{in("var_set", "login.mon"), in("var_set", "use.mon")}, wantRequests: 2},
		{name: "vars.set outside the selection does not count", files: []string{in("var_set", "use.mon")}, wantRequests: 1, wantErrors: 1,
			want: []string{`var_set/use.mon:2: me: unknown variable "token"`}},
		{name: "--filter leaves the setter out", files: []string{in("var_set", "login.mon"), in("var_set", "use.mon")}, opts: Opts{Filters: []string{"me"}}, wantRequests: 1, wantErrors: 1,
			want: []string{`unknown variable "token"`}},
		{name: "missing body file", files: []string{in("missing_body", "req.mon")}, wantErrors: 1,
			want: []string{`missing_body/req.mon:4: reading body file`}},
		{name: "JS syntax error", files: []string{in("js_syntax", "req.mon")}, wantRequests: 1, wantErrors: 1,
			want: []string{`js_syntax/req.mon:5: SyntaxError`}},
		{name: "monica.js syntax error", files: []string{in("hook_syntax", "req.mon")}, wantRequests: 1, wantErrors: 1,
			want: []string{`hook_syntax/monica.js:4: SyntaxError`}},
		{name: "missing secret is a warning", files: []string{in("secret_missing", "req.mon")}, opts: Opts{LookupEnv: noEnv}, wantRequests: 1,
			want: []string{`secret_missing/monica.yml:4: warning: ${MONICA_VALIDATE_UNSET_TOKEN} is not set`}},
		{name: "secret from the environment", files: []string{in("secret_missing", "req.mon")}, wantRequests: 1,
			opts: Opts{LookupEnv: func(string) (string, bool) { return "s3cret", true }}},
		{name: "invalid monica.yml", files: []string{in("bad_config", "req.mon")}, wantRequests: 1, wantErrors: 1,
			want: []string{`bad_config/monica.yml: yaml: unmarshal errors: line 3: field timeuot not found`}},
		{name: "missing file", files: []string{in("nope.mon")}, wantErrors: 1, want: []string{"nope.mon: reading mon file"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			res := Validate(tt.files, &opts, tt.rows)
			var lines []string
			for _, p := range res.Problems {
				lines = append(lines, filepath.ToSlash(p.String()))
			}
			got := strings.Join(lines, "\n")
			if res.Files != len(tt.files) || res.Requests != tt.wantRequests {
				t.Errorf("files, requests = %d, %d; want %d, %d", res.Files, res.Requests, len(tt.files), tt.wantRequests)
			}
			if res.Errors() != tt.wantErrors || len(lines) != len(tt.want) {
				t.Fatalf("errors = %d, problems = %d; want %d, %d:\n%s", res.Errors(), len(lines), tt.wantErrors, len(tt.want), got)
			}
			for i, w := range tt.want {
				if !strings.Contains(lines[i], w) {
					t.Errorf("problem %d = %q, want it to contain %q", i, lines[i], w)
				}
			}
		})
	}
}

func TestProblemString(t *testing.T) {
	tests := []struct {
		p    Problem
		want string
	}{
		{Problem{File: "a.mon", Line: 3, Severity: SeverityError, Msg: "boom"}, "a.mon:3: boom"},
		{Problem{File: "monica.yml", Severity: SeverityWarning, Msg: "hm"}, "monica.yml: warning: hm"},
		{Problem{Severity: SeverityError, Msg: "data"}, "data"},
	}
	for _, tt := range tests {
		if got := tt.p.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}
