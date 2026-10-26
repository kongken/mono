package account

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/platform/navpath"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// This file holds the two token flows of the spec (5.1 registration, 5.3
// password reset). Both follow the same rules:
//
//   - A request answers the same way for every well-formed address; only the
//     work behind the answer differs. Account-level mail limits suppress
//     silently, public limits are RESOURCE_EXHAUSTED.
//   - The challenge and its encrypted mail job are created in one transaction.
//   - Completing a flow consumes the token inside the transaction that changes
//     the account, after locking the rows that decide a race, so concurrent
//     submissions produce exactly one winner and the losers change nothing.
//   - Neither flow signs anybody in.

// MailPurpose is the kind of challenge a mail carries.
type MailPurpose string

const (
	PurposeRegistration  MailPurpose = "registration"
	PurposePasswordReset MailPurpose = "password_reset"
)

// MailRequest describes one challenge and its mail. UserID and AuthVersion bind
// a reset token to the account state it was issued for and are nil for a
// registration.
type MailRequest struct {
	Purpose     MailPurpose
	Email       string
	UserID      *uuid.UUID
	AuthVersion *int64
	Return      ReturnContext
}

// ReturnContext is where the user came from: a stable app ID and a canonical pathname
// in it (empty meaning the app's default path). It is bound to the challenge and
// handed back when the token is consumed, so another browser can finish the flow and
// still find its way back. usersrv checks the form only; the meaning (is this app
// registered, is the path allowed) belongs to the BFF navigation registry, which
// validates it on the way in and again on the way out. The zero value is no context.
type ReturnContext struct {
	AppID string
	Path  string
}

var appIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Validate checks the form of c.
func (c ReturnContext) Validate() error {
	if c.AppID == "" && c.Path == "" {
		return nil
	}
	if !appIDPattern.MatchString(c.AppID) {
		return &identity.ValidationError{Field: "app_id", Reason: "must be a stable app ID"}
	}
	if c.Path != "" {
		if err := navpath.Check(c.Path); err != nil {
			return &identity.ValidationError{Field: "return_path", Reason: err.Error()}
		}
	}
	return nil
}

// returnOf reads the context stored on a challenge; a row without one gives the zero value.
func returnOf(t *ent.AccountToken) ReturnContext {
	var c ReturnContext
	if t.ReturnAppID != nil {
		c.AppID = *t.ReturnAppID
	}
	if t.ReturnPath != nil {
		c.Path = *t.ReturnPath
	}
	if c.Validate() != nil {
		return ReturnContext{}
	}
	return c
}

// Mail creates the challenge and its encrypted outbox job inside tx. The
// account core does not know how the mail is stored or sent: package app wires
// the outbox (which also keeps this package free of an import cycle with it).
type Mail interface {
	Issue(ctx context.Context, tx *ent.Tx, in MailRequest) error
	// Central reports whether the central OzID landing origin is configured, which
	// every mail link needs.
	Central() bool
}

// MailVerdict is the outcome of the mail limits for one request.
type MailVerdict int

const (
	// MailAllow: create the challenge and the mail.
	MailAllow MailVerdict = iota
	// MailSuppress: an account-level limit (cooldown, per-mailbox caps) is hit; answer
	// the same neutral success and send nothing.
	MailSuppress
	// MailLimited: a public limit (client address, service-wide cap) is hit.
	MailLimited
)

// MailGuard applies the account mail limits of spec section 10 (atomic Redis
// counters, failing closed with an error when Redis cannot answer).
type MailGuard interface {
	Check(ctx context.Context, purpose MailPurpose, email, clientIP string) (MailVerdict, error)
}

// SetMail installs the outbox and its limits. Without them the request methods
// answer ErrUnavailable: usersrv must not look able to register users while no
// mail can leave.
func (s *Service) SetMail(m Mail, g MailGuard) { s.mail, s.guard = m, g }

// SetRegistrationOpen switches new sign-ups on or off (auth.registration_enabled).
// It never affects login, password reset or any existing account.
func (s *Service) SetRegistrationOpen(open bool) { s.registrationClosed = !open }

