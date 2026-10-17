// Package accountcleanup removes one tripmap user's business data in phases.
// Cross-user edges are cleared first, then object storage, then the users row
// (Ent ON DELETE CASCADE handles the rest). Update registry_test when User
// edges in internal/tripmap/ent/schema change.
package accountcleanup

import (
	"context"
	"errors"

	"github.com/kongken/mono/internal/tripmap/blobstore"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/friendship"
	entitinerary "github.com/kongken/mono/internal/tripmap/ent/itinerary"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	entshare "github.com/kongken/mono/internal/tripmap/ent/share"
	"github.com/kongken/mono/internal/tripmap/ent/sharedtrip"
	"github.com/kongken/mono/internal/tripmap/ent/tripmember"
	"github.com/kongken/mono/internal/tripmap/ent/user"
)

// ErrObjectStorageUnavailable means blob cleanup cannot proceed yet.
var ErrObjectStorageUnavailable = errors.New("object storage unavailable")

// Store is the persistence and object storage needed for cleanup.
type Store struct {
	DB    *ent.Client
	Blobs blobstore.Store
}

// Run deletes business data for userID. It is safe to retry after S3 or process
// failures. When the user row is already gone, Run returns nil.
func Run(ctx context.Context, st *Store, userID string) error {
	if userID == "" {
		return errors.New("user_id required")
	}
	if st == nil || st.DB == nil {
		return errors.New("store unavailable")
	}
	exists, err := st.DB.User.Query().Where(user.ID(userID)).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := cutPublicAccess(ctx, st.DB, userID); err != nil {
		return err
	}
	if err := purgeObjectStorage(ctx, st, userID); err != nil {
		return err
	}
	return deleteUserRow(ctx, st.DB, userID)
}

func cutPublicAccess(ctx context.Context, db *ent.Client, userID string) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Share.Delete().Where(entshare.UserID(userID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.Friendship.Delete().Where(friendship.Or(friendship.RequesterIDEQ(userID), friendship.AddresseeIDEQ(userID))).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.TripMember.Delete().Where(tripmember.UserIDEQ(userID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.SharedTrip.Delete().Where(sharedtrip.OwnerIDEQ(userID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.Itinerary.Delete().Where(entitinerary.UserIDEQ(userID)).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func purgeObjectStorage(ctx context.Context, st *Store, userID string) error {
	objects, err := st.DB.MediaObject.Query().Where(mediaobject.UserID(userID)).All(ctx)
	if err != nil {
		return err
	}
	if len(objects) == 0 {
		return nil
	}
	if st.Blobs == nil {
		return ErrObjectStorageUnavailable
	}
	for _, object := range objects {
		if err := deleteMediaKeys(ctx, st.Blobs, object); err != nil {
			return err
		}
	}
	// MediaCleanup rows are independent of users and survive account deletion.
	return nil
}

func deleteMediaKeys(ctx context.Context, blobs blobstore.Store, m *ent.MediaObject) error {
	for _, key := range []string{m.ObjectKey, m.ThumbnailKey, m.UploadKey} {
		if key != "" {
			if err := blobs.Delete(ctx, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteUserRow(ctx context.Context, db *ent.Client, userID string) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Query().Where(user.ID(userID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = tx.User.DeleteOneID(u.ID).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
