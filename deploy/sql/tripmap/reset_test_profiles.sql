-- OPTIONAL, DESTRUCTIVE, MANUAL ONLY. Run against tripmap BEFORE enabling new
-- Logto logins, with all application replicas and maintenance workers stopped.
-- The operator confirmed all existing cloud data is disposable test data.
-- Deletes ALL business profiles and FK-related footprints/media metadata/etc.
-- Does NOT delete S3 objects. Inventory/clean the project's test S3 objects
-- separately BEFORE deleting metadata if physical object removal is required.
-- Never run this against BFF or Logto. Never run after accepting real users.
BEGIN;
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
  IF to_regclass('city_visits') IS NULL OR to_regclass('province_marks') IS NULL THEN
    RAISE EXCEPTION 'This script requires the tripmap business database';
  END IF;
END $$;
DELETE FROM users;
-- Keep all business tables, and keep media_cleanups for delayed staging cleanup.
COMMIT;
