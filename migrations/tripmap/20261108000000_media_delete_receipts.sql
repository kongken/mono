-- Shared durable idempotency receipts for timeline and media mutations.
ALTER TABLE "media_objects" ADD COLUMN "public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "entry_public_ref" character varying;
ALTER TABLE "media_objects" ADD COLUMN "revision" integer NOT NULL DEFAULT 1;
UPDATE "media_objects" SET "public_ref" = 'med_' || encode(gen_random_bytes(13), 'hex'), "entry_public_ref" = 'ent_' || encode(gen_random_bytes(13), 'hex') WHERE "public_ref" IS NULL;
ALTER TABLE "media_objects" ALTER COLUMN "public_ref" SET NOT NULL, ALTER COLUMN "entry_public_ref" SET NOT NULL;
CREATE UNIQUE INDEX "media_objects_public_ref_key" ON "media_objects" ("public_ref");

CREATE TABLE "timeline_mutations" (
  "id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "mutation_id" character varying NOT NULL,
  "target_kind" character varying NOT NULL,
  "operation" character varying NOT NULL,
  "request_hash" character varying NOT NULL,
  "entry_ref" character varying NOT NULL,
  "media_ref" character varying,
  "result_revision" integer NOT NULL,
  "result_state" character varying NOT NULL,
  "result_snapshot" bytea NOT NULL,
  "snapshot_version" integer NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  "expires_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_mutations_users" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "timeline_mutations_target" CHECK ((target_kind = 'entry' AND entry_ref IS NOT NULL AND media_ref IS NULL) OR (target_kind = 'media' AND media_ref IS NOT NULL AND entry_ref IS NOT NULL)),
  CONSTRAINT "timeline_mutations_operation" CHECK ((target_kind = 'entry' AND operation IN ('create','update','trash','restore','purge')) OR (target_kind = 'media' AND operation = 'media_delete')),
  CONSTRAINT "timeline_mutations_hash" CHECK (length(request_hash) = 64),
  CONSTRAINT "timeline_mutations_state" CHECK (result_state IN ('active','trashed','purged','deleting')),
  CONSTRAINT "timeline_mutations_expiry" CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX "timeline_mutations_user_id_mutation_id" ON "timeline_mutations" ("user_id", "mutation_id");
CREATE INDEX "timeline_mutations_expires_at" ON "timeline_mutations" ("expires_at");
CREATE OR REPLACE FUNCTION timeline_mutations_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.id IS DISTINCT FROM NEW.id
    OR OLD.user_id IS DISTINCT FROM NEW.user_id
    OR OLD.mutation_id IS DISTINCT FROM NEW.mutation_id
    OR OLD.target_kind IS DISTINCT FROM NEW.target_kind
    OR OLD.operation IS DISTINCT FROM NEW.operation
    OR OLD.request_hash IS DISTINCT FROM NEW.request_hash
    OR OLD.entry_ref IS DISTINCT FROM NEW.entry_ref
    OR OLD.media_ref IS DISTINCT FROM NEW.media_ref
    OR OLD.result_revision IS DISTINCT FROM NEW.result_revision
    OR OLD.result_state IS DISTINCT FROM NEW.result_state
    OR OLD.result_snapshot IS DISTINCT FROM NEW.result_snapshot
    OR OLD.snapshot_version IS DISTINCT FROM NEW.snapshot_version
    OR OLD.created_at IS DISTINCT FROM NEW.created_at
    OR OLD.expires_at IS DISTINCT FROM NEW.expires_at THEN
    RAISE EXCEPTION 'timeline mutation receipts are immutable';
  END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER timeline_mutations_immutable BEFORE UPDATE ON timeline_mutations FOR EACH ROW EXECUTE FUNCTION timeline_mutations_immutable();
