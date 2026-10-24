package account

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
	"github.com/kongken/mono/internal/usersrv/ent/mailjob"
	"github.com/kongken/mono/internal/usersrv/ent/predicate"
	"github.com/kongken/mono/internal/usersrv/ent/securityaudit"
)

// auditEntry is one security_audit row. It never carries an email, a password
// or a token; the actor is "user:<uuid>", "system" or the CLI operator name.
type auditEntry struct {
	userID    *uuid.UUID
	actor     string
	action    string
	requestID string
	result    string
	reason    string
}

// audit writes the row inside the transaction of the change it describes.
func (s *Service) audit(ctx context.Context, tx *ent.Tx, e auditEntry) error {
	c := tx.SecurityAudit.Create().SetActor(e.actor).SetAction(e.action).SetRequestID(e.requestID).SetResult(e.result).SetReason(e.reason).
		SetCreatedAt(s.now())
	if e.userID != nil {
		c.SetUserID(*e.userID)
	}
	return c.Exec(ctx)
}

// auditAlone records an event that changes nothing else (a failed password
// check). It is best effort: the outcome of the request does not depend on it.
func (s *Service) auditAlone(ctx context.Context, e auditEntry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	c := s.db.SecurityAudit.Create().SetActor(e.actor).SetAction(e.action).SetRequestID(e.requestID).SetResult(e.result).SetReason(e.reason).
		SetCreatedAt(s.now())
	if e.userID != nil {
		c.SetUserID(*e.userID)
	}
	if err := c.Exec(ctx); err != nil {
		slog.Error("usersrv audit write failed", "action", e.action, "error", err)
	}
}

func (s *Service) purgeAudit(ctx context.Context, before time.Time) (int, error) {
	return s.db.SecurityAudit.Delete().Where(securityaudit.CreatedAtLT(before)).Exec(ctx)
}

// voidTokens deletes the account tokens bound to the user or to its address and
// cancels the mail jobs that were still waiting to carry them, erasing their
// ciphertext in the same transaction. A job that a worker is already sending
// cannot be recalled; its token no longer exists, so the link it carries is dead.
func (s *Service) voidTokens(ctx context.Context, tx *ent.Tx, id uuid.UUID, email string) error {
	match := accounttoken.Or(accounttoken.UserID(id), accounttoken.EmailCanonical(email))
	ids, err := tx.AccountToken.Query().Where(match).IDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		now := s.now()
		if _, err = tx.MailJob.Update().Where(mailjob.ChallengeIDIn(ids...), mailjob.StatusEQ(mailjob.StatusPending)).
			SetStatus(mailjob.StatusCancelled).SetResultClass("account_closed").
			ClearCiphertext().ClearKeyID().SetFinishedAt(now).Save(ctx); err != nil {
			return err
		}
	}
	_, err = tx.AccountToken.Delete().Where(match).Exec(ctx)
	return err
}

func accounttokenStale(before time.Time) predicate.AccountToken {
	return accounttoken.Or(accounttoken.ExpiresAtLT(before), accounttoken.ConsumedAtLT(before))
}
