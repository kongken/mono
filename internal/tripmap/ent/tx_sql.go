package ent

import (
	"context"

	"entgo.io/ent/dialect/sql"
)

// QuerySQL exposes the current Ent transaction connection for shared-schema
// lookups that are owned by another service's schema (Timeline Entries).
func (tx *Tx) QuerySQL(ctx context.Context, query string, args []any) (*sql.Rows, error) {
	var rows sql.Rows
	if err := tx.config.driver.Query(ctx, query, args, &rows); err != nil {
		return nil, err
	}
	return &rows, nil
}
