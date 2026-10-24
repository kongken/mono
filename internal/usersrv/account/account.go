// Package account is the account core of usersrv: it creates accounts, checks
// and changes passwords, keeps the base profile, runs the deletion steps and
// manages roles and account status. Everything that changes a credential, a
// status or a role bumps auth_version and writes the audit row in the same
// transaction as the change. The package knows nothing about gRPC or HTTP.
package account

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/deletiontombstone"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/ent/userrole"
	"github.com/kongken/mono/internal/usersrv/identity"
	"github.com/kongken/mono/internal/usersrv/passwordhash"
)

// Status is the account state (spec 4.3).
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	StatusDeleting Status = "deleting"
)

// TombstoneTTL is how long a finished deletion can be replayed.
const TombstoneTTL = 24 * time.Hour

// Retention defaults for rows that only exist for a limited time.
const (
	DefaultAuditRetention = 90 * 24 * time.Hour
	DefaultTokenRetention = 7 * 24 * time.Hour
)

// The ways an operation can be refused. Callers map them to RPC statuses.
var (
	// ErrNotFound: no such account (or tombstone).
	ErrNotFound = errors.New("account not found")
	// ErrInvalidCredentials: wrong password, or an address that cannot log in.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrNotActive: the account is disabled or deleting.
	ErrNotActive = errors.New("account is not active")
	// ErrEmailTaken: the canonical email already has an account.
	ErrEmailTaken = errors.New("email already registered")
	// ErrConflict: a concurrent change won; the caller may retry.
	ErrConflict = errors.New("concurrent change")
	// ErrDeletionMismatch: the deletion step does not match the workflow that owns it.
	ErrDeletionMismatch = errors.New("deletion belongs to another mutation")
	// ErrUnavailable: the store or the password budget could not serve the request.
	ErrUnavailable = errors.New("temporarily unavailable")
)

// Service is the account core over one usersrv database.
type Service struct {
	db     *ent.Client
	hasher *passwordhash.Hasher
	now    func() time.Time
	// AuditRetention and TokenRetention drive PurgeExpired.
	AuditRetention time.Duration
	TokenRetention time.Duration
}

// NewService returns the account core.
func NewService(db *ent.Client, hasher *passwordhash.Hasher) *Service {
	return &Service{db: db, hasher: hasher, now: func() time.Time { return time.Now().UTC() },
		AuditRetention: DefaultAuditRetention, TokenRetention: DefaultTokenRetention}
}

// SetClock replaces the time source; tests use it.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Profile is the base profile shared by all products.
type Profile struct {
	DisplayName string
	AvatarURL   string
	Version     int64
}

// AuthState is what the BFF re-checks before it forwards a request.
type AuthState struct {
	Status      Status
	AuthVersion int64
	Roles       []string
	Profile     Profile
}

// Account is one account as seen by its owner.
type Account struct {
	ID    uuid.UUID
	Email string
	AuthState
}

// NewUser describes an account whose email has been verified. Only the
// registration completion of stage P3 calls CreateVerifiedUser; an unverified
// address never becomes a row.
type NewUser struct {
	Email       string
	Password    string
	DisplayName string
	VerifiedAt  time.Time
}

// CreateVerifiedUser creates an active account with a fresh UUID. The unique
// index on the canonical email, not a prior lookup, decides a race: the loser
// gets ErrEmailTaken and nothing is written.
func (s *Service) CreateVerifiedUser(ctx context.Context, in NewUser) (uuid.UUID, error) {
	email, err := identity.NormalizeEmail(in.Email)
	if err != nil {
		return uuid.Nil, err
	}
	if err = identity.ValidatePassword("password", in.Password); err != nil {
		return uuid.Nil, err
	}
	name, err := identity.NormalizeDisplayName(in.DisplayName, true)
	if err != nil {
		return uuid.Nil, err
	}
	verified := in.VerifiedAt
	if verified.IsZero() {
		verified = s.now()
	}
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return uuid.Nil, mapStoreError(err)
	}
	id := uuid.New()
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		if _, err := tx.User.Create().SetID(id).SetEmailCanonical(email).SetEmailVerifiedAt(verified).
			SetPasswordHash(hash).SetDisplayName(name).Save(ctx); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &id, actor: userActor(id), action: "account.create", result: "ok"})
	})
	if err != nil {
		if isUniqueViolation(err, "users_email_canonical_key") {
			return uuid.Nil, ErrEmailTaken
		}
		return uuid.Nil, mapStoreError(err)
	}
	return id, nil
}

