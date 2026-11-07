-- Import staging table for the shared Timeline -> Tripmap migration.
-- Deployments create this table first, load and validate the one-time link
-- export, then run the Media/receipt migration.
CREATE TABLE "media_entry_links" (
  "media_id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "entry_ref" character varying NOT NULL,
  PRIMARY KEY ("media_id"),
  CONSTRAINT "media_entry_links_entry_owner_fkey"
    FOREIGN KEY ("entry_ref", "user_id") REFERENCES "timeline_entries" ("public_ref", "user_id")
);
