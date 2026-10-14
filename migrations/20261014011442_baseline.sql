-- Create "account_deletion_receipts" table
CREATE TABLE "account_deletion_receipts" ("id" character varying NOT NULL, "token_hash" character varying NOT NULL, "expires_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "account_deletion_receipts_token_hash_key" to table: "account_deletion_receipts"
CREATE UNIQUE INDEX "account_deletion_receipts_token_hash_key" ON "account_deletion_receipts" ("token_hash");
-- Create index "accountdeletionreceipt_expires_at" to table: "account_deletion_receipts"
CREATE INDEX "accountdeletionreceipt_expires_at" ON "account_deletion_receipts" ("expires_at");
-- Create "media_cleanups" table
CREATE TABLE "media_cleanups" ("id" character varying NOT NULL, "object_key" character varying NOT NULL, "delete_after" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "media_cleanups_object_key_key" to table: "media_cleanups"
CREATE UNIQUE INDEX "media_cleanups_object_key_key" ON "media_cleanups" ("object_key");
-- Create index "mediacleanup_delete_after" to table: "media_cleanups"
CREATE INDEX "mediacleanup_delete_after" ON "media_cleanups" ("delete_after");
-- Create "users" table
CREATE TABLE "users" ("id" character varying NOT NULL, "email" character varying NULL, "username" character varying NULL, "display_name" character varying NOT NULL DEFAULT '', "avatar_url" character varying NOT NULL DEFAULT '', "provider" character varying NOT NULL DEFAULT '', "external_id" character varying NOT NULL DEFAULT '', "password_hash" character varying NOT NULL DEFAULT '', "disabled" boolean NOT NULL DEFAULT false, "deletion_requested_at" timestamptz NULL, "revision" bigint NOT NULL DEFAULT 0, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, PRIMARY KEY ("id"));
-- Create index "user_provider_external_id" to table: "users"
CREATE UNIQUE INDEX "user_provider_external_id" ON "users" ("provider", "external_id") WHERE ((external_id)::text <> ''::text);
-- Create index "users_email_key" to table: "users"
CREATE UNIQUE INDEX "users_email_key" ON "users" ("email");
-- Create index "users_username_key" to table: "users"
CREATE UNIQUE INDEX "users_username_key" ON "users" ("username");
-- Create "auth_sessions" table
CREATE TABLE "auth_sessions" ("id" character varying NOT NULL, "token_hash" character varying NOT NULL, "created_at" timestamptz NOT NULL, "expires_at" timestamptz NOT NULL, "last_used_at" timestamptz NULL, "revoked" boolean NOT NULL DEFAULT false, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "auth_sessions_users_sessions" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "auth_sessions_token_hash_key" to table: "auth_sessions"
CREATE UNIQUE INDEX "auth_sessions_token_hash_key" ON "auth_sessions" ("token_hash");
-- Create index "session_expires_at" to table: "auth_sessions"
CREATE INDEX "session_expires_at" ON "auth_sessions" ("expires_at");
-- Create index "session_user_id" to table: "auth_sessions"
CREATE INDEX "session_user_id" ON "auth_sessions" ("user_id");
-- Create "city_visits" table
CREATE TABLE "city_visits" ("id" character varying NOT NULL, "region_code" character varying NOT NULL, "region_name" character varying NOT NULL, "province_code" character varying NOT NULL, "color" character varying NOT NULL, "travel_date" character varying NOT NULL DEFAULT '', "note" character varying NOT NULL DEFAULT '', "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "city_visits_users_visits" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "cityvisit_user_id_province_code" to table: "city_visits"
CREATE INDEX "cityvisit_user_id_province_code" ON "city_visits" ("user_id", "province_code");
-- Create index "cityvisit_user_id_region_code" to table: "city_visits"
CREATE UNIQUE INDEX "cityvisit_user_id_region_code" ON "city_visits" ("user_id", "region_code");
-- Create "footprint_changes" table
CREATE TABLE "footprint_changes" ("id" character varying NOT NULL, "revision" bigint NOT NULL, "payload" bytea NOT NULL, "created_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "footprint_changes_users_footprint_changes" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "footprintchange_user_id_revision" to table: "footprint_changes"
CREATE UNIQUE INDEX "footprintchange_user_id_revision" ON "footprint_changes" ("user_id", "revision");
-- Create "media_objects" table
CREATE TABLE "media_objects" ("id" character varying NOT NULL, "object_key" character varying NOT NULL, "content_type" character varying NOT NULL, "size_bytes" bigint NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "region_code" character varying NOT NULL DEFAULT '', "filename" character varying NOT NULL DEFAULT '', "upload_id" character varying NOT NULL DEFAULT '', "request_hash" character varying NOT NULL DEFAULT '', "upload_key" character varying NOT NULL DEFAULT '', "thumbnail_key" character varying NOT NULL DEFAULT '', "upload_expires_at" timestamptz NULL, "width" bigint NOT NULL DEFAULT 0, "height" bigint NOT NULL DEFAULT 0, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "media_objects_users_media_objects" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "media_objects_object_key_key" to table: "media_objects"
CREATE UNIQUE INDEX "media_objects_object_key_key" ON "media_objects" ("object_key");
-- Create index "mediaobject_user_id_region_code" to table: "media_objects"
CREATE INDEX "mediaobject_user_id_region_code" ON "media_objects" ("user_id", "region_code");
-- Create index "mediaobject_user_id_upload_id" to table: "media_objects"
CREATE UNIQUE INDEX "mediaobject_user_id_upload_id" ON "media_objects" ("user_id", "upload_id") WHERE ((upload_id)::text <> ''::text);
-- Create "province_marks" table
CREATE TABLE "province_marks" ("id" character varying NOT NULL, "province_code" character varying NOT NULL, "color" character varying NOT NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "province_marks_users_province_marks" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "provincemark_user_id_province_code" to table: "province_marks"
CREATE UNIQUE INDEX "provincemark_user_id_province_code" ON "province_marks" ("user_id", "province_code");
-- Create "snapshots" table
CREATE TABLE "snapshots" ("id" character varying NOT NULL, "reason" character varying NOT NULL, "revision" bigint NOT NULL, "visit_count" bigint NOT NULL, "mark_count" bigint NOT NULL, "payload" bytea NOT NULL, "created_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "snapshots_users_snapshots" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "snapshot_user_id_created_at" to table: "snapshots"
CREATE INDEX "snapshot_user_id_created_at" ON "snapshots" ("user_id", "created_at");
-- Create index "snapshot_user_id_revision" to table: "snapshots"
CREATE INDEX "snapshot_user_id_revision" ON "snapshots" ("user_id", "revision");
-- Create "sync_receipts" table
CREATE TABLE "sync_receipts" ("id" character varying NOT NULL, "mutation_id" character varying NOT NULL, "request_hash" character varying NOT NULL, "revision" bigint NOT NULL, "changed_count" bigint NOT NULL, "created_at" timestamptz NOT NULL, "snapshot_id" character varying NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "sync_receipts_users_sync_receipts" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "syncreceipt_user_id_mutation_id" to table: "sync_receipts"
CREATE UNIQUE INDEX "syncreceipt_user_id_mutation_id" ON "sync_receipts" ("user_id", "mutation_id");
