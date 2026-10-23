-- MANUAL ONLY. Run against the BFF database, never the Logto database.
-- Preconditions: all BFF replicas run the Logto-only release; its versioned
-- migration has been applied and Logto login/Me/business writes verified.
-- Old test accounts and their sessions are deliberately NOT migrated.
-- No CASCADE: unexpected dependencies must stop the operation for review.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
  IF to_regclass('auth_identities') IS NULL OR to_regclass('auth_browser_sessions') IS NULL THEN
    RAISE EXCEPTION 'Apply the Logto identity migration in the BFF database first';
  END IF;
END $$;
DROP TABLE IF EXISTS auth_sessions;
DROP TABLE IF EXISTS auth_users;
-- Keep auth_identities, auth_browser_sessions and account_deletion_receipts:
-- they implement identity mapping, browser sessions and cleanup retry receipts.
COMMIT;
