// Package application implements the metro RPC services over the Ent client.
package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/catalogstate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// stateID is the fixed key of the single catalog_state row.
const stateID = 1

// InitialVersion is the catalog version of a database nothing was written to.
const InitialVersion int64 = 1

// EnsureCatalogState creates the catalog_state row when it is missing. It is
// safe to call on every start and from several replicas at once.
func EnsureCatalogState(ctx context.Context, db *ent.Client) error {
	err := db.CatalogState.Create().SetID(stateID).SetVersion(InitialVersion).
		OnConflictColumns(catalogstate.FieldID).DoNothing().Exec(ctx)
	// DO NOTHING returns no row when the row already exists; that is success.
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// BumpVersion adds one to the catalog version inside tx and returns the new
// value. Every successful catalog write calls it in the same transaction as the
// write, so clients that cached the previous version see the change. The update
// takes a row lock held until the transaction ends, which also makes concurrent
// administrative writes take turns.
func BumpVersion(ctx context.Context, tx *ent.Tx) (int64, error) {
	if err := tx.CatalogState.Create().SetID(stateID).SetVersion(InitialVersion + 1).
		OnConflictColumns(catalogstate.FieldID).
		Update(func(u *ent.CatalogStateUpsert) { u.AddVersion(1) }).Exec(ctx); err != nil {
		return 0, err
	}
	row, err := tx.CatalogState.Get(ctx, stateID)
	if err != nil {
		return 0, err
	}
	return row.Version, nil
}

// readSnapshot runs fn in one read-only snapshot, so the version it reads and
// the rows it returns belong to the same committed state even while an
// administrator writes.
func readSnapshot(ctx context.Context, db *ent.Client, fn func(tx *ent.Tx, version int64) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	version := InitialVersion
	row, err := tx.CatalogState.Get(ctx, stateID)
	switch {
	case err == nil:
		version = row.Version
	case !ent.IsNotFound(err):
		return err
	}
	return fn(tx, version)
}

// internalError logs err and returns the generic INTERNAL status the BFF passes
// on; database detail never reaches a client.
func internalError(op string, err error) error {
	slog.Error("metro "+op, "error", err)
	return status.Error(codes.Internal, "internal error")
}