// GetAuthState reads status, auth_version, roles and the profile version.
func (s *Service) GetAuthState(ctx context.Context, id uuid.UUID) (AuthState, error) {
	u, err := s.db.User.Query().Where(user.ID(id)).WithRoles().Only(ctx)
	if err != nil {
		return AuthState{}, notFoundOr(err)
	}
	return stateOf(u), nil
}

// GetAccount reads the caller's own account including the login email.
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	u, err := s.db.User.Query().Where(user.ID(id)).WithRoles().Only(ctx)
	if err != nil {
		return Account{}, notFoundOr(err)
	}
	return Account{ID: u.ID, Email: u.EmailCanonical, AuthState: stateOf(u)}, nil
}

func stateOf(u *ent.User) AuthState {
	roles := make([]string, 0, len(u.Edges.Roles))
	for _, r := range u.Edges.Roles {
		roles = append(roles, r.Role)
	}
	sort.Strings(roles)
	return AuthState{
		Status: Status(u.Status), AuthVersion: u.AuthVersion, Roles: roles,
		Profile: Profile{DisplayName: u.DisplayName, AvatarURL: u.AvatarURL, Version: u.ProfileVersion},
	}
}

// Authenticate checks an email and password. An unknown address, a wrong
// password, a disabled and a deleting account all return ErrInvalidCredentials,
// and every path spends the cost of one password verification, so an unknown
// address has no obviously faster path. Stage P3 puts the rate limits and the
// RPC around it; nothing calls it from a public path in P1.
func (s *Service) Authenticate(ctx context.Context, email, password string) (uuid.UUID, AuthState, error) {
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return uuid.Nil, AuthState{}, err
	}
	if err = identity.CheckPasswordInput("password", password); err != nil {
		return uuid.Nil, AuthState{}, err
	}
	u, err := s.db.User.Query().Where(user.EmailCanonical(canonical)).WithRoles().Only(ctx)
	if ent.IsNotFound(err) {
		if err := s.hasher.VerifyDummy(ctx, password); err != nil {
			return uuid.Nil, AuthState{}, mapStoreError(err)
		}
		return uuid.Nil, AuthState{}, ErrInvalidCredentials
	}
	if err != nil {
		return uuid.Nil, AuthState{}, mapStoreError(err)
	}
	ok, err := s.hasher.Verify(ctx, u.PasswordHash, password)
	if err != nil {
		return uuid.Nil, AuthState{}, mapStoreError(err)
	}
	if !ok || Status(u.Status) != StatusActive {
		return uuid.Nil, AuthState{}, ErrInvalidCredentials
	}
	return u.ID, stateOf(u), nil
}

// VerifyPassword re-checks the current password of an active account and returns
// the auth_version it was checked at, which the BFF compares with its session.
func (s *Service) VerifyPassword(ctx context.Context, id uuid.UUID, password, requestID string) (int64, error) {
	if err := identity.CheckPasswordInput("password", password); err != nil {
		return 0, err
	}
	u, err := s.activeUser(ctx, id)
	if err != nil {
		return 0, err
	}
	ok, err := s.hasher.Verify(ctx, u.PasswordHash, password)
	if err != nil {
		return 0, mapStoreError(err)
	}
	if !ok {
		s.auditAlone(ctx, auditEntry{userID: &id, actor: userActor(id), action: "password.verify", requestID: requestID, result: "wrong_password"})
		return 0, ErrInvalidCredentials
	}
	return u.AuthVersion, nil
}

// ChangePassword replaces the password after checking the current one. The new
// hash is written only if auth_version is still the one the current password
// was checked at, so two concurrent changes cannot both pass with the old
// password. Success bumps auth_version and returns the new value.
func (s *Service) ChangePassword(ctx context.Context, id uuid.UUID, current, next, requestID string) (int64, error) {
	if err := identity.CheckPasswordInput("current_password", current); err != nil {
		return 0, err
	}
	if err := identity.ValidatePassword("new_password", next); err != nil {
		return 0, err
	}
	u, err := s.activeUser(ctx, id)
	if err != nil {
		return 0, err
	}
	ok, err := s.hasher.Verify(ctx, u.PasswordHash, current)
	if err != nil {
		return 0, mapStoreError(err)
	}
	if !ok {
		s.auditAlone(ctx, auditEntry{userID: &id, actor: userActor(id), action: "password.change", requestID: requestID, result: "wrong_password"})
		return 0, ErrInvalidCredentials
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return 0, mapStoreError(err)
	}
	var version int64
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		updated, err := tx.User.UpdateOneID(id).
			Where(user.AuthVersion(u.AuthVersion), user.StatusEQ(user.StatusActive)).
			SetPasswordHash(hash).AddAuthVersion(1).Save(ctx)
		if err != nil {
			return err
		}
		version = updated.AuthVersion
		return s.audit(ctx, tx, auditEntry{userID: &id, actor: userActor(id), action: "password.change", requestID: requestID, result: "ok"})
	})
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, ErrConflict
		}
		return 0, mapStoreError(err)
	}
	return version, nil
}

