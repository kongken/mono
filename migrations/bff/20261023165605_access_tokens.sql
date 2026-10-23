-- Create "bff_access_tokens" table
CREATE TABLE "bff_access_tokens" ("id" character varying NOT NULL, "name" character varying NOT NULL, "token_hash" character varying NOT NULL, "audience" character varying NOT NULL, "scopes" jsonb NOT NULL, "roles" jsonb NOT NULL, "created_at" timestamptz NOT NULL, "expires_at" timestamptz NOT NULL, "last_used_at" timestamptz NULL, "revoked_at" timestamptz NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "bff_access_tokens_auth_identities_access_tokens" FOREIGN KEY ("user_id") REFERENCES "auth_identities" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "bff_access_token_user_id" to table: "bff_access_tokens"
CREATE INDEX "bff_access_token_user_id" ON "bff_access_tokens" ("user_id");
-- Create index "bff_access_tokens_token_hash_key" to table: "bff_access_tokens"
CREATE UNIQUE INDEX "bff_access_tokens_token_hash_key" ON "bff_access_tokens" ("token_hash");
