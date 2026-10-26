package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/ratelimit"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Limiter counts attempts; *ratelimit.Limiter implements it.
type Limiter interface {
	Allow(ctx context.Context, rule ratelimit.Rule, subject ...string) (bool, error)
}

// Limits are the rules AccountService applies.
type Limits struct {
	PasswordPerAccount  ratelimit.Rule
	PasswordPerIP       ratelimit.Rule
	ProfileWritePerUser ratelimit.Rule
	DeletionPerUser     ratelimit.Rule
	// TokenSubmitPerIP and TokenSubmitPerToken bound CompleteRegistration and
	// ResetPassword; every submission counts, successful or not.
	TokenSubmitPerIP    ratelimit.Rule
	TokenSubmitPerToken ratelimit.Rule
	// Mail are the limits of account mail requests, applied by the registration
	// and reset request methods through outbox.Guard.
	Mail ratelimit.MailRules
}

// AccountService implements usersrv.v1.AccountService: registration with email
// verification, sign-in, password reset, auth state, password check and change,
// profile and deletion. Every method applies its rate limits before it does any
// work, and a limiter failure refuses the request instead of letting it through.
type AccountService struct {
	usersrvv1.UnimplementedAccountServiceServer
	accounts *account.Service
	limiter  Limiter
	limits   Limits
}

func NewAccountService(accounts *account.Service, limiter Limiter, limits Limits) *AccountService {
	return &AccountService{accounts: accounts, limiter: limiter, limits: limits}
}

// caller returns the user of the verified credential. The request never names a
// user: whatever the BFF signed as sub is the only account these methods touch.
func caller(ctx context.Context) (uuid.UUID, string, error) {
	call := rpcauth.CallFrom(ctx)
	id, err := uuid.Parse(call.UserID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, "", status.Error(codes.Unauthenticated, "invalid or missing service credential")
	}
	return id, call.RequestID, nil
}

// allow counts one attempt. A limit hit is RESOURCE_EXHAUSTED; a store failure
// is UNAVAILABLE and the guarded work does not run.
func (s *AccountService) allow(ctx context.Context, rule ratelimit.Rule, subject ...string) error {
	ok, err := s.limiter.Allow(ctx, rule, subject...)
	if err != nil {
		return toStatus("ratelimit", err)
	}
	if !ok {
		return errRateLimited()
	}
	return nil
}

// allowPassword applies the per-account and per-IP password limits. Both count
// every attempt, successful or not.
func (s *AccountService) allowPassword(ctx context.Context, id uuid.UUID) error {
	if err := s.allow(ctx, s.limits.PasswordPerAccount, id.String()); err != nil {
		return err
	}
	if ip := rpcauth.CallFrom(ctx).ClientIP; ip != "" {
		return s.allow(ctx, s.limits.PasswordPerIP, ip)
	}
	return nil
}

// clientIP and requestID come from the verified BFF credential; the browser
// cannot supply either.
func callMeta(ctx context.Context) (clientIP, requestID string) {
	call := rpcauth.CallFrom(ctx)
	return call.ClientIP, call.RequestID
}

// allowIP counts one attempt against an IP rule when the BFF supplied the address.
func (s *AccountService) allowIP(ctx context.Context, rule ratelimit.Rule, ip string) error {
	if ip == "" {
		return nil
	}
	return s.allow(ctx, rule, ip)
}

// allowToken counts one token submission against the client and the token. The
// token enters the counter key only as its SHA-256 digest (the limiter HMACs it
// again), whether or not it is well formed or exists.
func (s *AccountService) allowToken(ctx context.Context, token string) error {
	ip, _ := callMeta(ctx)
	if err := s.allowIP(ctx, s.limits.TokenSubmitPerIP, ip); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(token))
	return s.allow(ctx, s.limits.TokenSubmitPerToken, hex.EncodeToString(digest[:]))
}

