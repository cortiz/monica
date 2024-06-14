package logging

import (
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
	"time"
)

var Log zerolog.Logger

func init() {
	initLogger()
}

func initLogger() {
	Log = log.Output(zerolog.ConsoleWriter{TimeFormat: time.RFC3339, Out: os.Stdout, NoColor: false})
}
