package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	"github.com/kongken/mono/internal/usersrv/identity"
)

type BeginExternalInput struct {
	FlowBinding
	Intent        string
	Provider      externalprovider.ProviderName
	RedirectURI   string
	Authorization externalprovider.AuthorizationRequest
	Return        ReturnContext
}
type BegunExternal struct {
	ChallengeID      uuid.UUID
	AuthorizationURL string
	ConfigVersion    int64
}

func (BegunExternal) String() string         { return "[external authorization redacted]" }
func (r BegunExternal) GoString() string     { return r.String() }
func (r BegunExternal) LogValue() slog.Value { return slog.StringValue(r.String()) }

type CompleteExternalInput struct {
	ChallengeRef
	Exchange  externalprovider.CodeExchangeRequest
	RequestID string
}

// Pending results grant no session authority. Only authenticated results contain
// an Account. Browser/session creation remains entirely BFF-owned.
type ExternalResult struct {
	Status                                 string
	ChallengeID                            uuid.UUID
	CandidateEmail, DisplayName, AvatarURL string
	EmailConfirmed                         bool
	Account                                *Account
}

func (ExternalResult) String() string         { return "[external result redacted]" }
func (r ExternalResult) GoString() string     { return r.String() }
func (r ExternalResult) LogValue() slog.Value { return slog.StringValue(r.String()) }

