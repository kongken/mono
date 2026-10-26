package schema

import (
	"fmt"
	"regexp"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// WorldDocument is a user's single world footprint document (世界视图,
// docs/trip-map/world-spec.md section 9). The row appears with the first write
// that changes the (empty) cloud document; a user without a row has the empty
// document at revision 0. The document is replaced as a whole and revision grows
// by one with every change.
//
// payload is a gzip-compressed tripmap.v1.WorldDocument in its canonical form
// (sorted, names trimmed). World data stays out of footprints, snapshots, search,
// sharing, reports, collaboration, reminders and media: this table is its only
// home. One row per user is enforced by the unique user_id index, which also
// settles a race between two first writes (the one-to-one user edge makes user_id unique).
type WorldDocument struct{ ent.Schema }

func (WorldDocument) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "world_documents"}}
}
func (WorldDocument) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.Int64("revision").Positive(),
		field.Bytes("payload"),
		// Denormalised counts for operations and limits; the payload is the truth.
		field.Int("place_count").NonNegative(),
		field.Int("trip_count").NonNegative(),
	}, timestamps()...)
}
func (WorldDocument) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("world_document").Field("user_id").Unique().Required().Immutable()}
}

// WorldReceipt remembers one successful ReplaceWorld so that a client which lost
// the response can replay the same request_id. Only the last 32 per user are
// kept (oldest by seq go first). A receipt holds the request's digest and the
// result revision, never the document.
type WorldReceipt struct{ ent.Schema }

func (WorldReceipt) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "world_receipts"}}
}

var worldDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (WorldReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.String("request_id").NotEmpty().Immutable().Validate(validTripID),
		// Hex SHA-256 over base_revision and the canonical document, computed by the server.
		field.String("digest").Immutable().Validate(func(v string) error {
			if !worldDigestPattern.MatchString(v) {
				return fmt.Errorf("digest must be a hex SHA-256")
			}
			return nil
		}),
		// The result revision the request got (after a no-op, the unchanged revision).
		field.Int64("revision").NonNegative().Immutable(),
		// Per-user counter that orders receipts; assigned under the user's row lock.
		field.Int64("seq").Positive().Immutable(),
		field.Time("created_at").Default(now).Immutable(),
	}
}
func (WorldReceipt) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("world_receipts").Field("user_id").Unique().Required().Immutable()}
}
func (WorldReceipt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "request_id").Unique(),
		index.Fields("user_id", "seq").Unique(),
	}
}
