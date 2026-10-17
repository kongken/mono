package application

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/accountcleanup"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/accountdeletionreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/session"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"golang.org/x/crypto/bcrypt"
)

type deletionReceiptContextKey struct{}

func (s *AuthService) DeleteAccount(ctx context.Context, req *connect.Request[tripmapv1.DeleteAccountRequest]) (*connect.Response[tripmapv1.DeleteAccountResponse], error) {
	if req.Msg.Confirmation != "DELETE" {
		return nil, invalid("explicit DELETE confirmation is required")
	}
	if done, _ := ctx.Value(deletionReceiptContextKey{}).(bool); done {
		return connect.NewResponse(&tripmapv1.DeleteAccountResponse{}), nil
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "delete:"+p.UserID, 10, loginWindow); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Query().Where(user.ID(p.UserID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	if u.Disabled && u.DeletionRequestedAt == nil {
		return nil, errUnauthenticated
	}
	if u.PasswordHash != "" {
		if len(req.Msg.Password) > 72 || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Msg.Password)) != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("password verification failed"))
		}
	} else {
		sess, err := tx.Session.Get(ctx, p.SessionID)
		if err != nil {
			return nil, errUnauthenticated
		}
		if time.Since(sess.CreatedAt) > 15*time.Minute {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sign in again before deleting an OAuth account"))
		}
	}
	if u.DeletionRequestedAt == nil {
		if err = tx.User.UpdateOneID(u.ID).SetDisabled(true).SetDeletionRequestedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if _, err = tx.Session.Update().Where(session.UserID(u.ID), session.IDNEQ(p.SessionID)).SetRevoked(true).Save(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	if err := accountcleanup.Run(ctx, &accountcleanup.Store{DB: st.DB, Blobs: st.Blobs}, u.ID); err != nil {
		msg := "object cleanup incomplete; retry account deletion"
		if errors.Is(err, accountcleanup.ErrObjectStorageUnavailable) {
			msg = "object cleanup unavailable; retry account deletion"
		}
		return nil, connect.NewError(connect.CodeUnavailable, errors.New(msg))
	}
	final, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = final.Rollback() }()
	current, err := final.User.Query().Where(user.ID(u.ID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return connect.NewResponse(&tripmapv1.DeleteAccountResponse{}), nil
	}
	if err != nil {
		return nil, internalError(err)
	}
	if current.DeletionRequestedAt == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("deletion was not requested"))
	}
	sess, err := final.Session.Get(ctx, p.SessionID)
	if err != nil {
		return nil, errUnauthenticated
	}
	if err = final.AccountDeletionReceipt.Create().SetTokenHash(sess.TokenHash).SetExpiresAt(time.Now().UTC().Add(24 * time.Hour)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = final.User.DeleteOneID(u.ID).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = final.AccountDeletionReceipt.Delete().Where(accountdeletionreceipt.ExpiresAtLTE(time.Now().UTC())).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = final.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.DeleteAccountResponse{}), nil
}
