// Package schema defines the usersrv database: the accounts and everything that
// is owned by one account's login credential. usersrv has a database of its own;
// no other service reads it and nothing here references another service's rows.
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

// User is a formal account. A row exists only after the email was verified, so
// there is no "pending" user: email_verified_at is never null.
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "users"},
		entsql.Checks(map[string]string{
			"users_status":             "status IN ('active', 'disabled', 'deleting')",
			"users_auth_version":       "auth_version >= 1",
			"users_profile_version":    "profile_version >= 1",
			"users_email_canonical":    "email_canonical = lower(email_canonical) AND octet_length(email_canonical) <= 254",
			"users_display_name":       "char_length(display_name) BETWEEN 1 AND 40",
			"users_avatar_url":         "octet_length(avatar_url) <= 500",
			"users_deletion_marker":    "(status = 'deleting') = (deletion_mutation_id IS NOT NULL AND deletion_started_at IS NOT NULL)",
			"users_deletion_mutation":  "deletion_mutation_id IS NULL OR char_length(deletion_mutation_id) BETWEEN 1 AND 64",
			"users_password_hash_size": "char_length(password_hash) BETWEEN 1 AND 512",
		}),
	}
}

func (User) Fields() []ent.Field {
	return []ent.Field{
		// The immutable user ID: the sub of internal credentials and the key of
		// every product's data. The email is never a foreign key.
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		// The canonical login email (see identity.NormalizeEmail). The unique
		// index, not a read before the insert, keeps one account per mailbox.
		field.String("email_canonical").NotEmpty().Unique(),
		field.Time("email_verified_at"),
		// Self-describing Argon2id encoding; never in logs, responses or String().
		field.String("password_hash").Sensitive().NotEmpty(),
		field.String("display_name").Default("旅行者"),
		field.String("avatar_url").Default(""),
		field.Int64("profile_version").Default(1),
		field.Enum("status").Values("active", "disabled", "deleting").Default("active"),
		// Strictly increasing; every credential, status or role change bumps it in
		// the same transaction, which is what invalidates older sessions and tokens.
		field.Int64("auth_version").Default(1),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
		field.Time("deletion_started_at").Optional().Nillable(),
		// The deletion workflow that owns the deleting state.
		field.String("deletion_mutation_id").Optional().Nillable(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{edge.From("roles", UserRole.Type).Ref("user")}
}

// UserRole grants a role from the fixed whitelist. Rows go away with the user.
type UserRole struct{ ent.Schema }

func (UserRole) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "user_roles"},
		entsql.Checks(map[string]string{"user_roles_role": "role IN ('metro-admin', 'ozid-admin')"}),
	}
}

func (UserRole) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("user_id", uuid.UUID{}).Immutable(),
		field.String("role").NotEmpty().Immutable(),
		field.Time("granted_at").Default(now).Immutable(),
		// The operator identifier given to the role CLI; not a user ID.
		field.String("granted_by").Default("").Immutable(),
	}
}

func (UserRole) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).Field("user_id").Unique().Required().Immutable().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (UserRole) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "role").Unique()}
}

// AccountToken is a short-lived, single-use challenge (registration or password
// reset). Only the SHA-256 digest of the token is stored; the plaintext exists
// in the email and, until delivered, in the encrypted outbox of stage P2.
// Registration tokens bind an address, not a user: no account exists yet.
type AccountToken struct{ ent.Schema }

func (AccountToken) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_tokens"},
		entsql.Checks(map[string]string{
			"account_tokens_purpose":    "purpose IN ('registration', 'password_reset')",
			"account_tokens_hash_size":  "octet_length(token_hash) = 32",
			"account_tokens_binding":    "(purpose = 'registration' AND user_id IS NULL AND issued_auth_version IS NULL) OR (purpose = 'password_reset' AND user_id IS NOT NULL AND issued_auth_version IS NOT NULL)",
			"account_tokens_expiry":     "expires_at > created_at",
			"account_tokens_email":      "email_canonical = lower(email_canonical) AND octet_length(email_canonical) <= 254",
			"account_tokens_app_id":     "char_length(app_id) BETWEEN 1 AND 64",
			"account_tokens_auth_floor": "issued_auth_version IS NULL OR issued_auth_version >= 1",
			"account_tokens_return":     "(return_app_id IS NULL AND return_path IS NULL) OR (return_app_id IS NOT NULL AND char_length(return_app_id) BETWEEN 1 AND 64 AND (return_path IS NULL OR char_length(return_path) BETWEEN 1 AND 256))",
		}),
	}
}

