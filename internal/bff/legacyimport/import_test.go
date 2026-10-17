package legacyimport_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/kongken/mono/internal/bff/legacyimport"
)

func TestImportPreservesIDsAndIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	tripSchema := "legacy_import_trip_" + uuid.NewString()[:8]
	bffSchema := "legacy_import_bff_" + uuid.NewString()[:8]
	for _, s := range []string{tripSchema, bffSchema} {
		if _, err := admin.ExecContext(ctx, `CREATE SCHEMA "`+s+`"`); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, s := range []string{tripSchema, bffSchema} {
			_, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS "`+s+`" CASCADE`)
		}
	})
	tripDSN := scopedDSN(t, dsn, tripSchema)
	bffDSN := scopedDSN(t, dsn, bffSchema)
	seedTripmap(t, ctx, tripDSN)
	seedBFFAuth(t, ctx, bffDSN)

	rep1, err := legacyimport.Import(ctx, tripDSN, bffDSN, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep1.Inserted != 2 || rep1.Updated != 0 {
		t.Fatalf("first import: %+v", rep1)
	}
	verifyPair(t, ctx, tripDSN, bffDSN, "user-a", "user-b")

	rep2, err := legacyimport.Import(ctx, tripDSN, bffDSN, false)
	if err != nil || rep2.Unchanged != 2 || rep2.Inserted != 0 {
		t.Fatalf("second import: %+v err=%v", rep2, err)
	}

	dry, err := legacyimport.Import(ctx, tripDSN, bffDSN, true)
	if err != nil || dry.Unchanged != 2 {
		t.Fatalf("dry-run: %+v err=%v", dry, err)
	}
}

func scopedDSN(t *testing.T, dsn, schema string) string {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	return stdlib.RegisterConnConfig(cfg)
}

func seedTripmap(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `
CREATE TABLE users (
  id varchar PRIMARY KEY, email varchar NULL, username varchar NULL,
  display_name varchar NOT NULL DEFAULT '', avatar_url varchar NOT NULL DEFAULT '',
  provider varchar NOT NULL DEFAULT '', external_id varchar NOT NULL DEFAULT '',
  password_hash varchar NOT NULL DEFAULT '', disabled boolean NOT NULL DEFAULT false,
  deletion_requested_at timestamptz NULL, revision bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = db.ExecContext(ctx, `
INSERT INTO users (id, username, email, password_hash, display_name, created_at, updated_at)
VALUES ('user-a','alice','alice@example.com','hash-a','Alice',$1,$1),
       ('user-b',NULL,NULL,'','B-only',$1,$1)`, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
UPDATE users SET provider='github', external_id='99', password_hash='', disabled=true WHERE id='user-b'`)
	if err != nil {
		t.Fatal(err)
	}
}

func seedBFFAuth(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `
CREATE TABLE auth_users (
  id varchar PRIMARY KEY, username varchar NULL, display_name varchar NOT NULL DEFAULT '',
  avatar_url varchar NOT NULL DEFAULT '', provider varchar NOT NULL DEFAULT '',
  external_id varchar NOT NULL DEFAULT '', password_hash varchar NOT NULL DEFAULT '',
  disabled boolean NOT NULL DEFAULT false, deletion_requested_at timestamptz NULL,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
)`)
	if err != nil {
		t.Fatal(err)
	}
}

func verifyPair(t *testing.T, ctx context.Context, tripDSN, bffDSN string, ids ...string) {
	t.Helper()
	trip, _ := sql.Open("pgx", tripDSN)
	bff, _ := sql.Open("pgx", bffDSN)
	defer trip.Close()
	defer bff.Close()
	for _, id := range ids {
		var hash, provider, external string
		if err := trip.QueryRowContext(ctx, `SELECT password_hash, provider, external_id FROM users WHERE id=$1`, id).
			Scan(&hash, &provider, &external); err != nil {
			t.Fatal(err)
		}
		var gotHash, gotProvider, gotExternal string
		if err := bff.QueryRowContext(ctx, `SELECT password_hash, provider, external_id FROM auth_users WHERE id=$1`, id).
			Scan(&gotHash, &gotProvider, &gotExternal); err != nil {
			t.Fatalf("%s missing in bff: %v", id, err)
		}
		if gotHash != hash || gotProvider != provider || gotExternal != external {
			t.Fatalf("%s mismatch bff=%q/%q/%q trip=%q/%q/%q", id, gotHash, gotProvider, gotExternal, hash, provider, external)
		}
	}
}
