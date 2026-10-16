package application

import (
	"context"
	"log/slog"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/predicate"
	"github.com/kongken/mono/internal/tripmap/ent/snapshot"
	"github.com/kongken/mono/internal/tripmap/ent/user"
)

// maintenancePage bounds how many users one query returns.
const maintenancePage = 100

// needsScheduledSnapshot matches users whose footprints changed since their
// newest snapshot and who have no snapshot younger than snapshotInterval. Empty
// accounts (revision 0) are skipped.
func needsScheduledSnapshot(cutoff time.Time) predicate.User {
	return func(s *sql.Selector) {
		recent := sql.Table(snapshot.Table)
		s.Where(sql.NotExists(sql.Select(recent.C(snapshot.FieldID)).From(recent).Where(sql.And(
			sql.ColumnsEQ(recent.C(snapshot.FieldUserID), s.C(user.FieldID)),
			sql.GT(recent.C(snapshot.FieldCreatedAt), cutoff),
		))))
		current := sql.Table(snapshot.Table)
		s.Where(sql.NotExists(sql.Select(current.C(snapshot.FieldID)).From(current).Where(sql.And(
			sql.ColumnsEQ(current.C(snapshot.FieldUserID), s.C(user.FieldID)),
			sql.ColumnsGTE(current.C(snapshot.FieldRevision), s.C(user.FieldRevision)),
		))))
	}
}

// RunSnapshotMaintenance takes a scheduled snapshot for every account that
// changed since its last one and then applies retention to snapshots that have
// aged out. It is safe to run on several instances at once: each snapshot is
// decided again under the user's row lock. The first error is returned after
// every user has been attempted.
func RunSnapshotMaintenance(ctx context.Context, db *ent.Client, now time.Time) error {
	var first error
	remember := func(err error) {
		if first == nil {
			first = err
		}
	}
	last := ""
	for ctx.Err() == nil {
		users, err := db.User.Query().
			Where(user.IDGT(last), user.Disabled(false), user.RevisionGT(0), needsScheduledSnapshot(now.Add(-snapshotInterval))).
			Order(ent.Asc(user.FieldID)).Limit(maintenancePage).Select(user.FieldID).All(ctx)
		if err != nil {
			remember(err)
			break
		}
		for _, u := range users {
			last = u.ID
			if err := scheduledSnapshot(ctx, db, u.ID, now); err != nil {
				slog.Error("scheduled snapshot", "error", err)
				remember(err)
			}
		}
		if len(users) < maintenancePage {
			break
		}
	}

	// Accounts that stopped changing still shed snapshots once they age out.
	aged := now.Add(-snapshotMaxAge)
	last = ""
	for ctx.Err() == nil {
		users, err := db.User.Query().
			Where(user.IDGT(last), user.HasSnapshotsWith(snapshot.CreatedAtLT(aged))).
			Order(ent.Asc(user.FieldID)).Limit(maintenancePage).Select(user.FieldID).All(ctx)
		if err != nil {
			remember(err)
			break
		}
		for _, u := range users {
			last = u.ID
			if err := pruneSnapshots(ctx, db.Snapshot, u.ID, now); err != nil {
				slog.Error("snapshot retention", "error", err)
				remember(err)
			}
		}
		if len(users) < maintenancePage {
			break
		}
	}
	return first
}

// scheduledSnapshot re-checks the conditions under the user's lock so a
// concurrent write or another instance never produces a duplicate.
func scheduledSnapshot(ctx context.Context, db *ent.Client, uid string, now time.Time) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Query().Where(user.ID(uid)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.Disabled || u.Revision == 0 {
		return nil
	}
	recent, err := tx.Snapshot.Query().Where(snapshot.UserID(uid), snapshot.Or(
		snapshot.CreatedAtGT(now.Add(-snapshotInterval)), snapshot.RevisionGTE(u.Revision))).Exist(ctx)
	if err != nil || recent {
		return err
	}
	state, err := loadState(ctx, tx, uid, u.SchemaVersion)
	if err != nil {
		return err
	}
	if _, _, err = captureSnapshot(ctx, tx, u, snapshot.ReasonScheduled, state); err != nil {
		return err
	}
	return tx.Commit()
}
