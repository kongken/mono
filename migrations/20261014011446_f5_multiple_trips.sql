-- Modify "media_objects" table
ALTER TABLE "media_objects" ADD COLUMN "trip_id" character varying NOT NULL DEFAULT '';
-- Create index "mediaobject_user_id_trip_id" to table: "media_objects"
CREATE INDEX "mediaobject_user_id_trip_id" ON "media_objects" ("user_id", "trip_id");
-- Modify "snapshots" table
ALTER TABLE "snapshots" ADD COLUMN "schema_version" bigint NOT NULL DEFAULT 1, ADD COLUMN "trip_count" bigint NOT NULL DEFAULT 0;
-- Modify "users" table
ALTER TABLE "users" ADD COLUMN "schema_version" bigint NOT NULL DEFAULT 1;
-- Create "city_trips" table
CREATE TABLE "city_trips" ("id" character varying NOT NULL, "trip_id" character varying NOT NULL, "region_code" character varying NOT NULL, "travel_date" character varying NOT NULL DEFAULT '', "note" character varying NOT NULL DEFAULT '', "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "visit_id" character varying NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "city_trips_city_visits_trips" FOREIGN KEY ("visit_id") REFERENCES "city_visits" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "city_trips_users_trips" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "trip_user_id_region_code" to table: "city_trips"
CREATE INDEX "trip_user_id_region_code" ON "city_trips" ("user_id", "region_code");
-- Create index "trip_user_id_trip_id" to table: "city_trips"
CREATE UNIQUE INDEX "trip_user_id_trip_id" ON "city_trips" ("user_id", "trip_id");
-- Create index "trip_visit_id" to table: "city_trips"
CREATE INDEX "trip_visit_id" ON "city_trips" ("visit_id");
