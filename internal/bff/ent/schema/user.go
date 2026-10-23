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

// User is a local Logto identity mapping and profile cache, not an account store.
// Logto owns credentials and registration; ID is only the stable business key.
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_identities"}}
}

func (User) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("username").Optional().Nillable(),
		field.String("display_name").Default(""),
		field.String("avatar_url").Default(""),
		field.String("issuer").NotEmpty().Immutable(),
		field.String("external_id").NotEmpty().Immutable(),
		field.Bool("disabled").Default(false),
		field.Time("deletion_requested_at").Optional().Nillable(),
	}, timestamps()...)
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("issuer", "external_id").Unique(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sessions", Session.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("access_tokens", AccessToken.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
