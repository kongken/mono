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

// AccessToken is a management token for non-browser clients (the metro MCP
// endpoint). Only the SHA-256 digest of the secret is stored. It acts as its
// owner with the roles the owner had when it was created.
type AccessToken struct{ ent.Schema }

func (AccessToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bff_access_tokens"}}
}

func (AccessToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.String("name").NotEmpty().Immutable(),
		field.String("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		// Audience names the product endpoint that accepts the token.
		field.String("audience").NotEmpty().Immutable(),
		field.JSON("scopes", []string{}).Immutable(),
		// Roles is the owner's role snapshot taken at creation.
		field.JSON("roles", []string{}).Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at").Immutable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (AccessToken) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("access_tokens").Field("user_id").Unique().Required().Immutable(),
	}
}

func (AccessToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id").StorageKey("bff_access_token_user_id"),
	}
}