func (s *Service) BeginExternalLogin(ctx context.Context, in BeginExternalInput) (BegunExternal, error) {
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	var out BegunExternal
	if !validBinding(in.FlowBinding, true) || in.Return.Validate() != nil || (in.Intent != "login" && in.Intent != "link") || (in.Intent == "link") != (in.UserID != uuid.Nil) {
		return out, ErrChallengeInvalid
	}
	a, err := s.policy(in.Provider, 0)
	if err != nil {
		return out, err
	}
	p := s.external.Policies[in.Provider]
	if in.RedirectURI != p.RedirectURI {
		return out, ErrChallengeInvalid
	}
	u, err := a.AuthorizationURL(ctx, in.Authorization)
	if err != nil {
		return out, err
	}
	id := uuid.New()
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		kind := authchallenge.KindExternalLogin
		if in.UserID != uuid.Nil {
			if u, err := lockedUser(ctx, tx, in.UserID); err != nil {
				return err
			} else if u.AuthVersion != in.AuthVersion {
				return ErrConflict
			}
			kind = authchallenge.KindIdentityLink
		}
		c := tx.AuthChallenge.Create().SetID(id).SetKind(kind).SetProvider(authchallenge.Provider(in.Provider)).SetIssuer(issuerOf(in.Provider)).SetProviderConfigVersion(p.ConfigVersion).
			SetBffFlowID(in.FlowID).SetContextHash(in.ContextHash).SetReturnAppID(in.Return.AppID).SetReturnPath(in.Return.Path).SetExpiresAt(s.now().Add(ExternalFlowTTL)).SetCreatedAt(s.now())
		if in.UserID != uuid.Nil {
			c.SetUserID(in.UserID).SetAuthVersion(in.AuthVersion).SetSessionBindingHash(in.SessionHash)
		}
		return c.Exec(ctx)
	})
	if err != nil {
		return out, externalError(err)
	}
	return BegunExternal{ChallengeID: id, AuthorizationURL: u, ConfigVersion: p.ConfigVersion}, nil
}
func issuerOf(p externalprovider.ProviderName) string {
	if p == externalprovider.GoogleName {
		return externalprovider.GoogleIssuer
	}
	if p == externalprovider.GitHubName {
		return externalprovider.GitHubIssuer
	}
	return ""
}
func (s *Service) failExternal(ctx context.Context, id uuid.UUID, provider externalprovider.ProviderName, requestID string) {
	// A failed or unknown exchange outcome can never be reclaimed on another
	// replica. Only a new browser flow may exchange a fresh provider grant.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = s.inTx(ctx, func(tx *ent.Tx) error {
		_, err := tx.AuthChallenge.Update().Where(authchallenge.ID(id), authchallenge.StatusEQ(authchallenge.StatusExchanging)).SetStatus(authchallenge.StatusFailed).
			SetConsumedAt(s.now()).ClearProofCiphertext().ClearProofKeyID().ClearEmailTokenHash().Save(ctx)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{actor: "system", action: "external.login.reject", result: "rejected", reason: string(provider), requestID: requestID})
	})
}
func (s *Service) CompleteExternalLogin(ctx context.Context, in CompleteExternalInput) (ExternalResult, error) {
	var out ExternalResult
	s.admissionMu.RLock()
	var c *ent.AuthChallenge
	var adapter externalprovider.ExternalProvider
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		var err error
		c, err = tx.AuthChallenge.Query().Where(authchallenge.ID(in.ID)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrChallengeInvalid
		}
		if err != nil {
			return err
		}
		if !matches(c, in.ChallengeRef) || c.BffFlowID == nil || c.Status != authchallenge.StatusIssued || c.ConsumedAt != nil || !c.ExpiresAt.After(s.now()) || c.Provider == nil || c.ProviderConfigVersion == nil {
			return ErrChallengeInvalid
		}
		adapter, err = s.policy(externalprovider.ProviderName(*c.Provider), *c.ProviderConfigVersion)
		if err != nil {
			return err
		}
		if in.Exchange.Provider != externalprovider.ProviderName(*c.Provider) || in.Exchange.ConfigVersion != *c.ProviderConfigVersion {
			return ErrChallengeInvalid
		}
		return tx.AuthChallenge.UpdateOneID(c.ID).SetStatus(authchallenge.StatusExchanging).Exec(ctx)
	})
	s.admissionMu.RUnlock()
	if err != nil {
		return out, externalError(err)
	}
	i, err := adapter.ExchangeCode(ctx, in.Exchange)
	if err != nil {
		s.failExternal(ctx, c.ID, in.Exchange.Provider, in.RequestID)
		return out, err
	}
	// Do not hold any database lock or the configuration admission lock while
	// doing provider I/O. Re-admit after verification with current gates.
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		if _, err := s.policy(i.Provider, *c.ProviderConfigVersion); err != nil {
			return err
		}
		if i.Provider != in.Exchange.Provider || i.Issuer != issuerOf(i.Provider) || i.Subject == "" || !i.EmailVerified {
			return ErrProviderRejected
		}
		if err := lockExternal(ctx, tx, i); err != nil {
			return err
		}
		bound, err := findIdentity(ctx, tx, i)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		var u *ent.User
		if in.UserID != uuid.Nil {
			u, err = lockedUser(ctx, tx, in.UserID)
			if err != nil {
				return err
			}
			if u.AuthVersion != in.AuthVersion {
				return ErrConflict
			}
		} else if bound != nil {
			u, err = lockedUser(ctx, tx, bound.UserID)
			if err != nil {
				return ErrInvalidCredentials
			}
		}
		if bound != nil {
			bound, err = tx.ExternalIdentity.Query().Where(externalidentity.ID(bound.ID)).ForUpdate().Only(ctx)
			if err != nil {
				return err
			}
		}
		rows, err := lockChallenges(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		current := rows[c.ID]
		if !matches(current, in.ChallengeRef) || current.Status != authchallenge.StatusExchanging || current.ConsumedAt != nil || !current.ExpiresAt.After(s.now()) {
			return ErrChallengeInvalid
		}
		if in.UserID == uuid.Nil && bound != nil {
			if _, err = tx.ExternalIdentity.UpdateOneID(bound.ID).SetEmailSnapshot(i.Email).SetEmailVerifiedAt(s.now()).SetDisplayNameSnapshot(i.DisplayName).SetAvatarURLSnapshot(i.AvatarURL).SetLastAuthenticatedAt(s.now()).Save(ctx); err != nil {
				return err
			}
			if err = s.consume(ctx, tx, c.ID); err != nil {
				return err
			}
			if err = s.audit(ctx, tx, auditEntry{userID: &u.ID, actor: userActor(u.ID), action: "external.login", result: "ok", reason: string(i.Provider), requestID: in.RequestID}); err != nil {
				return err
			}
			account := Account{ID: u.ID, Email: u.EmailCanonical, AuthState: stateOf(u)}
			out = ExternalResult{Status: "authenticated", ChallengeID: c.ID, Account: &account}
			return nil
		}
		kind, status := authchallenge.KindIdentityLink, "link_pending"
		if in.UserID == uuid.Nil {
			if s.registrationClosed.Load() {
				return ErrRegistrationUnavailable
			}
			kind, status = authchallenge.KindExternalRegistration, "registration_pending"
		}
		proof := verifiedProof{Identity: i, ContactEmail: i.Email, EmailTrusted: i.EmailTrust == externalprovider.GoogleAuthoritative}
		if err = s.writeProof(ctx, tx, current, proof); err != nil {
			return err
		}
		update := tx.AuthChallenge.UpdateOneID(c.ID).SetKind(kind).SetStatus(authchallenge.StatusReady)
		if kind == authchallenge.KindExternalRegistration && proof.EmailTrusted {
			update.SetConfirmedAt(s.now())
		}
		if err = update.Exec(ctx); err != nil {
			return err
		}
		out = ExternalResult{Status: status, ChallengeID: c.ID, CandidateEmail: i.Email, DisplayName: i.DisplayName, AvatarURL: i.AvatarURL, EmailConfirmed: proof.EmailTrusted}
		return nil
	})
	if err != nil {
		s.failExternal(ctx, c.ID, i.Provider, in.RequestID)
		return ExternalResult{}, externalError(err)
	}
	return out, nil
}

