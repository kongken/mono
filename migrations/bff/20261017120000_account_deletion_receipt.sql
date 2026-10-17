-- Create "account_deletion_receipts" table
CREATE TABLE "account_deletion_receipts" ("id" character varying NOT NULL, "token_hash" character varying NOT NULL, "expires_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "account_deletion_receipts_token_hash_key" to table: "account_deletion_receipts"
CREATE UNIQUE INDEX "account_deletion_receipts_token_hash_key" ON "account_deletion_receipts" ("token_hash");
-- Create index "accountdeletionreceipt_expires_at" to table: "account_deletion_receipts"
CREATE INDEX "accountdeletionreceipt_expires_at" ON "account_deletion_receipts" ("expires_at");
