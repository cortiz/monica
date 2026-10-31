package logging

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var Log zerolog.Logger

func init() {
	initLogger()
}

// initLogger writes logs to stderr so stdout carries only request output.
func initLogger() {
	Log = log.Output(zerolog.ConsoleWriter{TimeFormat: time.RFC3339, Out: os.Stderr, NoColor: false})
}
