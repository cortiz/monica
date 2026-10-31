package script

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/dop251/goja"
	"jmpeax.com/sec/monica/pkg/request"
)

// DefaultTimeout bounds a script run when Context.Timeout is zero.
const DefaultTimeout = 5 * time.Second

// ErrorKind classifies a script failure.
type ErrorKind string

const (
	KindSyntax    ErrorKind = "syntax"    // the script does not compile
	KindException ErrorKind = "exception" // the script threw
	KindTimeout   ErrorKind = "timeout"   // the script ran too long
)

// Error is a script failure located in the script's file (the .mon file for
// inline blocks). Line and Column are 0 when unknown.
type Error struct {
	Kind   ErrorKind
	File   string
	Line   int
	Column int
	Msg    string
}

func (e *Error) Error() string {
	switch {
	case e.Line > 0 && e.Column > 0:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Msg)
	case e.Line > 0:
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
	default:
		return fmt.Sprintf("%s: %s", e.File, e.Msg)
	}
}

// syntaxPosRe extracts line, column and message from a goja syntax error.
var syntaxPosRe = regexp.MustCompile(`Line (\d+):(\d+) (.*?)(?: \(and \d+ more errors?\))?$`)

// timeoutValue is what the engine interrupts the runtime with.
type timeoutValue struct{ after time.Duration }

// toError converts a goja error for script s into an *Error. Other errors
// (e.g. from writing back req) pass through unchanged.
func toError(s *request.Script, err error) error {
	if err == nil {
		return nil
	}
	name := s.File
	if name == "" {
		name = "script"
	}
	var syn *goja.CompilerSyntaxError
	if errors.As(err, &syn) {
		e := &Error{Kind: KindSyntax, File: name, Msg: "SyntaxError: " + syn.Message}
		if syn.File != nil {
			pos := syn.File.Position(syn.Offset)
			e.Line, e.Column = pos.Line, pos.Column
		} else if m := syntaxPosRe.FindStringSubmatch(syn.Message); m != nil {
			// goja only gives the position as text: "file: Line 5:7 msg".
			e.Line, _ = strconv.Atoi(m[1])
			e.Column, _ = strconv.Atoi(m[2])
			e.Msg = "SyntaxError: " + m[3]
		}
		return e
	}
	var intr *goja.InterruptedError
	if errors.As(err, &intr) {
		e := &Error{Kind: KindTimeout, File: name, Line: s.Line, Msg: "script interrupted"}
		if tv, ok := intr.Value().(timeoutValue); ok {
			e.Msg = fmt.Sprintf("script timed out after %s", tv.after)
		}
		return e
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		e := &Error{Kind: KindException, File: name, Msg: ex.Value().String()}
		// The innermost frame in the script's own source is the throw site.
		for _, f := range ex.Stack() {
			if pos := f.Position(); pos.Filename == name && pos.Line > 0 {
				e.Line, e.Column = pos.Line, pos.Column
				break
			}
		}
		return e
	}
	return err
}

// interruptAfter stops the runtime after d; call the returned func when the
// run is over.
func (e *Engine) interruptAfter(d time.Duration) func() {
	if d <= 0 {
		d = DefaultTimeout
	}
	timer := time.AfterFunc(d, func() { e.rt.Interrupt(timeoutValue{after: d}) })
	return func() {
		timer.Stop()
		e.rt.ClearInterrupt()
	}
}
