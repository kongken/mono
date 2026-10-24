// Package accesstoken issues and checks the management tokens that non-browser
// clients (the metro MCP endpoint) present as a Bearer credential.
//
// A token acts as the administrator who created it, with at most the roles that
// administrator had at that moment and the auth_version the account had then. The
// holder of a token is not trusted on its own: the MCP endpoint asks usersrv, on
// every tool call, whether the account is still active at that version and still
// holds the role. Only the SHA-256 digest of the secret is
// stored, like a browser session token. Revocation is read from the database on
// every request, so it takes effect immediately.
package accesstoken

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	entaccesstoken "github.com/kongken/mono/internal/bff/ent/accesstoken"
	"github.com/kongken/mono/internal/bff/session"
)

const (
	// AudienceMetro names the metro MCP endpoint, the only audience in v1.
	AudienceMetro = "metro"
	// ScopeRead allows the read-only tools; ScopeWrite allows the write tools and
	// implies ScopeRead.
	ScopeRead  = "metro:read"
	ScopeWrite = "metro:write"

	// MaxActivePerUser bounds the valid tokens one administrator holds.
	MaxActivePerUser = 10
	// MaxLifetimeDays is the longest token validity.
	MaxLifetimeDays = 90
	// MaxListed bounds one List call.
	MaxListed = 200

	// SecretPrefix starts every token secret, so a leaked one is recognisable.
	SecretPrefix = "mcp_"
	secretLength = len(SecretPrefix) + 64
	maxNameRunes = 64
	// touchInterval is how often last_used_at is written for a busy token.
	touchInterval = time.Minute
)

var (
	// ErrInvalidToken covers an unknown, revoked, expired or otherwise unusable
	// token. Callers must not reveal which.
	ErrInvalidToken = errors.New("invalid access token")
)

// Principal is a verified token.
type Principal struct {
	TokenID  string
	UserID   string
	Audience string
	// AuthVersion is the account auth_version the token was created at.
	AuthVersion int64
	// Roles is the snapshot taken at creation; the effective roles of a call are
	// this set intersected with the account's current roles.
	Roles  []string
	Scopes []string
}

// Allows reports whether the token holds scope; metro:write includes metro:read.
func (p Principal) Allows(scope string) bool {
	if slices.Contains(p.Scopes, scope) {
		return true
	}
	return scope == ScopeRead && slices.Contains(p.Scopes, ScopeWrite)
}

// Service stores and verifies tokens.
type Service struct {
	db  *ent.Client
	now func() time.Time
}

// New returns a Service over db.
func New(db *ent.Client) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock replaces the time source (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// CreateInput is a validated creation request.
type CreateInput struct {
	Name          string
	Audience      string
	Scopes        []string
	ExpiresInDays int
}

// Create issues a token for owner, whose password must have been checked within
// session.SensitiveAuthMaxAge, and returns the plaintext secret, which is not
// stored and cannot be shown again.
func (s *Service) Create(ctx context.Context, owner session.BrowserSession, in CreateInput) (*ent.AccessToken, string, error) {
	if s.now().Sub(owner.AuthenticatedAt) > session.SensitiveAuthMaxAge {
		return nil, "", connect.NewError(connect.CodeFailedPrecondition, errors.New("enter your password again to create an access token"))
	}
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes || strings.ContainsFunc(name, unicode.IsControl) {
		return nil, "", invalid("name must be 1 to 64 characters without control characters")
	}
	audience := in.Audience
	if audience == "" {
		audience = AudienceMetro
	}
	if audience != AudienceMetro {
		return nil, "", invalid("audience must be metro")
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return nil, "", err
	}
	if in.ExpiresInDays < 1 || in.ExpiresInDays > MaxLifetimeDays {
		return nil, "", invalid("expires_in_days must be between 1 and 90")
	}
	secret, err := newSecret()
	if err != nil {
		return nil, "", internal(err)
	}
	now := s.now()

	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, "", internal(err)
	}
	defer func() { _ = tx.Rollback() }()
	// Serialise this user's creations so the cap cannot be raced past. The
	// account lives in usersrv, so there is no row of ours to lock.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "bff_access_token:"+owner.UserID); err != nil {
		return nil, "", internal(err)
	}
	active, err := tx.AccessToken.Query().Where(
		entaccesstoken.UserID(owner.UserID), entaccesstoken.RevokedAtIsNil(), entaccesstoken.ExpiresAtGT(now)).Count(ctx)
	if err != nil {
		return nil, "", internal(err)
	}
	if active >= MaxActivePerUser {
		return nil, "", connect.NewError(connect.CodeFailedPrecondition,
			errors.New("too many active tokens; revoke one first (at most 10)"))
	}
	row, err := tx.AccessToken.Create().
		SetUserID(owner.UserID).SetAuthVersion(owner.AuthVersion).SetName(name).SetTokenHash(session.HashToken(secret)).
		SetAudience(audience).SetScopes(scopes).SetRoles(slices.Clone(owner.Roles)).
		SetCreatedAt(now).SetExpiresAt(now.AddDate(0, 0, in.ExpiresInDays)).Save(ctx)
	if err != nil {
		return nil, "", internal(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, "", internal(err)
	}
	slog.Info("access token created", "token_id", row.ID, "user_id", row.UserID,
		"audience", audience, "scopes", scopes, "expires_at", row.ExpiresAt)
	return row, secret, nil
}

