-- Import staging table for the shared Timeline -> Tripmap migration.
-- Deployments create this table first, load and validate the one-time link
-- export, then run the Media/receipt migration.
CREATE TABLE "media_entry_links" (
  "media_id" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "entry_ref" character varying NOT NULL,
  PRIMARY KEY ("media_id")
);
