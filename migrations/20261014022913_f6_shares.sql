-- Create "shares" table
CREATE TABLE "shares" ("id" character varying NOT NULL, "token" character varying NULL, "visibility" character varying NOT NULL DEFAULT 'private', "show_cities" boolean NOT NULL DEFAULT false, "show_dates" boolean NOT NULL DEFAULT false, "show_notes" boolean NOT NULL DEFAULT false, "show_photos" boolean NOT NULL DEFAULT false, "photo_ids" jsonb NULL, "version" bigint NOT NULL DEFAULT 0, "published_revision" bigint NOT NULL DEFAULT 0, "published_at" timestamptz NULL, "payload" bytea NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "shares_users_share" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "shares_token_key" to table: "shares"
CREATE UNIQUE INDEX "shares_token_key" ON "shares" ("token");
-- Create index "shares_user_id_key" to table: "shares"
CREATE UNIQUE INDEX "shares_user_id_key" ON "shares" ("user_id");
