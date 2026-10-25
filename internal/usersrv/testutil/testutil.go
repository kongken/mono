// Package testutil provides isolated PostgreSQL and Redis state for usersrv
// tests. Tests are skipped unless USERSRV_TEST_POSTGRES_DSN (and, for Redis
// tests, USERSRV_TEST_REDIS_ADDR) are set; see make test-integration-usersrv.
// They share the development servers of the other services but never touch
// their data: each test gets a throwaway schema and a unique Redis key prefix.
package testutil

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/passwordhash"
	"github.com/redis/go-redis/v9"
)

// Hasher returns a hasher with cheap parameters so tests stay fast; the real
// parameters are covered by the passwordhash tests.
func Hasher(t testing.TB) *passwordhash.Hasher {
	t.Helper()
	h, err := passwordhash.New(passwordhash.Config{MemoryKiB: 64, Iterations: 1, Parallelism: 1, MaxConcurrent: 4})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// DSN returns the test PostgreSQL DSN or skips the test.
func DSN(t testing.TB) string {
	t.Helper()
	dsn := os.Getenv("USERSRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set USERSRV_TEST_POSTGRES_DSN (make test-integration-usersrv)")
	}
	return dsn
}

// Schema creates a throwaway schema and returns a database handle whose
// search_path is that schema. Everything is removed on cleanup.
func Schema(t testing.TB) (*sql.DB, string) {
	t.Helper()
	dsn := DSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "usersrv_test_" + uuid.NewString()[:8]
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
	return scoped, schema
}

// DB returns an Ent client bound to a throwaway schema with the tables created.
func DB(t testing.TB) *ent.Client {
	t.Helper()
	db, _ := DBWithSQL(t)
	return db
}

// DBWithSQL is DB plus the scoped *sql.DB under it, for tests that must write a row
// state no code path produces (a hand-edited or pre-migration row).
func DBWithSQL(t testing.TB) (*ent.Client, *sql.DB) {
	t.Helper()
	scoped, _ := Schema(t)
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	return db, scoped
}

// Redis returns a client and a unique key prefix, and removes the keys under
// the prefix on cleanup. Skips unless USERSRV_TEST_REDIS_ADDR is set.
func Redis(t testing.TB) (*redis.Client, string) {
	t.Helper()
	addr := os.Getenv("USERSRV_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set USERSRV_TEST_REDIS_ADDR (make test-integration-usersrv)")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("USERSRV_TEST_REDIS_PASSWORD")})
	prefix := "usersrv_test:" + uuid.NewString()[:8] + ":"
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		iter := rdb.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			rdb.Del(ctx, iter.Val())
		}
		_ = rdb.Close()
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis: %v", err)
	}
	return rdb, prefix
}

// SchemaDSN is like DB but returns a DSN (with search_path set to the throwaway
// schema, tables created) for tools that open their own connection.
func SchemaDSN(t testing.TB) string {
	t.Helper()
	scoped, schema := Schema(t)
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("USERSRV_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
