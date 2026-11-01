package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/report"
	"jmpeax.com/sec/monica/pkg/runner"
)

var (
	singleFile     string
	recursiveLevel int
	envName        string
	varFlags       []string
	configPath     string
	reporterName   string
	outPath        string
	bail           bool
	filters        []string
	quiet          bool
	dataPath       string
	insecure       bool
	run            = &cobra.Command{
		Use:   "run",
		Short: "Runs all mon files",
		Long:  "Runs all mon files found in the current working directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Flags parsed fine; from here on failures are not usage problems.
			vars, err := parseVars(varFlags)
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			cmd.SilenceUsage = true
			env := envName
			if !cmd.Flags().Changed("env") {
				env = os.Getenv(config.EnvVar)
			}
			var rows []runner.Row
			if dataPath != "" {
				if rows, err = runner.LoadData(dataPath); err != nil {
					return &exitError{code: ExitUsage, err: err}
				}
			}
			opts := &runner.Opts{Env: env, Vars: vars, ConfigPath: configPath, Bail: bail, Filters: filters, Quiet: quiet, Insecure: insecure}
			rep, closeOut, err := newReporter(reporterName, outPath, opts, cmd.OutOrStdout())
			if err != nil {
				return &exitError{code: ExitUsage, err: err}
			}
			r := runner.NewWithReporter(opts, rep)
			err = runIterations(r, rows)
			if herr := r.AfterAll(); herr != nil {
				if err == nil {
					err = &exitError{code: ExitFailed, err: herr}
				} else {
					logging.Log.Error().Msg(herr.Error())
				}
			}
			ferr := errors.Join(r.Finish(), closeOut())
			if ferr != nil && err == nil {
				err = &exitError{code: ExitFailed, err: fmt.Errorf("writing report: %w", ferr)}
			}
			return err
		},
	}
)

func init() {
	run.Flags().StringVarP(&singleFile, "single", "s", "", "runs a single mon file, path can be absolute o relative to current working directory")
	run.Flags().IntVarP(&recursiveLevel, "recursive", "r", 1, "runs all mon files in the current working directory and subdirectories up to the specified level")
	run.Flags().StringVarP(&envName, "env", "e", "", "environment from monica.yml (default $"+config.EnvVar+")")
	run.Flags().StringArrayVar(&varFlags, "var", nil, "set a variable, name=value (repeatable; overrides monica.yml)")
	run.Flags().StringVarP(&configPath, "config", "c", "", "config file to use instead of the nearest monica.yml")
	run.Flags().StringVar(&reporterName, "reporter", "console", "report format: console, json or junit")
	run.Flags().StringVarP(&outPath, "out", "o", "", "write the report to this file (json/junit keep the console report on stdout)")
	run.Flags().BoolVar(&bail, "bail", false, "stop at the first failed request")
	run.Flags().StringArrayVar(&filters, "filter", nil, "only run requests whose @name, ### title or \"METHOD URL\" matches this glob (repeatable)")
	run.Flags().BoolVarP(&quiet, "quiet", "q", false, "hide response headers and bodies")
	run.Flags().BoolVarP(&insecure, "insecure", "k", false, "skip TLS certificate verification (like insecureSkipVerify in monica.yml)")
	run.Flags().StringVar(&dataPath, "data", "", "run everything once per row of this CSV or JSON file, with the row's values as variables")
}