// mailReady returns ErrUnavailable until SetMail was called.
func (s *Service) mailReady() error {
	if s.mail == nil || s.guard == nil || !s.mail.Central() {
		return errors.Join(ErrUnavailable, errors.New("account mail is not configured"))
	}
	return nil
}

// checkReturn refuses a malformed return context. Nothing has been counted or
// written yet, so a refused context changes no state.
func (s *Service) checkReturn(c ReturnContext) error { return c.Validate() }

// admit runs the mail limits for one request. It reports proceed=false when an
// account-level limit suppresses the mail (the caller still answers success).
func (s *Service) admit(ctx context.Context, purpose MailPurpose, email, clientIP, action, requestID string) (proceed bool, err error) {
	dec, err := s.guard.Check(ctx, purpose, email, clientIP)
	if err != nil {
		return false, err
	}
	switch dec {
	case MailAllow:
		return true, nil
	case MailSuppress:
		s.auditAlone(ctx, auditEntry{actor: "system", action: action, requestID: requestID, result: "suppressed"})
		return false, nil
	}
	return false, ErrRateLimited
}

// RequestRegistration accepts an address for verification. For a well-formed
// address it always returns nil: a registration challenge and its mail are
// created only when no account exists for the address (whatever its status),
// and the mail limits may silently suppress it. Nothing about the account, the
// limits or the mail is revealed. The password is not part of this step, no
// user is created and no session exists.
func (s *Service) RequestRegistration(ctx context.Context, email string, ret ReturnContext, clientIP, requestID string) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	if s.registrationClosed {
		return ErrRegistrationClosed
	}
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err = s.checkReturn(ret); err != nil {
		return err
	}
	const action = "registration.request"
	proceed, err := s.admit(ctx, PurposeRegistration, canonical, clientIP, action, requestID)
	if err != nil || !proceed {
		return err
	}
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		taken, err := tx.User.Query().Where(user.EmailCanonical(canonical)).Exist(ctx)
		if err != nil {
			return err
		}
		if taken {
			return s.audit(ctx, tx, auditEntry{actor: "system", action: action, requestID: requestID, result: "ignored"})
		}
		if err = s.mail.Issue(ctx, tx, MailRequest{Purpose: PurposeRegistration, Email: canonical, Return: ret}); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{actor: "system", action: action, requestID: requestID, result: "issued"})
	})
	return mapStoreError(err)
}