func (AccountToken) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.Bytes("token_hash").Unique().Immutable().Sensitive(),
		field.Enum("purpose").Values("registration", "password_reset").Immutable(),
		field.String("email_canonical").NotEmpty().Immutable(),
		// No foreign key: tokens are removed by the account lifecycle code, and a
		// registration token has no user at all.
		field.UUID("user_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		field.Int64("issued_auth_version").Optional().Nillable().Immutable(),
		// The app whose origin the mail link points at: always the central OzID app
		// for a challenge issued now; trip-map or metro on a challenge issued before the
		// central landing existed.
		field.String("app_id").NotEmpty().Immutable(),
		// The return context bound to the challenge (the app the user came from and a
		// canonical pathname in it), returned when the token is consumed so that another
		// browser can finish the flow. NULL on a challenge issued without one, and on
		// every challenge issued before it existed: those return to no app.
		field.String("return_app_id").Optional().Nillable().Immutable(),
		field.String("return_path").Optional().Nillable().Immutable(),
		field.Time("expires_at").Immutable(),
		field.Time("consumed_at").Optional().Nillable(),
		field.Time("created_at").Default(now).Immutable(),
	}
}

func (AccountToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("email_canonical", "purpose"),
		index.Fields("user_id"),
		index.Fields("expires_at"),
	}
}

// SecurityAudit records who changed what: user UUID (when there is one), the
// operator, the action, the request and the result. It never holds an email, a
// password or a token, and it is not a way back to a deleted identity.
type SecurityAudit struct{ ent.Schema }

func (SecurityAudit) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "security_audit"},
		entsql.Checks(map[string]string{
			"security_audit_action": "char_length(action) BETWEEN 1 AND 64",
			"security_audit_result": "char_length(result) BETWEEN 1 AND 64",
			"security_audit_reason": "char_length(reason) <= 200",
			"security_audit_actor":  "char_length(actor) <= 64",
			"security_audit_change": "char_length(channel) <= 16 AND char_length(mutation_id) <= 64 AND " +
				"char_length(before_status) <= 16 AND char_length(after_status) <= 16 AND " +
				"char_length(before_roles) <= 200 AND char_length(after_roles) <= 200",
		}),
	}
}

func (SecurityAudit) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("user_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		// "user:<uuid>", "system", or the operator name given to the CLI.
		field.String("actor").Default("").Immutable(),
		field.String("action").NotEmpty().Immutable(),
		field.String("request_id").Default("").Immutable(),
		field.String("result").NotEmpty().Immutable(),
		// A free-text operator reason (CLI only). Operators must not put an email in it.
		field.String("reason").Default("").Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		// "web" for the administration console, "cli" for the role CLI, "" for rows
		// that predate the field or are not account changes.
		field.String("channel").Default("").Immutable(),
		// The operator account of a web change (actor is "user:<uuid>" as well; this
		// column lets the audit be queried by UUID without parsing text).
		field.UUID("actor_user_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		// The idempotency key of the web change this row records.
		field.String("mutation_id").Default("").Immutable(),
		// Status and roles of the target before and after a change; roles are a sorted,
		// comma-separated list of whitelisted names. Empty when not an account change.
		field.String("before_status").Default("").Immutable(),
		field.String("after_status").Default("").Immutable(),
		field.String("before_roles").Default("").Immutable(),
		field.String("after_roles").Default("").Immutable(),
		// Only on a "replayed" row: the id of the audit row of the change whose stored answer
		// was handed out again. It is the exact link (mutation IDs are reusable once their
		// receipt expires, so (operator, mutation_id) alone does not name one event). Empty for
		// every other row, and for a replay of a receipt that predates the link.
		field.UUID("origin_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		// Whether the reason text of this row went through the content check of the write path
		// (no email, password or token) or was generated by code. False for the history written
		// before the console could show reasons: that free text was never checked, so a reader
		// must not show it.
		field.Bool("reason_checked").Default(false).Immutable(),
	}
}

func (SecurityAudit) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"), index.Fields("user_id", "created_at"),
		index.Fields("actor_user_id", "created_at"), index.Fields("action", "created_at"),
	}
}

// DeletionTombstone proves that an account deletion finished, so a lost
// FinishDeletion response can be replayed within 24 hours by the same user and
// workflow without the missing user row allowing any other step. It holds
// neither email nor credentials.
type DeletionTombstone struct{ ent.Schema }

func (DeletionTombstone) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "deletion_tombstones"},
		entsql.Checks(map[string]string{
			"deletion_tombstones_mutation": "char_length(mutation_id) BETWEEN 1 AND 64",
			"deletion_tombstones_expiry":   "expires_at > completed_at",
		}),
	}
}

func (DeletionTombstone) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).StorageKey("user_id").Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(),
		field.Time("completed_at").Default(now).Immutable(),
		field.Time("expires_at").Immutable(),
	}
}

func (DeletionTombstone) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at")}
}

