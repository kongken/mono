package integration

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/usersrv/ent"
	"github.com/kongken/mono/internal/usersrv/ent/authchallenge"
	"github.com/kongken/mono/internal/usersrv/testutil"
)

func TestSecurityMigrationPreservesOldMailAndWidensActualCheck(t *testing.T) {
	raw, _ := testutil.Schema(t)
	ctx := context.Background()
	files, err := filepath.Glob(filepath.Join(migrationsDir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		if filepath.Base(file) < "20261102182653" {
			applyExternalMigration(t, ctx, raw, file)
		}
	}
	uid, oldJob := uuid.New(), uuid.New()
	if _, err := raw.ExecContext(ctx, `INSERT INTO users(id,email_canonical,email_verified_at,password_hash,created_at,updated_at) VALUES($1,'old-security@example.com',now(),'$argon2id$old',now(),now())`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO mail_jobs(id,challenge_id,provider,generation,template,ciphertext,key_id,next_attempt_at,expires_at,created_at,updated_at) VALUES($1,$2,'sendgrid',1,'reset_password',$3,'old-key',now(),now()+interval '1 hour',now(),now())`, oldJob, uuid.New(), []byte("old encrypted mail")); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if filepath.Base(file) >= "20261102182653" {
			applyExternalMigration(t, ctx, raw, file)
		}
	}
	var source string
	var cipher []byte
	if err := raw.QueryRowContext(ctx, `SELECT source_kind,ciphertext FROM mail_jobs WHERE id=$1`, oldJob).Scan(&source, &cipher); err != nil || source != "account_token" || !bytes.Equal(cipher, []byte("old encrypted mail")) {
		t.Fatal("upgrade changed old queued mail")
	}
	for _, template := range []string{"verify_email", "reset_password", "external_registration", "identity_operation", "identity_link", "identity_unlink", "password_set_first"} {
		if _, err := raw.ExecContext(ctx, `INSERT INTO mail_jobs(id,challenge_id,provider,generation,template,ciphertext,key_id,next_attempt_at,expires_at,created_at,updated_at) VALUES($1,$2,'sendgrid',1,$3,$4,'k1',now(),now()+interval '1 hour',now(),now())`, uuid.New(), uuid.New(), template, []byte("cipher")); err != nil {
			t.Fatalf("upgraded template whitelist refused %s: %v", template, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `UPDATE mail_jobs SET template='arbitrary' WHERE id=$1`, oldJob); err == nil {
		t.Fatal("upgraded whitelist became open-ended")
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, raw)))
	c := client.AuthChallenge.Create().SetKind(authchallenge.KindExternalLogin).SetProvider(authchallenge.ProviderGoogle).SetIssuer("https://accounts.google.com").SetProviderConfigVersion(1).SetContextHash(bytes.Repeat([]byte{1}, 32)).SetCreatedAt(time.Now()).SetExpiresAt(time.Now().Add(time.Minute)).SaveX(ctx)
	for _, change := range []string{"kind='unknown'", "status='unknown'", "context_hash='short'::bytea", "email_token_hash='short'::bytea", "auth_version=1", "proof_key_id='key-without-cipher'", "status='completed'", "issuer='https://untrusted.example.com'", "provider_config_version=NULL", "result_user_id=gen_random_uuid()"} {
		if _, err := raw.ExecContext(ctx, `UPDATE auth_challenges SET `+change+` WHERE id=$1`, c.ID); err == nil {
			t.Fatalf("database accepted invalid challenge: %s", change)
		}
	}
}
