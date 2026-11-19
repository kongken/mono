package accountcleanup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
var ErrTombstoneNotFound = errors.New("media delete receipt not found")

type Store struct {
	DB    *ent.Client
	Blobs blobstore.Store
}

// QueueObject adds an object key to the cleanup queue idempotently. Callers use
// it in the same transaction as the state transition so a retry cannot lose a
// deletion request or enqueue duplicate work.
func QueueObject(ctx context.Context, tx *ent.Tx, key string) error {
	if tx == nil || key == "" {
		return errors.New("cleanup queue unavailable")
	}
	return queueKey(ctx, tx, key)
}

// queueKey inserts key with an explicit conflict target. PostgreSQL rejects an
// ON CONFLICT DO UPDATE without one (which is what ent's Ignore() generates), and
// DO NOTHING leaves an already queued or completed row exactly as it was. When the
// key already exists PostgreSQL returns no row, which ent reports as ErrNoRows.
func queueKey(ctx context.Context, tx *ent.Tx, key string) error {
	err := tx.CleanupObject.Create().SetObjectKey(key).
		OnConflictColumns(cleanupobject.FieldObjectKey).DoNothing().Exec(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// ReplayMediaTombstone returns the immutable snapshot for a media delete
// mutation. It remains available after the media row and object are cleaned.
func ReplayMediaTombstone(ctx context.Context, db *ent.Client, userID, mutationID, mediaRef string) (map[string]any, error) {
	if db == nil || userID == "" || mutationID == "" || mediaRef == "" {
		return nil, ErrTombstoneNotFound
	}
	r, err := db.Receipt.Query().Where(receipt.UserIDEQ(userID), receipt.MutationIDEQ(mutationID), receipt.MediaRefEQ(mediaRef), receipt.OperationEQ("media_delete")).Only(ctx)
	if err != nil {
		return nil, ErrTombstoneNotFound
	}
	var snapshot map[string]any
	if err := json.Unmarshal(r.ResultSnapshot, &snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
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
			if err = queueKey(ctx, tx, key); err != nil {
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

// DrainBatch bounds how many queued objects one Drain call handles.
const DrainBatch = 200

// Drain deletes queued objects and marks successful rows done. A missing object is
// treated as success by the blob store. A failed delete (a temporary object-store
// outage) leaves the row pending with one more attempt recorded and does not stop the
// other rows, so the next call retries exactly the objects that are still there.
// Rows that are already done are never touched again.
func Drain(ctx context.Context, st *Store) error {
	if st == nil || st.DB == nil || st.Blobs == nil {
		return ErrObjectStorageUnavailable
	}
	rows, err := st.DB.CleanupObject.Query().Where(cleanupobject.StatusEQ("pending")).
		Order(ent.Asc(cleanupobject.FieldCreatedAt), ent.Asc(cleanupobject.FieldID)).Limit(DrainBatch).All(ctx)
	if err != nil {
		return err
	}
	var failed []error
	for _, row := range rows {
		if err := st.Blobs.Delete(ctx, row.ObjectKey); err != nil {
			// The key is opaque, but there is no reason to log it: record the attempt only.
			if _, uerr := row.Update().AddAttempts(1).Save(ctx); uerr != nil {
				failed = append(failed, uerr)
			}
			failed = append(failed, err)
			continue
		}
		if _, err := row.Update().SetStatus("done").AddAttempts(1).Save(ctx); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("drain cleanup queue: %w", errors.Join(failed...))
	}
	return nil
}
