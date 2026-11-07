-- Timeline account-owned records and opaque object cleanup queue.
CREATE TABLE "timeline_entries" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "title" character varying NOT NULL DEFAULT '',
  "body" character varying NOT NULL DEFAULT '',
  "occurred_at" timestamptz NOT NULL,
  "timezone" character varying NOT NULL,
  "revision" bigint NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id")
);
CREATE INDEX "timeline_entries_user_id_occurred_at_id" ON "timeline_entries" ("user_id", "occurred_at", "id");
CREATE TABLE "timeline_media_objects" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "entry_id" character varying NOT NULL,
  "object_key" character varying NOT NULL,
  "thumbnail_key" character varying NOT NULL DEFAULT '',
  "upload_key" character varying NOT NULL DEFAULT '',
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_media_objects_entries_media" FOREIGN KEY ("entry_id") REFERENCES "timeline_entries" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX "timeline_media_objects_user_id" ON "timeline_media_objects" ("user_id");
CREATE INDEX "timeline_media_objects_entry_id" ON "timeline_media_objects" ("entry_id");
CREATE TABLE "timeline_cleanup_objects" (
  "id" character varying NOT NULL,
  "object_key" character varying NOT NULL,
  "status" character varying NOT NULL DEFAULT 'pending',
  "attempts" bigint NOT NULL DEFAULT 0,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_cleanup_objects_status" CHECK ("status" IN ('pending', 'done'))
);
CREATE UNIQUE INDEX "timeline_cleanup_objects_object_key_key" ON "timeline_cleanup_objects" ("object_key");
