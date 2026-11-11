-- Timeline opaque refs and durable mutation receipts.
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- Ref backfill order is deliberate: entries first, then media snapshots, then receipts.
ALTER TABLE "timeline_entries" ADD COLUMN "public_ref" character varying;
UPDATE "timeline_entries" SET "public_ref" = 'ent_' || encode(gen_random_bytes(13), 'hex');
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM timeline_entries WHERE public_ref IS NULL OR public_ref !~ '^ent_[0-9a-f]{26}$') THEN RAISE EXCEPTION 'unresolvable timeline entry public_ref'; END IF;
END $$;
CREATE UNIQUE INDEX "timeline_entries_public_ref_user_key" ON "timeline_entries" ("public_ref", "user_id");
CREATE UNIQUE INDEX "timeline_entries_public_ref_key" ON "timeline_entries" ("public_ref");
ALTER TABLE "timeline_entries" ALTER COLUMN "public_ref" SET NOT NULL;

ALTER TABLE "timeline_media_objects"
  ADD COLUMN "public_ref" character varying,
  ADD COLUMN "entry_public_ref" character varying,
  ADD COLUMN "state" character varying NOT NULL DEFAULT 'pending',
  ADD COLUMN "revision" bigint NOT NULL DEFAULT 1,
  ADD COLUMN "filename" character varying NOT NULL DEFAULT '',
  ADD COLUMN "content_type" character varying NOT NULL DEFAULT '',
  ADD COLUMN "size_bytes" bigint NOT NULL DEFAULT 0,
  ADD COLUMN "width" bigint NULL,
  ADD COLUMN "height" bigint NULL,
  ADD COLUMN "upload_expires_at" timestamptz NULL,
  ADD COLUMN "updated_at" timestamptz NOT NULL DEFAULT now();
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM timeline_media_objects m JOIN timeline_entries e ON e.id = m.entry_id WHERE m.user_id <> e.user_id) THEN RAISE EXCEPTION 'timeline media owner mismatch'; END IF;
END $$;
UPDATE "timeline_media_objects" m SET
  "public_ref" = 'med_' || encode(gen_random_bytes(13), 'hex'),
  "entry_public_ref" = e."public_ref"
FROM "timeline_entries" e WHERE e."id" = m."entry_id";
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM timeline_media_objects WHERE public_ref IS NULL OR entry_public_ref IS NULL) THEN RAISE EXCEPTION 'unresolvable timeline media entry ref'; END IF;
END $$;
CREATE UNIQUE INDEX "timeline_media_objects_public_ref_key" ON "timeline_media_objects" ("public_ref");
ALTER TABLE "timeline_media_objects" ALTER COLUMN "public_ref" SET NOT NULL, ALTER COLUMN "entry_public_ref" SET NOT NULL;
ALTER TABLE "timeline_media_objects" ADD CONSTRAINT "timeline_media_objects_state" CHECK (state IN ('pending','ready','deleting'));

CREATE TABLE "timeline_mutations" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "mutation_id" character varying NOT NULL,
  "target_kind" character varying NOT NULL,
  "operation" character varying NOT NULL,
  "request_hash" character varying NOT NULL,
  "entry_ref" character varying NULL,
  "media_ref" character varying NULL,
  "result_revision" integer NOT NULL,
  "result_state" character varying NOT NULL,
  "result_snapshot" bytea NOT NULL,
  "snapshot_version" integer NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  "expires_at" timestamptz NOT NULL DEFAULT (now() + interval '30 days'),
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_mutations_target" CHECK ((target_kind = 'entry' AND entry_ref IS NOT NULL AND media_ref IS NULL) OR (target_kind = 'media' AND media_ref IS NOT NULL AND entry_ref IS NOT NULL)),
  CONSTRAINT "timeline_mutations_operation" CHECK ((target_kind = 'entry' AND operation IN ('create','update','trash','restore','purge')) OR (target_kind = 'media' AND operation = 'media_delete')),
  CONSTRAINT "timeline_mutations_hash" CHECK (length(request_hash) = 64),
  CONSTRAINT "timeline_mutations_state" CHECK (result_state IN ('active','trashed','purged','deleting')),
  CONSTRAINT "timeline_mutations_expiry" CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX "timeline_mutations_user_id_mutation_id" ON "timeline_mutations" ("user_id", "mutation_id");
CREATE INDEX "timeline_mutations_created_at" ON "timeline_mutations" ("created_at");
CREATE INDEX "timeline_mutations_expires_at" ON "timeline_mutations" ("expires_at");
CREATE FUNCTION timeline_mutations_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id IS DISTINCT FROM OLD.id
    OR NEW.user_id IS DISTINCT FROM OLD.user_id OR NEW.mutation_id <> OLD.mutation_id OR NEW.target_kind <> OLD.target_kind
    OR NEW.operation <> OLD.operation OR NEW.request_hash <> OLD.request_hash OR NEW.entry_ref IS DISTINCT FROM OLD.entry_ref
    OR NEW.media_ref IS DISTINCT FROM OLD.media_ref OR NEW.result_revision <> OLD.result_revision
    OR NEW.result_state <> OLD.result_state OR NEW.result_snapshot IS DISTINCT FROM OLD.result_snapshot
    OR NEW.snapshot_version <> OLD.snapshot_version OR NEW.created_at <> OLD.created_at
    OR NEW.expires_at <> OLD.expires_at THEN
    RAISE EXCEPTION 'timeline mutation receipts are immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER "timeline_mutations_immutable" BEFORE UPDATE ON "timeline_mutations"
  FOR EACH ROW EXECUTE FUNCTION timeline_mutations_immutable();
