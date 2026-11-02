package app

import (
	"context"
	"github.com/kongken/mono/internal/usersrv/testutil"
	"strings"
	"testing"
)

func TestAutoMigrateWidensExistingMailTemplateConstraint(t *testing.T) {
	db, raw := testutil.DBWithSQL(t)
	ctx := context.Background()
	if _, err := raw.ExecContext(ctx, `ALTER TABLE mail_jobs DROP CONSTRAINT mail_jobs_template, ADD CONSTRAINT mail_jobs_template CHECK(template IN ('verify_email','reset_password'))`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := raw.QueryRowContext(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='mail_jobs'::regclass AND conname='mail_jobs_template'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "identity_operation") || !strings.Contains(definition, "password_set_first") {
		t.Fatal("auto migration retained the old whitelist")
	}
}
