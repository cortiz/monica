package runner

import (
	"fmt"
	"io"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
)

// InputError marks a failure caused by the .mon input itself (unreadable
// file, invalid syntax) rather than by sending the request.
type InputError struct {
	Err error
}

func (e *InputError) Error() string { return e.Err.Error() }
func (e *InputError) Unwrap() error { return e.Err }

// RunSingleFile parses file, sends the request and writes the response to out.
// Read and parse failures are returned as *InputError.
func RunSingleFile(file string, r *Opts, out io.Writer) error {
	req, err := request.ParseMonFile(file)
	if err != nil {
		return &InputError{Err: err}
	}
	res, err := net.HTTPRequest(req, r.HeaderOnly)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	_, err = fmt.Fprintln(out, res)
	return err
}
