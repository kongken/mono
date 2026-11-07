-- Media-specific columns; the shared timeline_mutations table is owned by
-- migrations/timeline and must be applied before this migration.
ALTER TABLE "media_objects" ADD COLUMN "public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "entry_public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "revision" integer NOT NULL DEFAULT 1;
UPDATE "media_objects" SET
  "public_ref" = 'med_' || encode(gen_random_bytes(13), 'hex'),
  "entry_public_ref" = 'ent_' || encode(substr(digest("user_id" || E'\\x00' || "trip_id", 'sha256'), 1, 13), 'hex')
WHERE "public_ref" IS NULL;
ALTER TABLE "media_objects" ALTER COLUMN "public_ref" SET NOT NULL, ALTER COLUMN "entry_public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "media_objects_public_ref_key" ON "media_objects" ("public_ref");

ALTER TABLE "timeline_mutations" ADD COLUMN "expires_at" timestamptz;
UPDATE "timeline_mutations" SET "expires_at" = "created_at" + interval '30 days' WHERE "expires_at" IS NULL;
ALTER TABLE "timeline_mutations" ALTER COLUMN "expires_at" SET NOT NULL;
ALTER TABLE "timeline_mutations" ADD CONSTRAINT "timeline_mutations_expiry" CHECK (expires_at > created_at);
CREATE INDEX "timeline_mutations_expires_at" ON "timeline_mutations" ("expires_at");
