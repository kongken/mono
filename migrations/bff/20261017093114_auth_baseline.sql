-- Create "auth_users" table
CREATE TABLE "auth_users" ("id" character varying NOT NULL, "username" character varying NULL, "display_name" character varying NOT NULL DEFAULT '', "avatar_url" character varying NOT NULL DEFAULT '', "provider" character varying NOT NULL DEFAULT '', "external_id" character varying NOT NULL DEFAULT '', "password_hash" character varying NOT NULL DEFAULT '', "disabled" boolean NOT NULL DEFAULT false, "deletion_requested_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "auth_users_username_key" to table: "auth_users"
CREATE UNIQUE INDEX "auth_users_username_key" ON "auth_users" ("username");
-- Create index "user_provider_external_id" to table: "auth_users"
CREATE UNIQUE INDEX "user_provider_external_id" ON "auth_users" ("provider", "external_id") WHERE ((external_id)::text <> ''::text);
-- Create "auth_sessions" table
CREATE TABLE "auth_sessions" ("id" character varying NOT NULL, "token_hash" character varying NOT NULL, "created_at" timestamptz NOT NULL, "expires_at" timestamptz NOT NULL, "last_used_at" timestamptz NULL, "revoked" boolean NOT NULL DEFAULT false, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "auth_sessions_auth_users_sessions" FOREIGN KEY ("user_id") REFERENCES "auth_users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "auth_sessions_token_hash_key" to table: "auth_sessions"
CREATE UNIQUE INDEX "auth_sessions_token_hash_key" ON "auth_sessions" ("token_hash");
-- Create index "session_expires_at" to table: "auth_sessions"
CREATE INDEX "session_expires_at" ON "auth_sessions" ("expires_at");
-- Create index "session_user_id" to table: "auth_sessions"
CREATE INDEX "session_user_id" ON "auth_sessions" ("user_id");
