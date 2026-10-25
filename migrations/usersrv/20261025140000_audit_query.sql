-- Modify "admin_mutations" table
ALTER TABLE "admin_mutations" ADD COLUMN "audit_id" uuid NULL;
-- Modify "security_audit" table
ALTER TABLE "security_audit" ADD COLUMN "origin_id" uuid NULL;
-- Create index "securityaudit_action_created_at" to table: "security_audit"
CREATE INDEX "securityaudit_action_created_at" ON "security_audit" ("action", "created_at");
-- Create index "securityaudit_actor_user_id_created_at" to table: "security_audit"
CREATE INDEX "securityaudit_actor_user_id_created_at" ON "security_audit" ("actor_user_id", "created_at");
-- Classify the rows that predate the channel: where the actor came from is now always stored, so the
-- console can filter and label by channel without guessing. The order matters: an administrator's
-- console read ("admin.users.*", actor user:<uuid>) is a web row with a real operator UUID before
-- any other user:<uuid> row is taken for a self-service event; what is left was written by the CLI,
-- whose actor is its controlled operator name.
UPDATE "security_audit" SET "channel" = 'system' WHERE "channel" = '' AND "actor" = 'system';
UPDATE "security_audit" SET "channel" = 'web', "actor_user_id" = substr("actor", 6)::uuid
  WHERE "channel" = '' AND "action" LIKE 'admin.users.%'
    AND "actor" ~ '^user:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
UPDATE "security_audit" SET "channel" = 'self'
  WHERE "channel" = '' AND "actor" ~ '^user:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
UPDATE "security_audit" SET "channel" = 'cli' WHERE "channel" = '';
