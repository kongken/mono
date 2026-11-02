package integration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/account"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/externalidentity"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

const externalIdentityMigration = "20261102170537_external_identity_foundation.sql"

func applyExternalMigration(t *testing.T, ctx context.Context, db *sql.DB, file string) {
	t.Helper()
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalMigrationPreservesOldAccountsAndPasswordlessRollbackBoundary(t *testing.T) {
	raw, _ := testutil.Schema(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		if filepath.Base(f) < externalIdentityMigration {
			applyExternalMigration(t, ctx, raw, f)
		}
	}
	hasher := testutil.Hasher(t)
	hash, err := hasher.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := raw.ExecContext(ctx, `INSERT INTO users (id,email_canonical,email_verified_at,password_hash,display_name,auth_version,created_at,updated_at)
		VALUES ($1,'old@example.com',now(),$2,'历史用户',7,now(),now())`, id, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO user_roles (id,user_id,role,granted_at) VALUES ($1,$2,'metro-admin',now())`, uuid.New(), id); err != nil {
		t.Fatal(err)
	}
	applyExternalMigration(t, ctx, raw, filepath.Join(migrationsDir, externalIdentityMigration))
	db := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, raw)))
	u := db.User.GetX(ctx, id)
	if u.PasswordHash == nil || *u.PasswordHash != hash || u.PasswordSetAt != nil || u.AuthVersion != 7 || u.DisplayName != "历史用户" || u.QueryRoles().CountX(ctx) != 1 {
		t.Fatal("migration changed historical identity/password/roles or fabricated a password time")
	}
	svc := account.NewService(db, hasher)
	if got, st, err := svc.Authenticate(ctx, u.EmailCanonical, "correct horse battery"); err != nil || got != id || !st.HasPassword || st.AuthVersion != 7 {
		t.Fatalf("legacy login regressed: %v", err)
	}
	// A NULL hash is readable by the compatibility binary; an old NOT NULL
	// assumption cannot be restored after the first passwordless row appears.
	tx, err := db.Tx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	noPassword, err := tx.User.Create().SetEmailCanonical("new@example.com").SetEmailVerifiedAt(time.Now()).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.ExternalIdentity.Create().SetUserID(noPassword.ID).SetProvider(externalidentity.ProviderGoogle).
		SetIssuer("https://accounts.google.com").SetSubject("stable-google-subject").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var legacyString string
	if err := raw.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id=$1`, noPassword.ID).Scan(&legacyString); err == nil {
		t.Fatal("legacy string reader unexpectedly accepts NULL")
	}
	if _, err := raw.ExecContext(ctx, `ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL`); err == nil {
		t.Fatal("unsafe NOT NULL downgrade accepted")
	}
	if u := db.User.GetX(ctx, noPassword.ID); u.PasswordHash != nil || u.PasswordSetAt != nil {
		t.Fatal("compatibility handling fabricated a password")
	}
	if _, err := raw.ExecContext(ctx, `UPDATE users SET password_hash='' WHERE id=$1`, noPassword.ID); err == nil {
		t.Fatal("empty password hash accepted")
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, noPassword.ID); err != nil {
		t.Fatal(err)
	}
	if n := db.ExternalIdentity.Query().CountX(ctx); n != 0 {
		t.Fatal("identity survived owner deletion")
	}
}

func TestExternalIdentityDatabaseConstraintsUnderConcurrentInserts(t *testing.T) {
	for _, sameUser := range []bool{false, true} {
		t.Run(map[bool]string{false: "one owner per external key", true: "one identity per user and provider"}[sameUser], func(t *testing.T) {
			db := testutil.DB(t)
			ctx := context.Background()
			u1 := db.User.Create().SetEmailCanonical("one@example.com").SetEmailVerifiedAt(time.Now()).SetPasswordHash("$argon2id$fixture").SaveX(ctx)
			u2 := db.User.Create().SetEmailCanonical("two@example.com").SetEmailVerifiedAt(time.Now()).SetPasswordHash("$argon2id$fixture").SaveX(ctx)
			owners, subjects := []uuid.UUID{u1.ID, u2.ID}, []string{"101", "101"}
			if sameUser {
				owners[1], subjects[1] = u1.ID, "102"
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			for i := range 2 {
				go func(i int) {
					<-start
					results <- db.ExternalIdentity.Create().SetUserID(owners[i]).SetProvider(externalidentity.ProviderGithub).
						SetIssuer("https://github.com").SetSubject(subjects[i]).Exec(ctx)
				}(i)
			}
			close(start)
			wins, conflicts := 0, 0
			for range 2 {
				if err := <-results; err == nil {
					wins++
				} else if ent.IsConstraintError(err) {
					conflicts++
				} else {
					t.Fatal(err)
				}
			}
			if wins != 1 || conflicts != 1 || db.ExternalIdentity.Query().CountX(ctx) != 1 {
				t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
			}
		})
	}
}

func TestExternalIdentityRejectsInvalidKeysAndDoesNotLogSubjects(t *testing.T) {
	db := testutil.DB(t)
	ctx := context.Background()
	u := db.User.Create().SetEmailCanonical("one@example.com").SetEmailVerifiedAt(time.Now()).SetPasswordHash("$argon2id$fixture").SaveX(ctx)
	for _, tc := range []struct {
		provider        externalidentity.Provider
		issuer, subject string
	}{
		{externalidentity.ProviderGoogle, "https://attacker.example.com", "subject"},
		{externalidentity.ProviderGithub, "https://github.com", "login-name"},
		{externalidentity.ProviderGithub, "https://github.com", "00123"},
		{externalidentity.ProviderGoogle, "https://accounts.google.com", ""},
	} {
		if err := db.ExternalIdentity.Create().SetUserID(u.ID).SetProvider(tc.provider).SetIssuer(tc.issuer).SetSubject(tc.subject).Exec(ctx); err == nil {
			t.Fatal("invalid provider identity accepted")
		}
	}
	identity := db.ExternalIdentity.Create().SetUserID(u.ID).SetProvider(externalidentity.ProviderGoogle).
		SetIssuer("https://accounts.google.com").SetSubject("private-subject").SetEmailSnapshot("private@example.com").SaveX(ctx)
	if s := identity.String(); strings.Contains(s, "private-subject") || strings.Contains(s, "private@example.com") {
		t.Fatal("identity String exposes subject or email")
	}
	if err := db.ExternalIdentity.Create().SetUserID(uuid.New()).SetProvider(externalidentity.ProviderGoogle).
		SetIssuer("https://accounts.google.com").SetSubject("missing-owner").Exec(ctx); !ent.IsConstraintError(err) {
		t.Fatal("identity without owner accepted")
	}
}
