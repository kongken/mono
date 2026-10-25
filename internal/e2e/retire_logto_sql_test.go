package e2e_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// The manual BFF clean-up script (docs/usersrv/retirement.md) is destructive, so it is
// exercised on a real PostgreSQL: a database migrated through every BFF migration, holding
// rows of the Logto era. The script refuses to run while an account deletion is unfinished,
// and then drops exactly the retired tables and nothing else.
func TestRetireLogtoIdentitiesScript(t *testing.T) {
	dsn := os.Getenv("TRIPMAP_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TRIPMAP_TEST_POSTGRES_DSN for live PostgreSQL checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "bff_retire_" + uuid.NewString()[:8]
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA "`+schema+`" CASCADE`) }()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	scoped := stdlib.OpenDB(*cfg)
	defer scoped.Close()
	// One connection throughout: the script opens its own transaction.
	conn, err := scoped.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	files, err := filepath.Glob("../../migrations/bff/*.sql")
	if err != nil || len(files) < 2 {
		t.Fatalf("bff migrations missing: %v %v", files, err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
	}
	exists := func(table string) bool {
		t.Helper()
		var reg sql.NullString
		if err := conn.QueryRowContext(ctx, `SELECT to_regclass($1)::text`, table).Scan(&reg); err != nil {
			t.Fatal(err)
		}
		return reg.Valid
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec := func(q string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	// Logto-era rows, a session of the new release and a deletion workflow in progress.
	exec(`INSERT INTO auth_identities (id, issuer, external_id, created_at, updated_at) VALUES ('legacy-1','https://logto.example/oidc','sub-1',now(),now())`)
	exec(`INSERT INTO account_deletion_receipts (id, token_hash, expires_at) VALUES ('r1','h1',now() + interval '1 day')`)
	exec(`INSERT INTO auth_browser_sessions (id, token_hash, created_at, expires_at, user_id, auth_version)
		VALUES ('s-new','sha-new',now(),now() + interval '1 day','00000000-0000-0000-0000-000000000001',1)`)
	exec(`INSERT INTO bff_account_deletions (id, user_id, mutation_id, token_hash, products, completed_products, stage, next_attempt_at, created_at, updated_at)
		VALUES ('w1','00000000-0000-0000-0000-000000000002','m1','t1','["tripmap"]','[]','cleanup',now(),now(),now())`)

	script, err := os.ReadFile("../../deploy/sql/bff/retire_logto_identities.sql")
	if err != nil {
		t.Fatal(err)
	}
	retired := []string{"auth_identities", "account_deletion_receipts", "auth_sessions", "auth_users"}

	// An unfinished deletion stops the script, and the failed transaction drops nothing.
	if _, err = conn.ExecContext(ctx, string(script)); err == nil {
		t.Fatal("the script ran while an account deletion was unfinished")
	}
	exec(`ROLLBACK`)
	for _, table := range retired {
		if !exists(table) {
			t.Fatalf("%s was dropped although the script stopped", table)
		}
	}

	exec(`UPDATE bff_account_deletions SET stage = 'done'`)
	for i := 0; i < 2; i++ { // and it can be repeated
		if _, err = conn.ExecContext(ctx, string(script)); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
		for _, table := range retired {
			if exists(table) {
				t.Fatalf("run %d: %s still exists", i+1, table)
			}
		}
	}
	// What the new release uses is untouched.
	if count("auth_browser_sessions") != 1 || count("bff_account_deletions") != 1 || !exists("bff_access_tokens") {
		t.Fatal("the script touched tables the new release uses")
	}
}
