package application

import (
	"context"
	"errors"
	"time"

	"go.orx.me/apps/trip-map/internal/tripmap/ent"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/mediacleanup"
	"go.orx.me/apps/trip-map/internal/tripmap/ent/mediaobject"
)

// RunMediaMaintenance is idempotent across replicas. Keep failed work in the
// database; object deletion must finish before metadata is removed.
func RunMediaMaintenance(ctx context.Context, st *Store, now time.Time) error {
	if st.Blobs == nil {
		return errors.New("media storage unavailable")
	}
	rows, err := st.DB.MediaObject.Query().Where(mediaobject.Or(mediaobject.StatusEQ(mediaobject.StatusDeleting), mediaobject.And(mediaobject.StatusEQ(mediaobject.StatusPending), mediaobject.CreatedAtLTE(now.Add(-24*time.Hour))))).Limit(200).All(ctx)
	if err != nil {
		return err
	}
	for _, m := range rows {
		tx, err := mediaTx(ctx, st, m.UserID)
		if err != nil { // Deletion-pending accounts clean their own objects.
			continue
		}
		current, err := mediaOwned(ctx, tx.Client(), m.UserID, m.ID)
		if err != nil {
			_ = tx.Rollback()
			continue
		}
		if current.Status != mediaobject.StatusDeleting && (current.Status != mediaobject.StatusPending || current.CreatedAt.After(now.Add(-24*time.Hour))) {
			_ = tx.Rollback()
			continue
		}
		if err = tx.MediaObject.UpdateOne(current).SetStatus(mediaobject.StatusDeleting).Exec(ctx); err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
		if err != nil {
			return err
		}
		if err = deleteMediaKeys(ctx, st, current); err != nil {
			return err
		}
		// The independent queue still guards late staging PUTs after this row is removed.
		if err = st.DB.MediaObject.DeleteOneID(current.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return err
		}
	}
	queue, err := st.DB.MediaCleanup.Query().Where(mediacleanup.DeleteAfterLTE(now)).Limit(200).All(ctx)
	if err != nil {
		return err
	}
	for _, job := range queue {
		if err = st.Blobs.Delete(ctx, job.ObjectKey); err != nil {
			return err
		}
		if err = st.DB.MediaCleanup.DeleteOneID(job.ID).Exec(ctx); err != nil && !ent.IsNotFound(err) {
			return err
		}
	}
	return nil
}
