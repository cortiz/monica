package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFlagCompletion(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("monica.yml", "version: 1\nenvs:\n  staging: {}\n  prod: {}\n  dev: {}\n")
	other := write("other/custom.yml", "version: 1\nenvs:\n  qa: {}\n")
	sub := write("sub/monica.yml", "version: 1\nenvs:\n  local: {}\n")
	write("sub/a.mon", "GET https://example.test\n")
	write("broken/monica.yml", "nope: true\n")
	t.Chdir(dir)

	// flags builds a command with the shared flags set as given.
	flags := func(set map[string]string) *cobra.Command {
		cmd := &cobra.Command{Use: "x"}
		cmd.Flags().StringP("config", "c", "", "")
		cmd.Flags().StringP("single", "s", "", "")
		for name, value := range set {
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		return cmd
	}

	tests := []struct {
		name    string
		fn      completionFunc
		cmd     *cobra.Command
		want    []string
		wantDir cobra.ShellCompDirective
	}{
		{"envs of the nearest monica.yml", completeEnvs, flags(nil), []string{"dev", "prod", "staging"}, cobra.ShellCompDirectiveNoFileComp},
		{"envs of -c", completeEnvs, flags(map[string]string{"config": other}), []string{"qa"}, cobra.ShellCompDirectiveNoFileComp},
		{"envs above the -s file", completeEnvs, flags(map[string]string{"single": filepath.Join("sub", "a.mon")}), []string{"local"}, cobra.ShellCompDirectiveNoFileComp},
		{"envs of an invalid config", completeEnvs, flags(map[string]string{"config": filepath.Join("broken", "monica.yml")}), nil, cobra.ShellCompDirectiveNoFileComp},
		{"envs of -c sub", completeEnvs, flags(map[string]string{"config": sub}), []string{"local"}, cobra.ShellCompDirectiveNoFileComp},
		{"mon files", completeMonFiles, flags(nil), []string{"mon"}, cobra.ShellCompDirectiveFilterFileExt},
		{"data files", completeDataFiles, flags(nil), []string{"csv", "json"}, cobra.ShellCompDirectiveFilterFileExt},
		{"reporters", completeReporters, flags(nil), []string{"console", "json", "junit"}, cobra.ShellCompDirectiveNoFileComp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dir := tt.fn(tt.cmd, nil, "")
			if len(got) == 0 {
				got = nil
			}
			if !reflect.DeepEqual(got, tt.want) || dir != tt.wantDir {
				t.Errorf("got %v, %v; want %v, %v", got, dir, tt.want, tt.wantDir)
			}
		})
	}
}

func TestFlagCompletionsRegistered(t *testing.T) {
	registerCompletionsOnce.Do(registerCompletions)
	tests := []struct {
		cmd   *cobra.Command
		flags []string
	}{
		{run, []string{"env", "single", "data", "reporter"}},
		{loadCmd, []string{"env", "single"}},
		{tuiCmd, []string{"env"}},
		{validateCmd, []string{"env", "single", "data"}},
	}
	for _, tt := range tests {
		for _, name := range tt.flags {
			if _, ok := tt.cmd.GetFlagCompletionFunc(name); !ok {
				t.Errorf("%s --%s has no completion function", tt.cmd.Name(), name)
			}
		}
	}
}

// TestCompletionScripts checks the scripts "monica completion <shell>"
// prints. cobra's completion command keeps the writer it first saw, so the
// generators are called directly.
func TestCompletionScripts(t *testing.T) {
	gens := map[string]func(*bytes.Buffer) error{
		"bash":       func(b *bytes.Buffer) error { return rootCmd.GenBashCompletionV2(b, true) },
		"zsh":        func(b *bytes.Buffer) error { return rootCmd.GenZshCompletion(b) },
		"fish":       func(b *bytes.Buffer) error { return rootCmd.GenFishCompletion(b, true) },
		"powershell": func(b *bytes.Buffer) error { return rootCmd.GenPowerShellCompletionWithDesc(b) },
	}
	for shell, gen := range gens {
		t.Run(shell, func(t *testing.T) {
			var b bytes.Buffer
			if err := gen(&b); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), "monica") {
				t.Errorf("%s completion script does not mention monica", shell)
			}
		})
	}
}

// TestCompleteRequest drives cobra's hidden __complete command, which the
// shell scripts call, through execute.
func TestCompleteRequest(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "run", "--reporter", ""}, "console\njson\njunit\n:4\n"},
		{[]string{"__complete", "validate", "-s", ""}, "mon\n:8\n"},
		{[]string{"__complete", "run", "--data", ""}, "csv\njson\n:8\n"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[1:], " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&errOut)
			t.Cleanup(func() {
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
			})
			if code := execute(rootCmd, tt.args, &errOut); code != ExitOK {
				t.Fatalf("exit code = %d: %s", code, errOut.String())
			}
			if out.String() != tt.want {
				t.Errorf("stdout = %q, want %q", out.String(), tt.want)
			}
		})
	}
}
