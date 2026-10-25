package account

import (
	"context"
	"log/slog"
	"strings"
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
	// The fields of an administrator change; empty for other events.
	channel                   string
	actorUser                 *uuid.UUID
	mutationID                string
	beforeStatus, afterStatus Status
	beforeRoles, afterRoles   []string
}

func (e auditEntry) create(c *ent.SecurityAuditCreate, at time.Time) *ent.SecurityAuditCreate {
	c.SetActor(e.actor).SetAction(e.action).SetRequestID(e.requestID).SetResult(e.result).SetReason(e.reason).SetCreatedAt(at).
		SetChannel(e.channel).SetMutationID(e.mutationID).
		SetBeforeStatus(string(e.beforeStatus)).SetAfterStatus(string(e.afterStatus)).
		SetBeforeRoles(strings.Join(e.beforeRoles, ",")).SetAfterRoles(strings.Join(e.afterRoles, ","))
	if e.userID != nil {
		c.SetUserID(*e.userID)
	}
	if e.actorUser != nil {
		c.SetActorUserID(*e.actorUser)
	}
	return c
}

// audit writes the row inside the transaction of the change it describes.
func (s *Service) audit(ctx context.Context, tx *ent.Tx, e auditEntry) error {
	return e.create(tx.SecurityAudit.Create(), s.now()).Exec(ctx)
}

// auditAlone records an event that changes nothing else (a failed password
// check). It is best effort: the outcome of the request does not depend on it.
func (s *Service) auditAlone(ctx context.Context, e auditEntry) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := e.create(s.db.SecurityAudit.Create(), s.now()).Exec(ctx); err != nil {
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
	if err = s.cancelPendingJobs(ctx, tx, ids, "account_closed"); err != nil {
		return err
	}
	_, err = tx.AccountToken.Delete().Where(match).Exec(ctx)
	return err
}

// cancelPendingJobs cancels the mail jobs of the given challenges that no worker
// has taken yet and erases their ciphertext. A job already being sent cannot be
// recalled; the challenge it carries is consumed or gone by then.
func (s *Service) cancelPendingJobs(ctx context.Context, tx *ent.Tx, challengeIDs []uuid.UUID, class string) error {
	if len(challengeIDs) == 0 {
		return nil
	}
	_, err := tx.MailJob.Update().Where(mailjob.ChallengeIDIn(challengeIDs...), mailjob.StatusEQ(mailjob.StatusPending)).
		SetStatus(mailjob.StatusCancelled).SetResultClass(class).
		ClearCiphertext().ClearKeyID().SetFinishedAt(s.now()).Save(ctx)
	return err
}

func accounttokenStale(before time.Time) predicate.AccountToken {
	return accounttoken.Or(accounttoken.ExpiresAtLT(before), accounttoken.ConsumedAtLT(before))
}
