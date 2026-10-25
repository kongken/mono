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
	"time"

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
	// ReasonPlatformAdminProtected: the account holds the platform role, which
	// forbids closing it from the web until the role is revoked by an operator.
	ReasonPlatformAdminProtected = "PLATFORM_ADMIN_PROTECTED"
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
	admin   usersrvv1.UserAdminServiceClient
}

// New returns a client over the usersrv connection.
func New(conn grpc.ClientConnInterface) *Client {
	return &Client{account: usersrvv1.NewAccountServiceClient(conn), admin: usersrvv1.NewUserAdminServiceClient(conn)}
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

// Return is the return context usersrv binds to a mail challenge: the app the user
// came from and a pathname in it. The zero value is no context. usersrv checks only its
// form; the BFF validates its meaning against the navigation registry on both sides.
type Return struct {
	AppID string
	Path  string
}

func (c *Client) RequestRegistration(ctx context.Context, email string, ret Return) error {
	_, err := c.account.RequestRegistration(anonymous(ctx), &usersrvv1.RequestRegistrationRequest{
		Email: email, AppId: ret.AppID, ReturnPath: ret.Path})
	return MapError(err)
}

func (c *Client) CompleteRegistration(ctx context.Context, token, password, displayName string) (Return, error) {
	res, err := c.account.CompleteRegistration(anonymous(ctx), &usersrvv1.CompleteRegistrationRequest{
		Token: token, Password: password, DisplayName: displayName})
	if err != nil {
		return Return{}, MapError(err)
	}
	return Return{AppID: res.GetAppId(), Path: res.GetReturnPath()}, nil
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

func (c *Client) RequestPasswordReset(ctx context.Context, email string, ret Return) error {
	_, err := c.account.RequestPasswordReset(anonymous(ctx), &usersrvv1.RequestPasswordResetRequest{
		Email: email, AppId: ret.AppID, ReturnPath: ret.Path})
	return MapError(err)
}

func (c *Client) ResetPassword(ctx context.Context, token, newPassword string) (Return, error) {
	res, err := c.account.ResetPassword(anonymous(ctx), &usersrvv1.ResetPasswordRequest{Token: token, NewPassword: newPassword})
	if err != nil {
		return Return{}, MapError(err)
	}
	return Return{AppID: res.GetAppId(), Path: res.GetReturnPath()}, nil
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

// BeginDeletion idempotently moves the account to deleting under mutationID. An account
// that holds the platform role is refused for good: the error satisfies
// IsPlatformAdminProtected and nothing about the account has changed.
func (c *Client) BeginDeletion(ctx context.Context, userID, mutationID string) error {
	_, err := c.account.BeginDeletion(asUser(ctx, userID), &usersrvv1.BeginDeletionRequest{MutationId: mutationID})
	if Reason(err) == ReasonPlatformAdminProtected {
		return platformAdminProtected{connect.NewError(connect.CodeFailedPrecondition,
			errors.New("platform administrator accounts cannot be closed here; an operator must revoke the role first"))}
	}
	return MapError(err)
}

// platformAdminProtected marks the refusal to close a platform administrator's account. It
// unwraps to a Connect error, so the browser sees an ordinary FAILED_PRECONDITION.
type platformAdminProtected struct{ error }

func (e platformAdminProtected) Unwrap() error { return e.error }

// IsPlatformAdminProtected reports whether err is that refusal.
func IsPlatformAdminProtected(err error) bool {
	var p platformAdminProtected
	return errors.As(err, &p)
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

// AdminUser is an account as the user management section may show it.
type AdminUser struct {
	UserID      string
	Email       string
	DisplayName string
	Status      Status
	Roles       []string
	CreatedAt   time.Time
	// AuthVersion is the version a change of this account must name.
	AuthVersion int64
}

// UserChange is what a console change carries to usersrv. The operator is not here:
// it is the call's credential, taken from the trusted session.
type UserChange struct {
	UserID              string
	ExpectedAuthVersion int64
	MutationID          string
	Reason              string
}

// ChangeResult is the account after a console change.
type ChangeResult struct {
	UserID      string
	Status      Status
	Roles       []string
	AuthVersion int64
	Changed     bool
	Replayed    bool
}

// UserQuery narrows a user list. At most one of Email, UserID and Nickname is set.
type UserQuery struct {
	Email, UserID, Nickname string
	Status                  Status
	Role                    string
	PageSize                int32
	PageToken               string
}

// asAdmin describes a call made by the administrator userID. Unlike asUser it keeps the
// roles the ingress verified from the session, because usersrv.v1.UserAdminService
// declares a required role and checks it again from the credential.
func asAdmin(ctx context.Context, userID string) context.Context {
	c := rpcauth.CallFrom(ctx)
	c.UserID, c.TokenID = userID, ""
	return rpcauth.WithCall(ctx, c)
}

// ListUsers pages through accounts for the administrator operatorID.
func (c *Client) ListUsers(ctx context.Context, operatorID string, q UserQuery) ([]AdminUser, string, error) {
	res, err := c.admin.ListUsers(asAdmin(ctx, operatorID), &usersrvv1.ListUsersRequest{
		Email: q.Email, UserId: q.UserID, Nickname: q.Nickname, Status: protoStatus(q.Status), Role: q.Role,
		PageSize: q.PageSize, PageToken: q.PageToken,
	})
	if err != nil {
		return nil, "", MapError(err)
	}
	users := make([]AdminUser, 0, len(res.GetUsers()))
	for _, u := range res.GetUsers() {
		users = append(users, adminUser(u))
	}
	return users, res.GetNextPageToken(), nil
}

// GetUser reads one account for the administrator operatorID; a missing account is ErrNotFound.
func (c *Client) GetUser(ctx context.Context, operatorID, userID string) (AdminUser, error) {
	res, err := c.admin.GetUser(asAdmin(ctx, operatorID), &usersrvv1.GetUserRequest{UserId: userID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return AdminUser{}, ErrNotFound
		}
		return AdminUser{}, MapError(err)
	}
	return adminUser(res.GetUser()), nil
}

func adminUser(u *usersrvv1.AdminUser) AdminUser {
	return AdminUser{UserID: u.GetUserId(), Email: u.GetEmail(), DisplayName: u.GetDisplayName(), Status: statusOf(u.GetStatus()),
		Roles: u.GetRoles(), CreatedAt: u.GetCreatedAt().AsTime(), AuthVersion: u.GetAuthVersion()}
}

type changeCall func(context.Context, *usersrvv1.ChangeUserRequest, ...grpc.CallOption) (*usersrvv1.ChangeUserResponse, error)

func (c *Client) change(ctx context.Context, operatorID string, in UserChange, call func(usersrvv1.UserAdminServiceClient) changeCall) (ChangeResult, error) {
	res, err := call(c.admin)(asAdmin(ctx, operatorID), &usersrvv1.ChangeUserRequest{
		UserId: in.UserID, ExpectedAuthVersion: in.ExpectedAuthVersion, MutationId: in.MutationID, Reason: in.Reason})
	if err != nil {
		return ChangeResult{}, MapError(err)
	}
	return ChangeResult{UserID: res.GetUserId(), Status: statusOf(res.GetStatus()), Roles: res.GetRoles(), AuthVersion: res.GetAuthVersion(),
		Changed: res.GetChanged(), Replayed: res.GetReplayed()}, nil
}

// DisableUser, EnableUser, GrantMetroAdmin and RevokeMetroAdmin are the console's four
// account changes, made as the administrator operatorID. usersrv enforces the target
// constraints; a refusal arrives as a browser-safe error.
func (c *Client) DisableUser(ctx context.Context, operatorID string, in UserChange) (ChangeResult, error) {
	return c.change(ctx, operatorID, in, func(a usersrvv1.UserAdminServiceClient) changeCall { return a.DisableUser })
}

func (c *Client) EnableUser(ctx context.Context, operatorID string, in UserChange) (ChangeResult, error) {
	return c.change(ctx, operatorID, in, func(a usersrvv1.UserAdminServiceClient) changeCall { return a.EnableUser })
}

func (c *Client) GrantMetroAdmin(ctx context.Context, operatorID string, in UserChange) (ChangeResult, error) {
	return c.change(ctx, operatorID, in, func(a usersrvv1.UserAdminServiceClient) changeCall { return a.GrantMetroAdmin })
}

func (c *Client) RevokeMetroAdmin(ctx context.Context, operatorID string, in UserChange) (ChangeResult, error) {
	return c.change(ctx, operatorID, in, func(a usersrvv1.UserAdminServiceClient) changeCall { return a.RevokeMetroAdmin })
}

func protoStatus(s Status) usersrvv1.AccountStatus {
	switch s {
	case StatusActive:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case StatusDisabled:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED
	case StatusDeleting:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_DELETING
	}
	return usersrvv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
}
