package auth_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestBFFMigrationChecksum(t *testing.T) {
	d, err := migrate.NewLocalDir("../../../migrations/bff")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Validate(d); err != nil {
		t.Fatal(err)
	}
}

func TestLogtoMigrationAndManualCleanup(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "logto_migr_" + uuid.NewString()[:8]
	if _, err := admin.ExecContext(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(`DROP SCHEMA "` + schema + `" CASCADE`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(path string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(b))
	}
	files, err := filepath.Glob("../../../migrations/bff/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i, file := range files {
		apply(file)
		if i == 0 {
			exec(`INSERT INTO auth_users (id,username,password_hash,created_at,updated_at) VALUES ('old','old','old-hash',now(),now());
			INSERT INTO auth_sessions (id,token_hash,created_at,expires_at,user_id) VALUES ('old-session','old-hash',now(),now()+interval '1 day','old')`)
		}
	}
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM auth_users`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("migration removed old data: %d %v", n, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM auth_identities`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old identity migrated: %d %v", n, err)
	}
	exec(`INSERT INTO auth_identities (id,issuer,external_id,created_at,updated_at) VALUES ('new','https://identity.example/oidc','subject',now(),now());
	INSERT INTO auth_browser_sessions (id,token_hash,created_at,expires_at,user_id) VALUES ('new-session','new-hash',now(),now()+interval '1 day','new')`)
	for range 2 {
		apply("../../../deploy/sql/bff/retire_password_accounts.sql")
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM auth_browser_sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("cleanup damaged live sessions: %d %v", n, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='auth_identities' AND column_name='password_hash'`, schema).Scan(&n); err != nil || n != 0 {
		t.Fatalf("credential column exists: %d %v", n, err)
	}
}
