-- Media-specific columns; the shared timeline_mutations table is owned by
-- migrations/timeline and must be applied before this migration.
-- media_entry_links is created by the preceding staging migration and loaded
-- by the deployment orchestrator before this file runs.
ALTER TABLE "media_objects" ADD CONSTRAINT "media_objects_id_user_key" UNIQUE ("id", "user_id");
ALTER TABLE "media_entry_links" ADD CONSTRAINT "media_entry_links_media_owner_fkey"
  FOREIGN KEY ("media_id", "user_id") REFERENCES "media_objects" ("id", "user_id") ON DELETE CASCADE;
ALTER TABLE "media_entry_links" ADD CONSTRAINT "media_entry_links_entry_ref_format"
  CHECK ("entry_ref" ~ '^ent_[0-9a-f]{26}$');
ALTER TABLE "media_objects" ADD COLUMN "public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "entry_public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "revision" integer NOT NULL DEFAULT 1;
UPDATE "media_objects" m SET
  "public_ref" = COALESCE(m."public_ref", 'med_' || encode(gen_random_bytes(13), 'hex')),
  "entry_public_ref" = l."entry_ref"
FROM "media_entry_links" l
JOIN "timeline_entries" e ON e."public_ref" = l."entry_ref"
WHERE l."media_id" = m."id" AND m."user_id" = l."user_id" AND e."user_id" = l."user_id";
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM media_objects m LEFT JOIN media_entry_links l ON l.media_id = m.id AND l.user_id = m.user_id WHERE m.entry_public_ref IS NULL)
    OR EXISTS (SELECT 1 FROM media_entry_links l LEFT JOIN media_objects m ON m.id = l.media_id AND m.user_id = l.user_id LEFT JOIN timeline_entries e ON e.public_ref = l.entry_ref AND e.user_id = l.user_id WHERE m.id IS NULL OR e.id IS NULL)
    OR EXISTS (SELECT 1 FROM media_entry_links l JOIN media_objects m ON m.id = l.media_id WHERE m.user_id <> l.user_id) THEN
    RAISE EXCEPTION 'media entry link import is incomplete or crosses owner boundary';
  END IF;
END $$;
ALTER TABLE "media_objects" ALTER COLUMN "public_ref" SET NOT NULL, ALTER COLUMN "entry_public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "media_objects_public_ref_key" ON "media_objects" ("public_ref");
CREATE INDEX "media_entry_links_entry_ref" ON "media_entry_links" ("entry_ref");

ALTER TABLE "timeline_mutations" ADD COLUMN "expires_at" timestamptz;
UPDATE "timeline_mutations" SET "expires_at" = "created_at" + interval '30 days' WHERE "expires_at" IS NULL;
ALTER TABLE "timeline_mutations" ALTER COLUMN "expires_at" SET NOT NULL;
ALTER TABLE "timeline_mutations" ADD CONSTRAINT "timeline_mutations_expiry" CHECK (expires_at > created_at);
CREATE INDEX "timeline_mutations_expires_at" ON "timeline_mutations" ("expires_at");
