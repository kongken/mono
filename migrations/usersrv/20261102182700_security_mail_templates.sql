-- Ent does not diff replacement CHECK expressions on existing tables. Widen
-- the old template whitelist explicitly; existing jobs and purposes are intact.
ALTER TABLE "mail_jobs" DROP CONSTRAINT "mail_jobs_template",
  ADD CONSTRAINT "mail_jobs_template" CHECK (template IN ('verify_email', 'reset_password', 'external_registration', 'identity_operation', 'identity_link', 'identity_unlink', 'password_set_first'));
