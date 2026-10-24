-- Modify "auth_browser_sessions" table
ALTER TABLE "auth_browser_sessions" DROP CONSTRAINT "auth_browser_sessions_auth_identities_sessions", ADD COLUMN "auth_version" bigint NOT NULL DEFAULT 0;
-- Modify "bff_access_tokens" table
ALTER TABLE "bff_access_tokens" DROP CONSTRAINT "bff_access_tokens_auth_identities_access_tokens", ADD COLUMN "auth_version" bigint NOT NULL DEFAULT 0;
-- Create "bff_account_deletions" table
CREATE TABLE "bff_account_deletions" ("id" character varying NOT NULL, "user_id" character varying NOT NULL, "mutation_id" character varying NOT NULL, "token_hash" character varying NOT NULL, "products" jsonb NOT NULL, "completed_products" jsonb NOT NULL, "stage" character varying NOT NULL DEFAULT 'begin', "attempts" bigint NOT NULL DEFAULT 0, "last_error" character varying NOT NULL DEFAULT '', "lease_until" timestamptz NULL, "next_attempt_at" timestamptz NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "completed_at" timestamptz NULL, "receipt_expires_at" timestamptz NULL, PRIMARY KEY ("id"));
-- Create index "accountdeletion_receipt_expires_at" to table: "bff_account_deletions"
CREATE INDEX "accountdeletion_receipt_expires_at" ON "bff_account_deletions" ("receipt_expires_at");
-- Create index "accountdeletion_stage_next_attempt_at" to table: "bff_account_deletions"
CREATE INDEX "accountdeletion_stage_next_attempt_at" ON "bff_account_deletions" ("stage", "next_attempt_at");
-- Create index "accountdeletion_token_hash" to table: "bff_account_deletions"
CREATE INDEX "accountdeletion_token_hash" ON "bff_account_deletions" ("token_hash");
-- Create index "bff_account_deletions_user_id_key" to table: "bff_account_deletions"
CREATE UNIQUE INDEX "bff_account_deletions_user_id_key" ON "bff_account_deletions" ("user_id");
