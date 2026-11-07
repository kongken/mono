-- Media-specific columns; the shared timeline_mutations table is owned by
-- migrations/timeline and must be applied before this migration.
-- Historical rows must be supplied by the deployment's one-time import before
-- this migration runs. A digest of user_id/trip_id is not an Entry identity.
CREATE TABLE "media_entry_links" (
  "media_id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "entry_ref" character varying NOT NULL,
  PRIMARY KEY ("media_id")
);
ALTER TABLE "media_objects" ADD COLUMN "public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "entry_public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "revision" integer NOT NULL DEFAULT 1;
UPDATE "media_objects" m SET
  "public_ref" = COALESCE("public_ref", 'med_' || encode(gen_random_bytes(13), 'hex')),
  "entry_public_ref" = l."entry_ref"
FROM "media_entry_links" l
JOIN "timeline_entries" e ON e."public_ref" = l."entry_ref" AND e."user_id" = l."user_id"
WHERE l."media_id" = m."id";
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM media_objects WHERE entry_public_ref IS NULL)
    OR EXISTS (SELECT 1 FROM media_entry_links l LEFT JOIN timeline_entries e ON e.public_ref = l.entry_ref AND e.user_id = l.user_id WHERE e.id IS NULL) THEN
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