// newReporter builds the reporter for --reporter/--out. With json or junit
// and an --out file, the console report still goes to stdout; without
// --out, only the machine report does. closeOut closes the --out file.
func newReporter(name, outPath string, opts *runner.Opts, stdout io.Writer) (rep report.Reporter, closeOut func() error, err error) {
	var machine func(io.Writer) report.Reporter
	switch name {
	case "console":
	case "json":
		machine = func(w io.Writer) report.Reporter { return report.NewJSON(w) }
	case "junit":
		machine = func(w io.Writer) report.Reporter { return report.NewJUnit(w) }
	default:
		return nil, nil, fmt.Errorf("invalid --reporter %q (expected console, json or junit)", name)
	}
	console := func(w io.Writer) report.Reporter {
		c := report.NewConsole(w)
		c.Quiet = opts.Quiet
		return c
	}
	closeOut = func() error { return nil }
	if outPath == "" {
		if machine != nil {
			return machine(stdout), closeOut, nil
		}
		return console(stdout), closeOut, nil
	}
	f, err := os.Create(outPath)
	if err != nil {
		return nil, nil, fmt.Errorf("creating report file: %w", err)
	}
	if machine == nil {
		return console(f), f.Close, nil
	}
	return report.Multi(console(stdout), machine(f)), f.Close, nil
}

var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// parseVars turns --var name=value flags into a map; later flags win.
func parseVars(flags []string) (map[string]string, error) {
	vars := make(map[string]string, len(flags))
	for _, f := range flags {
		name, value, ok := strings.Cut(f, "=")
		if !ok || !varNameRe.MatchString(name) {
			return nil, fmt.Errorf("invalid --var %q (expected name=value)", f)
		}
		vars[name] = value
	}
	return vars, nil
}

// runIterations runs the -s file or every .mon file, once, or once per
// --data row, and returns the most severe failure.
func runIterations(r *runner.Runner, rows []runner.Row) error {
	runOnce := func() error { return runSingleFile(r, singleFile) }
	if singleFile == "" {
		files, err := FindMonFiles(".", recursiveLevel)
		if err != nil {
			return &exitError{code: ExitUsage, err: fmt.Errorf("finding mon files: %w", err)}
		}
		files = r.Order(files)
		logging.Log.Info().Msgf("Found %d mon files", len(files))
		runOnce = func() error { return runFiles(r, files) }
	}
	if len(rows) == 0 {
		return runOnce()
	}
	worst, failed := ExitOK, 0
	for i, row := range rows {
		if r.Bailed() {
			break
		}
		logging.Log.Info().Msgf("Data row %d of %d", i+1, len(rows))
		r.SetIteration(i+1, row)
		if err := runOnce(); err != nil {
			failed++
			logging.Log.Error().Msgf("data row %d: %v", i+1, err)
			if code := exitCode(err); code > worst {
				worst = code
			}
		}
	}
	if failed > 0 {
		return &exitError{code: worst, err: fmt.Errorf("%d of %d data rows failed", failed, len(rows))}
	}
	return nil
}

func runSingleFile(r *runner.Runner, file string) error {
	if err := r.RunFile(file); err != nil {
		return classify(err)
	}
	return nil
}

// runFiles runs files in order, continuing past failures, and returns an
// error carrying the most severe exit code seen.
func runFiles(r *runner.Runner, files []string) error {
	worst, failed := ExitOK, 0
	for _, file := range files {
		if r.Bailed() {
			break
		}
		if err := r.RunFile(file); err != nil {
			failed++
			logging.Log.Error().Msg(err.Error())
			if code := exitCode(classify(err)); code > worst {
				worst = code
			}
		}
	}
	if failed > 0 {
		return &exitError{code: worst, err: fmt.Errorf("%d of %d mon files failed", failed, len(files))}
	}
	return nil
}

// classify wraps a runner error with the exit code it should produce.
func classify(err error) error {
	var ie *runner.InputError
	if errors.As(err, &ie) {
		return &exitError{code: ExitUsage, err: err}
	}
	return &exitError{code: ExitFailed, err: err}
}

func FindMonFiles(root string, maxDepth int) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate the current depth
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		depth := len(strings.Split(filepath.ToSlash(relativePath), "/"))

		// If the current depth exceeds maxDepth, skip this directory
		if d.IsDir() {
			if depth > maxDepth {
				return filepath.SkipDir
			}
		} else {
			// Check if the file ends with .mon
			if strings.HasSuffix(d.Name(), ".mon") {
				files = append(files, path)
			}
		}

		return nil
	})

	return files, err
}
