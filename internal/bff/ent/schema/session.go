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

// Session is a browser session; only the token hash is stored.
type Session struct{ ent.Schema }

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_browser_sessions"}}
}

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.String("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at"),
		field.Time("last_used_at").Optional().Nillable(),
		field.Bool("revoked").Default(false),
		// Roles are the Logto role names read at sign-in. Sessions created before
		// roles were recorded have none and are never administrators.
		field.JSON("roles", []string{}).Optional(),
		// AuthenticatedAt is when the user signed in at Logto (the session's
		// issue time). Procedures that declare required_role are refused once it
		// is older than auth.admin_session_max_age. Null on older sessions, which
		// fall back to created_at.
		field.Time("authenticated_at").Optional().Nillable().Immutable(),
	}
}

func (Session) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("sessions").Field("user_id").Unique().Required().Immutable(),
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id").StorageKey("browser_session_user_id"),
		index.Fields("expires_at").StorageKey("browser_session_expires_at"),
	}
}
