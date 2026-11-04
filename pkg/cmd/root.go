package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/version"
)

// Process exit codes.
const (
	ExitOK     = 0 // all requests succeeded
	ExitFailed = 1 // at least one request failed
	ExitUsage  = 2 // usage or configuration/input error
)

// exitError carries the exit code a command failure should produce.
// silent means the command already printed what went wrong.
type exitError struct {
	code   int
	err    error
	silent bool
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

var (
	debug   bool
	rootCmd = &cobra.Command{
		Use:   "monica",
		Short: "Monica runs, tests and load-tests HTTP requests written in plain-text .mon files",
		Long: `Monica runs HTTP requests written as plain-text .mon files (raw request
text plus a few directives), tests their responses with JavaScript or
TypeScript scripts, and load-tests them with virtual users.`,
		Version:       version.Get(),
		SilenceErrors: true, // Execute prints errors itself
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if debug {
				zerolog.SetGlobalLevel(zerolog.TraceLevel)
			} else {
				zerolog.SetGlobalLevel(zerolog.InfoLevel)
			}
		},
	}
)

func init() {
	rootCmd.AddCommand(run, validateCmd)
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable Debug logging")
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	return execute(rootCmd, os.Args[1:], os.Stderr)
}

func execute(cmd *cobra.Command, args []string, stderr io.Writer) int {
	registerCompletionsOnce.Do(registerCompletions)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) && ee.silent {
		return ee.code
	}
	_, _ = fmt.Fprintln(stderr, "Error:", err)
	return exitCode(err)
}

// exitCode maps an error to a process exit code. Errors that did not come
// from a command's RunE (unknown flags, bad args) are usage errors.
func exitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return ExitUsage
}
