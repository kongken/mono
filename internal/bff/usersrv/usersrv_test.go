package usersrv_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/usersrv"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func withReason(t *testing.T, code codes.Code, reason, msg string) error {
	t.Helper()
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{Domain: "usersrv.v1", Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	return st.Err()
}

func TestMapErrorKeepsAuthenticationAndInternalFailuresApart(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want connect.Code
		msg  string
	}{
		"bad credentials":    {withReason(t, codes.Unauthenticated, usersrv.ReasonInvalidCredentials, "x"), connect.CodeUnauthenticated, usersrv.LoginFailure},
		"credential refused": {status.Error(codes.Unauthenticated, "bad token"), connect.CodeInternal, "internal error"},
		"unavailable":        {status.Error(codes.Unavailable, "dial"), connect.CodeUnavailable, "account service unavailable"},
		"invalid":            {withReason(t, codes.InvalidArgument, "INVALID_ARGUMENT", "password too short"), connect.CodeInvalidArgument, "password too short"},
		"unknown":            {status.Error(codes.DataLoss, "secret detail"), connect.CodeInternal, "internal error"},
	} {
		got := usersrv.MapError(tc.err)
		var ce *connect.Error
		if !errors.As(got, &ce) || ce.Code() != tc.want || ce.Message() != tc.msg {
			t.Errorf("%s: %v, want %v %q", name, got, tc.want, tc.msg)
		}
	}
	if usersrv.MapError(nil) != nil {
		t.Error("nil became an error")
	}
}
