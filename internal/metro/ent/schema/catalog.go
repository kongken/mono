// Package schema defines the metro database. Field rules that the service also
// enforces in code (lengths, colors, ranges) are repeated here as constraints so
// a bug or a manual edit cannot store a row the service would refuse.
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

func now() time.Time { return time.Now().UTC() }

// base holds the identity, optimistic-concurrency and timestamp fields every
// catalog entity shares. Revision starts at 1 and the service adds 1 on every
// change; administrators must present it when updating (expected_revision).
func base() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.Int("revision").Default(1).Positive(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

var (
	numeric82 = map[string]string{dialect.Postgres: "numeric(8,2)"}
	numeric62 = map[string]string{dialect.Postgres: "numeric(6,2)"}
	dateType  = map[string]string{dialect.Postgres: "date"}
)

// City is a city with a metro system, identified by slug. It is not the
// administrative "city footprint" of tripmap.
type City struct{ ent.Schema }

func (City) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "cities"},
		entsql.Checks(map[string]string{
			"cities_slug_format":  "slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'",
			"cities_slug_length":  "char_length(slug) <= 32",
			"cities_name_length":  "char_length(name) BETWEEN 1 AND 32",
			"cities_images_limit": "jsonb_array_length(header_images) <= 8",
			"cities_region":       "region IN ('', 'east', 'north', 'south', 'southwest', 'central', 'northeast', 'northwest')",
		}),
	}
}

func (City) Fields() []ent.Field {
	return append(base(),
		field.String("slug").Unique().NotEmpty(),
		field.String("name").NotEmpty(),
		field.String("region").Default(""),
		field.JSON("header_images", []string{}).Default([]string{}),
		field.String("map_svg_url").Default(""),
		field.Int("sort_order").Default(0),
	)
}

func (City) Edges() []ent.Edge {
	// RESTRICT: a city that still has lines cannot be deleted.
	return []ent.Edge{edge.To("lines", Line.Type).Annotations(entsql.OnDelete(entsql.Restrict))}
}

func (City) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sort_order", "slug")}
}

// RollingStock is one train model, identified by code (for example A型 or SFM04).
type RollingStock struct{ ent.Schema }

func (RollingStock) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "rolling_stocks"},
		entsql.Checks(map[string]string{
			"rolling_stocks_code_length": "char_length(code) BETWEEN 1 AND 32",
			"rolling_stocks_dimensions":  "(length_m IS NULL OR length_m > 0) AND (width_m IS NULL OR width_m > 0) AND (capacity IS NULL OR capacity > 0)",
		}),
	}
}

func (RollingStock) Fields() []ent.Field {
	return append(base(),
		field.String("code").Unique().NotEmpty(),
		field.Float("length_m").Optional().Nillable().SchemaType(numeric62),
		field.Float("width_m").Optional().Nillable().SchemaType(numeric62),
		field.Int("capacity").Optional().Nillable(),
	)
}

func (RollingStock) Edges() []ent.Edge {
	// RESTRICT: rolling stock used by a line cannot be deleted.
	return []ent.Edge{edge.To("lines", Line.Type).Annotations(entsql.OnDelete(entsql.Restrict))}
}

// LineStatuses are the stored line statuses, in lifecycle order.
var LineStatuses = []string{"planned", "under_construction", "operating", "suspended", "retired"}

// Line is one metro line of a city, identified by code (such as sh-1). Its code
// never changes after creation: rider records and external links depend on it.
type Line struct{ ent.Schema }

func (Line) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "lines"},
		entsql.Checks(map[string]string{
			"lines_code_format":   "code ~ '^[a-z0-9][a-z0-9-]{1,31}$'",
			"lines_name_length":   "char_length(name) BETWEEN 1 AND 32",
			"lines_counts":        "number >= 0 AND cars >= 0 AND cars_addition >= 0 AND stations >= 0 AND length_km >= 0",
			"lines_color_format":  "color ~ '^#[0-9A-Fa-f]{6}$'",
			"lines_vehicle_type":  "vehicle_type IN ('', 'a', 'b', 'l', 'rubber', 'other')",
			"lines_station_names": "jsonb_array_length(station_names) = 0 OR jsonb_array_length(station_names) = stations",
		}),
	}
}

