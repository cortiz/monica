package cmd

import (
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

var (
	debug   bool
	rootCmd = &cobra.Command{
		Use:     "monica",
		Short:   "Monica is a CLI tool to manage Network Requests",
		Long:    `Monica is a CLI tool to manage Network Requests like http, grpc, etc.`,
		Version: "0.1.0",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
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
	rootCmd.AddCommand(run)
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "Enable Debug logging")
}

func Execute() error {
	return rootCmd.Execute()
}
