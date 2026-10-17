-- Retire tripmap-side session tables. Legacy credential columns on users stay
-- until operators run bff-import-legacy-accounts and optionally apply
-- deploy/sql/tripmap/post_import_drop_legacy_auth_columns.sql (not in atlas).

DROP TABLE IF EXISTS "auth_sessions";
DROP TABLE IF EXISTS "account_deletion_receipts";
