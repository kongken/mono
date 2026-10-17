package auth

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/user"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	oauthWindow = 15 * time.Minute
	oauthPerIP  = 60
)

func (s *Service) SetOAuthRegistry(r *provider.Registry) { s.oauthReg = r }

func (s *Service) oauthRegistry() *provider.Registry {
	if s.oauthReg != nil {
		return s.oauthReg
	}
	return provider.BuildRegistry(*s.cfg)
}

func (s *Service) ListOAuthProviders(context.Context) (*bffv1.ListOAuthProvidersResponse, error) {
	list := s.oauthRegistry().List()
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
	out := &bffv1.ListOAuthProvidersResponse{}
	for _, p := range list {
		out.Providers = append(out.Providers, &bffv1.OAuthProvider{Name: p.Name(), DisplayName: p.DisplayName()})
	}
	return out, nil
}

func (s *Service) BeginOAuthFlow(ctx context.Context, clientIP string, req *bffv1.BeginOAuthFlowRequest) (*bffv1.BeginOAuthFlowResponse, error) {
	if s.oauthState == nil {
		return nil, internal(errors.New("oauth state store unavailable"))
	}
	p, err := s.oauthRegistry().Get(strings.TrimSpace(req.GetProvider()))
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown oauth provider"))
	}
	if err = s.allow(ctx, "oauth:ip:"+clientIP, oauthPerIP, oauthWindow); err != nil {
		return nil, err
	}
	pk, usesPKCE := p.(provider.PKCEProvider)
	var verifier, challenge string
	if usesPKCE {
		if verifier, challenge, err = provider.NewPKCE(); err != nil {
			slog.Error("create pkce verifier", "error", err)
			return nil, internal(err)
		}
	}
	state, err := s.oauthState.Create(ctx, p.Name(), verifier)
	if err != nil {
		slog.Error("create oauth state", "error", err)
		return nil, internal(err)
	}
	authorizeURL := ""
	if usesPKCE {
		authorizeURL = pk.AuthorizePKCEURL(state, challenge)
	} else {
		authorizeURL = p.AuthorizeURL(state)
	}
	return &bffv1.BeginOAuthFlowResponse{AuthorizeUrl: authorizeURL, State: state}, nil
}

func (s *Service) CompleteOAuthFlow(ctx context.Context, clientIP string, req *bffv1.CompleteOAuthFlowRequest) (*bffv1.CompleteOAuthFlowResponse, time.Time, string, error) {
	if s.oauthState == nil {
		return nil, time.Time{}, "", internal(errors.New("oauth state store unavailable"))
	}
	name, code, state := strings.TrimSpace(req.GetProvider()), strings.TrimSpace(req.GetCode()), strings.TrimSpace(req.GetState())
	if name == "" || code == "" || state == "" {
		return nil, time.Time{}, "", connect.NewError(connect.CodeInvalidArgument, errors.New("provider, code and state are required"))
	}
	if err := s.allow(ctx, "oauth:ip:"+clientIP, oauthPerIP, oauthWindow); err != nil {
		return nil, time.Time{}, "", err
	}
	attempt, err := s.oauthState.Consume(ctx, state)
	if errors.Is(err, oauth.ErrStateNotFound) || (err == nil && attempt.Provider != name) {
		return nil, time.Time{}, "", connect.NewError(connect.CodePermissionDenied, errors.New("invalid or expired state"))
	}
	if err != nil {
		slog.Error("consume oauth state", "error", err)
		return nil, time.Time{}, "", internal(err)
	}
	p, err := s.oauthRegistry().Get(name)
	if err != nil {
		return nil, time.Time{}, "", connect.NewError(connect.CodeNotFound, errors.New("unknown oauth provider"))
	}
	var claims *provider.Claims
	if pk, ok := p.(provider.PKCEProvider); ok {
		claims, err = pk.ExchangePKCE(ctx, code, attempt.CodeVerifier)
	} else {
		claims, err = p.Exchange(ctx, code)
	}
	if err != nil || claims == nil || claims.ExternalID == "" {
		slog.Warn("oauth exchange failed", "provider", name, "error", err)
		return nil, time.Time{}, "", connect.NewError(connect.CodeUnauthenticated, errors.New("oauth sign-in failed"))
	}
	u, err := s.oauthUser(ctx, name, claims)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, time.Time{}, "", connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	}
	s.syncProfile(ctx, u)
	token, expires, err := s.issueSession(ctx, u.ID)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return &bffv1.CompleteOAuthFlowResponse{User: toUser(u), ExpiresAt: timestamppb.New(expires)}, expires, token, nil
}

func (s *Service) oauthUser(ctx context.Context, providerName string, claims *provider.Claims) (*ent.User, error) {
	find := func() (*ent.User, error) {
		return s.db.User.Query().Where(user.Provider(providerName), user.ExternalID(claims.ExternalID)).Only(ctx)
	}
	u, err := find()
	if err == nil {
		return u, nil
	}
	if !ent.IsNotFound(err) {
		return nil, internal(err)
	}
	if s.cfg.RegistrationDisabled {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("registration is closed"))
	}
	display := strings.TrimSpace(claims.Name)
	if display == "" {
		display = strings.TrimSpace(claims.Login)
	}
	if display == "" {
		display = "旅行者"
	}
	if r := []rune(display); len(r) > maxDisplayRunes {
		display = string(r[:maxDisplayRunes])
	}
	avatar := claims.AvatarURL
	if len(avatar) > maxAvatarURLBytes || !(strings.HasPrefix(avatar, "https://") || strings.HasPrefix(avatar, "http://")) {
		avatar = ""
	}
	u, err = s.db.User.Create().SetProvider(providerName).SetExternalID(claims.ExternalID).SetDisplayName(display).SetAvatarURL(avatar).Save(ctx)
	if ent.IsConstraintError(err) {
		if u, err = find(); err != nil {
			return nil, internal(err)
		}
		return u, nil
	}
	if err != nil {
		slog.Error("create oauth user", "error", err)
		return nil, internal(err)
	}
	return u, nil
}

// OAuthDeps wires OAuth state and optional test registry.
type OAuthDeps struct {
	State    *oauth.StateStore
	Registry *provider.Registry
}

func applyOAuthDeps(s *Service, deps OAuthDeps) {
	s.oauthState = deps.State
	s.oauthReg = deps.Registry
}

