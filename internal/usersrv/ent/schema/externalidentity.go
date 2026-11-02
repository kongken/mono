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

// ExternalIdentity is a login credential owned by one formal account. Provider
// tokens and raw claims never belong here. Creation/removal must hold the user
// lock and preserve the credential invariants in ADR 0001; the NULL-password
// relationship cannot be expressed as a cross-table PostgreSQL CHECK.
type ExternalIdentity struct{ ent.Schema }

func (ExternalIdentity) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "external_identities"},
		entsql.Checks(map[string]string{
			"external_identities_provider": "provider IN ('google', 'github')",
			"external_identities_issuer": "(provider = 'google' AND issuer = 'https://accounts.google.com') OR " +
				"(provider = 'github' AND issuer = 'https://github.com')",
			"external_identities_subject": "char_length(subject) BETWEEN 1 AND 255 AND " +
				"(provider <> 'github' OR subject ~ '^[1-9][0-9]*$')",
		}),
	}
}

func (ExternalIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("user_id", uuid.UUID{}).Immutable(),
		field.Enum("provider").Values("google", "github").Immutable(),
		field.String("issuer").NotEmpty().Immutable(),
		field.String("subject").NotEmpty().Sensitive().Immutable(),
		field.String("email_snapshot").Optional().Nillable().Sensitive(),
		field.Time("email_verified_at").Optional().Nillable(),
		field.String("display_name_snapshot").Optional().Nillable(),
		field.String("avatar_url_snapshot").Optional().Nillable(),
		field.Time("linked_at").Default(now).Immutable(),
		field.Time("last_authenticated_at").Default(now),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

func (ExternalIdentity) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).Field("user_id").Unique().Required().Immutable().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ExternalIdentity) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "issuer", "subject").Unique(),
		index.Fields("user_id", "provider").Unique(),
		index.Fields("user_id", "linked_at", "id"),
	}
}