func (Line) Fields() []ent.Field {
	return append(base(),
		field.String("code").Unique().Immutable().NotEmpty(),
		field.String("city_id"),
		field.String("name").NotEmpty(),
		// 0 is unnumbered and sorts last.
		field.Int("number").Default(0),
		field.Enum("status").Values(LineStatuses...).Default("operating"),
		field.String("rolling_stock_id").Optional().Nillable(),
		field.Int("cars").Default(0),
		field.Int("cars_addition").Default(0),
		field.Int("stations").Default(0),
		field.Float("length_km").Default(0).SchemaType(numeric82),
		field.String("color"),
		field.Time("commenced_on").Optional().Nillable().SchemaType(dateType),
		field.Time("extended_on").Optional().Nillable().SchemaType(dateType),
		field.Bool("is_loop").Default(false),
		field.String("vehicle_type").Default(""),
		field.JSON("station_names", []string{}).Default([]string{}).Annotations(entsql.Default("[]")),
	)
}

func (Line) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("city", City.Type).Ref("lines").Field("city_id").Unique().Required(),
		edge.From("rolling_stock", RollingStock.Type).Ref("lines").Field("rolling_stock_id").Unique(),
	}
}

func (Line) Indexes() []ent.Index {
	return []ent.Index{index.Fields("city_id", "number", "code"), index.Fields("status")}
}

// RollingStockTopic selects operating lines by rolling stock code and car count
// and summarizes them by city.
type RollingStockTopic struct{ ent.Schema }

func (RollingStockTopic) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "rolling_stock_topics"},
		entsql.Checks(map[string]string{
			"rolling_stock_topics_slug_format": "slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'",
			"rolling_stock_topics_name_length": "char_length(name) BETWEEN 1 AND 64",
		}),
	}
}

func (RollingStockTopic) Fields() []ent.Field {
	return append(base(),
		field.String("slug").Unique().NotEmpty(),
		field.String("name").NotEmpty(),
		field.JSON("rolling_stock_codes", []string{}).Default([]string{}),
		field.JSON("cars", []int{}).Default([]int{}),
	)
}

// CatalogState is a single row whose version rises by one in the transaction of
// every successful catalog write. Clients use it to validate cached catalog data
// (known_version), and its row lock serializes concurrent administrative writes.
type CatalogState struct{ ent.Schema }

func (CatalogState) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "catalog_state"},
		entsql.Checks(map[string]string{"catalog_state_single_row": "id = 1"}),
	}
}

func (CatalogState) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").Immutable(),
		field.Int64("version").Default(1).Positive(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

// AuditChannels are the ways an administrator reaches the admin API.
var AuditChannels = []string{"web", "mcp"}

// AdminAuditLog records one catalog change: who, through which channel, what
// changed and the entity before and after. It is written in the same transaction
// as the change and is never updated or deleted by the service.
type AdminAuditLog struct{ ent.Schema }

func (AdminAuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "admin_audit_logs"}}
}

func (AdminAuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("actor_user_id").NotEmpty().Immutable(),
		field.Enum("channel").Values(AuditChannels...).Immutable(),
		// The management token behind an mcp call.
		field.String("token_id").Default("").Immutable(),
		field.String("request_id").Default("").Immutable(),
		field.String("action").NotEmpty().Immutable(),
		field.String("entity_type").NotEmpty().Immutable(),
		field.String("entity_id").NotEmpty().Immutable(),
		// JSON text snapshots; empty when absent.
		field.Text("before_json").Default("").Immutable(),
		field.Text("after_json").Default("").Immutable(),
		field.Time("created_at").Default(now).Immutable(),
	}
}

func (AdminAuditLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
		index.Fields("entity_type", "entity_id", "created_at"),
		index.Fields("actor_user_id", "created_at"),
	}
}
