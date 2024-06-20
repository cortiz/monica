package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/runner"
)

var (
	singleFile     string
	recursiveLevel int
	run            = &cobra.Command{
		Use:   "run",
		Short: "Runs all mon files",
		Long:  "Runs all mon files found in the current working directory",
		Run: func(cmd *cobra.Command, args []string) {
			if singleFile != "" {
				runSingleFile(singleFile)
			} else {
				runAllFiles(recursiveLevel)
			}
		},
	}
)

func init() {
	run.Flags().StringVarP(&singleFile, "single", "s", "", "runs a single mon file, path can be absolute o relative to current working directory")
	run.Flags().IntVarP(&recursiveLevel, "recursive", "r", 1, "runs all mon files in the current working directory and subdirectories up to the specified level")
}

func runSingleFile(file string) {
	runner.RunSingleFile(file, &runner.Opts{
		HeaderOnly: false,
	})
}

func runAllFiles(level int) {
	files, err := FindMonFiles(".", level)
	logging.Log.Info().Msgf("Found %d mon files", len(files))
	if err != nil {
		logging.Log.Error().Err(err).Msg("Error finding mon files")
		os.Exit(1)
	}
	for _, file := range files {
		runner.RunSingleFile(file, &runner.Opts{
			HeaderOnly: false,
		})
	}
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
