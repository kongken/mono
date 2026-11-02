package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/bff/session"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	usersrvv1 "github.com/kongken/mono/pkg/proto/usersrv/v1"
)

type IdentityUsers interface {
	GetExternalIdentities(context.Context, string) (*usersrvv1.GetExternalIdentitiesResponse, error)
	BeginIdentityOperation(context.Context, string, *usersrvv1.BeginIdentityOperationRequest) (*usersrvv1.BeginIdentityOperationResponse, error)
	ConfirmIdentityOperation(context.Context, *usersrvv1.ConfirmIdentityOperationRequest) error
	GetIdentityOperation(context.Context, string, *usersrvv1.GetIdentityOperationRequest) (*usersrvv1.GetIdentityOperationResponse, error)
	LinkExternalIdentity(context.Context, string, *usersrvv1.LinkExternalIdentityRequest) (*usersrvv1.LinkExternalIdentityResponse, error)
	UnlinkExternalIdentity(context.Context, string, *usersrvv1.UnlinkExternalIdentityRequest) (*usersrvv1.UnlinkExternalIdentityResponse, error)
}

type IdentityHandler struct {
	svc      *Service
	users    IdentityUsers
	external *ExternalHandler
}

func NewIdentityHandler(svc *Service, users IdentityUsers, external *ExternalHandler) *IdentityHandler {
	return &IdentityHandler{svc: svc, users: users, external: external}
}

func identityContext(bs session.BrowserSession, kind, target, provider string) []byte {
	h := sha256.New()
	h.Write([]byte("bff.identity.operation.v1\x00"))
	h.Write([]byte(bs.SessionID))
	h.Write([]byte("\x00"))
	h.Write([]byte(kind))
	h.Write([]byte("\x00"))
	h.Write([]byte(target))
	h.Write([]byte("\x00"))
	h.Write([]byte(provider))
	return h.Sum(nil)
}
func identityRef(bs session.BrowserSession, id, kind, target, provider string) *usersrvv1.ExternalChallengeReference {
	sh := sha256.Sum256([]byte(bs.SessionID))
	return &usersrvv1.ExternalChallengeReference{ChallengeId: id, Binding: &usersrvv1.ExternalFlowBinding{ContextHash: identityContext(bs, kind, target, provider), SessionBindingHash: sh[:], ExpectedAuthVersion: bs.AuthVersion}}
}
func identityKind(k string) bool { return k == "identity_link" || k == "identity_unlink" }
func identityProvider(p string) usersrvv1.ExternalProvider {
	if p == "google" {
		return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GOOGLE
	}
	if p == "github" {
		return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB
	}
	return usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_UNSPECIFIED
}
func requireBrowser(ctx context.Context) (session.BrowserSession, error) {
	bs, ok := session.FromBrowser(ctx)
	if !ok {
		return session.BrowserSession{}, connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))
	}
	return bs, nil
}

func (h *IdentityHandler) GetIdentityCapabilities(ctx context.Context, _ *connect.Request[bffv1.GetIdentityCapabilitiesRequest]) (*connect.Response[bffv1.GetIdentityCapabilitiesResponse], error) {
	bs, err := requireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := h.users.GetExternalIdentities(ctx, bs.UserID)
	if err != nil {
		return nil, err
	}
	state, err := h.svc.users.AuthState(ctx, bs.UserID)
	if err != nil {
		return nil, err
	}
	out := &bffv1.GetIdentityCapabilitiesResponse{HasPassword: state.HasPassword, AuthVersion: state.AuthVersion}
	for _, r := range rows.GetIdentities() {
		out.Identities = append(out.Identities, &bffv1.IdentitySummary{Id: r.GetExternalIdentityId(), Provider: providerName(r.GetProvider()), DisplayLabel: r.GetDisplayLabel(), LinkedAt: r.GetLinkedAt().AsTime().Format(time.RFC3339), LastAuthenticatedAt: r.GetLastAuthenticatedAt().AsTime().Format(time.RFC3339)})
	}
	return connect.NewResponse(out), nil
}
func providerName(p usersrvv1.ExternalProvider) string {
	if p == usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GOOGLE {
		return "google"
	}
	if p == usersrvv1.ExternalProvider_EXTERNAL_PROVIDER_GITHUB {
		return "github"
	}
	return ""
}

