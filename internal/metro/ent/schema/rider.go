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

// RiderProfile is a rider's public-facing profile. It is created the first time
// the rider writes it, not at sign-in, so metro needs no login hook.
type RiderProfile struct{ ent.Schema }

func (RiderProfile) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "rider_profiles"},
		entsql.Checks(map[string]string{
			"rider_profiles_handle_format": "handle IS NULL OR handle ~ '^[a-z0-9_]{3,32}$'",
			"rider_profiles_bio_length":    "char_length(bio) <= 500",
			"rider_profiles_images_limit":  "jsonb_array_length(header_images) <= 8",
			"rider_profiles_public_handle": "NOT is_public OR handle IS NOT NULL",
		}),
	}
}

func (RiderProfile) Fields() []ent.Field {
	return []ent.Field{
		// The primary key is the BFF's business user ID, the subject of the
		// internal credential.
		field.String("id").StorageKey("user_id").Immutable().NotEmpty(),
		field.String("handle").Optional().Nillable().Unique(),
		field.String("display_name").Default(""),
		field.String("subtitle").Default(""),
		field.String("bio").Default(""),
		field.String("location").Default(""),
		field.JSON("header_images", []string{}).Default([]string{}),
		field.String("twitter").Default(""),
		field.String("instagram").Default(""),
		field.String("weibo").Default(""),
		field.String("facebook").Default(""),
		field.String("github").Default(""),
		field.Bool("is_public").Default(false),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

// RiderLine records that a rider rode a line. Marking is one idempotent write
// per (user, line), and the line reference is RESTRICT: a line somebody rode is
// retired rather than deleted.
type RiderLine struct{ ent.Schema }

func (RiderLine) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "rider_lines"},
		entsql.Checks(map[string]string{"rider_lines_year": "ridden_year IS NULL OR ridden_year >= 1900"}),
	}
}

func (RiderLine) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").NotEmpty().Immutable(),
		field.String("line_id").Immutable(),
		field.Int("ridden_year").Optional().Nillable(),
		field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

func (RiderLine) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("line", Line.Type).Field("line_id").Unique().Required().Immutable().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (RiderLine) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "line_id").Unique(), index.Fields("line_id")}
}
