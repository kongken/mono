package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	"github.com/kongken/mono/internal/usersrv/identity"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ExternalIdentityService struct {
	usersrvv1.UnimplementedExternalIdentityServiceServer
	core *AccountService
}

func NewExternalIdentityService(a *account.Service, l Limiter, limits Limits) *ExternalIdentityService {
	return &ExternalIdentityService{core: NewAccountService(a, l, limits)}
}
func providerName(p usersrvv1.ExternalProvider) externalprovider.ProviderName {
	if p == usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GOOGLE {
		return externalprovider.GoogleName
	}
	if p == usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB {
		return externalprovider.GitHubName
	}
	return ""
}
func providerProto(p string) usersrvv1.ExternalProvider {
	if p == "google" {
		return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GOOGLE
	}
	return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB
}
func parseID(raw string, optional bool) (uuid.UUID, error) {
	if raw == "" && optional {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, &identity.ValidationError{Field: "id", Reason: "must be a UUID"}
	}
	return id, nil
}
func binding(ctx context.Context, b *usersrvv1.ExternalFlowBinding) (account.FlowBinding, error) {
	if b == nil {
		return account.FlowBinding{}, account.ErrChallengeInvalid
	}
	flow, err := parseID(b.GetFlowId(), true)
	if err != nil {
		return account.FlowBinding{}, err
	}
	uid, err := parseID(rpcauth.CallFrom(ctx).UserID, true)
	if err != nil {
		return account.FlowBinding{}, err
	}
	return account.FlowBinding{FlowID: flow, UserID: uid, ContextHash: b.GetContextHash(), SessionHash: b.GetSessionBindingHash(), AuthVersion: b.GetExpectedAuthVersion()}, nil
}
func reference(ctx context.Context, r *usersrvv1.ExternalChallengeReference) (account.ChallengeRef, error) {
	if r == nil {
		return account.ChallengeRef{}, account.ErrChallengeInvalid
	}
	id, err := parseID(r.GetChallengeId(), false)
	if err != nil {
		return account.ChallengeRef{}, err
	}
	b, err := binding(ctx, r.GetBinding())
	return account.ChallengeRef{ID: id, FlowBinding: b}, err
}
func pending(r account.ExternalResult) *usersrvv1.ExternalPending {
	return &usersrvv1.ExternalPending{ChallengeId: r.ChallengeID.String(), Intent: r.Status, CandidateEmail: r.CandidateEmail, DisplayName: r.DisplayName, AvatarUrl: r.AvatarURL, EmailConfirmed: r.EmailConfirmed}
}

