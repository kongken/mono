// Package testutil provides isolated PostgreSQL and Redis handles for tests
// that need real backing services. Tests are skipped unless
// TRIPMAP_TEST_POSTGRES_DSN and TRIPMAP_TEST_REDIS_ADDR are set (see
// make test-integration).
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
	"github.com/redis/go-redis/v9"
	"github.com/kongken/mono/internal/tripmap/ent"
)

// Backing returns an Ent client bound to a throwaway schema and a Redis client
// plus a key prefix unique to this test. Everything is removed on cleanup.
func Backing(t *testing.T) (db *ent.Client, rdb *redis.Client, prefix string) {
	t.Helper()
	dsn, addr := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN"), os.Getenv("TRIPMAP_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN and TRIPMAP_TEST_REDIS_ADDR (make test-integration)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "tripmap_test_" + uuid.NewString()[:8]
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
	db = ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	if err = db.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	rdb = redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("TRIPMAP_TEST_REDIS_PASSWORD")})
	prefix = "tripmap_test_" + uuid.NewString()[:8] + ":"
	t.Cleanup(func() {
		keys, _ := rdb.Keys(context.Background(), prefix+"*").Result()
		if len(keys) > 0 {
			_ = rdb.Del(context.Background(), keys...).Err()
		}
		_ = rdb.Close()
	})
	return db, rdb, prefix
}