// CompleteRegistration verifies the registration token and creates the active
// account with the password and nickname the user now chooses (an empty
// nickname takes the default). The email is the one the token was issued for;
// the request cannot name another. In one transaction it locks every
// registration challenge of the address (so completions of one mailbox are
// serialized), checks that this token is unexpired and unconsumed and that no
// account exists, creates the user, and consumes this token and all other
// registration tokens of the address, cancelling their unsent mails. A unique
// violation on the email is treated like any other lost race. It does not
// sign anybody in.
func (s *Service) CompleteRegistration(ctx context.Context, token, password, displayName, requestID string) (uuid.UUID, ReturnContext, error) {
	if s.registrationClosed {
		return uuid.Nil, ReturnContext{}, ErrRegistrationClosed
	}
	// Policy checks do not depend on the token, so they reveal nothing about it.
	if err := identity.ValidatePassword("password", password); err != nil {
		return uuid.Nil, ReturnContext{}, err
	}
	name, err := identity.NormalizeDisplayName(displayName, true)
	if err != nil {
		return uuid.Nil, ReturnContext{}, err
	}
	digest, ok := identity.TokenDigest(token)
	if !ok {
		return uuid.Nil, ReturnContext{}, ErrTokenInvalid
	}
	// Cheap read before the expensive hash: junk and stale tokens cost nothing.
	tok, err := s.db.AccountToken.Query().Where(accounttoken.TokenHash(digest), accounttoken.PurposeEQ(accounttoken.PurposeRegistration)).Only(ctx)
	if ent.IsNotFound(err) {
		return uuid.Nil, ReturnContext{}, ErrTokenInvalid
	}
	if err != nil {
		return uuid.Nil, ReturnContext{}, mapStoreError(err)
	}
	now := s.now()
	if tok.ConsumedAt != nil || !tok.ExpiresAt.After(now) {
		return uuid.Nil, ReturnContext{}, ErrTokenInvalid
	}
	if taken, err := s.db.User.Query().Where(user.EmailCanonical(tok.EmailCanonical)).Exist(ctx); err != nil {
		return uuid.Nil, ReturnContext{}, mapStoreError(err)
	} else if taken {
		return uuid.Nil, ReturnContext{}, ErrTokenInvalid
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return uuid.Nil, ReturnContext{}, mapStoreError(err)
	}

	id := uuid.New()
	var ret ReturnContext
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		now := s.now()
		rows, err := tx.AccountToken.Query().
			Where(accounttoken.EmailCanonical(tok.EmailCanonical), accounttoken.PurposeEQ(accounttoken.PurposeRegistration)).
			Order(ent.Asc(accounttoken.FieldID)).ForUpdate().All(ctx)
		if err != nil {
			return err
		}
		var own *ent.AccountToken
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
			if bytes.Equal(r.TokenHash, digest) {
				own = r
			}
		}
		if own == nil || own.ConsumedAt != nil || !own.ExpiresAt.After(now) {
			return ErrTokenInvalid
		}
		if taken, err := tx.User.Query().Where(user.EmailCanonical(own.EmailCanonical)).Exist(ctx); err != nil {
			return err
		} else if taken {
			return ErrTokenInvalid
		}
		ret = returnOf(own)
		if err = tx.User.Create().SetID(id).SetEmailCanonical(own.EmailCanonical).SetEmailVerifiedAt(now).
			SetPasswordHash(hash).SetDisplayName(name).Exec(ctx); err != nil {
			return err
		}
		if _, err = tx.AccountToken.Update().Where(accounttoken.IDIn(ids...), accounttoken.ConsumedAtIsNil()).
			SetConsumedAt(now).Save(ctx); err != nil {
			return err
		}
		if err = s.cancelPendingJobs(ctx, tx, ids, "challenge_consumed"); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &id, actor: userActor(id), action: "account.create", requestID: requestID, result: "ok"})
	})
	if err != nil {
		if isUniqueViolation(err, "users_email_canonical_key") {
			return uuid.Nil, ReturnContext{}, ErrTokenInvalid
		}
		return uuid.Nil, ReturnContext{}, mapStoreError(err)
	}
	return id, ret, nil
}

// RequestPasswordReset is RequestRegistration for the reset flow: nil for every
// well-formed address, a challenge and mail only for an active account, bound
// to the account and the auth_version it has at issue time. Asking for a reset
// changes nothing about the account, its password or its sessions.
func (s *Service) RequestPasswordReset(ctx context.Context, email string, ret ReturnContext, clientIP, requestID string) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	canonical, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err = s.checkReturn(ret); err != nil {
		return err
	}
	const action = "password_reset.request"
	proceed, err := s.admit(ctx, PurposePasswordReset, canonical, clientIP, action, requestID)
	if err != nil || !proceed {
		return err
	}
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		// Locking the row fixes the auth_version the token is bound to.
		u, err := tx.User.Query().Where(user.EmailCanonical(canonical)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) || (err == nil && Status(u.Status) != StatusActive) {
			return s.audit(ctx, tx, auditEntry{actor: "system", action: action, requestID: requestID, result: "ignored"})
		}
		if err != nil {
			return err
		}
		version := u.AuthVersion
		if err = s.mail.Issue(ctx, tx, MailRequest{Purpose: PurposePasswordReset, Email: canonical,
			UserID: &u.ID, AuthVersion: &version, Return: ret}); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &u.ID, actor: "system", action: action, requestID: requestID, result: "issued"})
	})
	return mapStoreError(err)
}

