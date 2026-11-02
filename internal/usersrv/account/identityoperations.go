package account

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/authoperationreceipt"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	"github.com/kongken/mono/internal/usersrv/externalprovider"
	"github.com/kongken/mono/internal/usersrv/identity"
)

type BeginOperationInput struct {
	FlowBinding
	Kind                          string
	TargetID                      uuid.UUID
	Provider                      externalprovider.ProviderName
	Password, ClientIP, RequestID string
}
type OperationState struct {
	ID           uuid.UUID
	Kind, Status string
	Confirmed    bool
}
type IdentityMutation struct {
	Operation     ChallengeRef
	ProviderProof ChallengeRef
	TargetID      uuid.UUID
	MutationID    string
	RequestID     string
}
type MutationResult struct {
	IdentityID  uuid.UUID
	AuthVersion int64
	Replayed    bool
	Result      string
}
type IdentitySummary struct {
	ID                            uuid.UUID
	Provider                      string
	LinkedAt, LastAuthenticatedAt time.Time
}

func (s *Service) BeginIdentityOperation(ctx context.Context, in BeginOperationInput) (uuid.UUID, error) {
	if !validBinding(in.FlowBinding, false) || in.UserID == uuid.Nil || (in.Kind != "identity_link" && in.Kind != "identity_unlink" && in.Kind != "account_deletion") {
		return uuid.Nil, ErrChallengeInvalid
	}
	if (in.Kind == "identity_unlink") != (in.TargetID != uuid.Nil) {
		return uuid.Nil, ErrChallengeInvalid
	}
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	if in.Kind == "identity_link" {
		if _, err := s.policy(in.Provider, 0); err != nil {
			return uuid.Nil, err
		}
	}
	u, err := s.activeUser(ctx, in.UserID)
	if err != nil {
		return uuid.Nil, err
	}
	if u.AuthVersion != in.AuthVersion {
		return uuid.Nil, ErrConflict
	}
	passwordVerified := false
	if u.PasswordHash != nil {
		if err = identity.CheckPasswordInput("current_password", in.Password); err != nil {
			return uuid.Nil, err
		}
		ok, err := s.verifyStoredPassword(ctx, u.PasswordHash, in.Password)
		if err != nil {
			return uuid.Nil, mapStoreError(err)
		}
		if !ok {
			return uuid.Nil, ErrInvalidCredentials
		}
		passwordVerified = true
	} else {
		if err = s.mailReady(); err != nil {
			return uuid.Nil, err
		}
		proceed, err := s.admit(ctx, PurposePasswordReset, u.EmailCanonical, in.ClientIP, "identity.operation.email", in.RequestID)
		if err != nil {
			return uuid.Nil, err
		}
		if !proceed {
			return uuid.Nil, ErrRateLimited
		}
	}
	id := uuid.New()
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		locked, err := lockedUser(ctx, tx, u.ID)
		if err != nil {
			return err
		}
		if locked.AuthVersion != in.AuthVersion {
			return ErrConflict
		}
		if in.Kind == "account_deletion" && hasRole(locked, "ozid-admin") {
			return ErrPlatformAdmin
		}
		if hasRole(locked, "ozid-admin") && !passwordVerified {
			return ErrInvalidCredentials
		}
		if in.Kind == "identity_unlink" {
			exists, err := tx.ExternalIdentity.Query().Where(externalidentity.ID(in.TargetID), externalidentity.UserID(u.ID)).Exist(ctx)
			if err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		if !passwordVerified {
			q := tx.AuthChallenge.Query().Where(authchallenge.UserID(u.ID), authchallenge.KindEQ(authchallenge.Kind(in.Kind)), authchallenge.ContextHash(in.ContextHash), authchallenge.SessionBindingHash(in.SessionHash), authchallenge.AuthVersion(in.AuthVersion), authchallenge.StatusEQ(authchallenge.StatusReady), authchallenge.ConsumedAtIsNil(), authchallenge.ExpiresAtGT(s.now()))
			if in.TargetID != uuid.Nil {
				q.Where(authchallenge.TargetIdentityID(in.TargetID))
			} else {
				q.Where(authchallenge.TargetIdentityIDIsNil())
			}
			if in.Kind == "identity_link" {
				q.Where(authchallenge.ProviderEQ(authchallenge.Provider(in.Provider)))
			} else {
				q.Where(authchallenge.ProviderIsNil())
			}
			old, err := q.Order(ent.Desc(authchallenge.FieldCreatedAt)).ForUpdate().First(ctx)
			if err != nil && !ent.IsNotFound(err) {
				return err
			}
			if old != nil {
				if old.ProviderConfigVersion != nil && *old.ProviderConfigVersion != s.external.Policies[in.Provider].ConfigVersion {
					return ErrChallengeInvalid
				}
				id = old.ID
				return s.queueChallenge(ctx, tx, old, u.EmailCanonical, "identity_operation")
			}
		}
		c := tx.AuthChallenge.Create().SetID(id).SetKind(authchallenge.Kind(in.Kind)).SetUserID(u.ID).SetAuthVersion(in.AuthVersion).SetContextHash(in.ContextHash).SetSessionBindingHash(in.SessionHash).
			SetStatus(authchallenge.StatusReady).SetExpiresAt(s.now().Add(ExternalFlowTTL)).SetCreatedAt(s.now())
		if in.TargetID != uuid.Nil {
			c.SetTargetIdentityID(in.TargetID)
		}
		if in.Kind == "identity_link" {
			p := s.external.Policies[in.Provider]
			c.SetProvider(authchallenge.Provider(in.Provider)).SetIssuer(issuerOf(in.Provider)).SetProviderConfigVersion(p.ConfigVersion)
		}
		if passwordVerified {
			c.SetConfirmedAt(s.now())
		}
		row, err := c.Save(ctx)
		if err != nil {
			return err
		}
		if !passwordVerified {
			return s.queueChallenge(ctx, tx, row, u.EmailCanonical, "identity_operation")
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, externalError(err)
	}
	return id, nil
}
func hasRole(u *ent.User, role string) bool {
	for _, r := range u.Edges.Roles {
		if r.Role == role {
			return true
		}
	}
	return false
}

func (s *Service) ConfirmIdentityOperation(ctx context.Context, id uuid.UUID, token string) error {
	// The email bearer can confirm on another device. It cannot choose an
	// account or change a target/session, and does not authorize final submission.
	preview, err := s.db.AuthChallenge.Get(ctx, id)
	if ent.IsNotFound(err) {
		return ErrChallengeInvalid
	}
	if err != nil {
		return externalError(err)
	}
	if preview.UserID == nil {
		return ErrChallengeInvalid
	}
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	return externalError(s.inTx(ctx, func(tx *ent.Tx) error {
		u, err := lockedUser(ctx, tx, *preview.UserID)
		if err != nil {
			return ErrChallengeInvalid
		}
		rows, err := lockChallenges(ctx, tx, id)
		if err != nil {
			return err
		}
		c := rows[id]
		if c.AuthVersion == nil || u.AuthVersion != *c.AuthVersion || (c.Kind != authchallenge.KindIdentityLink && c.Kind != authchallenge.KindIdentityUnlink && c.Kind != authchallenge.KindAccountDeletion) {
			return ErrChallengeInvalid
		}
		if hasRole(u, "ozid-admin") {
			return ErrInvalidCredentials
		}
		if c.Kind == authchallenge.KindIdentityLink {
			if _, err = s.policy(externalprovider.ProviderName(*c.Provider), *c.ProviderConfigVersion); err != nil {
				return err
			}
		}
		return s.confirmMail(ctx, tx, c, token)
	}))
}
func (s *Service) GetIdentityOperation(ctx context.Context, ref ChallengeRef) (OperationState, error) {
	c, err := s.db.AuthChallenge.Get(ctx, ref.ID)
	if ent.IsNotFound(err) {
		return OperationState{}, ErrChallengeInvalid
	}
	if err != nil {
		return OperationState{}, externalError(err)
	}
	if !matches(c, ref) || c.UserID == nil || (c.Kind != authchallenge.KindIdentityLink && c.Kind != authchallenge.KindIdentityUnlink && c.Kind != authchallenge.KindAccountDeletion) {
		return OperationState{}, ErrChallengeInvalid
	}
	// Completed queries are a bound result view only; they do not re-authorize
	// an old session. Keep the view for the same 24h as mutation receipts.
	if c.Status == authchallenge.StatusCompleted {
		if c.ConsumedAt == nil || !c.ConsumedAt.Add(OperationReceiptTTL).After(s.now()) {
			return OperationState{}, ErrChallengeInvalid
		}
	} else {
		u, err := s.db.User.Get(ctx, ref.UserID)
		if err != nil || u.AuthVersion != ref.AuthVersion || !c.ExpiresAt.After(s.now()) {
			return OperationState{}, ErrChallengeInvalid
		}
	}
	return OperationState{ID: c.ID, Kind: string(c.Kind), Status: string(c.Status), Confirmed: c.ConfirmedAt != nil}, nil
}
func mutationHash(in IdentityMutation, kind string) []byte {
	raw, _ := json.Marshal(struct {
		Operation, Proof ChallengeRef
		Target           uuid.UUID
		Kind             string
	}{in.Operation, in.ProviderProof, in.TargetID, kind})
	hash := sha256.Sum256(raw)
	return hash[:]
}
func (s *Service) receipt(ctx context.Context, tx *ent.Tx, in IdentityMutation, kind string, hash []byte) (*ent.AuthOperationReceipt, error) {
	r, err := tx.AuthOperationReceipt.Query().Where(authoperationreceipt.UserID(in.Operation.UserID), authoperationreceipt.OperationEQ(authoperationreceipt.Operation(kind)), authoperationreceipt.MutationID(in.MutationID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !r.ExpiresAt.After(s.now()) {
		if err = tx.AuthOperationReceipt.DeleteOneID(r.ID).Exec(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if !bytes.Equal(r.RequestHash, hash) || !bytes.Equal(r.SessionBindingHash, in.Operation.SessionHash) {
		return nil, ErrMutationReplayMismatch
	}
	return r, nil
}
func receiptResult(r *ent.AuthOperationReceipt) MutationResult {
	result := MutationResult{AuthVersion: r.AuthVersion, Result: string(r.Result), Replayed: true}
	if r.IdentityID != nil {
		result.IdentityID = *r.IdentityID
	}
	return result
}
func (s *Service) finishMutation(ctx context.Context, tx *ent.Tx, u *ent.User, in IdentityMutation, kind string, hash []byte, identityID uuid.UUID, changed bool) (MutationResult, error) {
	result, version := "already_applied", u.AuthVersion
	if changed {
		result = "applied"
		version++
		if err := tx.User.UpdateOneID(u.ID).SetAuthVersion(version).Exec(ctx); err != nil {
			return MutationResult{}, err
		}
	}
	r, err := tx.AuthOperationReceipt.Create().SetUserID(u.ID).SetOperation(authoperationreceipt.Operation(kind)).SetMutationID(in.MutationID).SetRequestHash(hash).
		SetSessionBindingHash(in.Operation.SessionHash).SetResult(authoperationreceipt.Result(result)).SetAuthVersion(version).SetIdentityID(identityID).SetCreatedAt(s.now()).SetExpiresAt(s.now().Add(OperationReceiptTTL)).Save(ctx)
	if err != nil {
		return MutationResult{}, err
	}
	if err = s.consume(ctx, tx, in.Operation.ID); err != nil {
		return MutationResult{}, err
	}
	if kind == "identity_link" {
		if err = s.consume(ctx, tx, in.ProviderProof.ID); err != nil {
			return MutationResult{}, err
		}
	}
	if changed {
		if err = s.notify(ctx, tx, u, "operation_receipt", r.ID, kind); err != nil {
			return MutationResult{}, err
		}
	}
	action := "external.link"
	if kind == "identity_unlink" {
		action = "external.unlink"
	}
	if err = s.audit(ctx, tx, auditEntry{userID: &u.ID, actor: userActor(u.ID), action: action, result: result, mutationID: in.MutationID, requestID: in.RequestID}); err != nil {
		return MutationResult{}, err
	}
	return MutationResult{IdentityID: identityID, AuthVersion: version, Result: result}, nil
}
func validMutation(in IdentityMutation) error {
	if _, err := identity.NormalizeMutationID(in.MutationID); err != nil {
		return err
	}
	if !validBinding(in.Operation.FlowBinding, false) || in.Operation.UserID == uuid.Nil || in.Operation.ID == uuid.Nil {
		return ErrChallengeInvalid
	}
	return nil
}
func operationMatches(c *ent.AuthChallenge, in IdentityMutation, kind authchallenge.Kind, s *Service) error {
	if err := s.usable(c, in.Operation, kind); err != nil {
		return err
	}
	if c.ConfirmedAt == nil {
		return ErrChallengeInvalid
	}
	if c.EmailExpiresAt != nil && !c.EmailExpiresAt.After(s.now()) {
		return ErrChallengeInvalid
	}
	if kind == authchallenge.KindIdentityUnlink && (c.TargetIdentityID == nil || *c.TargetIdentityID != in.TargetID) {
		return ErrChallengeInvalid
	}
	return nil
}

func (s *Service) LinkExternalIdentity(ctx context.Context, in IdentityMutation) (MutationResult, error) {
	if err := validMutation(in); err != nil {
		return MutationResult{}, err
	}
	if in.TargetID != uuid.Nil || in.ProviderProof.UserID != in.Operation.UserID || in.ProviderProof.AuthVersion != in.Operation.AuthVersion || !bytes.Equal(in.ProviderProof.SessionHash, in.Operation.SessionHash) {
		return MutationResult{}, ErrChallengeInvalid
	}
	s.admissionMu.RLock()
	defer s.admissionMu.RUnlock()
	// Completed challenges erase the subject proof. A receipt can be read under
	// the user lock alone and cannot perform another mutation or acquire a key.
	hash := mutationHash(in, "identity_link")
	c, err := s.db.AuthChallenge.Get(ctx, in.ProviderProof.ID)
	if ent.IsNotFound(err) {
		return MutationResult{}, ErrChallengeInvalid
	}
	if err != nil {
		return MutationResult{}, externalError(err)
	}
	if c.Status == authchallenge.StatusCompleted {
		return s.replayMutation(ctx, in, "identity_link", hash)
	}
	proof, err := s.readProof(c)
	if err != nil {
		return MutationResult{}, err
	}
	var result MutationResult
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		if err := lockExternal(ctx, tx, proof.Identity); err != nil {
			return err
		}
		u, err := lockedUser(ctx, tx, in.Operation.UserID)
		if err != nil {
			return err
		}
		bound, err := findIdentity(ctx, tx, proof.Identity)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		if bound != nil {
			bound, err = tx.ExternalIdentity.Query().Where(externalidentity.ID(bound.ID)).ForUpdate().Only(ctx)
			if err != nil {
				return err
			}
		}
		rows, err := lockChallenges(ctx, tx, in.Operation.ID, in.ProviderProof.ID)
		if err != nil {
			return err
		}
		r, err := s.receipt(ctx, tx, in, "identity_link", hash)
		if err != nil {
			return err
		}
		if r != nil {
			result = receiptResult(r)
			return nil
		}
		if u.AuthVersion != in.Operation.AuthVersion {
			return ErrConflict
		}
		if err = operationMatches(rows[in.Operation.ID], in, authchallenge.KindIdentityLink, s); err != nil {
			return err
		}
		p := rows[in.ProviderProof.ID]
		if err = s.usable(p, in.ProviderProof, authchallenge.KindIdentityLink); err != nil {
			return err
		}
		verified, err := s.readProof(p)
		if err != nil || externalKey(verified.Identity) != externalKey(proof.Identity) {
			return ErrChallengeInvalid
		}
		op := rows[in.Operation.ID]
		if op.Provider == nil || p.Provider == nil || *op.Provider != *p.Provider || op.ProviderConfigVersion == nil || p.ProviderConfigVersion == nil || *op.ProviderConfigVersion != *p.ProviderConfigVersion {
			return ErrChallengeInvalid
		}
		if _, err = s.policy(proof.Identity.Provider, *p.ProviderConfigVersion); err != nil {
			return err
		}
		if bound != nil && bound.UserID != u.ID {
			return ErrIdentityConflict
		}
		changed := bound == nil
		if changed {
			exists, err := tx.ExternalIdentity.Query().Where(externalidentity.UserID(u.ID), externalidentity.ProviderEQ(externalidentity.Provider(proof.Identity.Provider))).Exist(ctx)
			if err != nil {
				return err
			}
			if exists {
				return ErrIdentityLimitReached
			}
			bound, err = s.createIdentity(ctx, tx, u.ID, proof.Identity)
			if err != nil {
				return err
			}
		}
		result, err = s.finishMutation(ctx, tx, u, in, "identity_link", hash, bound.ID, changed)
		return err
	})
	return result, externalError(err)
}
func (s *Service) replayMutation(ctx context.Context, in IdentityMutation, kind string, hash []byte) (MutationResult, error) {
	var result MutationResult
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		if _, err := lockedUser(ctx, tx, in.Operation.UserID); err != nil {
			return err
		}
		r, err := s.receipt(ctx, tx, in, kind, hash)
		if err != nil {
			return err
		}
		if r == nil {
			return ErrChallengeInvalid
		}
		result = receiptResult(r)
		return nil
	})
	return result, externalError(err)
}
func (s *Service) UnlinkExternalIdentity(ctx context.Context, in IdentityMutation) (MutationResult, error) {
	if err := validMutation(in); err != nil {
		return MutationResult{}, err
	}
	if in.TargetID == uuid.Nil || in.ProviderProof.ID != uuid.Nil {
		return MutationResult{}, ErrChallengeInvalid
	}
	hash := mutationHash(in, "identity_unlink")
	target, err := s.db.ExternalIdentity.Query().Where(externalidentity.ID(in.TargetID), externalidentity.UserID(in.Operation.UserID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return MutationResult{}, externalError(err)
	}
	var result MutationResult
	err = s.inTx(ctx, func(tx *ent.Tx) error {
		if target != nil {
			i := externalprovider.ProviderIdentity{Provider: externalprovider.ProviderName(target.Provider), Issuer: target.Issuer, Subject: target.Subject}
			if err := lockExternal(ctx, tx, i); err != nil {
				return err
			}
		}
		u, err := lockedUser(ctx, tx, in.Operation.UserID)
		if err != nil {
			return err
		}
		bound, err := tx.ExternalIdentity.Query().Where(externalidentity.ID(in.TargetID), externalidentity.UserID(u.ID)).ForUpdate().Only(ctx)
		if err != nil && !ent.IsNotFound(err) {
			return err
		}
		rows, err := lockChallenges(ctx, tx, in.Operation.ID)
		if err != nil {
			return err
		}
		r, err := s.receipt(ctx, tx, in, "identity_unlink", hash)
		if err != nil {
			return err
		}
		if r != nil {
			result = receiptResult(r)
			return nil
		}
		if bound == nil {
			return ErrNotFound
		}
		if u.AuthVersion != in.Operation.AuthVersion {
			return ErrConflict
		}
		if err = operationMatches(rows[in.Operation.ID], in, authchallenge.KindIdentityUnlink, s); err != nil {
			return err
		}
		if u.PasswordHash == nil {
			n, err := tx.ExternalIdentity.Query().Where(externalidentity.UserID(u.ID)).Count(ctx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastCredential
			}
		}
		if err = tx.ExternalIdentity.DeleteOneID(bound.ID).Exec(ctx); err != nil {
			return err
		}
		result, err = s.finishMutation(ctx, tx, u, in, "identity_unlink", hash, bound.ID, true)
		return err
	})
	return result, externalError(err)
}
func (s *Service) GetExternalIdentities(ctx context.Context, uid uuid.UUID) ([]IdentitySummary, error) {
	if _, err := s.activeUser(ctx, uid); err != nil {
		return nil, err
	}
	rows, err := s.db.ExternalIdentity.Query().Where(externalidentity.UserID(uid)).Order(ent.Asc(externalidentity.FieldLinkedAt), ent.Asc(externalidentity.FieldID)).All(ctx)
	if err != nil {
		return nil, externalError(err)
	}
	out := make([]IdentitySummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, IdentitySummary{ID: r.ID, Provider: string(r.Provider), LinkedAt: r.LinkedAt, LastAuthenticatedAt: r.LastAuthenticatedAt})
	}
	return out, nil
}
