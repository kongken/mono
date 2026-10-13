package schema

import (
	"fmt"
	"time"
	"unicode/utf16"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

func now() time.Time { return time.Now().UTC() }

func timestamps() []ent.Field {
	return []ent.Field{field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now)}
}

// User owns cloud records. Password accounts have a username and password hash;
// OAuth accounts are identified by (provider, external_id) and have neither.
// Provider email addresses are intentionally not stored.
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "users"}}
}
func (User) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("email").Optional().Nillable().Unique(),
		field.String("username").Optional().Nillable().Unique(),
		field.String("display_name").Default(""),
		field.String("avatar_url").Default(""),
		field.String("provider").Default(""),
		field.String("external_id").Default(""),
		field.String("password_hash").Default("").Sensitive(),
		field.Bool("disabled").Default(false),
		field.Time("deletion_requested_at").Optional().Nillable(),
		field.Int64("revision").Default(0).NonNegative(),
	}, timestamps()...)
}
func (User) Indexes() []ent.Index {
	return []ent.Index{index.Fields("provider", "external_id").Unique().Annotations(entsql.IndexWhere("external_id <> ''"))}
}
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("visits", CityVisit.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("province_marks", ProvinceMark.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("media_objects", MediaObject.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sessions", Session.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("footprint_changes", FootprintChange.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sync_receipts", SyncReceipt.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("snapshots", Snapshot.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Session is a bearer session. Only the SHA-256 of the token is stored.
type Session struct{ ent.Schema }

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_sessions"}}
}
func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		field.Time("created_at").Default(now).Immutable(), field.Time("expires_at"),
		field.Time("last_used_at").Optional().Nillable(), field.Bool("revoked").Default(false),
	}
}
func (Session) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("sessions").Field("user_id").Unique().Required().Immutable()}
}
func (Session) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id"), index.Fields("expires_at")}
}

type CityVisit struct{ ent.Schema }

func (CityVisit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "city_visits"}}
}
func (CityVisit) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("region_code").NotEmpty(), field.String("region_name").NotEmpty(), field.String("province_code").NotEmpty(),
		field.String("color").Validate(validColor),
		field.String("travel_date").Default("").Validate(validDate),
		field.String("note").Default("").Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 1000 {
				return fmt.Errorf("note exceeds 1000 characters")
			}
			return nil
		}),
	}, timestamps()...)
}
func (CityVisit) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("visits").Field("user_id").Unique().Required().Immutable()}
}
func (CityVisit) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "region_code").Unique(), index.Fields("user_id", "province_code")}
}

type ProvinceMark struct{ ent.Schema }

func (ProvinceMark) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "province_marks"}}
}
func (ProvinceMark) Fields() []ent.Field {
	return append([]ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(), field.String("province_code").NotEmpty(), field.String("color").Validate(validColor)}, timestamps()...)
}
func (ProvinceMark) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("province_marks").Field("user_id").Unique().Required().Immutable()}
}
func (ProvinceMark) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "province_code").Unique()}
}

type MediaObject struct{ ent.Schema }

func (MediaObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "media_objects"}}
}
func (MediaObject) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("object_key").NotEmpty().Unique(), field.String("content_type").NotEmpty(), field.Int64("size_bytes").NonNegative(),
		field.Enum("status").Values("pending", "ready").Default("pending"),
	}, timestamps()...)
}
func (MediaObject) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("media_objects").Field("user_id").Unique().Required().Immutable()}
}

func validColor(v string) error {
	switch v {
	case "#4b937d", "#e2af5c", "#d88370", "#719fc6", "#a08cbd":
		return nil
	}
	return fmt.Errorf("unsupported footprint color")
}
func validDate(v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("invalid travel date")
	}
	return nil
}

// Changes retain explicit delete tombstones and full upsert values.
type FootprintChange struct{ ent.Schema }

func (FootprintChange) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.Int64("revision").Positive().Immutable(), field.Bytes("payload").Immutable(), field.Time("created_at").Default(now).Immutable(),
	}
}
func (FootprintChange) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("footprint_changes").Field("user_id").Unique().Required().Immutable()}
}
func (FootprintChange) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "revision").Unique()}
}

type SyncReceipt struct{ ent.Schema }

func (SyncReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(), field.String("request_hash").Immutable(),
		field.Int64("revision").NonNegative().Immutable(), field.Int("changed_count").NonNegative().Immutable(), field.Time("created_at").Default(now).Immutable(),
		// Set by snapshot restores: the pre-restore snapshot, so a replay can answer with it.
		field.String("snapshot_id").Optional().Nillable().Immutable(),
	}
}
func (SyncReceipt) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("sync_receipts").Field("user_id").Unique().Required().Immutable()}
}
func (SyncReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "mutation_id").Unique()}
}

// AccountDeletionReceipt contains no account identity or footprints. It proves
// completion to the deleting session after a lost response, for at most 24h.
type AccountDeletionReceipt struct{ ent.Schema }

func (AccountDeletionReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("token_hash").NotEmpty().Unique().Sensitive().Immutable(), field.Time("expires_at").Immutable(),
	}
}
func (AccountDeletionReceipt) Indexes() []ent.Index { return []ent.Index{index.Fields("expires_at")} }

// Snapshot is an immutable copy of a user's footprints at one revision. The
// payload is a gzip-compressed tripmap.v1.SnapshotData. Snapshots are removed
// only by retention or when the account is deleted.
type Snapshot struct{ ent.Schema }

func (Snapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.Enum("reason").Values("manual", "scheduled", "before_migration", "before_restore").Immutable(),
		field.Int64("revision").NonNegative().Immutable(),
		field.Int("visit_count").NonNegative().Immutable(), field.Int("mark_count").NonNegative().Immutable(),
		field.Bytes("payload").Immutable(), field.Time("created_at").Default(now).Immutable(),
	}
}
func (Snapshot) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("snapshots").Field("user_id").Unique().Required().Immutable()}
}
func (Snapshot) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at"), index.Fields("user_id", "revision")}
}
