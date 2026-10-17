package auth

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/kongken/mono/internal/bff/ent/accountdeletionreceipt"
	entsession "github.com/kongken/mono/internal/bff/ent/session"
	entuser "github.com/kongken/mono/internal/bff/ent/user"
	"github.com/kongken/mono/internal/bff/session"
	"github.com/kongken/mono/internal/platform/rpcauth"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const deletionReceiptTTL = 24 * time.Hour

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
	if err := s.allow(ctx, "delete:"+browser.UserID, 10, loginWindow); err != nil {
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
	if u.PasswordHash != "" {
		pw := req.GetPassword()
		if len(pw) > maxPasswordBytes || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(pw)) != nil {
			return connect.NewError(connect.CodePermissionDenied, errors.New("password verification failed"))
		}
	} else {
		sess, err := tx.Session.Get(ctx, browser.SessionID)
		if err != nil {
			return errUnauth
		}
		if time.Since(sess.CreatedAt) > 15*time.Minute {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("sign in again before deleting an OAuth account"))
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
	if s.cleanup != nil {
		callCtx := rpcauth.WithCall(ctx, rpcauth.Call{UserID: u.ID})
		if _, err = s.cleanup.CleanupUserData(callCtx, &tripmapv1.CleanupUserDataRequest{UserId: u.ID}); err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
				return connect.NewError(connect.CodeUnavailable, errors.New(st.Message()))
			}
			return connect.NewError(connect.CodeUnavailable, errors.New("object cleanup incomplete; retry account deletion"))
		}
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
