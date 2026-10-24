package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Session is a browser session; only the token hash is stored. user_id is a
// usersrv user UUID with no foreign key: the account lives in another database.
type Session struct{ ent.Schema }

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_browser_sessions"}}
}

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").NotEmpty().Immutable(),
		field.String("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at"),
		field.Time("last_used_at").Optional().Nillable(),
		field.Bool("revoked").Default(false),
		// AuthVersion is the account's auth_version when the session was opened. The
		// session is usable only while usersrv still reports the same value. Sessions
		// from before usersrv carry 0, which no account ever has, so they never match.
		field.Int64("auth_version").Default(0).Immutable(),
		// AuthenticatedAt is when the password was last really checked on this
		// session: sign-in, or a successful Reauthenticate. It is not the creation
		// time, and refreshing a Cookie or calling Me never moves it. Null on rows
		// from before usersrv.
		field.Time("authenticated_at").Optional().Nillable(),
		// roles (a Logto snapshot) was retired: roles are read from usersrv on every
		// request. The column stays in databases that have it and is no longer used.
	}
}

func (Session) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id").StorageKey("browser_session_user_id"),
		index.Fields("expires_at").StorageKey("browser_session_expires_at"),
	}
}
