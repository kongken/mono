package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// Deletion stages, in order. A workflow only moves forward and every stage is
// idempotent, so any stage can be repeated after a lost response or a restart.
const (
	DeletionStageBegin   = "begin"   // usersrv BeginDeletion
	DeletionStageCleanup = "cleanup" // each product's CleanupUserData
	DeletionStageFinish  = "finish"  // usersrv FinishDeletion
	DeletionStageLocal   = "local"   // the BFF's own sessions and tokens
	DeletionStageDone    = "done"    // finished; the row is the receipt
)

// AccountDeletion is one account deletion workflow and, once finished, its
// receipt. It is written before anything is removed. It holds no email, no
// password and no footprint data; the Cookie only as a SHA-256 digest.
type AccountDeletion struct{ ent.Schema }

func (AccountDeletion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "bff_account_deletions"}}
}

func (AccountDeletion) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		// UserID is the usersrv user UUID; an account is deleted once, so one row each.
		field.String("user_id").NotEmpty().Unique().Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(),
		// TokenHash is the digest of the Cookie that started the deletion. Only that
		// Cookie may read or continue the workflow, for 24 hours after it finished.
		field.String("token_hash").NotEmpty().Immutable().Sensitive(),
		// Products is the cleaner list fixed when the workflow started; a product
		// later removed from the configuration is not silently skipped.
		field.JSON("products", []string{}).Immutable(),
		field.JSON("completed_products", []string{}),
		field.String("stage").Default(DeletionStageBegin),
		field.Int("attempts").Default(0),
		// LastError is a short error class (for example "cleanup:tripmap:unavailable"),
		// never a message from a service.
		field.String("last_error").Default(""),
		// LeaseUntil lets one runner at a time advance the workflow; the request that
		// started it and the background runner both take it.
		field.Time("lease_until").Optional().Nillable(),
		field.Time("next_attempt_at").Default(now),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
		field.Time("completed_at").Optional().Nillable(),
		field.Time("receipt_expires_at").Optional().Nillable(),
	}
}

func (AccountDeletion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("token_hash"),
		index.Fields("stage", "next_attempt_at"),
		index.Fields("receipt_expires_at"),
	}
}
