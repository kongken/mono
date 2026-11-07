package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"time"
)

func now() time.Time { return time.Now().UTC() }

type Entry struct{ ent.Schema }

func (Entry) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_entries"}}
}
func (Entry) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("public_ref").NotEmpty().Immutable(), field.String("user_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(), field.String("body").Default(""), field.String("occurred_date").NotEmpty(), field.String("occurred_time").Default(""), field.String("timezone").NotEmpty(),
		field.String("category").Default(""), field.String("location").Default(""), field.Int64("amount_minor").Default(0), field.String("currency").Default(""),
		field.String("url").Default(""), field.String("url_title").Default(""), field.String("url_note").Default(""), field.Time("occurred_at"), field.Time("deleted_at").Optional().Nillable(),
		field.String("mutation_id").Default(""), field.Int("revision").Default(1).Positive(), field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}
func (Entry) Edges() []ent.Edge {
	return []ent.Edge{edge.To("media", MediaObject.Type).Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (Entry) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "occurred_at", "id"), index.Fields("public_ref").Unique()}
}

type MediaObject struct{ ent.Schema }

func (MediaObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_media_objects"}}
}
func (MediaObject) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("public_ref").NotEmpty().Immutable(), field.String("user_id").NotEmpty().Immutable(), field.String("entry_id").NotEmpty().Immutable(), field.String("entry_public_ref").NotEmpty().Immutable(), field.String("object_key").NotEmpty().Immutable(), field.String("thumbnail_key").Default(""), field.String("upload_key").Default(""),
		field.Enum("state").Values("pending", "ready", "deleting").Default("pending"), field.Int("revision").Default(1).Positive(), field.String("filename").Default(""), field.String("content_type").Default(""), field.Int64("size_bytes").Default(0), field.Int("width").Optional().Nillable(), field.Int("height").Optional().Nillable(), field.Time("upload_expires_at").Optional().Nillable(), field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}
func (MediaObject) Edges() []ent.Edge {
	return []ent.Edge{edge.From("entry", Entry.Type).Ref("media").Field("entry_id").Unique().Required().Immutable()}
}
func (MediaObject) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id"), index.Fields("entry_id"), index.Fields("public_ref").Unique()}
}

type Receipt struct{ ent.Schema }

func (Receipt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_mutations"}}
}
func (Receipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").NotEmpty().Immutable(), field.String("mutation_id").NotEmpty().Immutable(), field.String("target_kind").NotEmpty(), field.String("operation").NotEmpty(), field.String("request_hash").NotEmpty().Immutable(), field.String("entry_ref").Optional().Nillable().Immutable(), field.String("media_ref").Optional().Nillable().Immutable(), field.Int("result_revision").Positive().Immutable(), field.String("result_state").NotEmpty().Immutable(), field.Bytes("result_snapshot").NotEmpty().Immutable(), field.Int("snapshot_version").Default(1).Positive().Immutable(), field.Time("created_at").Default(now).Immutable(),
	}
}
func (Receipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "mutation_id").Unique()}
}

type CleanupObject struct{ ent.Schema }

func (CleanupObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_cleanup_objects"}}
}
func (CleanupObject) Fields() []ent.Field {
	return []ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("object_key").Unique().NotEmpty().Immutable(), field.Enum("status").Values("pending", "done").Default("pending"), field.Int("attempts").Default(0), field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now)}
}
