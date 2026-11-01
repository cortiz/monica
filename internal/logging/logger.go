package logging

import (
	"io"
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

// SetOutput sends logs to w as plain text lines (no colors), e.g. to keep
// them off the terminal while a full-screen UI runs. It is not safe to call
// while other goroutines log.
func SetOutput(w io.Writer) {
	Log = log.Output(zerolog.ConsoleWriter{TimeFormat: time.Kitchen, Out: w, NoColor: true})
}

// Reset sends logs to stderr again.
func Reset() { initLogger() }
