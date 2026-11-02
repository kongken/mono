package account

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// RequestExternalRegistrationEmail is only for an existing ready provider
// proof, never a replacement for missing/unverified provider email. Editing a
// valid contact address revokes the previous email token and trusted exemption.
func (s *Service) RequestExternalRegistrationEmail(ctx context.Context, ref ChallengeRef, email, ip, requestID string) error {
	email, err := identity.NormalizeEmail(email)
	if err != nil {
		return err
	}
	if err = s.mailReady(); err != nil {
		return err
	}
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	c, err := s.db.AuthChallenge.Get(ctx, ref.ID)
	if ent.IsNotFound(err) {
		return ErrChallengeInvalid
	}
	if err != nil {
		return externalError(err)
	}
	if err = s.usable(c, ref, authchallenge.KindExternalRegistration); err != nil {
		return err
	}
	if _, err = s.policy(externalprovider.ProviderName(*c.Provider), *c.ProviderConfigVersion); err != nil {
		return err
	}
	if s.registrationClosed.Load() {
		return ErrRegistrationUnavailable
	}
	proceed, err := s.admit(ctx, PurposeRegistration, email, ip, "external.registration.email", requestID)
	if err != nil || !proceed {
		return err
	}
	return externalError(s.inTx(ctx, func(tx *ent.Tx) error {
		rows, err := lockChallenges(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		current := rows[c.ID]
		if err = s.usable(current, ref, authchallenge.KindExternalRegistration); err != nil {
			return err
		}
		p, err := s.readProof(current)
		if err != nil {
			return err
		}
		p.ContactEmail, p.EmailTrusted = email, false
		if err = s.writeProof(ctx, tx, current, p); err != nil {
			return err
		}
		return s.queueChallenge(ctx, tx, current, email, "external_registration")
	}))
}
func (s *Service) queueChallenge(ctx context.Context, tx *ent.Tx, c *ent.AuthChallenge, email, kind string) error {
	if s.securityMail == nil {
		return ErrUnavailable
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ErrUnavailable
	}
	defer clear(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest, _ := identity.TokenDigest(token)
	expires := s.now().Add(ConfirmationTTL)
	if c.ExpiresAt.Before(expires) {
		expires = c.ExpiresAt
	}
	if err := tx.AuthChallenge.UpdateOneID(c.ID).SetEmailTokenHash(digest).SetEmailExpiresAt(expires).ClearConfirmedAt().Exec(ctx); err != nil {
		return err
	}
	// Cancel pending superseded emails in the same transaction. A mail already
	// in flight carries a dead digest and cannot confirm the new request.
	if err := s.cancelPendingJobs(ctx, tx, []uuid.UUID{c.ID}, "email_reissued"); err != nil {
		return err
	}
	return s.securityMail.QueueChallenge(ctx, tx, ChallengeMail{ID: c.ID, Email: email, Token: token, Kind: kind, ExpiresAt: expires})
}

// ConfirmExternalRegistrationEmail is an explicit POST capability. It can run
// on another device and marks email proof only; no user or session is created.
func (s *Service) ConfirmExternalRegistrationEmail(ctx context.Context, challengeID uuid.UUID, token string) error {
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	return externalError(s.inTx(ctx, func(tx *ent.Tx) error {
		c, err := tx.AuthChallenge.Query().Where(authchallenge.ID(challengeID)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrChallengeInvalid
		}
		if err != nil {
			return err
		}
		if c.Kind != authchallenge.KindExternalRegistration || c.Provider == nil || c.ProviderConfigVersion == nil {
			return ErrChallengeInvalid
		}
		if _, err = s.policy(externalprovider.ProviderName(*c.Provider), *c.ProviderConfigVersion); err != nil {
			return err
		}
		if s.registrationClosed.Load() {
			return ErrRegistrationUnavailable
		}
		return s.confirmMail(ctx, tx, c, token)
	}))
}
func (s *Service) confirmMail(ctx context.Context, tx *ent.Tx, c *ent.AuthChallenge, token string) error {
	digest, ok := identity.TokenDigest(token)
	if !ok || !bytes.Equal(digest, c.EmailTokenHash) || c.Status != authchallenge.StatusReady || c.ConsumedAt != nil || c.ConfirmedAt != nil || !c.ExpiresAt.After(s.now()) || c.EmailExpiresAt == nil || !c.EmailExpiresAt.After(s.now()) {
		return ErrChallengeInvalid
	}
	if err := tx.AuthChallenge.UpdateOneID(c.ID).SetConfirmedAt(s.now()).ClearEmailTokenHash().Exec(ctx); err != nil {
		return err
	}
	return s.cancelPendingJobs(ctx, tx, []uuid.UUID{c.ID}, "email_confirmed")
}

// GetExternalRegistration is a read bound to the initiating browser. It never
// confirms/consumes a token and does not look up candidate-email ownership.
func (s *Service) GetExternalRegistration(ctx context.Context, ref ChallengeRef) (ExternalResult, error) {
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	c, err := s.db.AuthChallenge.Get(ctx, ref.ID)
	if ent.IsNotFound(err) {
		return ExternalResult{}, ErrChallengeInvalid
	}
	if err != nil {
		return ExternalResult{}, externalError(err)
	}
	if err = s.usable(c, ref, authchallenge.KindExternalRegistration); err != nil {
		return ExternalResult{}, err
	}
	p, err := s.readProof(c)
	if err != nil {
		return ExternalResult{}, err
	}
	return ExternalResult{Status: "registration_pending", ChallengeID: c.ID, CandidateEmail: p.ContactEmail, DisplayName: p.Identity.DisplayName, AvatarURL: p.Identity.AvatarURL, EmailConfirmed: c.ConfirmedAt != nil}, nil
}
