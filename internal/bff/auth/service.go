package auth

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/ent/user"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	maxDisplayRunes   = 40
	maxAvatarURLBytes = 500
	minPasswordBytes  = 8
	maxPasswordBytes  = 72
	invalidCredential = "invalid username or password"
	touchInterval     = time.Minute
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

const (
	loginWindow    = 15 * time.Minute
	loginPerUser   = 10
	loginPerIP     = 40
	registerWindow = time.Hour
	registerPerIP  = 10
)

type Service struct {
	cfg      *config.AuthConfig
	cookies  session.CookieConfig
	db       *ent.Client
	limit    *ratelimit.Limiter
	profiles tripmapv1.ProfileServiceClient
	cleanup  tripmapv1.AccountCleanupServiceClient
}

func NewService(cfg *config.AuthConfig, cookies session.CookieConfig, db *ent.Client, limit *ratelimit.Limiter, profiles tripmapv1.ProfileServiceClient, cleanup tripmapv1.AccountCleanupServiceClient) *Service {
	return &Service{cfg: cfg, cookies: cookies, db: db, limit: limit, profiles: profiles, cleanup: cleanup}
}

var (
	dummyHashOnce sync.Once
	dummyHash     []byte
)

func burnPassword(password string) {
	dummyHashOnce.Do(func() { dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unused-password"), bcrypt.DefaultCost) })
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
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

func normalizeUsername(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if !usernamePattern.MatchString(name) {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("username must be 3-32 characters of a-z, 0-9 or underscore"))
	}
	return name, nil
}

func cleanDisplayName(raw, fallback string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return fallback, nil
	}
	if utf8.RuneCountInString(name) > maxDisplayRunes {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("display name is too long"))
	}
	return name, nil
}

func (s *Service) issueSession(ctx context.Context, userID string) (token string, expires time.Time, err error) {
	token, hash, err := session.NewToken()
	if err != nil {
		return "", time.Time{}, internal(err)
	}
	expires = time.Now().UTC().Add(s.SessionLifetime())
	if _, err = s.db.Session.Create().SetUserID(userID).SetTokenHash(hash).SetExpiresAt(expires).Save(ctx); err != nil {
		slog.Error("create session", "error", err)
		return "", time.Time{}, internal(err)
	}
	return token, expires, nil
}

func (s *Service) syncProfile(ctx context.Context, u *ent.User) {
	if s.profiles == nil {
		return
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
	}
}

func (s *Service) Register(ctx context.Context, clientIP string, req *bffv1.RegisterRequest) (*bffv1.RegisterResponse, time.Time, string, error) {
	if s.cfg.RegistrationDisabled {
		return nil, time.Time{}, "", connect.NewError(connect.CodePermissionDenied, errors.New("registration is closed"))
	}
	if err := s.allow(ctx, "register:ip:"+clientIP, registerPerIP, registerWindow); err != nil {
		return nil, time.Time{}, "", err
	}
	username, err := normalizeUsername(req.GetUsername())
	if err != nil {
		return nil, time.Time{}, "", err
	}
	password := req.GetPassword()
	if len(password) < minPasswordBytes || len(password) > maxPasswordBytes {
		return nil, time.Time{}, "", connect.NewError(connect.CodeInvalidArgument, errors.New("password must be 8-72 bytes"))
	}
	display, err := cleanDisplayName(req.GetDisplayName(), username)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, time.Time{}, "", internal(err)
	}
	u, err := s.db.User.Create().SetUsername(username).SetDisplayName(display).SetPasswordHash(string(hash)).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, time.Time{}, "", connect.NewError(connect.CodeAlreadyExists, errors.New("username is taken"))
		}
		slog.Error("create user", "error", err)
		return nil, time.Time{}, "", internal(err)
	}
	s.syncProfile(ctx, u)
	token, expires, err := s.issueSession(ctx, u.ID)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return &bffv1.RegisterResponse{User: toUser(u), ExpiresAt: timestamppb.New(expires)}, expires, token, nil
}

func (s *Service) Login(ctx context.Context, clientIP string, req *bffv1.LoginRequest) (*bffv1.LoginResponse, time.Time, string, error) {
	username := strings.ToLower(strings.TrimSpace(req.GetUsername()))
	password := req.GetPassword()
	if username == "" || password == "" {
		return nil, time.Time{}, "", connect.NewError(connect.CodeInvalidArgument, errors.New("username and password are required"))
	}
	if len(username) > 64 || len(password) > 1024 {
		return nil, time.Time{}, "", connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	}
	if err := s.allow(ctx, "login:ip:"+clientIP, loginPerIP, loginWindow); err != nil {
		return nil, time.Time{}, "", err
	}
	if err := s.allow(ctx, "login:user:"+username, loginPerUser, loginWindow); err != nil {
		return nil, time.Time{}, "", err
	}
	u, err := s.db.User.Query().Where(user.UsernameEQ(username)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		burnPassword(password)
		return nil, time.Time{}, "", connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	case err != nil:
		slog.Error("login lookup", "error", err)
		return nil, time.Time{}, "", internal(err)
	}
	if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		if u.PasswordHash == "" {
			burnPassword(password)
		}
		return nil, time.Time{}, "", connect.NewError(connect.CodeUnauthenticated, errors.New(invalidCredential))
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, time.Time{}, "", connect.NewError(connect.CodePermissionDenied, errors.New("account is disabled"))
	}
	s.syncProfile(ctx, u)
	token, expires, err := s.issueSession(ctx, u.ID)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return &bffv1.LoginResponse{User: toUser(u), ExpiresAt: timestamppb.New(expires)}, expires, token, nil
}

func (s *Service) Me(ctx context.Context, userID string) (*bffv1.MeResponse, error) {
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
	return &bffv1.MeResponse{User: toUser(u)}, nil
}

func (s *Service) Logout(ctx context.Context, sessionID string) error {
	if err := s.db.Session.UpdateOneID(sessionID).SetRevoked(true).Exec(ctx); err != nil && !ent.IsNotFound(err) {
		return internal(err)
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
	return session.BrowserSession{
		UserID: sess.UserID, SessionID: sess.ID,
		DeletionPending: u.DeletionRequestedAt != nil,
	}, nil
}

var errUnauth = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

func internal(err error) error {
	slog.Error("internal", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
