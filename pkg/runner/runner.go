package runner

import (
	"errors"
	"fmt"
	"io"

	"jmpeax.com/sec/monica/internal/logging"
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

// RunSingleFile parses file, sends its requests in order (except those marked
// @skip) and writes each response to out. A failed request does not stop the
// ones after it; all failures are returned joined. Read and parse failures
// are returned as *InputError before anything is sent.
func RunSingleFile(file string, r *Opts, out io.Writer) error {
	reqs, err := request.ParseMonFile(file)
	if err != nil {
		return &InputError{Err: err}
	}
	var errs []error
	for _, req := range reqs {
		where := fmt.Sprintf("%s:%d", file, req.Line)
		if req.Skip {
			msg := fmt.Sprintf("%s: skipping %s", where, req.Label())
			if req.SkipReason != "" {
				msg += ": " + req.SkipReason
			}
			logging.Log.Info().Msg(msg)
			continue
		}
		logging.Log.Info().Msgf("%s: %s", where, req.Label())
		if req.PreScript != nil || req.PostScript != nil {
			logging.Log.Warn().Msgf("%s: scripts are parsed but not run yet", where)
		}
		res, err := net.HTTPRequest(req, r.HeaderOnly)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", where, req.Label(), err))
			continue
		}
		if _, err := fmt.Fprintln(out, res); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}
