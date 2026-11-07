-- Durable idempotency receipts for media deletion.
CREATE TABLE "media_delete_receipts" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "mutation_id" character varying NOT NULL,
  "media_id" character varying NOT NULL,
  "request_hash" character varying NOT NULL,
  "result_state" character varying NOT NULL,
  "created_at" timestamptz NOT NULL,
  "expires_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "media_delete_receipts_users_media_delete_receipts" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "media_delete_receipts_expiry" CHECK (expires_at > created_at),
  CONSTRAINT "media_delete_receipts_hash" CHECK (length(request_hash) = 64),
  CONSTRAINT "media_delete_receipts_state" CHECK (result_state = 'deleting')
);
CREATE UNIQUE INDEX "media_delete_receipt_user_id_mutation_id" ON "media_delete_receipts" ("user_id", "mutation_id");
CREATE INDEX "media_delete_receipt_expires_at" ON "media_delete_receipts" ("expires_at");