// UpdateProfile replaces nickname and avatar of an active account. The profile
// version rises only when something changed, and auth_version never does.
func (s *Service) UpdateProfile(ctx context.Context, id uuid.UUID, displayName, avatarURL string) (Profile, error) {
	name, err := identity.NormalizeDisplayName(displayName, false)
	if err != nil {
		return Profile{}, err
	}
	avatar, err := identity.NormalizeAvatarURL(avatarURL)
	if err != nil {
		return Profile{}, err
	}
	u, err := s.activeUser(ctx, id)
	if err != nil {
		return Profile{}, err
	}
	if u.DisplayName == name && u.AvatarURL == avatar {
		return Profile{DisplayName: u.DisplayName, AvatarURL: u.AvatarURL, Version: u.ProfileVersion}, nil
	}
	updated, err := s.db.User.UpdateOneID(id).Where(user.StatusEQ(user.StatusActive)).
		SetDisplayName(name).SetAvatarURL(avatar).AddProfileVersion(1).Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			_, cerr := s.activeUser(ctx, id)
			if cerr != nil {
				return Profile{}, cerr
			}
			return Profile{}, ErrConflict
		}
		return Profile{}, mapStoreError(err)
	}
	return Profile{DisplayName: updated.DisplayName, AvatarURL: updated.AvatarURL, Version: updated.ProfileVersion}, nil
}

// activeUser loads the account and requires it to be active.
func (s *Service) activeUser(ctx context.Context, id uuid.UUID) (*ent.User, error) {
	u, err := s.db.User.Get(ctx, id)
	if err != nil {
		return nil, notFoundOr(err)
	}
	if Status(u.Status) != StatusActive {
		return nil, ErrNotActive
	}
	return u, nil
}

// BeginDeletion moves an active account to deleting for one workflow. Repeating
// it with the same mutation ID succeeds; another mutation ID is refused. It
// bumps auth_version and voids every account token of the user and of its
// address. (Cancelling unsent mail belongs to the mail outbox of stage P2.)
func (s *Service) BeginDeletion(ctx context.Context, id uuid.UUID, mutationID, requestID string) (int64, error) {
	mutationID, err := identity.NormalizeMutationID(mutationID)
	if err != nil {
		return 0, err
	}
	var version int64
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		u, err := tx.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)
		if err != nil {
			return notFoundOr(err)
		}
		switch Status(u.Status) {
		case StatusDeleting:
			if u.DeletionMutationID == nil || *u.DeletionMutationID != mutationID {
				return ErrDeletionMismatch
			}
			version = u.AuthVersion
			return nil
		case StatusActive:
		default:
			return ErrNotActive
		}
		updated, err := tx.User.UpdateOneID(id).SetStatus(user.StatusDeleting).SetDeletionStartedAt(s.now()).
			SetDeletionMutationID(mutationID).AddAuthVersion(1).Save(ctx)
		if err != nil {
			return err
		}
		version = updated.AuthVersion
		if err = s.voidTokens(ctx, tx, id, u.EmailCanonical); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &id, actor: userActor(id), action: "deletion.begin", requestID: requestID, result: "ok"})
	})
	if err != nil {
		return 0, mapStoreError(err)
	}
	return version, nil
}

