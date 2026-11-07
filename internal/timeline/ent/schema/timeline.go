package schema

import (
	"crypto/rand"
	"encoding/base32"
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
func newEntryRef() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "ent_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])[:26]
}

func newMediaRef() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "med_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])[:26]
}

type Entry struct{ ent.Schema }

func (Entry) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_entries"}}
}
func (Entry) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("public_ref").DefaultFunc(newEntryRef).Unique().Immutable(), field.String("user_id").NotEmpty().Immutable(),
		field.String("title").NotEmpty(), field.String("body").Default(""), field.String("occurred_date").NotEmpty(), field.String("occurred_time").Default(""), field.String("timezone").NotEmpty(), field.String("category").Default(""), field.String("location").Default(""), field.Int64("amount_minor").Default(0), field.String("currency").Default(""), field.String("url").Default(""), field.String("url_title").Default(""), field.String("url_note").Default(""), field.Time("occurred_at"), field.Time("deleted_at").Optional().Nillable(), field.String("mutation_id").Default(""), field.Int("revision").Default(1).Positive(), field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}
func (Entry) Edges() []ent.Edge {
	return []ent.Edge{edge.To("media", MediaObject.Type).Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (Entry) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "occurred_at", "id")} }

type MediaObject struct{ ent.Schema }

func (MediaObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_media_objects"}}
}
func (MediaObject) Fields() []ent.Field {
	return []ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("public_ref").DefaultFunc(newMediaRef).Unique().Immutable(), field.String("user_id").NotEmpty().Immutable(), field.String("entry_id").NotEmpty().Immutable(), field.String("entry_public_ref").NotEmpty().Immutable(), field.String("object_key").NotEmpty().Immutable(), field.String("thumbnail_key").Default(""), field.String("upload_key").Default(""), field.Time("created_at").Default(now).Immutable()}
}
func (MediaObject) Edges() []ent.Edge {
	return []ent.Edge{edge.From("entry", Entry.Type).Ref("media").Field("entry_id").Unique().Required().Immutable()}
}
func (MediaObject) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id"), index.Fields("entry_id")}
}

type CleanupObject struct{ ent.Schema }

func (CleanupObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_cleanup_objects"}}
}
func (CleanupObject) Fields() []ent.Field {
	return []ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("object_key").Unique().NotEmpty().Immutable(), field.Enum("status").Values("pending", "done").Default("pending"), field.Int("attempts").Default(0), field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now)}
}