func (s *ExternalIdentityService) BeginExternalLogin(ctx context.Context, r *usersrvv1.BeginExternalLoginRequest) (*usersrvv1.BeginExternalLoginResponse, error) {
	if providerName(r.GetProvider()) == "" {
		return nil, toStatus("external.begin", &identity.ValidationError{Field: "provider", Reason: "must be google or github"})
	}
	b, err := binding(ctx, r.GetBinding())
	if err != nil {
		return nil, toStatus("external.begin", err)
	}
	if err = s.core.allowToken(ctx, r.GetState()); err != nil {
		return nil, err
	}
	out, err := s.core.accounts.BeginExternalLogin(ctx, account.BeginExternalInput{FlowBinding: b, Intent: r.GetIntent(), Provider: providerName(r.GetProvider()), RedirectURI: r.GetRedirectUri(), Authorization: externalprovider.AuthorizationRequest{State: r.GetState(), Nonce: r.GetNonce(), CodeChallenge: r.GetCodeChallenge()}, Return: account.ReturnContext{AppID: r.GetAppId(), Path: r.GetReturnPath()}})
	if err != nil {
		return nil, toStatus("external.begin", err)
	}
	return &usersrvv1.BeginExternalLoginResponse{ChallengeId: out.ChallengeID.String(), AuthorizationUrl: out.AuthorizationURL, ProviderConfigVersion: out.ConfigVersion}, nil
}
func (s *ExternalIdentityService) CompleteExternalLogin(ctx context.Context, r *usersrvv1.CompleteExternalLoginRequest) (*usersrvv1.CompleteExternalLoginResponse, error) {
	if providerName(r.GetProvider()) == "" {
		return nil, toStatus("external.complete", &identity.ValidationError{Field: "provider", Reason: "must be google or github"})
	}
	ref, err := reference(ctx, r.GetChallenge())
	if err != nil {
		return nil, toStatus("external.complete", err)
	}
	if err = s.core.allowToken(ctx, r.GetCode()); err != nil {
		return nil, err
	}
	out, err := s.core.accounts.CompleteExternalLogin(ctx, account.CompleteExternalInput{ChallengeRef: ref, RequestID: rpcauth.CallFrom(ctx).RequestID, Exchange: externalprovider.CodeExchangeRequest{Provider: providerName(r.GetProvider()), ConfigVersion: r.GetProviderConfigVersion(), Code: r.GetCode(), Verifier: r.GetVerifier(), Nonce: r.GetNonce(), CodeChallenge: r.GetCodeChallenge()}})
	if err != nil {
		return nil, toStatus("external.complete", err)
	}
	response := &usersrvv1.CompleteExternalLoginResponse{}
	if out.Account != nil {
		a := out.Account
		response.Result = &usersrvv1.CompleteExternalLoginResponse_Authenticated{Authenticated: &usersrvv1.ExternalAuthenticatedAccount{UserId: a.ID.String(), Profile: protoProfile(a.Profile), Roles: a.Roles, AuthVersion: a.AuthVersion, HasPassword: a.HasPassword}}
	} else {
		response.Result = &usersrvv1.CompleteExternalLoginResponse_Pending{Pending: pending(out)}
	}
	return response, nil
}
func (s *ExternalIdentityService) RequestExternalRegistrationEmail(ctx context.Context, r *usersrvv1.RequestExternalRegistrationEmailRequest) (*usersrvv1.RequestExternalRegistrationEmailResponse, error) {
	ref, err := reference(ctx, r.GetChallenge())
	if err != nil {
		return nil, toStatus("external.email.request", err)
	}
	ip, requestID := callMeta(ctx)
	if err = s.core.accounts.RequestExternalRegistrationEmail(ctx, ref, r.GetEmail(), ip, requestID); err != nil {
		return nil, toStatus("external.email.request", err)
	}
	return &usersrvv1.RequestExternalRegistrationEmailResponse{}, nil
}
func (s *ExternalIdentityService) ConfirmExternalRegistrationEmail(ctx context.Context, r *usersrvv1.ConfirmExternalRegistrationEmailRequest) (*usersrvv1.ConfirmExternalRegistrationEmailResponse, error) {
	id, err := parseID(r.GetChallengeId(), false)
	if err != nil {
		return nil, toStatus("external.email.confirm", err)
	}
	if err = s.core.allowToken(ctx, r.GetToken()); err != nil {
		return nil, err
	}
	if err = s.core.accounts.ConfirmExternalRegistrationEmail(ctx, id, r.GetToken()); err != nil {
		return nil, toStatus("external.email.confirm", err)
	}
	return &usersrvv1.ConfirmExternalRegistrationEmailResponse{}, nil
}
func (s *ExternalIdentityService) GetExternalRegistration(ctx context.Context, r *usersrvv1.GetExternalRegistrationRequest) (*usersrvv1.GetExternalRegistrationResponse, error) {
	ref, err := reference(ctx, r.GetChallenge())
	if err != nil {
		return nil, toStatus("external.registration.get", err)
	}
	if err = s.core.allowToken(ctx, ref.ID.String()); err != nil {
		return nil, err
	}
	out, err := s.core.accounts.GetExternalRegistration(ctx, ref)
	if err != nil {
		return nil, toStatus("external.registration.get", err)
	}
	return &usersrvv1.GetExternalRegistrationResponse{Pending: pending(out)}, nil
}
func (s *ExternalIdentityService) CompleteExternalRegistration(ctx context.Context, r *usersrvv1.CompleteExternalRegistrationRequest) (*usersrvv1.CompleteExternalRegistrationResponse, error) {
	ref, err := reference(ctx, r.GetChallenge())
	if err != nil {
		return nil, toStatus("external.registration.complete", err)
	}
	if err = s.core.allowToken(ctx, ref.ID.String()); err != nil {
		return nil, err
	}
	id, err := s.core.accounts.CompleteExternalRegistration(ctx, account.RegistrationInput{ChallengeRef: ref, MutationID: r.GetMutationId(), DisplayName: r.GetDisplayName(), AvatarURL: r.GetAvatarUrl()})
	if err != nil {
		return nil, toStatus("external.registration.complete", err)
	}
	return &usersrvv1.CompleteExternalRegistrationResponse{CreatedUserId: id.String()}, nil
}
func operationKind(k usersrvv1.IdentityOperationKind) string {
	switch k {
	case usersrvv1.IdentityOperationKind_IDENTITY_OPERATION_KIND_LINK:
		return "identity_link"
	case usersrvv1.IdentityOperationKind_IDENTITY_OPERATION_KIND_UNLINK:
		return "identity_unlink"
	case usersrvv1.IdentityOperationKind_IDENTITY_OPERATION_KIND_ACCOUNT_DELETION:
		return "account_deletion"
	}
	return ""
}
func (s *ExternalIdentityService) BeginIdentityOperation(ctx context.Context, r *usersrvv1.BeginIdentityOperationRequest) (*usersrvv1.BeginIdentityOperationResponse, error) {
	id, requestID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.core.allowPassword(ctx, id); err != nil {
		return nil, err
	}
	b, err := binding(ctx, r.GetBinding())
	if err != nil {
		return nil, toStatus("identity.operation.begin", err)
	}
	target, err := parseID(r.GetExternalIdentityId(), true)
	if err != nil {
		return nil, toStatus("identity.operation.begin", err)
	}
	ip, _ := callMeta(ctx)
	challenge, err := s.core.accounts.BeginIdentityOperation(ctx, account.BeginOperationInput{FlowBinding: b, Kind: operationKind(r.GetKind()), TargetID: target, Provider: providerName(r.GetProvider()), Password: r.GetCurrentPassword(), ClientIP: ip, RequestID: requestID})
	if err != nil {
		return nil, toStatus("identity.operation.begin", err)
	}
	return &usersrvv1.BeginIdentityOperationResponse{ChallengeId: challenge.String()}, nil
}
func (s *ExternalIdentityService) ConfirmIdentityOperation(ctx context.Context, r *usersrvv1.ConfirmIdentityOperationRequest) (*usersrvv1.ConfirmIdentityOperationResponse, error) {
	id, err := parseID(r.GetChallengeId(), false)
	if err != nil {
		return nil, toStatus("identity.operation.confirm", err)
	}
	if err = s.core.allowToken(ctx, r.GetToken()); err != nil {
		return nil, err
	}
	if err = s.core.accounts.ConfirmIdentityOperation(ctx, id, r.GetToken()); err != nil {
		return nil, toStatus("identity.operation.confirm", err)
	}
	return &usersrvv1.ConfirmIdentityOperationResponse{}, nil
}
func (s *ExternalIdentityService) GetIdentityOperation(ctx context.Context, r *usersrvv1.GetIdentityOperationRequest) (*usersrvv1.GetIdentityOperationResponse, error) {
	if _, _, err := caller(ctx); err != nil {
		return nil, err
	}
	ref, err := reference(ctx, r.GetChallenge())
	if err != nil {
		return nil, toStatus("identity.operation.get", err)
	}
	if err = s.core.allowToken(ctx, ref.ID.String()); err != nil {
		return nil, err
	}
	out, err := s.core.accounts.GetIdentityOperation(ctx, ref)
	if err != nil {
		return nil, toStatus("identity.operation.get", err)
	}
	return &usersrvv1.GetIdentityOperationResponse{ChallengeId: out.ID.String(), Kind: out.Kind, Status: out.Status, Confirmed: out.Confirmed}, nil
}
func (s *ExternalIdentityService) LinkExternalIdentity(ctx context.Context, r *usersrvv1.LinkExternalIdentityRequest) (*usersrvv1.LinkExternalIdentityResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.core.allow(ctx, s.core.limits.ProfileWritePerUser, id.String()); err != nil {
		return nil, err
	}
	op, err := reference(ctx, r.GetOperation())
	if err != nil {
		return nil, toStatus("identity.link", err)
	}
	proof, err := reference(ctx, r.GetProviderProof())
	if err != nil {
		return nil, toStatus("identity.link", err)
	}
	out, err := s.core.accounts.LinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, ProviderProof: proof, MutationID: r.GetMutationId(), RequestID: rpcauth.CallFrom(ctx).RequestID})
	if err != nil {
		return nil, toStatus("identity.link", err)
	}
	return &usersrvv1.LinkExternalIdentityResponse{ExternalIdentityId: out.IdentityID.String(), AuthVersion: out.AuthVersion, Replayed: out.Replayed, Result: out.Result}, nil
}
func (s *ExternalIdentityService) UnlinkExternalIdentity(ctx context.Context, r *usersrvv1.UnlinkExternalIdentityRequest) (*usersrvv1.UnlinkExternalIdentityResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.core.allow(ctx, s.core.limits.ProfileWritePerUser, id.String()); err != nil {
		return nil, err
	}
	op, err := reference(ctx, r.GetOperation())
	if err != nil {
		return nil, toStatus("identity.unlink", err)
	}
	target, err := parseID(r.GetExternalIdentityId(), false)
	if err != nil {
		return nil, toStatus("identity.unlink", err)
	}
	out, err := s.core.accounts.UnlinkExternalIdentity(ctx, account.IdentityMutation{Operation: op, TargetID: target, MutationID: r.GetMutationId(), RequestID: rpcauth.CallFrom(ctx).RequestID})
	if err != nil {
		return nil, toStatus("identity.unlink", err)
	}
	return &usersrvv1.UnlinkExternalIdentityResponse{AuthVersion: out.AuthVersion, Replayed: out.Replayed}, nil
}
func (s *ExternalIdentityService) GetExternalIdentities(ctx context.Context, r *usersrvv1.GetExternalIdentitiesRequest) (*usersrvv1.GetExternalIdentitiesResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.core.allow(ctx, s.core.limits.ProfileWritePerUser, id.String()); err != nil {
		return nil, err
	}
	rows, err := s.core.accounts.GetExternalIdentities(ctx, id)
	if err != nil {
		return nil, toStatus("identity.list", err)
	}
	out := &usersrvv1.GetExternalIdentitiesResponse{}
	for _, row := range rows {
		out.Identities = append(out.Identities, &usersrvv1.ExternalIdentitySummary{ExternalIdentityId: row.ID.String(), Provider: providerProto(row.Provider), DisplayLabel: row.Provider, LinkedAt: timestamppb.New(row.LinkedAt), LastAuthenticatedAt: timestamppb.New(row.LastAuthenticatedAt)})
	}
	return out, nil
}
func (s *ExternalIdentityService) BeginConfirmedDeletion(ctx context.Context, r *usersrvv1.BeginConfirmedDeletionRequest) (*usersrvv1.BeginConfirmedDeletionResponse, error) {
	id, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.core.allow(ctx, s.core.limits.DeletionPerUser, id.String()); err != nil {
		return nil, err
	}
	ref, err := reference(ctx, r.GetOperation())
	if err != nil {
		return nil, toStatus("deletion.confirmed", err)
	}
	version, err := s.core.accounts.BeginConfirmedDeletion(ctx, ref, r.GetMutationId(), r.GetConfirmation())
	if err != nil {
		return nil, toStatus("deletion.confirmed", err)
	}
	return &usersrvv1.BeginConfirmedDeletionResponse{AuthVersion: version}, nil
}
