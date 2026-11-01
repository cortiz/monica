package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/pkg/scaffold"
)

var (
	initForce   bool
	initBaseURL string

	initCmd = &cobra.Command{
		Use:   "init [dir]",
		Short: "Creates a monica.yml, an example mon file and .gitignore entries",
		Long: `Scaffolds a Monica project in dir (default: the current directory):
monica.yml with a baseUrl variable and a dev environment, example.mon with two
requests and tests, and .gitignore entries for .env and monica.local.yml.
Existing files are kept unless --force; .gitignore is only appended to.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runInit,
	}
)

func init() {
	initCmd.Flags().BoolVarP(&initForce, "force", "f", false, "overwrite an existing monica.yml and example.mon")
	initCmd.Flags().StringVar(&initBaseURL, "base-url", scaffold.DefaultBaseURL, "value of the baseUrl variable in monica.yml")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	cmd.SilenceUsage = true
	results, err := scaffold.Init(dir, scaffold.Options{BaseURL: initBaseURL, Force: initForce})
	out := cmd.OutOrStdout()
	for _, r := range results {
		note := ""
		if r.Action == scaffold.Skipped {
			note = " (exists; --force to overwrite)"
			if filepath.Base(r.Path) == ".gitignore" {
				note = " (already lists .env and monica.local.yml)"
			}
		}
		_, _ = fmt.Fprintf(out, "%-11s %s%s\n", r.Action, r.Path, note)
	}
	if err != nil {
		return &exitError{code: ExitUsage, err: err}
	}
	return nil
}
