// Package auth is the BFF's account layer: the Cookie session, the checks that
// run before every protected request, and the account deletion workflow. The
// account itself (email, password, roles, account mail) is usersrv's; this package
// keeps no password, no email index and no identity mapping.
package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/bff/config"
	"github.com/kongken/mono/internal/bff/ent"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	"github.com/kongken/mono/internal/bff/ratelimit"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/bff/usersrv"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	touchInterval = time.Minute
	// deletionWindow is the window of the per-account limit on account deletion starts and retries.
	deletionWindow = 15 * time.Minute
	deletionLimit  = 10
)

// Users is the part of usersrv the account layer needs; *usersrv.Client implements it.
type Users interface {
	RequestRegistration(ctx context.Context, email, appID string) error
	CompleteRegistration(ctx context.Context, token, password, displayName string) error
	Authenticate(ctx context.Context, email, password string) (usersrv.Login, error)
	RequestPasswordReset(ctx context.Context, email, appID string) error
	ResetPassword(ctx context.Context, token, newPassword string) error
	AuthState(ctx context.Context, userID string) (usersrv.AuthState, error)
	Account(ctx context.Context, userID string) (usersrv.Account, error)
	VerifyPassword(ctx context.Context, userID, password string) (int64, error)
	ChangePassword(ctx context.Context, userID, current, next string) (int64, error)
	UpdateProfile(ctx context.Context, userID, displayName, avatarURL string) (usersrv.Profile, error)
	BeginDeletion(ctx context.Context, userID, mutationID string) error
	FinishDeletion(ctx context.Context, userID, mutationID string) error
}

// UserDataCleaner removes one product's data of a user when the user deletes
// their account. CleanupUserData must be idempotent: it is repeated on every
// retry of a deletion, and succeeds when there is nothing left to remove.
type UserDataCleaner interface {
	// Name identifies the product in logs, in auth.account_cleanup and in the
	// workflow's product list.
	Name() string
	CleanupUserData(ctx context.Context, userID string) error
}

// Service is the account layer over the BFF's own tables and usersrv.
type Service struct {
	cfg      *config.AuthConfig
	cookies  session.CookieConfig
	db       *ent.Client
	limit    *ratelimit.Limiter
	users    Users
	cleaners []UserDataCleaner
	now      func() time.Time
}

// NewService returns the account layer. cleaners are the products enabled by
// auth.account_cleanup, in the order they are cleaned.
func NewService(cfg *config.AuthConfig, cookies session.CookieConfig, db *ent.Client, limit *ratelimit.Limiter, users Users, cleaners []UserDataCleaner) *Service {
	return &Service{cfg: cfg, cookies: cookies, db: db, limit: limit, users: users, cleaners: cleaners,
		now: func() time.Time { return time.Now().UTC() }}
}

// SetClock replaces the time source (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// SessionLifetime is the fixed lifetime of a new session.
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

func toUser(id string, p usersrv.Profile) *bffv1.User {
	return &bffv1.User{Id: id, DisplayName: p.DisplayName, AvatarUrl: p.AvatarURL}
}

// Login checks the credentials with usersrv and, only on success, opens a new
// session: a fresh random token (only its digest is stored), the account's
// auth_version, and the time the password was really checked. A session ID from
// before the login is never reused.
func (s *Service) Login(ctx context.Context, email, password string) (*bffv1.LoginWithEmailResponse, string, error) {
	l, err := s.users.Authenticate(ctx, email, password)
	if err != nil {
		return nil, "", err
	}
	token, hash, err := session.NewToken()
	if err != nil {
		return nil, "", internal(err)
	}
	authenticated := s.now()
	expires := authenticated.Add(s.SessionLifetime())
	if _, err = s.db.Session.Create().SetUserID(l.UserID).SetTokenHash(hash).SetExpiresAt(expires).
		SetAuthVersion(l.AuthVersion).SetAuthenticatedAt(authenticated).Save(ctx); err != nil {
		return nil, "", internal(err)
	}
	return &bffv1.LoginWithEmailResponse{User: toUser(l.UserID, l.Profile), ExpiresAt: timestamppb.New(expires)}, token, nil
}

// Me returns the caller's own account from usersrv, for the owner's screen.
func (s *Service) Me(ctx context.Context, bs session.BrowserSession) (*bffv1.MeResponse, error) {
	acct, err := s.users.Account(ctx, bs.UserID)
	if err != nil {
		if errors.Is(err, usersrv.ErrNotFound) {
			return nil, errUnauth
		}
		return nil, err
	}
	// The account moved on between the request's check and this read.
	if acct.Status != usersrv.StatusActive || acct.AuthVersion != bs.AuthVersion {
		return nil, errUnauth
	}
	return &bffv1.MeResponse{
		User: toUser(acct.UserID, acct.Profile), Roles: acct.Roles, Email: acct.Email,
		AuthenticatedAt:  timestamppb.New(bs.AuthenticatedAt),
		SessionExpiresAt: timestamppb.New(bs.ExpiresAt),
	}, nil
}

// Reauthenticate re-checks the current password and records the time on this
// session. The version usersrv reports must still be the session's: a session that
// is already stale is not upgraded into a valid one.
func (s *Service) Reauthenticate(ctx context.Context, bs session.BrowserSession, password string) (time.Time, error) {
	version, err := s.users.VerifyPassword(ctx, bs.UserID, password)
	if err != nil {
		return time.Time{}, err
	}
	if version != bs.AuthVersion {
		s.revoke(ctx, bs.SessionID)
		return time.Time{}, errUnauth
	}
	at := s.now()
	if err := s.db.Session.UpdateOneID(bs.SessionID).Where(entsession.Revoked(false)).SetAuthenticatedAt(at).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return time.Time{}, errUnauth
		}
		return time.Time{}, internal(err)
	}
	return at, nil
}

