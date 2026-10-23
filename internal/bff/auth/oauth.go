package auth

import (
	"context"
	"errors"
	"log/slog"
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
	out := &bffv1.ListOAuthProvidersResponse{}
	if p, err := s.oauthRegistry().Get("logto"); err == nil {
		out.Providers = append(out.Providers, &bffv1.OAuthProvider{Name: p.Name(), DisplayName: p.DisplayName()})
	}
	return out, nil
}

func (s *Service) finishOAuth(ctx context.Context, name, code string, attempt oauth.OAuthState) (*bffv1.CompleteOAuthFlowResponse, time.Time, string, error) {
	if name != "logto" {
		return nil, time.Time{}, "", connect.NewError(connect.CodeNotFound, errors.New("only Logto sign-in is supported"))
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
		slog.Warn("oauth exchange failed", "provider", name)
		return nil, time.Time{}, "", connect.NewError(connect.CodeUnauthenticated, errors.New("oauth sign-in failed"))
	}
	u, err := s.oauthUser(ctx, name, claims)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, time.Time{}, "", connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	}
	if err := s.syncProfile(ctx, u); err != nil {
		return nil, time.Time{}, "", err
	}
	token, expires, err := s.issueSession(ctx, u.ID)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return &bffv1.CompleteOAuthFlowResponse{User: toUser(u), ExpiresAt: timestamppb.New(expires)}, expires, token, nil
}

func (s *Service) oauthUser(ctx context.Context, providerName string, claims *provider.Claims) (*ent.User, error) {
	issuer := strings.TrimRight(s.cfg.OAuthProviders[providerName].IssuerURL, "/")
	if issuer == "" {
		return nil, internal(errors.New("Logto issuer is not configured"))
	}
	find := func() (*ent.User, error) {
		return s.db.User.Query().Where(user.Issuer(issuer), user.ExternalID(claims.ExternalID)).Only(ctx)
	}
	u, err := find()
	if err != nil && !ent.IsNotFound(err) {
		return nil, internal(err)
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
	// Names are projections, never account keys and never used for linking.
	login := strings.TrimSpace(claims.Login)
	if len(login) > 64 {
		login = ""
	}
	if u != nil {
		return s.db.User.UpdateOne(u).SetUsername(login).SetDisplayName(display).SetAvatarURL(avatar).Save(ctx)
	}
	u, err = s.db.User.Create().SetIssuer(issuer).SetExternalID(claims.ExternalID).SetUsername(login).SetDisplayName(display).SetAvatarURL(avatar).Save(ctx)
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
