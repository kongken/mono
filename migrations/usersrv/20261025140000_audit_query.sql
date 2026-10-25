-- Modify "admin_mutations" table
ALTER TABLE "admin_mutations" ADD COLUMN "audit_id" uuid NULL;
-- Modify "security_audit" table
ALTER TABLE "security_audit" ADD COLUMN "origin_id" uuid NULL, ADD COLUMN "reason_checked" boolean NOT NULL DEFAULT false;
-- Create index "securityaudit_action_created_at" to table: "security_audit"
CREATE INDEX "securityaudit_action_created_at" ON "security_audit" ("action", "created_at");
-- Create index "securityaudit_actor_user_id_created_at" to table: "security_audit"
CREATE INDEX "securityaudit_actor_user_id_created_at" ON "security_audit" ("actor_user_id", "created_at");
-- Mark the reasons that are known to be safe to show. Free text written before the console could
-- show it was never checked for an email, a password or a token (the CLI checked only its length until
-- the web changes arrived), so it stays unchecked and a reader hides it. The reason of a console change
-- (channel web) went through the content check when it was written.
UPDATE "security_audit" SET "reason_checked" = true WHERE "channel" = 'web';
-- Classify the rows that predate the channel: where the actor came from is now always stored, so the
-- console can filter and label by channel without guessing. The actor text alone proves nothing: the
-- CLI's controlled operator name is free-form ([A-Za-z0-9._:@-], 1-64), so "system" and "user:<uuid>"
-- are legal operator names. The rows are therefore classified by the action first, which only one
-- source ever wrote, and only then by the actor:
--   1. the CLI's actions (role.grant:*, role.revoke:*, account.enable, account.disable) -> cli, whatever
--      the operator is called;
--   2. an administrator's console read (admin.users.*, actor user:<uuid>) -> web, with that UUID (its
--      reason is a description the code generated, so it is marked checked);
--   3. the system's own flows (registration.request, password_reset.request, actor system) -> system;
--   4. an account holder's own flows -> self, but only when the actor names the very account the row is
--      about (actor = 'user:' || user_id): that is how those flows wrote it and the only reliable proof;
--   5. whatever is left cannot be attributed to a web or self identity, and stays a command-line row
--      with its actor text as the controlled label, which the console never shows as an account.
UPDATE "security_audit" SET "channel" = 'cli'
  WHERE "channel" = '' AND ("action" LIKE 'role.grant:%' OR "action" LIKE 'role.revoke:%' OR "action" IN ('account.enable', 'account.disable'));
UPDATE "security_audit" SET "channel" = 'web', "reason_checked" = true, "actor_user_id" = substr("actor", 6)::uuid
  WHERE "channel" = '' AND "action" LIKE 'admin.users.%'
    AND "actor" ~ '^user:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
UPDATE "security_audit" SET "channel" = 'system'
  WHERE "channel" = '' AND "actor" = 'system' AND "action" IN ('registration.request', 'password_reset.request');
UPDATE "security_audit" SET "channel" = 'self'
  WHERE "channel" = '' AND "user_id" IS NOT NULL AND "actor" = 'user:' || "user_id"::text
    AND "action" IN ('account.create', 'login', 'password.verify', 'password.change', 'password.reset', 'deletion.begin', 'deletion.finish');
UPDATE "security_audit" SET "channel" = 'cli' WHERE "channel" = '';
