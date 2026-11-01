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

	loadCmd = &cobra.Command{
		Use:   "load",
		Short: "Load-tests mon files with virtual users",
		Long: `Runs the same mon files as "monica run" with several virtual users (VUs) at
once. Each VU loops over the files (one iteration = every file in run order)
with its own script runtime and variables, until --duration has passed or
--iterations (shared by all VUs) have run. Defaults come from load: in
monica.yml; without them, 1 VU runs 1 iteration.`,
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
	f.IntVarP(&loadVUs, "vus", "u", 1, "virtual users running at once (default load.vus, else 1)")
	f.DurationVar(&loadDuration, "duration", 0, "stop starting iterations after this long, e.g. 30s (default load.duration)")
	f.IntVarP(&loadIterations, "iterations", "i", 0, "total iterations, shared by all VUs")
	f.DurationVar(&loadGracefulStop, "graceful-stop", load.DefaultGracefulStop, "how long iterations running at the end of --duration may take before their requests are aborted")
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
	ropts := runner.Opts{Env: env, Vars: vars, ConfigPath: loadConfig, Filters: loadFilters}

	opts, thresholds, err := loadOptions(cmd)
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	opts.Run = ropts
	if loadSingle != "" {
		opts.Files = []string{loadSingle}
	} else {
		files, err := FindMonFiles(".", loadRecursive)
		if err != nil {
			return &exitError{code: ExitUsage, err: fmt.Errorf("finding mon files: %w", err)}
		}
		opts.Files = runner.New(&ropts, io.Discard).Order(files)
	}

	// Per-request logs would drown the summary: only warnings and errors
	// unless --debug.
	if !debug {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := load.Run(ctx, opts)
	var ie *runner.InputError
	switch {
	case errors.As(err, &ie):
		return &exitError{code: ExitUsage, err: err}
	case err != nil && !errors.Is(err, context.Canceled):
		return &exitError{code: ExitUsage, err: err}
	}
	checked := load.Check(res, thresholds)
	_, _ = fmt.Fprint(cmd.OutOrStdout(), res.Summary(opts)+load.ThresholdReport(checked))
	if err != nil {
		return &exitError{code: ExitFailed, err: errors.New("load test interrupted")}
	}
	if n := load.Failed(checked); n > 0 {
		return &exitError{code: ExitFailed, err: fmt.Errorf("%d of %d thresholds failed", n, len(checked))}
	}
	return nil
}

// loadOptions combines the flags with load: from monica.yml (--config or
// the nearest one above the current directory). --iterations without
// --duration means no time limit, even when the config sets one;
// --threshold replaces load.thresholds.
func loadOptions(cmd *cobra.Command) (load.Options, []load.Threshold, error) {
	opts := load.Options{VUs: loadVUs, Duration: loadDuration, Iterations: loadIterations, GracefulStop: loadGracefulStop}
	path := loadConfig
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
	if cmd.Flags().Changed("threshold") {
		thresholds, err = load.ParseThresholds(loadThresholds)
	} else if thresholds, err = load.ParseThresholds(settings.Thresholds); err != nil {
		err = fmt.Errorf("%s: load.thresholds: %w", path, err)
	}
	if err != nil {
		return opts, nil, err
	}
	changed := cmd.Flags().Changed
	if !changed("vus") && settings.VUs > 0 {
		opts.VUs = settings.VUs
	}
	if !changed("duration") && !changed("iterations") {
		opts.Duration = settings.Duration.Duration
	}
	if opts.Duration == 0 && opts.Iterations == 0 {
		opts.Iterations = 1
	}
	if opts.GracefulStop < 0 {
		return opts, nil, fmt.Errorf("invalid --graceful-stop %s (must not be negative)", opts.GracefulStop)
	}
	return opts, thresholds, nil
}
