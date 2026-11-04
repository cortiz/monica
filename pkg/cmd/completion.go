package cmd

import (
	"path/filepath"
	"sort"
	"sync"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/config"
)

// registerCompletionsOnce guards registerCompletions: execute calls it
// (after every command's init has defined its flags), possibly many times.
var registerCompletionsOnce sync.Once

// completionFunc completes a flag value.
type completionFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// registerCompletions adds value completion for the flags run, load, tui
// and validate share; a command without one of the flags is skipped.
// Shell scripts come from cobra's completion command
// (monica completion bash|zsh|fish|powershell).
func registerCompletions() {
	funcs := map[string]completionFunc{
		"env":      completeEnvs,
		"single":   completeMonFiles,
		"data":     completeDataFiles,
		"reporter": completeReporters,
		"cert":     completePEMFiles,
		"key":      completePEMFiles,
		"cacert":   completePEMFiles,
		"proxy":    completeProxy,
	}
	for _, cmd := range []*cobra.Command{run, loadCmd, tuiCmd, validateCmd} {
		for name, fn := range funcs {
			if cmd.Flags().Lookup(name) != nil {
				_ = cmd.RegisterFlagCompletionFunc(name, fn)
			}
		}
	}
}

// completeEnvs lists the environments of the config the command would use:
// -c if given, else the nearest monica.yml above the -s file (if given)
// or the current directory.
func completeEnvs(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	path := flagValue(cmd, "config")
	if path == "" {
		dir := "."
		if single := flagValue(cmd, "single"); single != "" {
			dir = filepath.Dir(single)
		}
		var err error
		if path, err = config.Find(dir); err != nil || path == "" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(cfg.Envs))
	for name := range cfg.Envs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

// completeMonFiles completes .mon files (and folders).
func completeMonFiles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"mon"}, cobra.ShellCompDirectiveFilterFileExt
}

// completeDataFiles completes .csv and .json files (and folders).
func completeDataFiles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"csv", "json"}, cobra.ShellCompDirectiveFilterFileExt
}

// completePEMFiles completes certificate and key files (and folders).
func completePEMFiles(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"pem", "crt", "cer", "key"}, cobra.ShellCompDirectiveFilterFileExt
}

// completeProxy offers "none"; proxy URLs are typed.
func completeProxy(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"none"}, cobra.ShellCompDirectiveNoFileComp
}

// completeReporters lists the --reporter formats.
func completeReporters(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return []string{"console", "json", "junit"}, cobra.ShellCompDirectiveNoFileComp
}

// flagValue is the value of cmd's flag name, or "" when it has none.
func flagValue(cmd *cobra.Command, name string) string {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}
