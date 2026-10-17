-- Align baseline index names with the Ent schema for legacy auth columns.
ALTER INDEX IF EXISTS "users_email_key" RENAME TO "user_email";
