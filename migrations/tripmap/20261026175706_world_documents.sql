-- Create "world_documents" table
CREATE TABLE "world_documents" ("id" character varying NOT NULL, "revision" bigint NOT NULL, "payload" bytea NOT NULL, "place_count" bigint NOT NULL, "trip_count" bigint NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "world_documents_users_world_document" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "world_documents_user_id_key" to table: "world_documents"
CREATE UNIQUE INDEX "world_documents_user_id_key" ON "world_documents" ("user_id");
-- Create "world_receipts" table
CREATE TABLE "world_receipts" ("id" character varying NOT NULL, "request_id" character varying NOT NULL, "digest" character varying NOT NULL, "revision" bigint NOT NULL, "seq" bigint NOT NULL, "created_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "world_receipts_users_world_receipts" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "worldreceipt_user_id_request_id" to table: "world_receipts"
CREATE UNIQUE INDEX "worldreceipt_user_id_request_id" ON "world_receipts" ("user_id", "request_id");
-- Create index "worldreceipt_user_id_seq" to table: "world_receipts"
CREATE UNIQUE INDEX "worldreceipt_user_id_seq" ON "world_receipts" ("user_id", "seq");
