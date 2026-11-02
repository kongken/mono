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

// AuthChallenge holds browser/session-bound proofs, never OAuth credentials.
type AuthChallenge struct{ ent.Schema }

func (AuthChallenge) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_challenges"}, entsql.Checks(map[string]string{
		"auth_challenges_kind":          "kind IN ('external_login','external_registration','identity_link','identity_unlink','set_password','recovery','account_deletion')",
		"auth_challenges_status":        "status IN ('issued','exchanging','ready','completed','failed')",
		"auth_challenges_context":       "octet_length(context_hash) = 32 AND (session_binding_hash IS NULL OR octet_length(session_binding_hash) = 32)",
		"auth_challenges_owner":         "(user_id IS NULL AND auth_version IS NULL AND session_binding_hash IS NULL) OR (user_id IS NOT NULL AND auth_version IS NOT NULL AND auth_version >= 1 AND session_binding_hash IS NOT NULL)",
		"auth_challenges_email_hash":    "email_token_hash IS NULL OR octet_length(email_token_hash) = 32",
		"auth_challenges_result_hash":   "result_request_hash IS NULL OR octet_length(result_request_hash) = 32",
		"auth_challenges_kind_owner":    "(kind IN ('external_login','external_registration') AND user_id IS NULL) OR (kind NOT IN ('external_login','external_registration') AND user_id IS NOT NULL)",
		"auth_challenges_issuer":        "provider IS NULL OR (provider = 'google' AND issuer = 'https://accounts.google.com') OR (provider = 'github' AND issuer = 'https://github.com')",
		"auth_challenges_target":        "kind <> 'identity_unlink' OR target_identity_id IS NOT NULL",
		"auth_challenges_kind_provider": "kind NOT IN ('external_login','external_registration','identity_link') OR provider IS NOT NULL",
		"auth_challenges_result":        "(result_mutation_id IS NULL AND result_request_hash IS NULL AND result_user_id IS NULL AND result_expires_at IS NULL) OR (status = 'completed' AND result_mutation_id IS NOT NULL AND char_length(result_mutation_id) BETWEEN 1 AND 64 AND result_request_hash IS NOT NULL AND result_user_id IS NOT NULL AND result_expires_at IS NOT NULL)",
		"auth_challenges_expiry":        "expires_at > created_at",
		"auth_challenges_proof":         "(proof_ciphertext IS NULL) = (proof_key_id IS NULL)",
		"auth_challenges_completed":     "status <> 'completed' OR consumed_at IS NOT NULL",
		"auth_challenges_provider":      "(provider IS NULL AND issuer IS NULL AND provider_config_version IS NULL) OR (provider IS NOT NULL AND provider IN ('google','github') AND issuer IS NOT NULL AND provider_config_version IS NOT NULL AND provider_config_version >= 1)",
	})}
}
func (AuthChallenge) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.Enum("kind").Values("external_login", "external_registration", "identity_link", "identity_unlink", "set_password", "recovery", "account_deletion"),
		field.UUID("user_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		field.Enum("provider").Values("google", "github").Optional().Nillable().Immutable(),
		field.String("issuer").Optional().Nillable().Immutable(),
		field.UUID("bff_flow_id", uuid.UUID{}).Optional().Nillable().Unique().Immutable(),
		field.Bytes("context_hash").Sensitive().Immutable(),
		field.Bytes("session_binding_hash").Optional().Sensitive().Immutable(),
		field.UUID("target_identity_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		field.Int64("provider_config_version").Optional().Nillable().Immutable(),
		field.Bytes("proof_ciphertext").Optional().Sensitive(),
		field.String("proof_key_id").Optional().Nillable(),
		field.Bytes("email_token_hash").Optional().Sensitive(),
		field.Time("email_expires_at").Optional().Nillable(),
		field.Time("confirmed_at").Optional().Nillable(),
		field.Enum("status").Values("issued", "exchanging", "ready", "completed", "failed").Default("issued"),
		field.String("result_mutation_id").Optional().Nillable(),
		field.Bytes("result_request_hash").Optional().Sensitive(),
		field.UUID("result_user_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("result_expires_at").Optional().Nillable(),
		field.Int64("auth_version").Optional().Nillable().Immutable(),
		field.String("return_app_id").Default("").Immutable(),
		field.String("return_path").Default("").Immutable(),
		field.Time("expires_at"),
		field.Time("consumed_at").Optional().Nillable(),
		field.Time("created_at").Default(now).Immutable(),
	}
}
func (AuthChallenge) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", User.Type).Field("user_id").Unique().Immutable().Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (AuthChallenge) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at"), index.Fields("user_id", "kind", "expires_at"), index.Fields("email_token_hash").Unique()}
}

type AuthOperationReceipt struct{ ent.Schema }

func (AuthOperationReceipt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_operation_receipts"}, entsql.Checks(map[string]string{
		"auth_receipts_operation": "operation IN ('identity_link','identity_unlink','set_password','recovery')",
		"auth_receipts_result":    "result IN ('applied','already_applied','rejected')",
		"auth_receipts_hash":      "octet_length(request_hash) = 32 AND octet_length(session_binding_hash) = 32",
		"auth_receipts_mutation":  "char_length(mutation_id) BETWEEN 1 AND 64",
		"auth_receipts_version":   "auth_version >= 1",
		"auth_receipts_expiry":    "expires_at > created_at",
	})}
}
func (AuthOperationReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("user_id", uuid.UUID{}).Immutable(),
		field.Enum("operation").Values("identity_link", "identity_unlink", "set_password", "recovery").Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(),
		field.Bytes("request_hash").Sensitive().Immutable(),
		field.Bytes("session_binding_hash").Sensitive().Immutable(),
		field.Enum("result").Values("applied", "already_applied", "rejected").Immutable(),
		field.Int64("auth_version").Immutable(),
		field.UUID("identity_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at").Immutable(),
	}
}
func (AuthOperationReceipt) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", User.Type).Field("user_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (AuthOperationReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "operation", "mutation_id").Unique(), index.Fields("user_id", "created_at"), index.Fields("expires_at")}
}

type AuthNotification struct{ ent.Schema }

func (AuthNotification) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auth_notifications"}, entsql.Checks(map[string]string{"auth_notifications_source": "source_kind IN ('operation_receipt','reset_token')", "auth_notifications_kind": "notification_kind IN ('identity_link','identity_unlink','password_set_first')"})}
}
func (AuthNotification) Fields() []ent.Field {
	return []ent.Field{field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(), field.UUID("user_id", uuid.UUID{}).Immutable(),
		field.Enum("source_kind").Values("operation_receipt", "reset_token").Immutable(), field.UUID("source_id", uuid.UUID{}).Immutable(),
		field.Enum("notification_kind").Values("identity_link", "identity_unlink", "password_set_first").Immutable(), field.Time("created_at").Default(now).Immutable()}
}
func (AuthNotification) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", User.Type).Field("user_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (AuthNotification) Indexes() []ent.Index {
	return []ent.Index{index.Fields("source_kind", "source_id", "notification_kind").Unique(), index.Fields("user_id", "created_at")}
}
