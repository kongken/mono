// Package usersrv is the BFF's adapter to the internal account service. It is the
// only place that calls usersrv.v1.AccountService: every other BFF package asks
// for a typed result and gets a browser-safe error back.
//
// The BFF never imports internal/usersrv; the two meet through the generated gRPC
// contract. Each call is signed by the upstream client interceptor with the
// Call in the context, so a method that acts on a user is always called with that
// user as the credential's sub, and the anonymous methods are called without one.
package usersrv

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service is the upstream name in the gateway route table and BFF configuration.
const Service = "usersrv"

// errorDomain is the google.rpc.ErrorInfo domain of usersrv's domain errors. Only an
// error carrying it is an answer of usersrv about the caller; an UNAUTHENTICATED
// status without it means the BFF's own service credential was refused.
const errorDomain = "usersrv.v1"

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
)

// LoginFailure is the one text of every failed login (spec 5.2): it does not say
// whether the address exists, the password was wrong or the account is unusable.
const LoginFailure = "邮箱或密码错误，或账号不可用"

// Status is the account state usersrv reports for the caller.
type Status int

const (
	StatusUnknown Status = iota
	StatusActive
	StatusDisabled
	StatusDeleting
)

// AuthState is what the BFF re-checks before it forwards a request.
type AuthState struct {
	Status         Status
	AuthVersion    int64
	Roles          []string
	ProfileVersion int64
}

// Profile is the cross-product nickname and avatar.
type Profile struct {
	DisplayName    string
	AvatarURL      string
	ProfileVersion int64
}

// Account is the caller's own account, including the login email.
type Account struct {
	UserID      string
	Email       string
	Profile     Profile
	Roles       []string
	Status      Status
	AuthVersion int64
}

// Login is what usersrv knows after a correct email and password.
type Login struct {
	UserID      string
	Profile     Profile
	Roles       []string
	AuthVersion int64
}

// ErrNotFound means the account does not exist (never registered, or deleted).
var ErrNotFound = errors.New("account not found")

// Client calls usersrv.
type Client struct {
	account usersrvv1.AccountServiceClient
}

// New returns a client over the usersrv connection.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{account: usersrvv1.NewAccountServiceClient(conn)}
}

// anonymous describes a call without a user: the client address and request ID of
// the browser request survive, a user and roles never do.
func anonymous(ctx context.Context) context.Context {
	c := rpcauth.CallFrom(ctx)
	c.UserID, c.Roles, c.TokenID = "", nil, ""
	return rpcauth.WithCall(ctx, c)
}

// asUser describes a call made for userID.
func asUser(ctx context.Context, userID string) context.Context {
	c := rpcauth.CallFrom(ctx)
	c.UserID, c.Roles, c.TokenID = userID, nil, ""
	return rpcauth.WithCall(ctx, c)
}

func (c *Client) RequestRegistration(ctx context.Context, email, appID string) error {
	_, err := c.account.RequestRegistration(anonymous(ctx), &usersrvv1.RequestRegistrationRequest{Email: email, AppId: appID})
	return MapError(err)
}

func (c *Client) CompleteRegistration(ctx context.Context, token, password, displayName string) error {
	_, err := c.account.CompleteRegistration(anonymous(ctx), &usersrvv1.CompleteRegistrationRequest{
		Token: token, Password: password, DisplayName: displayName})
	return MapError(err)
}

// Authenticate checks an email and password. A failure that says the credentials
// are wrong, or the account is not usable, is returned as LoginFailure.
func (c *Client) Authenticate(ctx context.Context, email, password string) (Login, error) {
	res, err := c.account.AuthenticateWithEmail(anonymous(ctx), &usersrvv1.AuthenticateWithEmailRequest{Email: email, Password: password})
	if err != nil {
		if r := Reason(err); r == ReasonInvalidCredentials || r == ReasonAccountNotActive {
			return Login{}, connect.NewError(connect.CodeUnauthenticated, errors.New(LoginFailure))
		}
		return Login{}, MapError(err)
	}
	return Login{UserID: res.GetUserId(), Profile: profile(res.GetProfile()), Roles: res.GetRoles(), AuthVersion: res.GetAuthVersion()}, nil
}

func (c *Client) RequestPasswordReset(ctx context.Context, email, appID string) error {
	_, err := c.account.RequestPasswordReset(anonymous(ctx), &usersrvv1.RequestPasswordResetRequest{Email: email, AppId: appID})
	return MapError(err)
}

func (c *Client) ResetPassword(ctx context.Context, token, newPassword string) error {
	_, err := c.account.ResetPassword(anonymous(ctx), &usersrvv1.ResetPasswordRequest{Token: token, NewPassword: newPassword})
	return MapError(err)
}

// AuthState reads the status, auth_version and current roles of userID. It returns
// ErrNotFound when the account does not exist and a Connect error (UNAVAILABLE
// when usersrv or its stores cannot answer) otherwise.
func (c *Client) AuthState(ctx context.Context, userID string) (AuthState, error) {
	res, err := c.account.GetAuthState(asUser(ctx, userID), &usersrvv1.GetAuthStateRequest{})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return AuthState{}, ErrNotFound
		}
		return AuthState{}, MapError(err)
	}
	return AuthState{Status: statusOf(res.GetStatus()), AuthVersion: res.GetAuthVersion(), Roles: res.GetRoles(), ProfileVersion: res.GetProfileVersion()}, nil
}