// RequestRegistration always answers the same for a well-formed address.
func (s *AccountService) RequestRegistration(ctx context.Context, req *usersrvv1.RequestRegistrationRequest) (*usersrvv1.RequestRegistrationResponse, error) {
	ip, requestID := callMeta(ctx)
	if err := s.accounts.RequestRegistration(ctx, req.GetEmail(), account.ReturnContext{AppID: req.GetAppId(), Path: req.GetReturnPath()}, ip, requestID); err != nil {
		return nil, toStatus("request_registration", err)
	}
	return &usersrvv1.RequestRegistrationResponse{}, nil
}

func (s *AccountService) CompleteRegistration(ctx context.Context, req *usersrvv1.CompleteRegistrationRequest) (*usersrvv1.CompleteRegistrationResponse, error) {
	if err := s.allowToken(ctx, req.GetToken()); err != nil {
		return nil, err
	}
	_, requestID := callMeta(ctx)
	_, ret, err := s.accounts.CompleteRegistration(ctx, req.GetToken(), req.GetPassword(), req.GetDisplayName(), requestID)
	if err != nil {
		return nil, toStatus("complete_registration", err)
	}
	return &usersrvv1.CompleteRegistrationResponse{AppId: ret.AppID, ReturnPath: ret.Path}, nil
}

// AuthenticateWithEmail counts the attempt per mailbox and per client address
// (every attempt, known address or not) before it spends a password hash.
func (s *AccountService) AuthenticateWithEmail(ctx context.Context, req *usersrvv1.AuthenticateWithEmailRequest) (*usersrvv1.AuthenticateWithEmailResponse, error) {
	ip, requestID := callMeta(ctx)
	canonical, err := identity.NormalizeEmail(req.GetEmail())
	if err != nil {
		return nil, toStatus("authenticate_with_email", err)
	}
	if err = s.allow(ctx, s.limits.PasswordPerAccount, "email", canonical); err != nil {
		return nil, err
	}
	if err = s.allowIP(ctx, s.limits.PasswordPerIP, ip); err != nil {
		return nil, err
	}
	id, st, err := s.accounts.Login(ctx, canonical, req.GetPassword(), requestID)
	if err != nil {
		return nil, toStatus("authenticate_with_email", err)
	}
	return &usersrvv1.AuthenticateWithEmailResponse{
		UserId: id.String(), Profile: protoProfile(st.Profile), Roles: st.Roles, AuthVersion: st.AuthVersion,
	}, nil
}

func (s *AccountService) RequestPasswordReset(ctx context.Context, req *usersrvv1.RequestPasswordResetRequest) (*usersrvv1.RequestPasswordResetResponse, error) {
	ip, requestID := callMeta(ctx)
	if err := s.accounts.RequestPasswordReset(ctx, req.GetEmail(), account.ReturnContext{AppID: req.GetAppId(), Path: req.GetReturnPath()}, ip, requestID); err != nil {
		return nil, toStatus("request_password_reset", err)
	}
	return &usersrvv1.RequestPasswordResetResponse{}, nil
}

func (s *AccountService) ResetPassword(ctx context.Context, req *usersrvv1.ResetPasswordRequest) (*usersrvv1.ResetPasswordResponse, error) {
	if err := s.allowToken(ctx, req.GetToken()); err != nil {
		return nil, err
	}
	_, requestID := callMeta(ctx)
	ret, err := s.accounts.ResetPassword(ctx, req.GetToken(), req.GetNewPassword(), requestID)
	if err != nil {
		return nil, toStatus("reset_password", err)
	}
	return &usersrvv1.ResetPasswordResponse{AppId: ret.AppID, ReturnPath: ret.Path}, nil
}

func (s *AccountService) GetAuthState(ctx context.Context, _ *usersrvv1.GetAuthStateRequest) (*usersrvv1.GetAuthStateResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	st, err := s.accounts.GetAuthState(ctx, id)
	if err != nil {
		return nil, toStatus("get_auth_state", err)
	}
	return &usersrvv1.GetAuthStateResponse{
		Status: protoStatus(st.Status), AuthVersion: st.AuthVersion, Roles: st.Roles, ProfileVersion: st.Profile.Version,
	}, nil
}

