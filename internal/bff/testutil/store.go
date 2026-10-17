package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/bff/ent"
	"github.com/redis/go-redis/v9"
)

// Backing returns an isolated BFF Ent client and Redis prefix when TRIPMAP_TEST_* is set.
func Backing(t *testing.T) (*ent.Client, *redis.Client, string) {
	t.Helper()
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	addr := os.Getenv("TRIPMAP_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("TRIPMAP_TEST_POSTGRES_DSN and TRIPMAP_TEST_REDIS_ADDR required")
	}
	schema := "bff_test_" + uuid.NewString()[:8]
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	if _, err = admin.ExecContext(context.Background(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) })
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	prefix := fmt.Sprintf("bff_test_%s:", uuid.NewString()[:8])
	opts := &redis.Options{Addr: addr}
	if p := os.Getenv("TRIPMAP_TEST_REDIS_PASSWORD"); p != "" {
		opts.Password = p
	}
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	return client, rdb, prefix
}
