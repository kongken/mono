package accountcleanup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"entgo.io/ent/dialect/sql"
	"errors"
	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/cleanupobject"
	"github.com/kongken/mono/internal/timeline/ent/entry"
	"github.com/kongken/mono/internal/timeline/ent/mediaobject"
	"github.com/kongken/mono/internal/timeline/ent/timelinemutation"
	"github.com/kongken/mono/internal/tripmap/blobstore"
)

var ErrObjectStorageUnavailable = errors.New("object storage unavailable")

var ErrTombstoneNotFound = errors.New("media tombstone not found")

// ReplayMediaTombstone is the shared post-cleanup replay contract for attachment
// deletion. It queries only the authenticated owner scope; another owner's ref
// is intentionally indistinguishable from an unknown ref.
func ReplayMediaTombstone(ctx context.Context, db *ent.Client, userID, mutationID, mediaRef string) (map[string]any, error) {
	if db == nil || userID == "" || mutationID == "" || mediaRef == "" {
		return nil, ErrTombstoneNotFound
	}
	r, err := db.TimelineMutation.Query().Where(
		timelinemutation.UserID(userID), timelinemutation.MutationIDEQ(mutationID),
		timelinemutation.TargetKindEQ("media"), timelinemutation.OperationEQ("media_delete"),
		timelinemutation.MediaRefEQ(mediaRef),
	).Only(ctx)
	if err != nil {
		return nil, ErrTombstoneNotFound
	}
	return r.ResultSnapshot, nil
}

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
		h := sha256.Sum256([]byte("media_delete:" + m.ID + ":" + userID))
		hash := hex.EncodeToString(h[:])
		mutationID := "cleanup:media:" + m.ID
		_, err = tx.TimelineMutation.Create().SetUserID(userID).SetMutationID(mutationID).SetTargetKind("media").SetOperation("media_delete").SetEntryRef(m.EntryPublicRef).SetMediaRef(m.PublicRef).SetRequestHash(hash).SetResultRevision(1).SetResultState("tombstoned").SetResultSnapshot(map[string]any{"media_ref": m.PublicRef, "entry_ref": m.EntryPublicRef, "object_key": m.ObjectKey, "thumbnail_key": m.ThumbnailKey, "upload_key": m.UploadKey, "deleted": true}).Save(ctx)
		if err != nil {
			if old, qerr := tx.TimelineMutation.Query().Where(timelinemutation.UserID(userID), timelinemutation.MutationIDEQ(mutationID)).Only(ctx); qerr != nil || old.RequestHash != hash {
				return err
			}
		}
		keys := []string{m.ObjectKey, m.ThumbnailKey, m.UploadKey}
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err = QueueObject(ctx, tx, key); err != nil {
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
	return tx.Commit()
}

// QueueObject records an object for deletion, ignoring a key that is already
// queued. Neither ent shortcut works here: Ignore() only drops fields from the DO
// UPDATE clause, so PostgreSQL rejects the statement for a missing conflict target
// (42601), and DoNothing() asks for `RETURNING "id"`, which comes back empty when
// the key is already queued, failing the whole delete with sql.ErrNoRows. A
// conflict on the unique object_key therefore updates the row with its own values:
// no change to status or attempts, no extra row, and the transaction stays usable.
func QueueObject(ctx context.Context, tx *ent.Tx, key string) error {
	if tx == nil || key == "" {
		return errors.New("cleanup object requires a key")
	}
	return tx.CleanupObject.Create().
		SetObjectKey(key).
		OnConflict(sql.ConflictColumns(cleanupobject.FieldObjectKey)).
		Ignore().
		Exec(ctx)
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