func (s *AccountService) GetMyAccount(ctx context.Context, _ *usersrvv1.GetMyAccountRequest) (*usersrvv1.GetMyAccountResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	a, err := s.accounts.GetAccount(ctx, id)
	if err != nil {
		return nil, toStatus("get_my_account", err)
	}
	return &usersrvv1.GetMyAccountResponse{
		UserId: a.ID.String(), Email: a.Email, Profile: protoProfile(a.Profile), Roles: a.Roles,
		Status: protoStatus(a.Status), AuthVersion: a.AuthVersion,
	}, nil
}

func (s *AccountService) VerifyPassword(ctx context.Context, req *usersrvv1.VerifyPasswordRequest) (*usersrvv1.VerifyPasswordResponse, error) {
	id, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.allowPassword(ctx, id); err != nil {
		return nil, err
	}
	version, err := s.accounts.VerifyPassword(ctx, id, req.GetPassword(), requestID)
	if err != nil {
		return nil, toStatus("verify_password", err)
	}
	return &usersrvv1.VerifyPasswordResponse{AuthVersion: version}, nil
}

func (s *AccountService) ChangePassword(ctx context.Context, req *usersrvv1.ChangePasswordRequest) (*usersrvv1.ChangePasswordResponse, error) {
	id, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.allowPassword(ctx, id); err != nil {
		return nil, err
	}
	version, err := s.accounts.ChangePassword(ctx, id, req.GetCurrentPassword(), req.GetNewPassword(), requestID)
	if err != nil {
		return nil, toStatus("change_password", err)
	}
	return &usersrvv1.ChangePasswordResponse{AuthVersion: version}, nil
}

func (s *AccountService) UpdateMyProfile(ctx context.Context, req *usersrvv1.UpdateMyProfileRequest) (*usersrvv1.UpdateMyProfileResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, s.limits.ProfileWritePerUser, id.String()); err != nil {
		return nil, err
	}
	p, err := s.accounts.UpdateProfile(ctx, id, req.GetDisplayName(), req.GetAvatarUrl())
	if err != nil {
		return nil, toStatus("update_my_profile", err)
	}
	return &usersrvv1.UpdateMyProfileResponse{Profile: protoProfile(p)}, nil
}

func (s *AccountService) BeginDeletion(ctx context.Context, req *usersrvv1.BeginDeletionRequest) (*usersrvv1.BeginDeletionResponse, error) {
	id, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, s.limits.DeletionPerUser, id.String()); err != nil {
		return nil, err
	}
	version, err := s.accounts.BeginDeletion(ctx, id, req.GetMutationId(), requestID)
	if err != nil {
		return nil, toStatus("begin_deletion", err)
	}
	return &usersrvv1.BeginDeletionResponse{AuthVersion: version}, nil
}

func (s *AccountService) FinishDeletion(ctx context.Context, req *usersrvv1.FinishDeletionRequest) (*usersrvv1.FinishDeletionResponse, error) {
	id, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, s.limits.DeletionPerUser, id.String()); err != nil {
		return nil, err
	}
	removed, err := s.accounts.FinishDeletion(ctx, id, req.GetMutationId(), requestID)
	if err != nil {
		return nil, toStatus("finish_deletion", err)
	}
	return &usersrvv1.FinishDeletionResponse{Removed: removed}, nil
}

func protoStatus(s account.Status) usersrvv1.AccountStatus {
	switch s {
	case account.StatusActive:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case account.StatusDisabled:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_DISABLED
	case account.StatusDeleting:
		return usersrvv1.AccountStatus_ACCOUNT_STATUS_DELETING
	}
	return usersrvv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
}

func protoProfile(p account.Profile) *usersrvv1.BaseProfile {
	return &usersrvv1.BaseProfile{DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL, ProfileVersion: p.Version}
}