// Account reads the caller's own account.
func (c *Client) Account(ctx context.Context, userID string) (Account, error) {
	res, err := c.account.GetMyAccount(asUser(ctx, userID), &usersrvv1.GetMyAccountRequest{})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return Account{}, ErrNotFound
		}
		return Account{}, MapError(err)
	}
	return Account{UserID: res.GetUserId(), Email: res.GetEmail(), Profile: profile(res.GetProfile()), Roles: res.GetRoles(),
		Status: statusOf(res.GetStatus()), AuthVersion: res.GetAuthVersion()}, nil
}

// VerifyPassword re-checks the current password and returns the account's
// auth_version. A wrong password is UNAUTHENTICATED "current password is incorrect".
func (c *Client) VerifyPassword(ctx context.Context, userID, password string) (int64, error) {
	res, err := c.account.VerifyPassword(asUser(ctx, userID), &usersrvv1.VerifyPasswordRequest{Password: password})
	if err != nil {
		return 0, wrongPassword(err)
	}
	return res.GetAuthVersion(), nil
}

// ChangePassword changes the password; usersrv bumps auth_version.
func (c *Client) ChangePassword(ctx context.Context, userID, current, next string) (int64, error) {
	res, err := c.account.ChangePassword(asUser(ctx, userID), &usersrvv1.ChangePasswordRequest{CurrentPassword: current, NewPassword: next})
	if err != nil {
		return 0, wrongPassword(err)
	}
	return res.GetAuthVersion(), nil
}

func (c *Client) UpdateProfile(ctx context.Context, userID, displayName, avatarURL string) (Profile, error) {
	res, err := c.account.UpdateMyProfile(asUser(ctx, userID), &usersrvv1.UpdateMyProfileRequest{DisplayName: displayName, AvatarUrl: avatarURL})
	if err != nil {
		return Profile{}, MapError(err)
	}
	return profile(res.GetProfile()), nil
}

// BeginDeletion idempotently moves the account to deleting under mutationID.
func (c *Client) BeginDeletion(ctx context.Context, userID, mutationID string) error {
	_, err := c.account.BeginDeletion(asUser(ctx, userID), &usersrvv1.BeginDeletionRequest{MutationId: mutationID})
	return MapError(err)
}

// FinishDeletion removes the account (or confirms an earlier removal by its
// tombstone).
func (c *Client) FinishDeletion(ctx context.Context, userID, mutationID string) error {
	_, err := c.account.FinishDeletion(asUser(ctx, userID), &usersrvv1.FinishDeletionRequest{MutationId: mutationID})
	return MapError(err)
}

func profile(p *usersrvv1.BaseProfile) Profile {
	return Profile{DisplayName: p.GetDisplayName(), AvatarURL: p.GetAvatarUrl(), ProfileVersion: p.GetProfileVersion()}
}

func statusOf(s usersrvv1.AccountStatus) Status {
	switch s {
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return StatusActive
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED:
		return StatusDisabled
	case usersrvv1.AccountStatus_ACCOUNT_STATUS_DELETING:
		return StatusDeleting
	}
	return StatusUnknown
}

func wrongPassword(err error) error {
	if Reason(err) == ReasonInvalidCredentials {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("current password is incorrect"))
	}
	return MapError(err)
}

// Reason returns the usersrv ErrorInfo reason of err, or "" when err is not a
// usersrv domain error (in particular for a refused service credential).
func Reason(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return ""
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == errorDomain {
			return info.GetReason()
		}
	}
	return ""
}

// MapError turns a usersrv failure into the error a browser may see. A domain
// error keeps its fixed message; a failure of the BFF's own credential
// (UNAUTHENTICATED without a usersrv reason) is an internal error, never "sign in
// again". nil stays nil.
func MapError(err error) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	}
	st := status.Convert(err)
	reason := Reason(err)
	switch st.Code() {
	case codes.Unauthenticated:
		if reason == ReasonInvalidCredentials {
			return connect.NewError(connect.CodeUnauthenticated, errors.New(LoginFailure))
		}
		slog.Error("usersrv refused the BFF service credential", "code", st.Code().String())
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	case codes.Unavailable:
		return connect.NewError(connect.CodeUnavailable, errors.New("account service unavailable"))
	case codes.DeadlineExceeded:
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("account service timed out"))
	case codes.Canceled:
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	case codes.InvalidArgument, codes.FailedPrecondition, codes.Aborted, codes.ResourceExhausted, codes.AlreadyExists, codes.NotFound, codes.PermissionDenied:
		// usersrv writes these messages for users: fixed text, no internals.
		if reason == "" {
			slog.Warn("usersrv answered without a reason", "code", st.Code().String())
		}
		return connect.NewError(connect.Code(st.Code()), errors.New(st.Message()))
	}
	slog.Error("usersrv call failed", "code", st.Code().String(), "reason", reason)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
