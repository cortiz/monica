package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/runner"
)

var (
	valSingle    string
	valRecursive int
	valEnv       string
	valVarFlags  []string
	valConfig    string
	valFilters   []string
	valData      string
	valTLS       tlsFlags

	validateCmd = &cobra.Command{
		Use:   "validate",
		Short: "Checks mon files, configs and scripts without sending anything",
		Long: `Checks the mon files "monica run" would run (same file selection flags)
without sending a request or running a script: .mon syntax, missing body and
script files, monica.yml and the selected environment, JavaScript/TypeScript
syntax (inline blocks, script files and monica.js hooks), TLS certificate
files and proxy URLs (tls:, proxy: and the --cert/--key/--cacert/--proxy
flags; files are read, nothing is dialed), and {{variables}}
that nothing defines. A variable counts as defined when monica.yml, --var or a
--data column has it, or when any script of the run calls vars.set("name", ...)
with that literal name. ${NAME} secrets missing from the environment and .env
are warnings.

Prints one line per problem (file:line: message), then a summary. Exits 0
when there are no errors (warnings allowed) and 2 otherwise.`,
		Args: cobra.NoArgs,
		RunE: runValidate,
	}
)

func init() {
	f := validateCmd.Flags()
	f.StringVarP(&valSingle, "single", "s", "", "validate a single mon file")
	f.IntVarP(&valRecursive, "recursive", "r", 1, "validate all mon files in the current directory and subdirectories up to this depth")
	f.StringVarP(&valEnv, "env", "e", "", "environment from monica.yml (default $"+config.EnvVar+")")
	f.StringArrayVar(&valVarFlags, "var", nil, "set a variable, name=value (repeatable; overrides monica.yml)")
	f.StringVarP(&valConfig, "config", "c", "", "config file to use instead of the nearest monica.yml")
	f.StringArrayVar(&valFilters, "filter", nil, "only check requests whose @name, ### title or \"METHOD URL\" matches this glob (repeatable)")
	f.StringVar(&valData, "data", "", "CSV or JSON data file whose columns count as defined variables")
	valTLS.register(validateCmd)
}

func runValidate(cmd *cobra.Command, _ []string) error {
	vars, err := parseVars(valVarFlags)
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	cmd.SilenceUsage = true
	env := valEnv
	if !cmd.Flags().Changed("env") {
		env = os.Getenv(config.EnvVar)
	}
	files := []string{valSingle}
	if valSingle == "" {
		if files, err = FindMonFiles(".", valRecursive); err != nil {
			return &exitError{code: ExitUsage, err: fmt.Errorf("finding mon files: %w", err)}
		}
		sort.Strings(files)
	}
	var rows []runner.Row
	var dataErr error
	if valData != "" {
		rows, dataErr = runner.LoadData(valData)
	}
	opts := &runner.Opts{Env: env, Vars: vars, ConfigPath: valConfig, Filters: valFilters,
		ClientCert: valTLS.cert, ClientKey: valTLS.key, CACert: valTLS.caCert, Proxy: valTLS.proxy}
	res := runner.Validate(files, opts, rows)
	if dataErr != nil {
		res.Problems = append([]runner.Problem{{Severity: runner.SeverityError, Msg: dataErr.Error()}}, res.Problems...)
	}

	out := cmd.OutOrStdout()
	cwd, _ := os.Getwd()
	for _, p := range res.Problems {
		p.File = displayPath(cwd, p.File)
		_, _ = fmt.Fprintln(out, p.String())
	}
	if res.Errors() == 0 {
		summary := fmt.Sprintf("%s, %s OK", plural(res.Files, "file"), plural(res.Requests, "request"))
		if w := len(res.Problems); w > 0 {
			summary += fmt.Sprintf(" (%s)", plural(w, "warning"))
		}
		_, _ = fmt.Fprintln(out, summary)
		return nil
	}
	_, _ = fmt.Fprintln(out, plural(len(res.Problems), "problem"))
	return &exitError{code: ExitUsage, err: errors.New("validation failed"), silent: true}
}

// displayPath shows an absolute path under cwd (such as a monica.yml
// found by walking up) relative to it.
func displayPath(cwd, path string) string {
	if cwd == "" || !filepath.IsAbs(path) {
		return path
	}
	if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

// plural formats n with noun, adding "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
