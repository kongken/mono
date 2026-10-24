-- OPTIONAL, DESTRUCTIVE, MANUAL ONLY. Run against tripmap BEFORE opening sign-in on
-- usersrv, with all application replicas and maintenance workers stopped.
-- The operator confirmed all existing cloud data is disposable test data.
-- Deletes ALL business profiles and FK-related footprints/media metadata/etc.
-- Does NOT delete S3 objects. Inventory/clean the project's test S3 objects
-- separately BEFORE deleting metadata if physical object removal is required.
-- Never run this against the BFF, usersrv or metro database. Never run after accepting
-- real users. See docs/usersrv/retirement.md.
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
