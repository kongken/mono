package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

const migrationsDir = "../../../migrations/usersrv"

func TestMigrationDirectoryChecksum(t *testing.T) {
	dir, err := migrate.NewLocalDir(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Validate(dir); err != nil {
		t.Fatalf("atlas.sum does not match the files; regenerate with make usersrv-migration: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	for _, f := range files {
		body, _ := os.ReadFile(f)
		if up := strings.ToUpper(string(body)); strings.Contains(up, "DROP TABLE") || strings.Contains(up, "DROP COLUMN") || strings.Contains(up, "TRUNCATE") {
			t.Fatalf("%s: destructive statements belong in a manual, reviewed script, not in a startup migration", f)
		}
	}
}

// The versioned files, applied the way the deployment notes say (one
// transaction per file), must produce exactly the schema Ent expects, and the
// database must enforce the account invariants on its own.
func TestVersionedMigrationsMatchEntAndEnforceInvariants(t *testing.T) {
	scoped, _ := testutil.Schema(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("migrations missing: %v %v", files, err)
	}
	sort.Strings(files)
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := scoped.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, scoped)))
	reference := testutil.DB(t)
	if got, want := pending(t, ctx, client), pending(t, ctx, reference); got != want {
		t.Fatalf("migrations differ from the Ent schema.\nmigrated database wants:\n%s\nEnt-created database wants:\n%s", got, want)
	}

	exec := func(q string, args ...any) error {
		_, err := scoped.ExecContext(ctx, q, args...)
		return err
	}
	const (
		u1 = "00000000-0000-0000-0000-000000000001"
		u2 = "00000000-0000-0000-0000-000000000002"
	)
	insertUser := func(id, email string, extra string) error {
		return exec(`INSERT INTO users (id, email_canonical, email_verified_at, password_hash, created_at, updated_at`+extra+`) VALUES ($1,$2,now(),'$argon2id$x',now(),now())`, id, email)
	}
	if err := insertUser(u1, "a@example.com", ""); err != nil {
		t.Fatal(err)
	}
	mustFail := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("the database accepted: %s", what)
		}
	}
	mustFail("a second account for one canonical email", insertUser(u2, "a@example.com", ""))
	mustFail("an upper-case canonical email", insertUser(u2, "B@example.com", ""))
	mustFail("a status outside active/disabled/deleting", exec(`UPDATE users SET status='banned' WHERE id=$1`, u1))
	mustFail("auth_version below 1", exec(`UPDATE users SET auth_version=0 WHERE id=$1`, u1))
	mustFail("an empty nickname", exec(`UPDATE users SET display_name='' WHERE id=$1`, u1))
	mustFail("a 41-character nickname", exec(`UPDATE users SET display_name=$2 WHERE id=$1`, u1, strings.Repeat("字", 41)))
	mustFail("a deleting account without a workflow", exec(`UPDATE users SET status='deleting' WHERE id=$1`, u1))
	mustFail("a workflow on an active account", exec(`UPDATE users SET deletion_mutation_id='m', deletion_started_at=now() WHERE id=$1`, u1))
	if err := insertUser(u2, "b@example.com", ""); err != nil {
		t.Fatal(err)
	}

	// Roles: whitelist, one per user and role, removed with the user.
	mustFail("a role outside the whitelist", exec(`INSERT INTO user_roles (id, user_id, role, granted_at) VALUES (gen_random_uuid(), $1, 'root', now())`, u1))
	if err := exec(`INSERT INTO user_roles (id, user_id, role, granted_at) VALUES (gen_random_uuid(), $1, 'metro-admin', now())`, u1); err != nil {
		t.Fatal(err)
	}
	mustFail("the same role twice", exec(`INSERT INTO user_roles (id, user_id, role, granted_at) VALUES (gen_random_uuid(), $1, 'metro-admin', now())`, u1))
	mustFail("a role for a missing user", exec(`INSERT INTO user_roles (id, user_id, role, granted_at) VALUES (gen_random_uuid(), gen_random_uuid(), 'metro-admin', now())`))

	// Tokens: 32-byte digests, purpose-specific bindings, unique digests.
	digest := strings.Repeat("a", 32)
	insertToken := func(hash, purpose string, user any, version any) error {
		return exec(`INSERT INTO account_tokens (id, token_hash, purpose, email_canonical, user_id, issued_auth_version, app_id, expires_at, created_at)
			VALUES (gen_random_uuid(), $1::bytea, $2, 'c@example.com', $3, $4, 'trip-map', now()+interval '10 minutes', now())`, hash, purpose, user, version)
	}
	if err := insertToken(digest, "registration", nil, nil); err != nil {
		t.Fatal(err)
	}
	mustFail("a reused token digest", insertToken(digest, "registration", nil, nil))
	mustFail("a 16-byte digest", insertToken(strings.Repeat("b", 16), "registration", nil, nil))
	mustFail("a registration token bound to a user", insertToken(strings.Repeat("c", 32), "registration", u1, 1))
	mustFail("a reset token without a user", insertToken(strings.Repeat("d", 32), "password_reset", nil, nil))
	mustFail("a reset token without a version", insertToken(strings.Repeat("e", 32), "password_reset", u1, nil))
	mustFail("an unknown purpose", insertToken(strings.Repeat("f", 32), "login", nil, nil))
	if err := insertToken(strings.Repeat("g", 32), "password_reset", u1, 1); err != nil {
		t.Fatal(err)
	}
	mustFail("a token that expires before it was created", exec(`INSERT INTO account_tokens (id, token_hash, purpose, email_canonical, app_id, expires_at, created_at)
		VALUES (gen_random_uuid(), $1::bytea, 'registration', 'd@example.com', 'metro', now()-interval '1 hour', now())`, strings.Repeat("h", 32)))

	// Deleting the user removes the roles; tokens and audit rows are cleaned by the lifecycle code.
	if err := exec(`DELETE FROM users WHERE id=$1`, u1); err != nil {
		t.Fatal(err)
	}
	var roles int
	if err := scoped.QueryRowContext(ctx, `SELECT count(*) FROM user_roles`).Scan(&roles); err != nil || roles != 0 {
		t.Fatalf("%d roles survive the user: %v", roles, err)
	}
	// Columns that must never exist: no plaintext secret, no email in audit or tombstones.
	rows, err := scoped.QueryContext(ctx, `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		switch {
		case column == "token" || column == "password" || column == "plaintext":
			t.Errorf("%s.%s stores a secret in clear", table, column)
		case (table == "security_audit" || table == "deletion_tombstones") && strings.Contains(column, "email"):
			t.Errorf("%s.%s keeps an email", table, column)
		}
	}
}

func pending(t *testing.T, ctx context.Context, c *ent.Client) string {
	t.Helper()
	var b bytes.Buffer
	if err := c.Schema.WriteTo(ctx, &b); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(b.String())
}
