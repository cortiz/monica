package main

import (
	"flag"
	"github.com/rs/zerolog"
	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/runner"
	"os"
)

var (
	verbosity  = flag.Int("v", -1, "verbosity level")
	file       = flag.String("f", "", "Mon File to run")
	headerOnly = flag.Bool("h", false, "Output headers only")
)

func main() {
	flag.Parse()
	switch *verbosity {
	case 1:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case 2:
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	logging.Log.Info().Msg("Welcome to Monica!")
	if *file == "" {
		logging.Log.Error().Msg("No Mon File provided")
		return
	}
	monFile, err := os.Stat(*file)
	if err != nil {
		logging.Log.Error().Msgf("Mon File not found: %s", *file)
		return
	}
	if !monFile.Mode().IsRegular() {
		logging.Log.Error().Msgf("Mon File is not a regular file: %s", *file)
		return
	}
	logging.Log.Info().Msgf("Running Mon File: %s", *file)
	runner.RunSingleFile(*file, &runner.Opts{HeaderOnly: *headerOnly})
}
