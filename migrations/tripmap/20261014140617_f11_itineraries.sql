-- Create "itineraries" table
CREATE TABLE "itineraries" ("id" character varying NOT NULL, "itinerary_id" character varying NOT NULL, "title" character varying NOT NULL, "note" character varying NOT NULL DEFAULT '', "color" character varying NOT NULL DEFAULT '#4b937d', "stops" jsonb NOT NULL, "version" bigint NOT NULL DEFAULT 1, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "itineraries_users_itineraries" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "itinerary_user_id_itinerary_id" to table: "itineraries"
CREATE UNIQUE INDEX "itinerary_user_id_itinerary_id" ON "itineraries" ("user_id", "itinerary_id");
-- Create index "itinerary_user_id_updated_at" to table: "itineraries"
CREATE INDEX "itinerary_user_id_updated_at" ON "itineraries" ("user_id", "updated_at");