// ResetPassword consumes a reset token and sets a new password. The target is
// the account the token was issued for; the request cannot name one. In one
// transaction it locks the account, then the token, and requires the token to
// be unexpired and unconsumed and the account to be active at the auth_version
// the token was issued at. It then writes the new hash, bumps auth_version
// (which ends every browser session and management token at its next check),
// and consumes every reset token of the account, cancelling their unsent mails.
// A second submission of the same token, or of a sibling token, finds the
// version moved on and changes nothing. It does not sign anybody in.
func (s *Service) ResetPassword(ctx context.Context, token, newPassword, requestID string) (ReturnContext, error) {
	if err := identity.ValidatePassword("new_password", newPassword); err != nil {
		return ReturnContext{}, err
	}
	digest, ok := identity.TokenDigest(token)
	if !ok {
		return ReturnContext{}, ErrTokenInvalid
	}
	// Unlocked read: cheap rejection before the hash, and the account to lock first.
	tok, err := s.db.AccountToken.Query().Where(accounttoken.TokenHash(digest), accounttoken.PurposeEQ(accounttoken.PurposePasswordReset)).Only(ctx)
	if ent.IsNotFound(err) {
		return ReturnContext{}, ErrTokenInvalid
	}
	if err != nil {
		return ReturnContext{}, mapStoreError(err)
	}
	if tok.UserID == nil || tok.IssuedAuthVersion == nil || tok.ConsumedAt != nil || !tok.ExpiresAt.After(s.now()) {
		return ReturnContext{}, ErrTokenInvalid
	}
	userID, issued := *tok.UserID, *tok.IssuedAuthVersion
	if u, err := s.db.User.Get(ctx, userID); err != nil {
		if ent.IsNotFound(err) {
			return ReturnContext{}, ErrTokenInvalid
		}
		return ReturnContext{}, mapStoreError(err)
	} else if Status(u.Status) != StatusActive || u.AuthVersion != issued {
		return ReturnContext{}, ErrTokenInvalid
	}
	hash, err := s.hasher.Hash(ctx, newPassword)
	if err != nil {
		return ReturnContext{}, mapStoreError(err)
	}

	var ret ReturnContext
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		now := s.now()
		// Lock order is account, then tokens, like the deletion and admin paths.
		u, err := tx.User.Query().Where(user.ID(userID)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		t, err := tx.AccountToken.Query().Where(accounttoken.TokenHash(digest), accounttoken.PurposeEQ(accounttoken.PurposePasswordReset)).
			ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		if t.ConsumedAt != nil || !t.ExpiresAt.After(now) || Status(u.Status) != StatusActive || u.AuthVersion != issued {
			return ErrTokenInvalid
		}
		ret = returnOf(t)
		if _, err = tx.User.UpdateOneID(userID).Where(user.AuthVersion(issued), user.StatusEQ(user.StatusActive)).
			SetPasswordHash(hash).AddAuthVersion(1).Save(ctx); err != nil {
			if ent.IsNotFound(err) {
				return ErrTokenInvalid
			}
			return err
		}
		ids, err := tx.AccountToken.Query().
			Where(accounttoken.UserID(userID), accounttoken.PurposeEQ(accounttoken.PurposePasswordReset), accounttoken.ConsumedAtIsNil()).IDs(ctx)
		if err != nil {
			return err
		}
		if _, err = tx.AccountToken.Update().Where(accounttoken.IDIn(ids...)).SetConsumedAt(now).Save(ctx); err != nil {
			return err
		}
		if err = s.cancelPendingJobs(ctx, tx, ids, "challenge_consumed"); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &userID, actor: userActor(userID), action: "password.reset", requestID: requestID, result: "ok"})
	})
	if err != nil {
		if errors.Is(err, ErrTokenInvalid) {
			slog.Debug("usersrv password reset refused", "user_id", userID)
		}
		return ReturnContext{}, mapStoreError(err)
	}
	return ret, nil
}
