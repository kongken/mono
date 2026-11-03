package account

import (
	"context"
	"github.com/google/uuid"

	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/user"
	"github.com/kongken/mono/internal/usersrv/identity"
)

// BeginConfirmedDeletion provides the passwordless email handoff for a later
// BFF recovery-cookie workflow. Confirmation alone never starts deletion.
func (s *Service) BeginConfirmedDeletion(ctx context.Context, ref ChallengeRef, mutation, confirmation string) (int64, error) {
	if confirmation != "注销" || ref.UserID == uuid.Nil || !validBinding(ref.FlowBinding, false) {
		return 0, ErrChallengeInvalid
	}
	if _, err := identity.NormalizeMutationID(mutation); err != nil {
		return 0, err
	}
	var version int64
	err := s.inTx(ctx, func(tx *ent.Tx) error {
		u, err := tx.User.Query().Where(user.ID(ref.UserID)).WithRoles().ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := lockChallenges(ctx, tx, ref.ID)
		if err != nil {
			return err
		}
		c := rows[ref.ID]
		if !matches(c, ref) || c.Kind != authchallenge.KindAccountDeletion {
			return ErrChallengeInvalid
		}
		if u.Status == user.StatusDeleting {
			if u.DeletionMutationID == nil || *u.DeletionMutationID != mutation || c.Status != authchallenge.StatusCompleted {
				return ErrDeletionMismatch
			}
			version = u.AuthVersion
			return nil
		}
		if u.Status != user.StatusActive {
			return ErrNotActive
		}
		if u.AuthVersion != ref.AuthVersion {
			return ErrConflict
		}
		if hasRole(u, RolePlatformAdmin) {
			return ErrPlatformAdmin
		}
		if err = s.usable(c, ref, authchallenge.KindAccountDeletion); err != nil {
			return err
		}
		if c.ConfirmedAt == nil || (c.EmailExpiresAt != nil && !c.EmailExpiresAt.After(s.now())) {
			return ErrChallengeInvalid
		}
		version = u.AuthVersion + 1
		if err = tx.User.UpdateOneID(u.ID).SetStatus(user.StatusDeleting).SetDeletionMutationID(mutation).SetDeletionStartedAt(s.now()).SetAuthVersion(version).Exec(ctx); err != nil {
			return err
		}
		if err = s.consume(ctx, tx, c.ID); err != nil {
			return err
		}
		if err = s.voidTokens(ctx, tx, u.ID, u.EmailCanonical); err != nil {
			return err
		}
		return s.audit(ctx, tx, auditEntry{userID: &u.ID, actor: userActor(u.ID), action: "deletion.begin", result: "ok", mutationID: mutation})
	})
	return version, externalError(err)
}
