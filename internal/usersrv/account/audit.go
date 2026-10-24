package account

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/accounttoken"
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

// voidTokens deletes the account tokens bound to the user or to its address.
func (s *Service) voidTokens(ctx context.Context, tx *ent.Tx, id uuid.UUID, email string) error {
	_, err := tx.AccountToken.Delete().Where(accounttoken.Or(accounttoken.UserID(id), accounttoken.EmailCanonical(email))).Exec(ctx)
	return err
}

func accounttokenStale(before time.Time) predicate.AccountToken {
	return accounttoken.Or(accounttoken.ExpiresAtLT(before), accounttoken.ConsumedAtLT(before))
}
