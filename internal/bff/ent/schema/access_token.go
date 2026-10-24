package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AccessToken is a management token for non-browser clients (the metro MCP
// endpoint). Only the SHA-256 digest of the secret is stored. It acts as its
// owner, but only while the owner's account is active at the same auth_version and
// still holds the roles the token needs (the intersection of the creation-time
// snapshot and the current roles).
type AccessToken struct{ ent.Schema }

func (AccessToken) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bff_access_tokens"}}
}

func (AccessToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty().Immutable(),
		field.String("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		// Audience names the product endpoint that accepts the token.
		field.String("audience").NotEmpty().Immutable(),
		field.JSON("scopes", []string{}).Immutable(),
		// Roles is the owner's role snapshot taken at creation.
		field.JSON("roles", []string{}).Immutable(),
		// AuthVersion is the owner's auth_version at creation. Tokens from before
		// usersrv carry 0 and never match.
		field.Int64("auth_version").Default(0).Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at").Immutable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (AccessToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id").StorageKey("bff_access_token_user_id"),
	}
}
