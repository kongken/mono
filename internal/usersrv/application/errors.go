package application

import (
	"context"
	"errors"
	"log/slog"

	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorDomain is the google.rpc.ErrorInfo domain of every usersrv error detail.
const ErrorDomain = "usersrv.v1"

// The ErrorInfo reasons documented in proto/usersrv/v1/account.proto.
const (
	ReasonInvalidCredentials = "INVALID_CREDENTIALS"
	ReasonAccountNotActive   = "ACCOUNT_NOT_ACTIVE"
	ReasonVersionConflict    = "VERSION_CONFLICT"
	ReasonRateLimited        = "RATE_LIMITED"
	ReasonInvalidArgument    = "INVALID_ARGUMENT"
	ReasonDeletionMismatch   = "DELETION_MISMATCH"
	ReasonTokenInvalid       = "TOKEN_INVALID"
	ReasonRegistrationClosed = "REGISTRATION_CLOSED"
	// ReasonPlatformAdminProtected: BeginDeletion of an account that holds the
	// platform role ozid-admin; the role must be revoked through the operator CLI first.
	ReasonPlatformAdminProtected = "PLATFORM_ADMIN_PROTECTED"
)

func detailed(code codes.Code, message, reason string, metadata map[string]string) error {
	st := status.New(code, message)
	if withDetail, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: ErrorDomain, Metadata: metadata}); err == nil {
		return withDetail.Err()
	}
	return st.Err()
}

// errRateLimited never says how much quota is left or which counter tripped.
func errRateLimited() error {
	return detailed(codes.ResourceExhausted, "too many requests, try again later", ReasonRateLimited, nil)
}

func errUnavailable() error {
	return status.Error(codes.Unavailable, "temporarily unavailable")
}

// toStatus maps an account-core or limiter error to the status the BFF sees.
// Messages are fixed text: no database, Redis or hashing detail, and never the
// offending input.
func toStatus(op string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(interface{ GRPCStatus() *status.Status }); ok {
		return err
	}
	if v, ok := identity.IsValidation(err); ok {
		return detailed(codes.InvalidArgument, v.Field+": "+v.Reason, ReasonInvalidArgument, map[string]string{"field": v.Field})
	}
	switch {
	case errors.Is(err, account.ErrInvalidCredentials):
		return detailed(codes.Unauthenticated, "invalid credentials", ReasonInvalidCredentials, nil)
	case errors.Is(err, account.ErrNotActive):
		return detailed(codes.FailedPrecondition, "account is not active", ReasonAccountNotActive, nil)
	case errors.Is(err, account.ErrNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, account.ErrConflict):
		return detailed(codes.Aborted, "concurrent change, retry", ReasonVersionConflict, nil)
	case errors.Is(err, account.ErrDeletionMismatch):
		return detailed(codes.FailedPrecondition, "deletion belongs to another request", ReasonDeletionMismatch, nil)
	case errors.Is(err, account.ErrTokenInvalid):
		return detailed(codes.FailedPrecondition, "link is not usable", ReasonTokenInvalid, nil)
	case errors.Is(err, account.ErrRegistrationClosed):
		return detailed(codes.FailedPrecondition, "registration is closed", ReasonRegistrationClosed, nil)
	case errors.Is(err, account.ErrPlatformAdmin):
		return detailed(codes.FailedPrecondition, "platform administrator accounts cannot be closed here", ReasonPlatformAdminProtected, nil)
	case errors.Is(err, account.ErrForbidden):
		return status.Error(codes.PermissionDenied, "permission denied")
	case errors.Is(err, account.ErrRateLimited):
		return errRateLimited()
	case errors.Is(err, account.ErrEmailTaken):
		return status.Error(codes.AlreadyExists, "email already registered")
	case errors.Is(err, account.ErrUnavailable), errors.Is(err, ratelimit.ErrUnavailable),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		if !errors.Is(err, context.Canceled) {
			slog.Warn("usersrv unavailable", "op", op, "error", err)
		}
		return errUnavailable()
	}
	slog.Error("usersrv "+op, "error", err)
	return status.Error(codes.Internal, "internal error")
}
