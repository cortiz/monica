package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// validateCLI runs monica validate with fresh flags in dir.
func validateCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	valSingle, valRecursive, valEnv, valVarFlags, valConfig, valFilters, valData = "", 1, "", nil, "", nil, ""
	for _, name := range []string{"single", "recursive", "env", "var", "config", "filter", "data"} {
		validateCmd.Flags().Lookup(name).Changed = false
	}
	t.Setenv("MONICA_ENV", "")
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	code = execute(rootCmd, append([]string{"validate"}, args...), &errOut)
	return code, out.String(), errOut.String()
}

func TestValidateCommand(t *testing.T) {
	fixtures, err := filepath.Abs(filepath.Join("..", "runner", "testdata", "validate"))
	if err != nil {
		t.Fatal(err)
	}
	examples, err := filepath.Abs(filepath.Join("..", "..", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		dir        string
		args       []string
		wantCode   int
		wantStdout []string
	}{
		{"good project", filepath.Join(fixtures, "good"), nil, ExitOK, []string{"1 file, 2 requests OK\n"}},
		{"examples", examples, []string{"-r", "5"}, ExitOK, []string{"requests OK\n"}},
		{"examples, staging", examples, []string{"-r", "5", "-e", "staging"}, ExitOK, []string{"requests OK\n"}},
		{"unknown env", filepath.Join(fixtures, "good"), []string{"-e", "nope"}, ExitUsage,
			[]string{"monica.yml: unknown environment \"nope\" (defined: staging)\n", "1 problem\n"}},
		{"bad syntax", filepath.Join(fixtures, "bad_syntax"), nil, ExitUsage, []string{"bad.mon:2: invalid request line", "1 problem\n"}},
		{"unknown var", filepath.Join(fixtures, "unknown_var"), nil, ExitUsage,
			[]string{"req.mon:2: getUser: unknown variable \"userId\"\n", "req.mon:2: getUser: unknown variable \"token\"\n", "2 problems\n"}},
		{"unknown var given by --var", filepath.Join(fixtures, "unknown_var"), []string{"--var", "userId=1", "--var", "token=t"}, ExitOK, []string{"1 file, 1 request OK\n"}},
		{"var set by vars.set", filepath.Join(fixtures, "var_set"), nil, ExitOK, []string{"2 files, 2 requests OK\n"}},
		{"var set by vars.set, -s", filepath.Join(fixtures, "var_set"), []string{"-s", "use.mon"}, ExitUsage, []string{"unknown variable \"token\"", "1 problem\n"}},
		{"missing body file", filepath.Join(fixtures, "missing_body"), nil, ExitUsage, []string{"req.mon:4: reading body file", "1 problem\n"}},
		{"JS syntax error", filepath.Join(fixtures, "js_syntax"), nil, ExitUsage, []string{"req.mon:5: SyntaxError", "1 problem\n"}},
		{"hook syntax error", filepath.Join(fixtures, "hook_syntax"), nil, ExitUsage, []string{"monica.js:4: SyntaxError", "1 problem\n"}},
		{"missing data file", filepath.Join(fixtures, "good"), []string{"--data", "nope.csv"}, ExitUsage, []string{"reading data file", "1 problem\n"}},
		{"bad --var", filepath.Join(fixtures, "good"), []string{"--var", "nope"}, ExitUsage, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := validateCLI(t, tt.dir, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, stdout, stderr)
			}
			for _, w := range tt.wantStdout {
				if !strings.Contains(stdout, w) {
					t.Errorf("stdout = %q, want it to contain %q", stdout, w)
				}
			}
			if tt.wantCode == ExitOK && strings.Contains(stdout, "problem") {
				t.Errorf("stdout = %q, want no problems", stdout)
			}
		})
	}
}

func TestValidateMissingSecretWarns(t *testing.T) {
	dir, err := filepath.Abs(filepath.Join("..", "runner", "testdata", "validate", "secret_missing"))
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := validateCLI(t, dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d\n%s%s", code, ExitOK, stdout, stderr)
	}
	want := "monica.yml:4: warning: ${MONICA_VALIDATE_UNSET_TOKEN} is not set (in the environment or .env)\n1 file, 1 request OK (1 warning)\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}
