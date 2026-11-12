-- Persist the upload mutation key so PrepareUpload retries return the same pending object.
ALTER TABLE "timeline_media_objects" ADD COLUMN "mutation_id" character varying NOT NULL DEFAULT '';
CREATE UNIQUE INDEX "timeline_media_objects_user_id_mutation_id_key" ON "timeline_media_objects" ("user_id", "mutation_id") WHERE "mutation_id" <> '';
