package app

import (
	"context"
	"github.com/kongken/mono/internal/usersrv/ent"
)

// migrateSchema is the development auto_migrate path. Ent adds tables/columns
// but does not replace an existing CHECK expression. Keep this explicit update
// aligned with 20261102182700_security_mail_templates.sql; production applies
// the versioned migrations with auto_migrate=false.
func migrateSchema(ctx context.Context, db *ent.Client) error {
	if err := db.Schema.Create(ctx); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `ALTER TABLE mail_jobs DROP CONSTRAINT IF EXISTS mail_jobs_template,
ADD CONSTRAINT mail_jobs_template CHECK (template IN ('verify_email','reset_password','external_registration','identity_operation','identity_link','identity_unlink','password_set_first'))`)
	return err
}
