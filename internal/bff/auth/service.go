package auth

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/oauth"
	"github.com/kongken/mono/internal/bff/oauth/provider"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxDisplayRunes   = 40
	maxAvatarURLBytes = 500
	touchInterval     = time.Minute
)

// UserDataCleaner removes one product's data of a user when the user deletes
// their cloud data. CleanupUserData must be idempotent: it is repeated for every
// product on each retry of a deletion, and succeeds when there is nothing left
// to remove.
type UserDataCleaner interface {
	// Name identifies the product in logs and in auth.account_cleanup.
	Name() string
	CleanupUserData(ctx context.Context, userID string) error
}

type Service struct {
	cfg        *config.AuthConfig
	cookies    session.CookieConfig
	db         *ent.Client
	limit      *ratelimit.Limiter
	profiles   tripmapv1.ProfileServiceClient
	cleaners   []UserDataCleaner
	oauthState *oauth.StateStore
	oauthReg   *provider.Registry
}

func NewService(cfg *config.AuthConfig, cookies session.CookieConfig, db *ent.Client, limit *ratelimit.Limiter, profiles tripmapv1.ProfileServiceClient, cleaners []UserDataCleaner, oauth OAuthDeps) *Service {
	s := &Service{cfg: cfg, cookies: cookies, db: db, limit: limit, profiles: profiles, cleaners: cleaners}
	applyOAuthDeps(s, oauth)
	return s
}

func (s *Service) SessionLifetime() time.Duration {
	if s.cfg.SessionTTL <= 0 {
		return 168 * time.Hour
	}
	return s.cfg.SessionTTL
}

func (s *Service) allow(ctx context.Context, key string, limit int64, window time.Duration) error {
	ok, err := s.limit.Allow(ctx, key, limit, window)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many attempts, try again later"))
	}
	return nil
}

func toUser(u *ent.User) *bffv1.User {
	out := &bffv1.User{
		Id: u.ID, DisplayName: u.DisplayName, AvatarUrl: u.AvatarURL,
		CreatedAt: timestamppb.New(u.CreatedAt), DeletionPending: u.DeletionRequestedAt != nil,
	}
	if u.Username != nil {
		out.Username = *u.Username
	}
	return out
}

func (s *Service) issueSession(ctx context.Context, userID string, roles []string) (token string, expires time.Time, err error) {
	token, hash, err := session.NewToken()
	if err != nil {
		return "", time.Time{}, internal(err)
	}
	authenticated := time.Now().UTC()
	expires = authenticated.Add(s.SessionLifetime())
	if _, err = s.db.Session.Create().SetUserID(userID).SetTokenHash(hash).SetExpiresAt(expires).
		SetAuthenticatedAt(authenticated).SetRoles(slices.Clone(roles)).Save(ctx); err != nil {
		slog.Error("create session", "error", err)
		return "", time.Time{}, internal(err)
	}
	return token, expires, nil
}

func (s *Service) syncProfile(ctx context.Context, u *ent.User) error {
	if s.profiles == nil || u.DeletionRequestedAt != nil {
		return nil
	}
	name := u.DisplayName
	userName := ""
	if u.Username != nil {
		userName = *u.Username
	}
	callCtx := rpcauth.WithCall(ctx, rpcauth.Call{UserID: u.ID})
	_, err := s.profiles.EnsureProfile(callCtx, &tripmapv1.EnsureProfileRequest{
		UserId: u.ID, Username: userName, DisplayName: name, AvatarUrl: u.AvatarURL,
	})
	if err != nil {
		slog.Warn("ensure tripmap profile", "user", u.ID, "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("profile synchronization unavailable; retry sign-in"))
	}
	return nil
}

func (s *Service) Me(ctx context.Context, userID string, roles []string) (*bffv1.MeResponse, error) {
	u, err := s.db.User.Get(ctx, userID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, errUnauth
		}
		return nil, internal(err)
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, errUnauth
	}
	return &bffv1.MeResponse{User: toUser(u), Roles: slices.Clone(roles)}, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if err := s.db.Session.UpdateOneID(sessionID).SetRevoked(true).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return internal(err)
	}
	return nil
}

func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) (*bffv1.ListSessionsResponse, error) {
	rows, err := s.db.Session.Query().
		Where(entsession.UserID(userID), entsession.Revoked(false), entsession.ExpiresAtGT(time.Now().UTC())).
		Order(ent.Desc(entsession.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, internal(err)
	}
	out := &bffv1.ListSessionsResponse{}
	for _, r := range rows {
		item := &bffv1.Session{
			Id: r.ID, CreatedAt: timestamppb.New(r.CreatedAt), ExpiresAt: timestamppb.New(r.ExpiresAt),
			Current: r.ID == currentSessionID,
		}
		if r.LastUsedAt != nil {
			item.LastUsedAt = timestamppb.New(*r.LastUsedAt)
		}
		out.Sessions = append(out.Sessions, item)
	}
	return out, nil
}

func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	n, err := s.db.Session.Update().
		Where(entsession.ID(sessionID), entsession.UserID(userID)).SetRevoked(true).Save(ctx)
	if err != nil {
		return internal(err)
	}
	if n == 0 {
		return connect.NewError(connect.CodeNotFound, errors.New("session not found"))
	}
	return nil
}

func (s *Service) IssueCSRF() (string, error) { return session.NewCSRF() }

// ResolveSession validates the browser session cookie and returns the session facts.
func (s *Service) ResolveSession(ctx context.Context, token string) (session.BrowserSession, error) {
	if token == "" {
		return session.BrowserSession{}, errUnauth
	}
	now := time.Now().UTC()
	sess, err := s.db.Session.Query().
		Where(entsession.TokenHash(session.HashToken(token)), entsession.Revoked(false), entsession.ExpiresAtGT(now)).
		WithUser().Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return session.BrowserSession{}, errUnauth
		}
		slog.Error("session lookup", "error", err)
		return session.BrowserSession{}, internal(err)
	}
	u := sess.Edges.User
	if u == nil || (u.Disabled && u.DeletionRequestedAt == nil) {
		return session.BrowserSession{}, errUnauth
	}
	if sess.LastUsedAt == nil || now.Sub(*sess.LastUsedAt) > touchInterval {
		if err := s.db.Session.UpdateOneID(sess.ID).SetLastUsedAt(now).Exec(ctx); err != nil {
			slog.Warn("touch session", "error", err)
		}
	}
	authenticated := sess.CreatedAt
	if sess.AuthenticatedAt != nil {
		authenticated = *sess.AuthenticatedAt
	}
	return session.BrowserSession{
		UserID: sess.UserID, SessionID: sess.ID,
		DeletionPending: u.DeletionRequestedAt != nil,
		Roles:           slices.Clone(sess.Roles), AuthenticatedAt: authenticated,
	}, nil
}

var errUnauth = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

func internal(err error) error {
	slog.Error("internal", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
