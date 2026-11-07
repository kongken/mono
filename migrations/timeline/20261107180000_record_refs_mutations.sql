-- KON-108: opaque refs and durable immutable mutation receipts.
ALTER TABLE "timeline_entries" ADD COLUMN "public_ref" character varying;
UPDATE "timeline_entries" SET "public_ref" = 'ent_' || substr(md5("id"), 1, 26) WHERE "public_ref" IS NULL;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM "timeline_entries" WHERE "public_ref" IS NULL) OR EXISTS (SELECT "public_ref" FROM "timeline_entries" GROUP BY "public_ref" HAVING count(*) > 1) THEN RAISE EXCEPTION 'timeline entry public_ref backfill incomplete or collided'; END IF;
END $$;
ALTER TABLE "timeline_entries" ALTER COLUMN "public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "timeline_entries_public_ref_key" ON "timeline_entries" ("public_ref");
ALTER TABLE "timeline_entries" ADD CONSTRAINT "timeline_entries_public_ref_format" CHECK ("public_ref" ~ '^ent_[0-9a-hjkmnp-tv-z]{26}$');

ALTER TABLE "timeline_media_objects" ADD COLUMN "public_ref" character varying;
ALTER TABLE "timeline_media_objects" ADD COLUMN "entry_public_ref" character varying;
UPDATE "timeline_media_objects" m SET "public_ref" = 'med_' || substr(md5("id"), 1, 26), "entry_public_ref" = e."public_ref" FROM "timeline_entries" e WHERE m."entry_id" = e."id" AND (m."public_ref" IS NULL OR m."entry_public_ref" IS NULL);
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM "timeline_media_objects" WHERE "public_ref" IS NULL OR "entry_public_ref" IS NULL) OR EXISTS (SELECT "public_ref" FROM "timeline_media_objects" GROUP BY "public_ref" HAVING count(*) > 1) OR EXISTS (SELECT 1 FROM "timeline_media_objects" m LEFT JOIN "timeline_entries" e ON e."id" = m."entry_id" WHERE e."public_ref" IS NULL OR m."entry_public_ref" <> e."public_ref") THEN RAISE EXCEPTION 'timeline media ref backfill incomplete, collided, or entry mismatch'; END IF;
END $$;
ALTER TABLE "timeline_media_objects" ALTER COLUMN "public_ref" SET NOT NULL;
ALTER TABLE "timeline_media_objects" ALTER COLUMN "entry_public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "timeline_media_objects_public_ref_key" ON "timeline_media_objects" ("public_ref");
ALTER TABLE "timeline_media_objects" ADD CONSTRAINT "timeline_media_objects_public_ref_format" CHECK ("public_ref" ~ '^med_[0-9a-hjkmnp-tv-z]{26}$');
ALTER TABLE "timeline_media_objects" ADD CONSTRAINT "timeline_media_objects_entry_public_ref_format" CHECK ("entry_public_ref" ~ '^ent_[0-9a-hjkmnp-tv-z]{26}$');

CREATE TABLE "timeline_mutations" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "mutation_id" character varying NOT NULL,
  "target_kind" character varying NOT NULL,
  "operation" character varying NOT NULL,
  "entry_ref" character varying NULL,
  "media_ref" character varying NULL,
  "request_hash" character varying NOT NULL,
  "result_revision" bigint NOT NULL,
  "result_state" character varying NOT NULL,
  "result_snapshot" jsonb NOT NULL,
  "snapshot_version" bigint NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_mutations_target_operation" CHECK (("target_kind" = 'entry' AND "operation" IN ('create','update','trash','restore','purge') AND "entry_ref" IS NOT NULL AND "media_ref" IS NULL) OR ("target_kind" = 'media' AND "operation" = 'media_delete' AND "media_ref" IS NOT NULL AND "entry_ref" IS NOT NULL)),
  CONSTRAINT "timeline_mutations_entry_ref_format" CHECK ("entry_ref" IS NULL OR "entry_ref" ~ '^ent_[0-9a-hjkmnp-tv-z]{26}$'),
  CONSTRAINT "timeline_mutations_media_ref_format" CHECK ("media_ref" IS NULL OR "media_ref" ~ '^med_[0-9a-hjkmnp-tv-z]{26}$'),
  CONSTRAINT "timeline_mutations_hash" CHECK (length("request_hash") = 64),
  CONSTRAINT "timeline_mutations_revision" CHECK ("result_revision" >= 1)
);
CREATE UNIQUE INDEX "timeline_mutations_user_id_mutation_id" ON "timeline_mutations" ("user_id", "mutation_id");
CREATE INDEX "timeline_mutations_created_at" ON "timeline_mutations" ("created_at");
