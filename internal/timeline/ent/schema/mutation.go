package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"time"
)

type TimelineMutation struct{ ent.Schema }

func (TimelineMutation) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "timeline_mutations"}}
}
func (TimelineMutation) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").NotEmpty().Immutable(), field.String("mutation_id").NotEmpty().Immutable(), field.String("target_kind").NotEmpty().Immutable(), field.String("operation").NotEmpty().Immutable(), field.String("entry_ref").Optional().Nillable().Immutable(), field.String("media_ref").Optional().Nillable().Immutable(), field.String("request_hash").NotEmpty().Immutable(), field.Int("result_revision").Positive().Immutable(), field.String("result_state").NotEmpty().Immutable(), field.JSON("result_snapshot", map[string]any{}).Immutable(), field.Int("snapshot_version").Default(1).Immutable(), field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
	}
}
func (TimelineMutation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "mutation_id").Unique()}
}
