package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/runner"
)

var (
	loadSingle       string
	loadRecursive    int
	loadEnv          string
	loadVarFlags     []string
	loadConfig       string
	loadFilters      []string
	loadVUs          int
	loadDuration     time.Duration
	loadIterations   int
	loadGracefulStop time.Duration
	loadThresholds   []string
	loadRate         string
	loadStages       []string
	loadOut          string
	loadQuiet        bool
	loadInsecure     bool
	loadNoKeepAlive  bool

	loadCmd = &cobra.Command{
		Use:   "load",
		Short: "Load-tests mon files with virtual users",
		Long: `Runs the same mon files as "monica run" with several virtual users (VUs) at
once. Each VU loops over the files (one iteration = every file in run order)
with its own script runtime and variables, until --duration has passed or
--iterations (shared by all VUs) have run. --stage ramps the VU count instead;
--rate starts iterations at a fixed rate on up to --vus VUs (with --stage, the
rate ramps). Defaults come from load: in monica.yml; without them, 1 VU runs
1 iteration.`,
		RunE: runLoad,
	}
)

func init() {
	f := loadCmd.Flags()
	f.StringVarP(&loadSingle, "single", "s", "", "load-test a single mon file")
	f.IntVarP(&loadRecursive, "recursive", "r", 1, "use all mon files in the current directory and subdirectories up to this depth")
	f.StringVarP(&loadEnv, "env", "e", "", "environment from monica.yml (default $"+config.EnvVar+")")
	f.StringArrayVar(&loadVarFlags, "var", nil, "set a variable, name=value (repeatable; overrides monica.yml)")
	f.StringVarP(&loadConfig, "config", "c", "", "config file to use instead of the nearest monica.yml")
	f.StringArrayVar(&loadFilters, "filter", nil, "only run requests whose @name, ### title or \"METHOD URL\" matches this glob (repeatable)")
	f.IntVarP(&loadVUs, "vus", "u", 1, "virtual users running at once; with --stage, the count to ramp from (default 0); with --rate, the most at once (default 100); else load.vus or 1")
	f.DurationVar(&loadDuration, "duration", 0, "stop starting iterations after this long, e.g. 30s (default load.duration)")
	f.IntVarP(&loadIterations, "iterations", "i", 0, "total iterations, shared by all VUs")
	f.DurationVar(&loadGracefulStop, "graceful-stop", load.DefaultGracefulStop, "how long iterations running at the end of --duration may take before their requests are aborted")
	f.StringVar(&loadRate, "rate", "", "start iterations at this rate instead of looping VUs, e.g. 100/s, 6000/m (default load.rate)")
	f.StringArrayVar(&loadStages, "stage", nil, "ramp step duration:target, e.g. 30s:50; targets are VUs, or iterations/s with --rate (repeatable; replaces load.stages)")
	f.StringVarP(&loadOut, "out", "o", "", "also write a JSON report to this file; \"-\" prints it on stdout instead of the text summary")
	f.BoolVarP(&loadInsecure, "insecure", "k", false, "skip TLS certificate verification (like insecureSkipVerify in monica.yml)")
	f.BoolVar(&loadNoKeepAlive, "no-keepalive", false, "open a new connection for every request instead of reusing them")
	f.BoolVarP(&loadQuiet, "quiet", "q", false, "no live progress line (it is only shown when stderr is a terminal)")
	f.StringArrayVar(&loadThresholds, "threshold", nil, "pass/fail condition such as \"p95<300ms\", \"login:errors<1%\" or \"rps>100\" (repeatable; replaces load.thresholds)")
	rootCmd.AddCommand(loadCmd)
}

