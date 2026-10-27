// Package testutil provides an isolated PostgreSQL schema for metro tests. Tests
// are skipped unless TRIPMAP_TEST_POSTGRES_DSN is set (see make test-integration);
// the metro tests share the development PostgreSQL server of the other products
// but never touch its data: each test gets a throwaway schema.
package testutil

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/metro/application"
	"github.com/kongken/mono/internal/metro/ent"
)

// Schema returns a connection bound to an empty throwaway schema, removed on cleanup.
func Schema(t testing.TB) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN (make test-integration)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "metro_test_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) })
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = scoped.Close() })
	return scoped
}

// DB returns an Ent client bound to a throwaway schema with the tables created
// and the catalog_state row seeded. Everything is removed on cleanup.
func DB(t testing.TB) *ent.Client {
	t.Helper()
	scoped := Schema(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	if err := db.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	if err := application.EnsureCatalogState(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
