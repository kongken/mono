-- Optional cleanup AFTER bff-import-legacy-accounts verified the BFF auth DB.
-- Not applied by atlas migrations; run manually when retiring legacy columns.

DROP INDEX IF EXISTS "user_provider_external_id";
DROP INDEX IF EXISTS "users_email_key";

ALTER TABLE "users" DROP COLUMN IF EXISTS "email";
ALTER TABLE "users" DROP COLUMN IF EXISTS "provider";
ALTER TABLE "users" DROP COLUMN IF EXISTS "external_id";
ALTER TABLE "users" DROP COLUMN IF EXISTS "password_hash";
ALTER TABLE "users" DROP COLUMN IF EXISTS "disabled";
ALTER TABLE "users" DROP COLUMN IF EXISTS "deletion_requested_at";
