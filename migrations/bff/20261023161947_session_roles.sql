-- Modify "auth_browser_sessions" table
ALTER TABLE "auth_browser_sessions" ADD COLUMN "roles" jsonb NULL, ADD COLUMN "authenticated_at" timestamptz NULL;
