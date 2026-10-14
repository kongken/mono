-- Modify "users" table
ALTER TABLE "users" ADD COLUMN "friend_code" character varying NULL, ADD COLUMN "friend_show_cities" boolean NOT NULL DEFAULT true, ADD COLUMN "friend_show_dates" boolean NOT NULL DEFAULT false, ADD COLUMN "friend_show_notes" boolean NOT NULL DEFAULT false;
-- Create index "users_friend_code_key" to table: "users"
CREATE UNIQUE INDEX "users_friend_code_key" ON "users" ("friend_code");
-- Create "friendships" table
CREATE TABLE "friendships" ("id" character varying NOT NULL, "user_low_id" character varying NOT NULL, "user_high_id" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "accepted_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "requester_id" character varying NOT NULL, "addressee_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "friendships_users_received_friendships" FOREIGN KEY ("addressee_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "friendships_users_sent_friendships" FOREIGN KEY ("requester_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "friendship_addressee_id_status" to table: "friendships"
CREATE INDEX "friendship_addressee_id_status" ON "friendships" ("addressee_id", "status");
-- Create index "friendship_requester_id_status" to table: "friendships"
CREATE INDEX "friendship_requester_id_status" ON "friendships" ("requester_id", "status");
-- Create index "friendship_user_low_id_user_high_id" to table: "friendships"
CREATE UNIQUE INDEX "friendship_user_low_id_user_high_id" ON "friendships" ("user_low_id", "user_high_id");
-- Create "shared_trips" table
CREATE TABLE "shared_trips" ("id" character varying NOT NULL, "region_code" character varying NOT NULL, "trip_id" character varying NOT NULL, "travel_date" character varying NOT NULL DEFAULT '', "note" character varying NOT NULL DEFAULT '', "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "owner_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "shared_trips_users_owned_shared_trips" FOREIGN KEY ("owner_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "sharedtrip_owner_id_region_code" to table: "shared_trips"
CREATE INDEX "sharedtrip_owner_id_region_code" ON "shared_trips" ("owner_id", "region_code");
-- Create index "sharedtrip_owner_id_trip_id" to table: "shared_trips"
CREATE UNIQUE INDEX "sharedtrip_owner_id_trip_id" ON "shared_trips" ("owner_id", "trip_id");
-- Create "trip_members" table
CREATE TABLE "trip_members" ("id" character varying NOT NULL, "member_trip_id" character varying NOT NULL DEFAULT '', "role" character varying NOT NULL DEFAULT 'editor', "status" character varying NOT NULL DEFAULT 'invited', "invited_by" character varying NOT NULL DEFAULT '', "joined_at" timestamptz NULL, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "shared_trip_id" character varying NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "trip_members_shared_trips_members" FOREIGN KEY ("shared_trip_id") REFERENCES "shared_trips" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "trip_members_users_trip_memberships" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "tripmember_shared_trip_id_user_id" to table: "trip_members"
CREATE UNIQUE INDEX "tripmember_shared_trip_id_user_id" ON "trip_members" ("shared_trip_id", "user_id");
-- Create index "tripmember_user_id_member_trip_id" to table: "trip_members"
CREATE INDEX "tripmember_user_id_member_trip_id" ON "trip_members" ("user_id", "member_trip_id");
-- Create index "tripmember_user_id_status" to table: "trip_members"
CREATE INDEX "tripmember_user_id_status" ON "trip_members" ("user_id", "status");
