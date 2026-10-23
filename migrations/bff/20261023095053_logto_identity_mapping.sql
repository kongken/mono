-- Create "auth_identities" table
CREATE TABLE "auth_identities" ("id" character varying NOT NULL, "username" character varying NULL, "display_name" character varying NOT NULL DEFAULT '', "avatar_url" character varying NOT NULL DEFAULT '', "issuer" character varying NOT NULL, "external_id" character varying NOT NULL, "disabled" boolean NOT NULL DEFAULT false, "deletion_requested_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "user_issuer_external_id" to table: "auth_identities"
CREATE UNIQUE INDEX "user_issuer_external_id" ON "auth_identities" ("issuer", "external_id");
-- Create "auth_browser_sessions" table
CREATE TABLE "auth_browser_sessions" ("id" character varying NOT NULL, "token_hash" character varying NOT NULL, "created_at" timestamptz NOT NULL, "expires_at" timestamptz NOT NULL, "last_used_at" timestamptz NULL, "revoked" boolean NOT NULL DEFAULT false, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "auth_browser_sessions_auth_identities_sessions" FOREIGN KEY ("user_id") REFERENCES "auth_identities" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "auth_browser_sessions_token_hash_key" to table: "auth_browser_sessions"
CREATE UNIQUE INDEX "auth_browser_sessions_token_hash_key" ON "auth_browser_sessions" ("token_hash");
-- Create index "browser_session_expires_at" to table: "auth_browser_sessions"
CREATE INDEX "browser_session_expires_at" ON "auth_browser_sessions" ("expires_at");
-- Create index "browser_session_user_id" to table: "auth_browser_sessions"
CREATE INDEX "browser_session_user_id" ON "auth_browser_sessions" ("user_id");