// MailJob is one outbox entry of an account mail. The recipient, the template
// parameters and the plaintext token live only in ciphertext (AES-256-GCM, key
// outside this database, associated data bound to the job ID and template); the
// plain columns carry no address, token, link or body. A job that reached a
// final state keeps its status record for retention but never its ciphertext.
type MailJob struct{ ent.Schema }

func (MailJob) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "mail_jobs"},
		entsql.Checks(map[string]string{
			"mail_jobs_status":     "status IN ('pending', 'sending', 'accepted', 'failed', 'uncertain', 'expired', 'cancelled')",
			"mail_jobs_provider":   "provider IN ('mailgun', 'sendgrid', 'smtp', 'smtp2go')",
			"mail_jobs_template":   "template IN ('verify_email', 'reset_password')",
			"mail_jobs_attempts":   "attempts BETWEEN 0 AND 3",
			"mail_jobs_generation": "generation >= 1",
			"mail_jobs_result":     "char_length(result_class) <= 64",
			"mail_jobs_key_id":     "key_id IS NULL OR char_length(key_id) BETWEEN 1 AND 64",
			"mail_jobs_expiry":     "expires_at > created_at",
			// Secrets exist exactly while the job can still be sent.
			"mail_jobs_ciphertext": "(status IN ('pending', 'sending')) = (ciphertext IS NOT NULL AND key_id IS NOT NULL)",
			"mail_jobs_lease":      "(status = 'sending') = (lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)",
			"mail_jobs_finished":   "(status IN ('pending', 'sending')) = (finished_at IS NULL)",
		}),
	}
}

func (MailJob) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		// The account_tokens row this mail carries. No foreign key: challenges are
		// purged by the lifecycle code and the job record must outlive them.
		field.UUID("challenge_id", uuid.UUID{}).Immutable(),
		// Fixed when the job is issued: a configuration change never re-routes a
		// queued job to another vendor.
		field.String("provider").NotEmpty().Immutable(),
		field.Int("generation").Immutable(),
		field.String("template").NotEmpty().Immutable(),
		field.Bytes("ciphertext").Optional().Sensitive(),
		field.String("key_id").Optional().Nillable(),
		field.Enum("status").Values("pending", "sending", "accepted", "failed", "uncertain", "expired", "cancelled").Default("pending"),
		field.Int("attempts").Default(0),
		field.Time("next_attempt_at"),
		field.String("lease_owner").Optional().Nillable(),
		field.Time("lease_expires_at").Optional().Nillable(),
		// A short fixed classification such as "ok", "temporary", "permanent",
		// "uncertain", "decrypt_failed", "challenge_consumed"; never vendor text.
		field.String("result_class").Default(""),
		field.Time("expires_at").Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (MailJob) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "next_attempt_at"),
		index.Fields("status", "lease_expires_at"),
		index.Fields("challenge_id"),
		index.Fields("finished_at"),
	}
}

// AdminMutation is the receipt of one administrator change made through the web
// console: who asked (operator), under which key (mutation_id), for what (a hash of
// the canonical request) and what the answer was. A retry with the same key and the
// same content gets this answer back without a second change; the same key with other
// content is refused. Receipts are keyed by operator, so one administrator's key can
// neither replay nor block another's, and they expire after AdminMutationTTL. A
// receipt holds UUIDs and the outcome only: no email, reason text or secret.
type AdminMutation struct{ ent.Schema }

func (AdminMutation) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "admin_mutations"},
		entsql.Checks(map[string]string{
			"admin_mutations_mutation": "char_length(mutation_id) BETWEEN 1 AND 64",
			"admin_mutations_action":   "char_length(action) BETWEEN 1 AND 64",
			"admin_mutations_hash":     "octet_length(request_hash) = 32",
			"admin_mutations_expiry":   "expires_at > created_at",
			"admin_mutations_status":   "status IN ('active', 'disabled', 'deleting')",
			"admin_mutations_version":  "auth_version >= 1",
			"admin_mutations_roles":    "char_length(roles) <= 200",
		}),
	}
}

func (AdminMutation) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("operator_id", uuid.UUID{}).Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(),
		field.String("action").NotEmpty().Immutable(),
		field.UUID("target_id", uuid.UUID{}).Immutable(),
		// SHA-256 of the canonical request (action, target, expected version, reason).
		field.Bytes("request_hash").Immutable(),
		// What the first answer said: whether anything changed, and the target as it
		// stood right after.
		field.Bool("changed").Immutable(),
		field.String("status").Immutable(),
		field.String("roles").Default("").Immutable(),
		field.Int64("auth_version").Immutable(),
		// The audit row of the request that made this receipt: what a replay points to.
		// Empty for a receipt written before the link existed.
		field.UUID("audit_id", uuid.UUID{}).Optional().Nillable().Immutable(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("expires_at").Immutable(),
	}
}

func (AdminMutation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("operator_id", "mutation_id").Unique(), index.Fields("expires_at")}
}
