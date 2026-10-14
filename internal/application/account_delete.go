package application

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/accountdeletionreceipt"
	"go.orx.me/apps/trip-map/internal/ent/friendship"
	entitinerary "go.orx.me/apps/trip-map/internal/ent/itinerary"
	"go.orx.me/apps/trip-map/internal/ent/mediaobject"
	"go.orx.me/apps/trip-map/internal/ent/session"
	entshare "go.orx.me/apps/trip-map/internal/ent/share"
	"go.orx.me/apps/trip-map/internal/ent/sharedtrip"
	"go.orx.me/apps/trip-map/internal/ent/tripmember"
	"go.orx.me/apps/trip-map/internal/ent/user"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
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
	// Disable first. Only deletion retry, Me and Logout remain available. Other
	// sessions are revoked; the current session can retry after an S3 failure.
	if u.DeletionRequestedAt == nil {
		if err = tx.User.UpdateOneID(u.ID).SetDisabled(true).SetDeletionRequestedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if _, err = tx.Session.Update().Where(session.UserID(u.ID), session.IDNEQ(p.SessionID)).SetRevoked(true).Save(ctx); err != nil {
		return nil, internalError(err)
	}
	// Public access ends with the first transaction, before any slow object cleanup
	// can fail: the share row is removed (its link and snapshot with it) and public
	// reads also require an enabled account, so nothing stays reachable meanwhile.
	if _, err = tx.Share.Delete().Where(entshare.UserID(u.ID)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = tx.Friendship.Delete().Where(friendship.Or(friendship.RequesterIDEQ(u.ID), friendship.AddresseeIDEQ(u.ID))).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = tx.TripMember.Delete().Where(tripmember.UserIDEQ(u.ID)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = tx.SharedTrip.Delete().Where(sharedtrip.OwnerIDEQ(u.ID)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if _, err = tx.Itinerary.Delete().Where(entitinerary.UserIDEQ(u.ID)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	objects, err := st.DB.MediaObject.Query().Where(mediaobject.UserID(u.ID)).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	if len(objects) > 0 && st.Blobs == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("object cleanup unavailable; retry account deletion"))
	}
	for _, object := range objects {
		if err = deleteMediaKeys(ctx, st, object); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("object cleanup incomplete; retry account deletion"))
		}
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
