-- Drop browser-session and account-credential storage from tripmap. Auth data lives
-- in the BFF database (migrations/bff/). Business rows in users keep the same IDs
-- and footprint columns; credential columns are removed without deleting user rows.

DROP TABLE IF EXISTS "auth_sessions";
DROP TABLE IF EXISTS "account_deletion_receipts";

DROP INDEX IF EXISTS "user_provider_external_id";
DROP INDEX IF EXISTS "users_email_key";

ALTER TABLE "users" DROP COLUMN IF EXISTS "email";
ALTER TABLE "users" DROP COLUMN IF EXISTS "provider";
ALTER TABLE "users" DROP COLUMN IF EXISTS "external_id";
ALTER TABLE "users" DROP COLUMN IF EXISTS "password_hash";
ALTER TABLE "users" DROP COLUMN IF EXISTS "disabled";
ALTER TABLE "users" DROP COLUMN IF EXISTS "deletion_requested_at";