// ChangePassword changes the password at usersrv, which bumps auth_version. The
// account's browser sessions are revoked as well so they do not linger; they were
// unusable from that moment anyway.
func (s *Service) ChangePassword(ctx context.Context, bs session.BrowserSession, current, next string) error {
	if _, err := s.users.ChangePassword(ctx, bs.UserID, current, next); err != nil {
		return err
	}
	s.revokeAll(ctx, bs.UserID)
	return nil
}

// UpdateProfile replaces the nickname and avatar at usersrv.
func (s *Service) UpdateProfile(ctx context.Context, bs session.BrowserSession, displayName, avatarURL string) (*bffv1.User, error) {
	p, err := s.users.UpdateProfile(ctx, bs.UserID, displayName, avatarURL)
	if err != nil {
		return nil, err
	}
	return toUser(bs.UserID, p), nil
}

// Logout revokes the session of token. It never fails for a missing or unknown
// token: logging out twice is the same as logging out once.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if _, err := s.db.Session.Update().Where(entsession.TokenHash(session.HashToken(token))).SetRevoked(true).Save(ctx); err != nil {
		return internal(err)
	}
	return nil
}

func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) (*bffv1.ListSessionsResponse, error) {
	rows, err := s.db.Session.Query().
		Where(entsession.UserID(userID), entsession.Revoked(false), entsession.ExpiresAtGT(s.now())).
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

// revoke ends one session; failures only log, because it is housekeeping next to
// a rejection that has already been decided.
func (s *Service) revoke(ctx context.Context, sessionID string) {
	if err := s.db.Session.UpdateOneID(sessionID).SetRevoked(true).Exec(context.WithoutCancel(ctx)); err != nil && !ent.IsNotFound(err) {
		slog.Warn("revoke session", "error", err)
	}
}

func (s *Service) revokeAll(ctx context.Context, userID string) {
	if _, err := s.db.Session.Update().Where(entsession.UserID(userID), entsession.Revoked(false)).SetRevoked(true).Save(context.WithoutCancel(ctx)); err != nil {
		slog.Warn("revoke sessions", "error", err)
	}
}

// ResolveSession is the check in front of every protected request: the Cookie
// must name a live session row, and usersrv must say, now, that the account is
// active at the very auth_version the session was opened with. It returns the
// account's current roles with the session. There is no positive cache: when
// usersrv or its stores cannot answer, the result is UNAVAILABLE and the request
// is not forwarded, with no fallback to an older role snapshot.
//
// A request that finds the session stale (account gone, disabled, deleting, or
// the version moved) is UNAUTHENTICATED, and its session row is revoked.
func (s *Service) ResolveSession(ctx context.Context, token string) (session.BrowserSession, error) {
	if token == "" {
		return session.BrowserSession{}, errUnauth
	}
	now := s.now()
	row, err := s.db.Session.Query().
		Where(entsession.TokenHash(session.HashToken(token)), entsession.Revoked(false), entsession.ExpiresAtGT(now)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return session.BrowserSession{}, errUnauth
		}
		slog.Error("session lookup", "error", err)
		return session.BrowserSession{}, internal(err)
	}
	// Rows from before usersrv name users that usersrv has never heard of (and
	// carry version 0); they are never forwarded.
	if _, perr := uuid.Parse(row.UserID); perr != nil || row.AuthVersion <= 0 {
		s.revoke(ctx, row.ID)
		return session.BrowserSession{}, errUnauth
	}
	state, err := s.users.AuthState(ctx, row.UserID)
	if err != nil {
		if errors.Is(err, usersrv.ErrNotFound) {
			s.revoke(ctx, row.ID)
			return session.BrowserSession{}, errUnauth
		}
		return session.BrowserSession{}, err
	}
	if state.Status != usersrv.StatusActive || state.AuthVersion != row.AuthVersion {
		s.revoke(ctx, row.ID)
		return session.BrowserSession{}, errUnauth
	}
	if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) > touchInterval {
		if err := s.db.Session.UpdateOneID(row.ID).SetLastUsedAt(now).Exec(ctx); err != nil {
			slog.Warn("touch session", "error", err)
		}
	}
	authenticated := row.CreatedAt
	if row.AuthenticatedAt != nil {
		authenticated = *row.AuthenticatedAt
	}
	return session.BrowserSession{
		UserID: row.UserID, SessionID: row.ID, AuthVersion: row.AuthVersion, ProfileVersion: state.ProfileVersion,
		Roles: state.Roles, AuthenticatedAt: authenticated, ExpiresAt: row.ExpiresAt,
	}, nil
}

// RequireRecentPassword refuses a sensitive action unless the password was checked
// on this session within SensitiveAuthMaxAge. The caller can fix that with
// Reauthenticate.
func (s *Service) RequireRecentPassword(bs session.BrowserSession) error {
	if s.now().Sub(bs.AuthenticatedAt) > session.SensitiveAuthMaxAge {
		return ErrReauthRequired
	}
	return nil
}

// ErrReauthRequired is returned when a sensitive action needs a fresher password check.
var ErrReauthRequired = connect.NewError(connect.CodeFailedPrecondition, errors.New("enter your password again to continue"))

var errUnauth = connect.NewError(connect.CodeUnauthenticated, errors.New("sign in required"))

func internal(err error) error {
	slog.Error("internal", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
