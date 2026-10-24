-- MANUAL ONLY. Run against the BFF database after the usersrv release has served
-- registration, sign-in, Me, sign-out and account deletion without problems.
-- Preconditions: every BFF replica runs the usersrv release, so nothing reads the
-- old tables any more; the versioned migration 20261024161800_usersrv_sessions.sql
-- has been applied. Old accounts, identity mappings and receipts are deliberately
-- NOT migrated. After this the database cannot be rolled back to the Logto release
-- without restoring a backup.
-- No CASCADE: unexpected dependencies must stop the operation for review.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
  IF to_regclass('bff_account_deletions') IS NULL OR to_regclass('auth_browser_sessions') IS NULL THEN
    RAISE EXCEPTION 'Apply the usersrv session migration in the BFF database first';
  END IF;
  -- A deletion still running under the new release must finish first.
  IF EXISTS (SELECT 1 FROM bff_account_deletions WHERE stage <> 'done') THEN
    RAISE EXCEPTION 'Unfinished account deletions exist; let them complete first';
  END IF;
END $$;
DROP TABLE IF EXISTS auth_identities;
DROP TABLE IF EXISTS account_deletion_receipts;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS auth_users;
-- Kept: auth_browser_sessions (browser sessions), bff_access_tokens (management
-- tokens), bff_account_deletions (deletion workflows and receipts).
COMMIT;