func runLoad(cmd *cobra.Command, _ []string) error {
	vars, err := parseVars(loadVarFlags)
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	cmd.SilenceUsage = true
	env := loadEnv
	if !cmd.Flags().Changed("env") {
		env = os.Getenv(config.EnvVar)
	}
	ropts := runner.Opts{Env: env, Vars: vars, ConfigPath: loadConfig, Filters: loadFilters, Insecure: loadInsecure}

	opts, thresholds, err := loadOptions(cmd.Flags().Changed, loadConfig)
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	opts.Run = ropts
	opts.NoKeepAlive = loadNoKeepAlive
	if loadSingle != "" {
		opts.Files = []string{loadSingle}
	} else {
		files, err := FindMonFiles(".", loadRecursive)
		if err != nil {
			return &exitError{code: ExitUsage, err: fmt.Errorf("finding mon files: %w", err)}
		}
		opts.Files = runner.New(&ropts, io.Discard).Order(files)
	}

	// Open the report file first: a bad path should fail before the test.
	var outFile *os.File
	if loadOut != "" && loadOut != "-" {
		if outFile, err = os.Create(loadOut); err != nil {
			return &exitError{code: ExitUsage, err: fmt.Errorf("creating report file: %w", err)}
		}
		defer func() { _ = outFile.Close() }() // on early returns; closed and checked below otherwise
	}

	// Per-request logs would drown the summary: only warnings and errors
	// unless --debug.
	if !debug {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	}
	stderr := cmd.ErrOrStderr()
	showProgress := !loadQuiet && isTerminal(stderr)
	if showProgress {
		opts.Progress = func(p load.Progress) { _, _ = fmt.Fprint(stderr, clearLine+p.String()) }
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := load.Run(ctx, opts)
	if showProgress {
		_, _ = fmt.Fprint(stderr, clearLine)
	}
	var ie *runner.InputError
	switch {
	case errors.As(err, &ie):
		return &exitError{code: ExitUsage, err: err}
	case err != nil && !errors.Is(err, context.Canceled):
		return &exitError{code: ExitUsage, err: err}
	}
	checked := load.Check(res, thresholds)
	if loadOut != "" {
		data, jerr := res.JSON(opts, checked, err != nil)
		if jerr == nil {
			data = append(data, '\n')
			if outFile != nil {
				_, jerr = outFile.Write(data)
				if cerr := outFile.Close(); jerr == nil {
					jerr = cerr
				}
			} else {
				_, jerr = cmd.OutOrStdout().Write(data)
			}
		}
		if jerr != nil {
			return &exitError{code: ExitFailed, err: fmt.Errorf("writing report: %w", jerr)}
		}
	}
	if loadOut != "-" {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), res.Summary(opts)+load.ThresholdReport(checked))
	}
	if err != nil {
		return &exitError{code: ExitFailed, err: errors.New("load test interrupted")}
	}
	if n := load.Failed(checked); n > 0 {
		return &exitError{code: ExitFailed, err: fmt.Errorf("%d of %d thresholds failed", n, len(checked))}
	}
	return nil
}

// clearLine returns the cursor to the start of the line and clears it.
const clearLine = "\r\x1b[2K"

// isTerminal reports whether w is a terminal (a character device).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// loadOptions combines the flags with load: from monica.yml (--config or
// the nearest one above the current directory). --iterations without
// --duration means no time limit, even when the config sets one;
// --threshold replaces load.thresholds.
// changed reports whether a flag was set; configPath is --config.
func loadOptions(changed func(string) bool, configPath string) (load.Options, []load.Threshold, error) {
	opts := load.Options{VUs: loadVUs, Duration: loadDuration, Iterations: loadIterations, GracefulStop: loadGracefulStop}
	path := configPath
	if path == "" {
		var err error
		if path, err = config.Find("."); err != nil {
			return opts, nil, err
		}
	}
	var settings config.LoadSettings
	if path != "" {
		cfg, err := config.Load(path)
		if err != nil {
			return opts, nil, err
		}
		settings = cfg.Load
	}
	var thresholds []load.Threshold
	var err error
	if changed("threshold") {
		thresholds, err = load.ParseThresholds(loadThresholds)
	} else if thresholds, err = load.ParseThresholds(settings.Thresholds); err != nil {
		err = fmt.Errorf("%s: load.thresholds: %w", path, err)
	}
	if err != nil {
		return opts, nil, err
	}
	rateText := settings.Rate
	if changed("rate") {
		rateText = loadRate
	}
	if rateText != "" {
		if opts.Rate, err = config.ParseRate(rateText); err != nil {
			return opts, nil, err
		}
	}
	switch {
	case changed("stage"):
		for _, s := range loadStages {
			st, err := config.ParseStage(s)
			if err != nil {
				return opts, nil, err
			}
			opts.Stages = append(opts.Stages, load.Stage{Duration: st.Duration.Duration, Target: st.Target})
		}
	case !changed("duration"): // --duration replaces load.stages
		for _, st := range settings.Stages {
			opts.Stages = append(opts.Stages, load.Stage{Duration: st.Duration.Duration, Target: st.Target})
		}
	}
	if !changed("duration") && !changed("iterations") && len(opts.Stages) == 0 {
		opts.Duration = settings.Duration.Duration
	}
	if !changed("vus") {
		switch {
		case settings.VUs > 0:
			opts.VUs = settings.VUs
		case opts.Rate > 0:
			opts.VUs = 100
		case len(opts.Stages) > 0:
			opts.VUs = 0
		}
	}
	if opts.Duration == 0 && opts.Iterations == 0 && len(opts.Stages) == 0 {
		opts.Iterations = 1
	}
	if opts.GracefulStop < 0 {
		return opts, nil, fmt.Errorf("invalid --graceful-stop %s (must not be negative)", opts.GracefulStop)
	}
	return opts, thresholds, nil
}
