package schema

import (
	"fmt"
	"time"
	"unicode/utf16"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

func now() time.Time { return time.Now().UTC() }

func timestamps() []ent.Field {
	return []ent.Field{field.Time("created_at").Default(now).Immutable(), field.Time("updated_at").Default(now).UpdateDefault(now)}
}

// User reserves ownership of cloud records; authentication is a later feature.
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "users"}}
}
func (User) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("email").Optional().Nillable().Unique(),
		field.String("display_name").Default(""),
		field.Int64("revision").Default(0).NonNegative(),
	}, timestamps()...)
}
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("visits", CityVisit.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("province_marks", ProvinceMark.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("media_objects", MediaObject.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

type CityVisit struct{ ent.Schema }

func (CityVisit) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "city_visits"}}
}
func (CityVisit) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("region_code").NotEmpty(), field.String("region_name").NotEmpty(), field.String("province_code").NotEmpty(),
		field.String("color").Validate(validColor),
		field.String("travel_date").Default("").Validate(validDate),
		field.String("note").Default("").Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 1000 {
				return fmt.Errorf("note exceeds 1000 characters")
			}
			return nil
		}),
	}, timestamps()...)
}
func (CityVisit) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("visits").Field("user_id").Unique().Required().Immutable()}
}
func (CityVisit) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "region_code").Unique(), index.Fields("user_id", "province_code")}
}

type ProvinceMark struct{ ent.Schema }

func (ProvinceMark) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "province_marks"}}
}
func (ProvinceMark) Fields() []ent.Field {
	return append([]ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(), field.String("province_code").NotEmpty(), field.String("color").Validate(validColor)}, timestamps()...)
}
func (ProvinceMark) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("province_marks").Field("user_id").Unique().Required().Immutable()}
}
func (ProvinceMark) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "province_code").Unique()}
}

type MediaObject struct{ ent.Schema }

func (MediaObject) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "media_objects"}}
}
func (MediaObject) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("object_key").NotEmpty().Unique(), field.String("content_type").NotEmpty(), field.Int64("size_bytes").NonNegative(),
		field.Enum("status").Values("pending", "ready").Default("pending"),
	}, timestamps()...)
}
func (MediaObject) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("media_objects").Field("user_id").Unique().Required().Immutable()}
}

func validColor(v string) error {
	switch v {
	case "#4b937d", "#e2af5c", "#d88370", "#719fc6", "#a08cbd":
		return nil
	}
	return fmt.Errorf("unsupported footprint color")
}
func validDate(v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("invalid travel date")
	}
	return nil
}
