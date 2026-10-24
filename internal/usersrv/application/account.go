package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
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
}

// AccountService implements usersrv.v1.AccountService. Stage P1 serves the
// methods that need no mail: auth state, password check and change, profile and
// deletion. The registration, login and reset methods are part of the contract
// but stay UNIMPLEMENTED (the embedded server) until stage P3.
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