// List returns userID's tokens, or everyone's when all is set, newest first.
// Revoked and expired tokens are included.
func (s *Service) List(ctx context.Context, userID string, all bool) ([]*ent.AccessToken, error) {
	q := s.db.AccessToken.Query().Order(ent.Desc(entaccesstoken.FieldCreatedAt), ent.Desc(entaccesstoken.FieldID)).Limit(MaxListed)
	if !all {
		q = q.Where(entaccesstoken.UserID(userID))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, internal(err)
	}
	return rows, nil
}

// Revoke ends a token. Revoking a revoked token succeeds and keeps the first
// revocation time.
func (s *Service) Revoke(ctx context.Context, actorUserID, id string) error {
	now := s.now()
	n, err := s.db.AccessToken.Update().Where(entaccesstoken.ID(id), entaccesstoken.RevokedAtIsNil()).SetRevokedAt(now).Save(ctx)
	if err != nil {
		return internal(err)
	}
	if n == 0 {
		exists, err := s.db.AccessToken.Query().Where(entaccesstoken.ID(id)).Exist(ctx)
		if err != nil {
			return internal(err)
		}
		if !exists {
			return connect.NewError(connect.CodeNotFound, errors.New("token not found"))
		}
		return nil
	}
	slog.Info("access token revoked", "token_id", id, "actor_user_id", actorUserID)
	return nil
}

// Authenticate verifies a presented secret for audience. Every failure is
// ErrInvalidToken except a storage failure.
func (s *Service) Authenticate(ctx context.Context, secret, audience string) (Principal, error) {
	if len(secret) != secretLength || !strings.HasPrefix(secret, SecretPrefix) {
		return Principal{}, ErrInvalidToken
	}
	now := s.now()
	row, err := s.db.AccessToken.Query().
		Where(entaccesstoken.TokenHash(session.HashToken(secret)), entaccesstoken.RevokedAtIsNil(), entaccesstoken.ExpiresAtGT(now)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Principal{}, ErrInvalidToken
		}
		return Principal{}, err
	}
	if row.Audience != audience || row.AuthVersion <= 0 {
		return Principal{}, ErrInvalidToken
	}
	if row.LastUsedAt == nil || now.Sub(*row.LastUsedAt) > touchInterval {
		if err := s.db.AccessToken.UpdateOneID(row.ID).SetLastUsedAt(now).Exec(ctx); err != nil {
			slog.Warn("touch access token", "error", err)
		}
	}
	return Principal{
		TokenID: row.ID, UserID: row.UserID, Audience: row.Audience, AuthVersion: row.AuthVersion,
		Roles: slices.Clone(row.Roles), Scopes: slices.Clone(row.Scopes),
	}, nil
}

func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{ScopeRead}, nil
	}
	var out []string
	for _, sc := range in {
		if sc != ScopeRead && sc != ScopeWrite {
			return nil, invalid("scopes may only be metro:read and metro:write")
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	slices.Sort(out)
	return out, nil
}

func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return SecretPrefix + hex.EncodeToString(buf), nil
}

func invalid(msg string) error { return connect.NewError(connect.CodeInvalidArgument, errors.New(msg)) }

func internal(err error) error {
	slog.Error("access token", "error", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}
