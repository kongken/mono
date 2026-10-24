-- MANUAL ONLY: tripmap database, never BFF, usersrv or metro. Historical filename kept.
-- Deploy the usersrv-era tripmap to ALL replicas first: older Ent clients select these
-- columns and cannot run after this cleanup. No account import is needed.
-- See docs/usersrv/retirement.md.
-- These are credential remnants, not business profile columns.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
  IF to_regclass('city_visits') IS NULL OR to_regclass('province_marks') IS NULL THEN
    RAISE EXCEPTION 'This script requires the tripmap business database';
  END IF;
END $$;

DROP INDEX IF EXISTS "user_provider_external_id";
DROP INDEX IF EXISTS "users_email_key";
-- A cached username of the former identity provider is mutable and may be shared by different subjects.
DROP INDEX IF EXISTS "users_username_key";

ALTER TABLE "users" DROP COLUMN IF EXISTS "email";
ALTER TABLE "users" DROP COLUMN IF EXISTS "provider";
ALTER TABLE "users" DROP COLUMN IF EXISTS "external_id";
ALTER TABLE "users" DROP COLUMN IF EXISTS "password_hash";
ALTER TABLE "users" DROP COLUMN IF EXISTS "disabled";
ALTER TABLE "users" DROP COLUMN IF EXISTS "deletion_requested_at";
COMMIT;
