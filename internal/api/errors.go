package api

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/use-fabrica/loom/internal/engine"
)

// errInternal is the message a CodeInternal error exposes on the wire. The
// real cause is preserved as far as the interceptor (see interceptor.go),
// which logs it in full and swaps in this sentinel before the response is
// sent: a client must never see SQL, provider, or other internal detail.
var errInternal = errors.New("internal error")

// mapError classifies a Service or context error into the Connect status
// code the proto contract promises. Every branch keeps err as the returned
// error's cause (via Unwrap), so the interceptor can still log the full
// detail for the CodeInternal case before redacting the response.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, engine.ErrInvalidArgument):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, engine.ErrReindexRequired):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, engine.ErrWorkFailed):
		return connect.NewError(connect.CodeAborted, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
