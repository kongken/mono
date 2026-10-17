package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AccountDeletionReceipt proves a deletion finished; it stores only a session
// token hash and expiry (no account or footprint data).
type AccountDeletionReceipt struct{ ent.Schema }

func (AccountDeletionReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("token_hash").Unique(),
		field.Time("expires_at"),
	}
}

func (AccountDeletionReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("expires_at")}
}