type RegistrationInput struct {
	ChallengeRef
	MutationID, DisplayName, AvatarURL string
}

func registrationHash(in RegistrationInput) []byte {
	raw, _ := json.Marshal(struct {
		Challenge, Flow uuid.UUID
		Context         []byte
		Name, Avatar    string
	}{in.ID, in.FlowID, in.ContextHash, in.DisplayName, in.AvatarURL})
	sum := sha256.Sum256(raw)
	return sum[:]
}
func (s *Service) CompleteExternalRegistration(ctx context.Context, in RegistrationInput) (uuid.UUID, error) {
	if _, err := identity.NormalizeMutationID(in.MutationID); err != nil {
		return uuid.Nil, err
	}
	name, err := identity.NormalizeDisplayName(in.DisplayName, true)
	if err != nil {
		return uuid.Nil, err
	}
	avatar, err := identity.NormalizeAvatarURL(in.AvatarURL)
	if err != nil {
		return uuid.Nil, err
	}
	in.DisplayName, in.AvatarURL = name, avatar
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	c, err := s.db.AuthChallenge.Get(ctx, in.ID)
	if ent.IsNotFound(err) {
		return uuid.Nil, ErrChallengeInvalid
	}
	if err != nil {
		return uuid.Nil, externalError(err)
	}
	if !matches(c, in.ChallengeRef) || c.Kind != authchallenge.KindExternalRegistration || c.UserID != nil {
		return uuid.Nil, ErrChallengeInvalid
	}
	hash := registrationHash(in)
	// A completed result has no proof and is only a receipt, never session or
	// login authority. Match its original browser binding and request exactly.
	if c.Status == authchallenge.StatusCompleted {
		return s.registrationReplay(ctx, s.db, c, in, hash)
	}
	p, err := s.readProof(c)
	if err != nil {
		return uuid.Nil, err
	}
	lockedKey := externalKey(p.Identity)
	var id uuid.UUID
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		if err := lockExternal(ctx, tx, p.Identity); err != nil {
			return err
		}
		// A second submission waits on the same external key before looking at
		// the completed challenge; it never creates a second transient account.
		current, err := tx.AuthChallenge.Get(ctx, c.ID)
		if err != nil {
			return err
		}
		if current.Status == authchallenge.StatusCompleted {
			id, err = s.registrationReplay(ctx, tx.Client(), current, in, hash)
			return err
		}
		if err = s.usable(current, in.ChallengeRef, authchallenge.KindExternalRegistration); err != nil {
			return err
		}
		p, err = s.readProof(current)
		if err != nil {
			return err
		}
		if externalKey(p.Identity) != lockedKey {
			return ErrChallengeInvalid
		}
		if _, err = s.policy(p.Identity.Provider, *current.ProviderConfigVersion); err != nil {
			return err
		}
		if s.registrationClosed.Load() {
			return ErrRegistrationUnavailable
		}
		if current.ConfirmedAt == nil || (!p.EmailTrusted && (current.EmailExpiresAt == nil || !current.EmailExpiresAt.After(s.now()))) {
			return ErrChallengeInvalid
		}
		if bound, err := findIdentity(ctx, tx, p.Identity); err == nil && bound != nil {
			return ErrRegistrationUnavailable
		} else if err != nil && !ent.IsNotFound(err) {
			return err
		}
		id = uuid.New()
		u, err := tx.User.Create().SetID(id).SetEmailCanonical(p.ContactEmail).SetEmailVerifiedAt(*current.ConfirmedAt).SetDisplayName(name).SetAvatarURL(avatar).Save(ctx)
		if err != nil {
			return err
		}
		if _, err = s.createIdentity(ctx, tx, id, p.Identity); err != nil {
			return err
		}
		// Final lock order: external key -> inserted user -> identity -> challenge.
		// A concurrent mail edit/confirmation may run before this lock; if it
		// changed our proof, roll back the entire provisional account.
		rows, err := lockChallenges(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		locked := rows[c.ID]
		if err = s.usable(locked, in.ChallengeRef, authchallenge.KindExternalRegistration); err != nil {
			return err
		}
		if !bytes.Equal(locked.ProofCiphertext, current.ProofCiphertext) || locked.ConfirmedAt == nil ||
			(!p.EmailTrusted && (locked.EmailExpiresAt == nil || !locked.EmailExpiresAt.After(s.now()))) {
			return ErrChallengeInvalid
		}
		if err = s.consume(ctx, tx, c.ID); err != nil {
			return err
		}
		if err = tx.AuthChallenge.UpdateOneID(c.ID).SetResultMutationID(in.MutationID).SetResultRequestHash(hash).SetResultUserID(id).SetResultExpiresAt(s.now().Add(OperationReceiptTTL)).Exec(ctx); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &u.ID, actor: userActor(u.ID), action: "account.create", result: "ok"})
	})
	if isUniqueViolation(err, "") {
		return uuid.Nil, ErrRegistrationUnavailable
	}
	if err != nil {
		return uuid.Nil, externalError(err)
	}
	return id, nil
}
func (s *Service) registrationReplay(ctx context.Context, db *ent.Client, c *ent.AuthChallenge, in RegistrationInput, hash []byte) (uuid.UUID, error) {
	if !matches(c, in.ChallengeRef) || c.ResultExpiresAt == nil || !c.ResultExpiresAt.After(s.now()) || c.ResultUserID == nil || c.ResultMutationID == nil {
		return uuid.Nil, ErrChallengeInvalid
	}
	if *c.ResultMutationID != in.MutationID || !bytes.Equal(c.ResultRequestHash, hash) {
		return uuid.Nil, ErrMutationReplayMismatch
	}
	exists, err := db.User.Query().Where(user.ID(*c.ResultUserID)).Exist(ctx)
	if err != nil {
		return uuid.Nil, ErrUnavailable
	}
	if !exists {
		return uuid.Nil, ErrChallengeInvalid
	}
	return *c.ResultUserID, nil
}
