package schema

import (
	"time"

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
	return []ent.Field{
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

// User holds BFF-owned account identity (password and future OAuth bindings).
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_users"}}
}

func (User) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("username").Optional().Nillable().Unique(),
		field.String("display_name").Default(""),
		field.String("avatar_url").Default(""),
		field.String("provider").Default(""),
		field.String("external_id").Default(""),
		field.String("password_hash").Default("").Sensitive(),
		field.Bool("disabled").Default(false),
		field.Time("deletion_requested_at").Optional().Nillable(),
	}, timestamps()...)
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "external_id").Unique().Annotations(entsql.IndexWhere("external_id <> ''")),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sessions", Session.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
