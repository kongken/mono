package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// NotificationProfile holds one account's reminder preferences and the email
// address verification state (F12). The row appears with the first settings
// write; a missing row means defaults and nothing enabled.
type NotificationProfile struct{ ent.Schema }

func (NotificationProfile) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "notification_profiles"}}
}
func (NotificationProfile) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		// IANA name that decides which local day a reminder belongs to.
		field.String("timezone").Default("Asia/Shanghai"),
		// Local hour (0-23) from which the day's reminders may be sent.
		field.Int("send_hour").Default(9).Min(0).Max(23),
		// Opt-in: without it, notifications never name a place.
		field.Bool("show_places").Default(false),
		// Verified notification address. Empty until a code was confirmed.
		field.String("email").Default(""),
		field.Time("email_verified_at").Optional().Nillable(),
		// Address awaiting confirmation with its single-use code (SHA-256 hash only).
		field.String("pending_email").Default(""),
		field.String("code_hash").Default("").Sensitive(),
		field.Time("code_expires_at").Optional().Nillable(),
		field.Int("code_attempts").Default(0).NonNegative(),
	}, timestamps()...)
}
func (NotificationProfile) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("notification_profile").Field("user_id").Unique().Required().Immutable()}
}

// NotificationSetting switches one kind of notification on a channel. Absent
// rows mean off: reminders are opt-in per kind and per channel.
type NotificationSetting struct{ ent.Schema }

func (NotificationSetting) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "notification_settings"}}
}
func (NotificationSetting) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.Enum("kind").Values("memory", "annual_report").Immutable(),
		field.Enum("channel").Values("email", "push").Immutable(),
		field.Bool("enabled").Default(false),
	}, timestamps()...)
}
func (NotificationSetting) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("notification_settings").Field("user_id").Unique().Required().Immutable()}
}
func (NotificationSetting) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "kind", "channel").Unique(), index.Fields("enabled", "user_id")}
}

// PushSubscription is one browser's Web Push endpoint. The endpoint is a bearer
// capability, so it is only ever used server-side and never returned to clients.
type PushSubscription struct{ ent.Schema }

func (PushSubscription) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "push_subscriptions"}}
}
func (PushSubscription) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		// Mutable on purpose: a browser re-registered under another account moves
		// to it, so the previous account stops receiving on this device.
		field.String("user_id"),
		field.String("endpoint_hash").NotEmpty().Unique().Immutable(),
		field.String("endpoint").NotEmpty().Immutable().Sensitive(),
		field.String("p256dh").NotEmpty().Sensitive(),
		field.String("auth").NotEmpty().Sensitive(),
		field.Int("failures").Default(0).NonNegative(),
	}, timestamps()...)
}
func (PushSubscription) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("push_subscriptions").Field("user_id").Unique().Required()}
}
func (PushSubscription) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id")}
}

// NotificationDelivery records that a notification of one kind was claimed for a
// period (a local date, or a year for the annual report) on a channel. The unique
// key is what stops duplicate sends across replicas, restarts and retries.
type NotificationDelivery struct{ ent.Schema }

func (NotificationDelivery) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "notification_deliveries"}}
}
func (NotificationDelivery) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.Enum("kind").Values("memory", "annual_report").Immutable(),
		field.Enum("channel").Values("email", "push").Immutable(),
		field.String("period").NotEmpty().Immutable(),
		field.Enum("status").Values("pending", "sent", "failed", "skipped").Default("pending"),
		field.Int("attempts").Default(1).Positive(),
	}, timestamps()...)
}
func (NotificationDelivery) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("notification_deliveries").Field("user_id").Unique().Required().Immutable()}
}
func (NotificationDelivery) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "kind", "channel", "period").Unique(), index.Fields("created_at")}
}
