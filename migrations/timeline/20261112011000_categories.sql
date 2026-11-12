CREATE TABLE "timeline_categories" (
  "id" character varying NOT NULL,
  "public_ref" character varying NOT NULL,
  "user_id" character varying NOT NULL,
  "name" character varying NOT NULL,
  "active" boolean NOT NULL DEFAULT true,
  "is_default" boolean NOT NULL DEFAULT false,
  "is_other" boolean NOT NULL DEFAULT false,
  "revision" integer NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL,
  "updated_at" timestamptz NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "timeline_categories_public_ref_key" UNIQUE ("public_ref"),
  CONSTRAINT "timeline_categories_user_name_key" UNIQUE ("user_id", "name"),
  CONSTRAINT "timeline_categories_revision_check" CHECK ("revision" > 0),
  CONSTRAINT "timeline_categories_other_check" CHECK (NOT "is_other" OR "name" = '其他')
);
CREATE INDEX "timeline_categories_user_id" ON "timeline_categories" ("user_id");
