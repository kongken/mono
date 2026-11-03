package account

import (
	"context"
	"time"

	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/ent/authnotification"
	"github.com/kongken/mono/internal/usersrv/ent/authoperationreceipt"
)

// PurgeSecurity erases secrets at proof expiry, retaining only bound result
// references for their 24h replay window. An orphan exchanging flow never
// becomes issued again, including after a process restart.
func (s *Service) PurgeSecurity(ctx context.Context) error {
	return s.inTx(ctx, func(tx *ent.Tx) error {
		if _, err := tx.AuthChallenge.Update().Where(authchallenge.ExpiresAtLTE(s.now()), authchallenge.StatusNotIn(authchallenge.StatusCompleted, authchallenge.StatusFailed)).SetStatus(authchallenge.StatusFailed).SetConsumedAt(s.now()).ClearProofCiphertext().ClearProofKeyID().ClearEmailTokenHash().Save(ctx); err != nil {
			return err
		}
		if _, err := tx.AuthChallenge.Delete().Where(authchallenge.ExpiresAtLT(s.now().Add(-OperationReceiptTTL))).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.AuthOperationReceipt.Delete().Where(authoperationreceipt.ExpiresAtLTE(s.now())).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.AuthNotification.Delete().Where(authnotification.CreatedAtLT(s.now().Add(-7 * 24 * time.Hour))).Exec(ctx)
		return err
	})
}
