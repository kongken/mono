-- Create "notification_deliveries" table
CREATE TABLE "notification_deliveries" ("id" character varying NOT NULL, "kind" character varying NOT NULL, "channel" character varying NOT NULL, "period" character varying NOT NULL, "status" character varying NOT NULL DEFAULT 'pending', "attempts" bigint NOT NULL DEFAULT 1, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "notification_deliveries_users_notification_deliveries" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "notificationdelivery_created_at" to table: "notification_deliveries"
CREATE INDEX "notificationdelivery_created_at" ON "notification_deliveries" ("created_at");
-- Create index "notificationdelivery_user_id_kind_channel_period" to table: "notification_deliveries"
CREATE UNIQUE INDEX "notificationdelivery_user_id_kind_channel_period" ON "notification_deliveries" ("user_id", "kind", "channel", "period");
-- Create "notification_profiles" table
CREATE TABLE "notification_profiles" ("id" character varying NOT NULL, "timezone" character varying NOT NULL DEFAULT 'Asia/Shanghai', "send_hour" bigint NOT NULL DEFAULT 9, "show_places" boolean NOT NULL DEFAULT false, "email" character varying NOT NULL DEFAULT '', "email_verified_at" timestamptz NULL, "pending_email" character varying NOT NULL DEFAULT '', "code_hash" character varying NOT NULL DEFAULT '', "code_expires_at" timestamptz NULL, "code_attempts" bigint NOT NULL DEFAULT 0, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "notification_profiles_users_notification_profile" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "notification_profiles_user_id_key" to table: "notification_profiles"
CREATE UNIQUE INDEX "notification_profiles_user_id_key" ON "notification_profiles" ("user_id");
-- Create "notification_settings" table
CREATE TABLE "notification_settings" ("id" character varying NOT NULL, "kind" character varying NOT NULL, "channel" character varying NOT NULL, "enabled" boolean NOT NULL DEFAULT false, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "notification_settings_users_notification_settings" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "notificationsetting_enabled_user_id" to table: "notification_settings"
CREATE INDEX "notificationsetting_enabled_user_id" ON "notification_settings" ("enabled", "user_id");
-- Create index "notificationsetting_user_id_kind_channel" to table: "notification_settings"
CREATE UNIQUE INDEX "notificationsetting_user_id_kind_channel" ON "notification_settings" ("user_id", "kind", "channel");
-- Create "push_subscriptions" table
CREATE TABLE "push_subscriptions" ("id" character varying NOT NULL, "endpoint_hash" character varying NOT NULL, "endpoint" character varying NOT NULL, "p256dh" character varying NOT NULL, "auth" character varying NOT NULL, "failures" bigint NOT NULL DEFAULT 0, "created_at" timestamptz NOT NULL, "updated_at" timestamptz NOT NULL, "user_id" character varying NOT NULL, PRIMARY KEY ("id"), CONSTRAINT "push_subscriptions_users_push_subscriptions" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- Create index "push_subscriptions_endpoint_hash_key" to table: "push_subscriptions"
CREATE UNIQUE INDEX "push_subscriptions_endpoint_hash_key" ON "push_subscriptions" ("endpoint_hash");
-- Create index "pushsubscription_user_id" to table: "push_subscriptions"
CREATE INDEX "pushsubscription_user_id" ON "push_subscriptions" ("user_id");
