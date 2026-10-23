package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/accountdeletionreceipt"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	entuser "github.com/kongken/mono/internal/bff/ent/user"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const deletionReceiptTTL = 24 * time.Hour

// cleanupProducts asks every enabled product to remove the user's data. Every
// cleaner runs even when an earlier one failed, so one product being down does
// not stop the others from making progress. Any failure leaves the account in
// the pending-deletion state and returns UNAVAILABLE, so the client retries; the
// retry repeats the products that already succeeded, which is harmless because
// each cleanup is idempotent. The completion receipt is written only after all
// of them have succeeded.
func (s *Service) cleanupProducts(ctx context.Context, userID string) error {
	callCtx := rpcauth.WithCall(ctx, rpcauth.Call{UserID: userID})
	var failed error
	for _, c := range s.cleaners {
		err := c.CleanupUserData(callCtx, userID)
		if err == nil {
			continue
		}
		slog.Warn("product data cleanup failed", "product", c.Name(), "user", userID, "error", err)
		if failed != nil {
			continue
		}
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
			failed = connect.NewError(connect.CodeUnavailable, errors.New(st.Message()))
		} else {
			failed = connect.NewError(connect.CodeUnavailable, errors.New("object cleanup incomplete; retry account deletion"))
		}
	}
	return failed
}

// CompletionReceiptValid reports whether token may replay a finished deletion.
func (s *Service) CompletionReceiptValid(ctx context.Context, token string) bool {
	if token == "" {
		return false
	}
	ok, err := s.db.AccountDeletionReceipt.Query().
		Where(
			accountdeletionreceipt.TokenHash(session.HashToken(token)),
			accountdeletionreceipt.ExpiresAtGT(time.Now().UTC()),
		).
		Exist(ctx)
	return err == nil && ok
}

func (s *Service) DeleteAccount(ctx context.Context, browser session.BrowserSession, req *bffv1.DeleteAccountRequest) error {
	if req.GetConfirmation() != "DELETE" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("explicit DELETE confirmation is required"))
	}
	if err := s.allow(ctx, "delete:"+browser.UserID, 10, oauthWindow); err != nil {
		return err
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return internal(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Query().Where(entuser.ID(browser.UserID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return errUnauth
	}
	if err != nil {
		return internal(err)
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return errUnauth
	}
	// This deletes application data, never the Logto identity. Once cleanup has
	// started, the retained session may retry without another freshness check.
	if u.DeletionRequestedAt == nil {
		sess, err := tx.Session.Get(ctx, browser.SessionID)
		if err != nil || sess.UserID != u.ID {
			return errUnauth
		}
		if time.Since(sess.CreatedAt) > 15*time.Minute {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("sign in again before deleting cloud data"))
		}
	}
	if u.DeletionRequestedAt == nil {
		now := time.Now().UTC()
		if err = tx.User.UpdateOneID(u.ID).SetDisabled(true).SetDeletionRequestedAt(now).Exec(ctx); err != nil {
			return internal(err)
		}
	}
	if _, err = tx.Session.Update().
		Where(entsession.UserID(u.ID), entsession.IDNEQ(browser.SessionID)).
		SetRevoked(true).
		Save(ctx); err != nil {
		return internal(err)
	}
	if err = tx.Commit(); err != nil {
		return internal(err)
	}
	if err = s.cleanupProducts(ctx, u.ID); err != nil {
		return err
	}
	final, err := s.db.Tx(ctx)
	if err != nil {
		return internal(err)
	}
	defer func() { _ = final.Rollback() }()
	current, err := final.User.Query().Where(entuser.ID(u.ID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return internal(err)
	}
	if current.DeletionRequestedAt == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("deletion was not requested"))
	}
	sess, err := final.Session.Get(ctx, browser.SessionID)
	if err != nil {
		return errUnauth
	}
	expires := time.Now().UTC().Add(deletionReceiptTTL)
	if err = final.AccountDeletionReceipt.Create().
		SetTokenHash(sess.TokenHash).
		SetExpiresAt(expires).
		Exec(ctx); err != nil {
		return internal(err)
	}
	if err = final.User.DeleteOneID(u.ID).Exec(ctx); err != nil {
		return internal(err)
	}
	if _, err = final.AccountDeletionReceipt.Delete().
		Where(accountdeletionreceipt.ExpiresAtLTE(time.Now().UTC())).
		Exec(ctx); err != nil {
		return internal(err)
	}
	if err = final.Commit(); err != nil {
		return internal(err)
	}
	return nil
}
