-- Create "admin_audit_logs" table
CREATE TABLE "admin_audit_logs" ("id" character varying NOT NULL, "actor_user_id" character varying NOT NULL, "channel" character varying NOT NULL, "token_id" character varying NOT NULL DEFAULT '', "request_id" character varying NOT NULL DEFAULT '', "action" character varying NOT NULL, "entity_type" character varying NOT NULL, "entity_id" character varying NOT NULL, "before_json" text NOT NULL DEFAULT '', "after_json" text NOT NULL DEFAULT '', "created_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "adminauditlog_actor_user_id_created_at" to table: "admin_audit_logs"
CREATE INDEX "adminauditlog_actor_user_id_created_at" ON "admin_audit_logs" ("actor_user_id", "created_at");
-- Create index "adminauditlog_created_at" to table: "admin_audit_logs"
CREATE INDEX "adminauditlog_created_at" ON "admin_audit_logs" ("created_at");
-- Create index "adminauditlog_entity_type_entity_id_created_at" to table: "admin_audit_logs"
CREATE INDEX "adminauditlog_entity_type_entity_id_created_at" ON "admin_audit_logs" ("entity_type", "entity_id", "created_at");
