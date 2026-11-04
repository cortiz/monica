package cmd

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/runner"
	"jmpeax.com/sec/monica/pkg/tui"
)

var (
	tuiRecursive int
	tuiEnv       string
	tuiVarFlags  []string
	tuiConfig    string
	tuiInsecure  bool
	tuiNoCookies bool
	tuiTLS       tlsFlags

	tuiCmd = &cobra.Command{
		Use:   "tui",
		Short: "Browses and sends mon files in an interactive terminal UI",
		Long: `Opens a full-screen terminal UI over the mon files in the current directory:
a tree of folders, files and requests, the selected request, and its response
(body, headers, timings, tests, log). Requests are sent with the same runner as
"monica run", in one session: vars.set values and cookies carry over between
sends. Press ? inside for the keys.`,
		Args: cobra.NoArgs,
		RunE: runTUI,
	}
)

func init() {
	f := tuiCmd.Flags()
	f.IntVarP(&tuiRecursive, "recursive", "r", 3, "list mon files in the current directory and subdirectories up to this depth")
	f.StringVarP(&tuiEnv, "env", "e", "", "environment from monica.yml to start with (default $"+config.EnvVar+")")
	f.StringArrayVar(&tuiVarFlags, "var", nil, "set a variable, name=value (repeatable; overrides monica.yml)")
	f.StringVarP(&tuiConfig, "config", "c", "", "config file to use instead of the nearest monica.yml")
	f.BoolVarP(&tuiInsecure, "insecure", "k", false, "skip TLS certificate verification")
	tuiTLS.register(tuiCmd)
	f.BoolVar(&tuiNoCookies, "no-cookies", false, "don't keep cookies between requests")
	rootCmd.AddCommand(tuiCmd)
}

func runTUI(cmd *cobra.Command, _ []string) error {
	vars, err := parseVars(tuiVarFlags)
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	cmd.SilenceUsage = true
	if !isTerminal(cmd.OutOrStdout()) {
		return &exitError{code: ExitUsage, err: errors.New("monica tui needs a terminal (use monica run in scripts and CI)")}
	}
	env := tuiEnv
	if !cmd.Flags().Changed("env") {
		env = os.Getenv(config.EnvVar)
	}
	configPath := tuiConfig
	if configPath == "" {
		if configPath, err = config.Find("."); err != nil {
			return &exitError{code: ExitUsage, err: err}
		}
	}
	runOpts := runner.Opts{Env: env, Vars: vars, ConfigPath: tuiConfig, Insecure: tuiInsecure}
	if err := tuiTLS.apply(&runOpts); err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	sess, err := tui.NewSession(tui.SessionOptions{
		Run:        runOpts,
		NoCookies:  tuiNoCookies,
		ConfigPath: configPath,
		LoadOptions: func() (load.Options, []load.Threshold, error) {
			return loadOptions(func(string) bool { return false }, configPath)
		},
	})
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	files := func() ([]string, error) {
		found, err := FindMonFiles(".", tuiRecursive)
		if err != nil {
			return nil, err
		}
		order := runOpts
		return runner.NewWithReporter(&order, nil).Order(found), nil
	}

	// Logs (console.log, warnings) would garble the screen: the Log tab
	// shows them instead.
	logs := tui.NewLogBuffer(0)
	logging.SetOutput(logs)
	defer logging.Reset()

	// The model's View asks for the alternate screen and mouse wheel events.
	p := tea.NewProgram(tui.New(sess, tui.Options{Files: files, Log: logs}),
		tea.WithInput(cmd.InOrStdin()), tea.WithOutput(cmd.OutOrStdout()))
	_, err = p.Run()
	if cerr := sess.Close(); err == nil && cerr != nil {
		logging.Log.Warn().Msg(cerr.Error())
	}
	if err != nil {
		return &exitError{code: ExitFailed, err: err}
	}
	return nil
}
