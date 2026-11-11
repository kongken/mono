package accountcleanup

import (
	"context"
	"errors"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/cleanupobject"
	"github.com/kongken/mono/internal/timeline/ent/entry"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/ent/receipt"
	"github.com/kongken/mono/internal/tripmap/blobstore"
	"time"
)

const ReceiptRetention = 30 * 24 * time.Hour

// ExpireReceipts removes immutable mutation receipts beyond their replay window.
// Callers should run this from the timeline maintenance schedule.
func ExpireReceipts(ctx context.Context, db *ent.Client, now time.Time) (int, error) {
	if db == nil {
		return 0, errors.New("database unavailable")
	}
	return db.Receipt.Delete().Where(receipt.CreatedAtLT(now.UTC().Add(-ReceiptRetention))).Exec(ctx)
}

var ErrObjectStorageUnavailable = errors.New("object storage unavailable")

type Store struct {
	DB    *ent.Client
	Blobs blobstore.Store
}

// Run removes all Timeline rows and records opaque object keys for asynchronous deletion.
// It never stores a user identifier in the cleanup queue and is safe to retry.
func Run(ctx context.Context, st *Store, userID string) error {
	if userID == "" {
		return errors.New("user_id required")
	}
	if st == nil || st.DB == nil {
		return errors.New("store unavailable")
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	media, err := tx.MediaObject.Query().Where(mediaobject.UserID(userID)).All(ctx)
	if err != nil {
		return err
	}
	for _, m := range media {
		keys := []string{m.ObjectKey, m.ThumbnailKey, m.UploadKey}
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err = tx.CleanupObject.Create().SetObjectKey(key).OnConflict().Ignore().Exec(ctx); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Entry.Delete().Where(entry.UserID(userID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.MediaObject.Delete().Where(mediaobject.UserID(userID)).Exec(ctx); err != nil {
		return err
	}
	if _, err = tx.Receipt.Delete().Where(receipt.UserIDEQ(userID)).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// Drain deletes queued objects and marks successful rows. A missing object is treated as success by the blob store.
func Drain(ctx context.Context, st *Store) error {
	if st == nil || st.DB == nil || st.Blobs == nil {
		return ErrObjectStorageUnavailable
	}
	rows, err := st.DB.CleanupObject.Query().Where(cleanupobject.StatusEQ("pending")).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := st.Blobs.Delete(ctx, row.ObjectKey); err != nil {
			return err
		}
		if _, err := row.Update().SetStatus("done").AddAttempts(1).Save(ctx); err != nil {
			return err
		}
	}
	return nil
}