// FinishDeletion removes the account and everything it owns and leaves a
// tombstone. It only proceeds for an account that is deleting under this
// mutation ID. When the account is already gone, a matching unexpired tombstone
// answers success (removed=false); a missing or foreign tombstone does not let
// anything advance. The email is free for a new registration afterwards.
func (s *Service) FinishDeletion(ctx context.Context, id uuid.UUID, mutationID, requestID string) (removed bool, err error) {
	mutationID, err = identity.NormalizeMutationID(mutationID)
	if err != nil {
		return false, err
	}
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		u, err := tx.User.Query().Where(user.ID(id)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			tomb, terr := tx.DeletionTombstone.Query().Where(deletiontombstone.ID(id)).Only(ctx)
			if ent.IsNotFound(terr) || (terr == nil && !tomb.ExpiresAt.After(s.now())) {
				return ErrNotFound
			}
			if terr != nil {
				return terr
			}
			if tomb.MutationID != mutationID {
				return ErrDeletionMismatch
			}
			return nil
		}
		if err != nil {
			return err
		}
		if Status(u.Status) != StatusDeleting || u.DeletionMutationID == nil || *u.DeletionMutationID != mutationID {
			return ErrDeletionMismatch
		}
		if err = s.voidTokens(ctx, tx, id, u.EmailCanonical); err != nil {
			return err
		}
		if err = tx.User.DeleteOneID(id).Exec(ctx); err != nil { // roles cascade
			return err
		}
		done := s.now()
		if err = tx.DeletionTombstone.Create().SetID(id).SetMutationID(mutationID).SetCompletedAt(done).
			SetExpiresAt(done.Add(TombstoneTTL)).Exec(ctx); err != nil {
			return err
		}
		removed = true
		return s.audit(ctx, tx, auditEntry{userID: &id, actor: userActor(id), action: "deletion.finish", requestID: requestID, result: "ok"})
	})
	if err != nil {
		return false, mapStoreError(err)
	}
	return removed, nil
}

// PurgeResult counts what PurgeExpired removed.
type PurgeResult struct{ Tokens, Tombstones, Audit int }

// PurgeExpired removes challenges that expired (or were consumed) longer ago
// than TokenRetention, finished deletion tombstones past their 24 hours, and
// audit rows past AuditRetention. It is idempotent and safe on several replicas.
func (s *Service) PurgeExpired(ctx context.Context) (PurgeResult, error) {
	now := s.now()
	var res PurgeResult
	var err error
	if res.Tokens, err = s.db.AccountToken.Delete().Where(accounttokenStale(now.Add(-s.TokenRetention))).Exec(ctx); err != nil {
		return res, err
	}
	if res.Tombstones, err = s.db.DeletionTombstone.Delete().Where(deletiontombstone.ExpiresAtLT(now)).Exec(ctx); err != nil {
		return res, err
	}
	res.Audit, err = s.purgeAudit(ctx, now.Add(-s.AuditRetention))
	return res, err
}

// inTx runs fn in a transaction: commit on nil, roll back otherwise.
func (s *Service) inTx(ctx context.Context, fn func(tx *ent.Tx) error) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil {
			slog.Error("usersrv rollback", "error", rerr)
		}
		return err
	}
	return tx.Commit()
}

func userActor(id uuid.UUID) string { return "user:" + id.String() }

func notFoundOr(err error) error {
	if ent.IsNotFound(err) {
		return ErrNotFound
	}
	return mapStoreError(err)
}

// mapStoreError keeps the sentinel and validation errors and turns outages
// (connection, timeout, cancellation, no password budget) into ErrUnavailable.
// Other errors pass through for the caller to log as internal.
func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidCredentials), errors.Is(err, ErrNotActive), errors.Is(err, ErrEmailTaken),
		errors.Is(err, ErrConflict), errors.Is(err, ErrDeletionMismatch), errors.Is(err, ErrUnavailable):
		return err
	case errors.Is(err, passwordhash.ErrBusy):
		return fmt.Errorf("%w: password budget", ErrUnavailable)
	}
	if _, ok := identity.IsValidation(err); ok {
		return err
	}
	if isOutage(err) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

func isOutage(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, sql.ErrConnDone) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		// 08 connection, 53 insufficient resources, 57 operator intervention, 40001/40P01 serialization.
		return strings.HasPrefix(pg.Code, "08") || strings.HasPrefix(pg.Code, "53") || strings.HasPrefix(pg.Code, "57")
	}
	var conn *pgconn.ConnectError
	return errors.As(err, &conn)
}

func isUniqueViolation(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && (constraint == "" || pg.ConstraintName == constraint)
}

// IsUnavailable reports whether err is a temporary outage.
func IsUnavailable(err error) bool { return errors.Is(err, ErrUnavailable) }

// roleNames lists a user's roles sorted.
func roleNames(ctx context.Context, tx *ent.Tx, id uuid.UUID) ([]string, error) {
	rows, err := tx.UserRole.Query().Where(userrole.UserID(id)).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Role)
	}
	sort.Strings(out)
	return out, nil
}
