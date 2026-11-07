-- KON-108: opaque entry refs and durable, immutable mutation receipts.
-- Entry refs are filled before the receipt table is enabled; the transaction aborts on any gap.
ALTER TABLE "timeline_entries" ADD COLUMN "public_ref" character varying;
UPDATE "timeline_entries" SET "public_ref" = 'ent_' || substr(md5("id"), 1, 26) WHERE "public_ref" IS NULL;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM "timeline_entries" WHERE "public_ref" IS NULL)
     OR EXISTS (SELECT "public_ref" FROM "timeline_entries" GROUP BY "public_ref" HAVING count(*) > 1)
  THEN RAISE EXCEPTION 'timeline entry public_ref backfill incomplete or collided'; END IF;
END $$;
ALTER TABLE "timeline_entries" ALTER COLUMN "public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "timeline_entries_public_ref_key" ON "timeline_entries" ("public_ref");
ALTER TABLE "timeline_entries" ADD CONSTRAINT "timeline_entries_public_ref_format" CHECK ("public_ref" ~ '^ent_[0-9a-hjkmnp-tv-z]{26}$');

CREATE TABLE "timeline_mutations" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "mutation_id" character varying NOT NULL,
  "target_kind" character varying NOT NULL,
  "operation" character varying NOT NULL,
  "entry_ref" character varying NOT NULL,
  "request_hash" character varying NOT NULL,
  "result_revision" bigint NOT NULL,
  "result_state" character varying NOT NULL,
  "result_snapshot" jsonb NOT NULL,
  "snapshot_version" bigint NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_mutations_target_operation" CHECK (("target_kind" = 'entry' AND "operation" IN ('create','update','trash','restore','purge')) OR ("target_kind" = 'media' AND "operation" = 'media_delete')),
  CONSTRAINT "timeline_mutations_target_ref" CHECK ("target_kind" = 'entry' AND "entry_ref" IS NOT NULL OR "target_kind" = 'media' AND "entry_ref" IS NOT NULL),
  CONSTRAINT "timeline_mutations_hash" CHECK (length("request_hash") = 64),
  CONSTRAINT "timeline_mutations_revision" CHECK ("result_revision" >= 1)
);
CREATE UNIQUE INDEX "timeline_mutations_user_id_mutation_id" ON "timeline_mutations" ("user_id", "mutation_id");
CREATE INDEX "timeline_mutations_created_at" ON "timeline_mutations" ("created_at");
