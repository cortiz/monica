package main

import (
	"flag"
	"github.com/rs/zerolog"
	"jmpeax.com/sec/monica/internal/logging"
)

var (
	verbosity = flag.Int("v", -1, "verbosity level")
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
}
