package upstream

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ToConnect turns an error from a business service into the one a browser may
// see. Service messages are not forwarded: they can describe internals. An
// UNAUTHENTICATED answer means the BFF's own credential was refused, which is a
// server fault and not the user's sign-in state, so it is reported as internal.
func ToConnect(service string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	}
	code := status.Code(err)
	switch code {
	case codes.Unauthenticated, codes.Unknown, codes.DataLoss, codes.Internal:
		slog.Error("service refused the BFF call", "service", service, "code", code.String())
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	case codes.Unavailable:
		slog.Warn("service unavailable", "service", service)
		return connect.NewError(connect.CodeUnavailable, errors.New("service unavailable"))
	case codes.DeadlineExceeded:
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("service timed out"))
	case codes.Canceled:
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	}
	// The remaining gRPC codes are numbered like Connect codes; the domain
	// message is written for users and stays.
	return connect.NewError(connect.Code(code), errors.New(status.Convert(err).Message()))
}