func (h *IdentityHandler) BeginIdentityOperation(ctx context.Context, req *connect.Request[bffv1.BeginIdentityOperationRequest]) (*connect.Response[bffv1.BeginIdentityOperationResponse], error) {
	bs, err := requireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	kind := req.Msg.GetKind()
	target := req.Msg.GetExternalIdentityId()
	provider := req.Msg.GetProvider()
	if !identityKind(kind) || (kind == "identity_unlink" && target == "") || (kind == "identity_link" && identityProvider(provider) == 0) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid identity operation"))
	}
	if target != "" {
		if _, err = uuid.Parse(target); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid identity"))
		}
	}
	r := &usersrvv1.BeginIdentityOperationRequest{Kind: mapKind(kind), ExternalIdentityId: target, Provider: identityProvider(provider), CurrentPassword: req.Msg.GetCurrentPassword(), Binding: &usersrvv1.ExternalFlowBinding{ContextHash: identityContext(bs, kind, target, provider), SessionBindingHash: sha256Bytes(bs.SessionID), ExpectedAuthVersion: bs.AuthVersion}}
	out, err := h.users.BeginIdentityOperation(ctx, bs.UserID, r)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.BeginIdentityOperationResponse{ChallengeId: out.GetChallengeId()}), nil
}
func mapKind(k string) usersrvv1.IdentityOperationKind {
	if k == "identity_link" {
		return usersrvv1.IdentityOperationKind_IDENTITY_OPERATION_KIND_LINK
	}
	return usersrvv1.IdentityOperationKind_IDENTITY_OPERATION_KIND_UNLINK
}
func sha256Bytes(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
func (h *IdentityHandler) ConfirmIdentityOperation(ctx context.Context, req *connect.Request[bffv1.ConfirmIdentityOperationRequest]) (*connect.Response[bffv1.ConfirmIdentityOperationResponse], error) {
	if err := h.users.ConfirmIdentityOperation(ctx, &usersrvv1.ConfirmIdentityOperationRequest{ChallengeId: req.Msg.GetChallengeId(), Token: req.Msg.GetToken()}); err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.ConfirmIdentityOperationResponse{}), nil
}
func (h *IdentityHandler) GetIdentityOperation(ctx context.Context, req *connect.Request[bffv1.GetIdentityOperationRequest]) (*connect.Response[bffv1.GetIdentityOperationResponse], error) {
	bs, err := requireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	if !identityKind(req.Msg.GetKind()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid identity operation"))
	}
	out, err := h.users.GetIdentityOperation(ctx, bs.UserID, &usersrvv1.GetIdentityOperationRequest{Challenge: &usersrvv1.ExternalChallengeReference{ChallengeId: req.Msg.GetChallengeId(), Binding: identityRef(bs, req.Msg.GetChallengeId(), req.Msg.GetKind(), req.Msg.GetExternalIdentityId(), req.Msg.GetProvider()).GetBinding()}})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&bffv1.GetIdentityOperationResponse{ChallengeId: out.GetChallengeId(), Kind: out.GetKind(), Status: out.GetStatus(), Confirmed: out.GetConfirmed()}), nil
}
func (h *IdentityHandler) LinkExternalIdentity(ctx context.Context, req *connect.Request[bffv1.LinkExternalIdentityRequest]) (*connect.Response[bffv1.LinkExternalIdentityResponse], error) {
	bs, err := requireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	if h.external == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("external flow unavailable"))
	}
	row, err := h.external.flow(ctx, req.Header(), req.Msg.GetProviderFlowId())
	if err != nil {
		return nil, err
	}
	if row.Intent != "link" || row.UserID == nil || *row.UserID != bs.UserID {
		return nil, stateInvalid()
	}
	op := identityRef(bs, "", "identity_link", "", providerName(identityProvider(string(row.Provider))))
	op.ChallengeId = req.Msg.GetOperationChallengeId()
	out, err := h.users.LinkExternalIdentity(ctx, bs.UserID, &usersrvv1.LinkExternalIdentityRequest{Operation: op, ProviderProof: h.external.ref(row), MutationId: req.Msg.GetMutationId()})
	if err != nil {
		return nil, err
	}
	if !out.GetReplayed() && out.GetAuthVersion() != bs.AuthVersion {
		h.svc.revokeAll(ctx, bs.UserID)
		res := connect.NewResponse(&bffv1.LinkExternalIdentityResponse{ExternalIdentityId: out.GetExternalIdentityId(), AuthVersion: out.GetAuthVersion(), Replayed: out.GetReplayed(), Result: out.GetResult()})
		session.ClearSessionCookie(res.Header(), h.svc.cookies)
		return res, nil
	}
	return connect.NewResponse(&bffv1.LinkExternalIdentityResponse{ExternalIdentityId: out.GetExternalIdentityId(), AuthVersion: out.GetAuthVersion(), Replayed: out.GetReplayed(), Result: out.GetResult()}), nil
}
func (h *IdentityHandler) UnlinkExternalIdentity(ctx context.Context, req *connect.Request[bffv1.UnlinkExternalIdentityRequest]) (*connect.Response[bffv1.UnlinkExternalIdentityResponse], error) {
	bs, err := requireBrowser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = uuid.Parse(req.Msg.GetExternalIdentityId()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid identity"))
	}
	op := identityRef(bs, req.Msg.GetOperationChallengeId(), "identity_unlink", req.Msg.GetExternalIdentityId(), "")
	op.ChallengeId = req.Msg.GetOperationChallengeId()
	out, err := h.users.UnlinkExternalIdentity(ctx, bs.UserID, &usersrvv1.UnlinkExternalIdentityRequest{Operation: op, ExternalIdentityId: req.Msg.GetExternalIdentityId(), MutationId: req.Msg.GetMutationId()})
	if err != nil {
		return nil, err
	}
	if !out.GetReplayed() && out.GetAuthVersion() != bs.AuthVersion {
		h.svc.revokeAll(ctx, bs.UserID)
		res := connect.NewResponse(&bffv1.UnlinkExternalIdentityResponse{AuthVersion: out.GetAuthVersion(), Replayed: out.GetReplayed()})
		session.ClearSessionCookie(res.Header(), h.svc.cookies)
		return res, nil
	}
	return connect.NewResponse(&bffv1.UnlinkExternalIdentityResponse{AuthVersion: out.GetAuthVersion(), Replayed: out.GetReplayed()}), nil
}
