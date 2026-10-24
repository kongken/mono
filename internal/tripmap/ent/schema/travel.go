package schema

import (
	"fmt"
	"regexp"
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

// User is the tripmap profile keyed by the usersrv user UUID.
// usersrv owns accounts and credentials; the BFF owns browser sessions. This row is the display projection of an account (ID, nickname, avatar) plus tripmap's own data.
type User struct{ ent.Schema }

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "users"}}
}
func (User) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("username").Optional().Nillable(),
		field.String("display_name").Default(""),
		field.String("avatar_url").Default(""),
		field.Int64("revision").Default(0).NonNegative(),
		// 1 = date/note live on city_visits (frozen once upgraded); 2 = trips live in
		// city_trips. Upgraded in place by the first write from a schema 2 client.
		field.Int("schema_version").Default(1).Min(1),
		// Invite code for F7 friendships (the only way to add a friend).
		field.String("friend_code").Optional().Nillable().Unique(),
		// F7 visibility towards confirmed friends: cities on by default, dates/notes opt-in.
		field.Bool("friend_show_cities").Default(true),
		field.Bool("friend_show_dates").Default(false),
		field.Bool("friend_show_notes").Default(false),
	}, timestamps()...)
}
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("visits", CityVisit.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("trips", Trip.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("province_marks", ProvinceMark.Type).Annotations(entsql.OnDelete(entsql.Cascade)), edge.To("media_objects", MediaObject.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("footprint_changes", FootprintChange.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sync_receipts", SyncReceipt.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("snapshots", Snapshot.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("share", Share.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sent_friendships", Friendship.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("received_friendships", Friendship.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("owned_shared_trips", SharedTrip.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("trip_memberships", TripMember.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("itineraries", Itinerary.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("notification_profile", NotificationProfile.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("notification_settings", NotificationSetting.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("push_subscriptions", PushSubscription.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("notification_deliveries", NotificationDelivery.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
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
	return []ent.Edge{
		edge.From("user", User.Type).Ref("visits").Field("user_id").Unique().Required().Immutable(),
		// Removing a city removes its trips; media is a separate, soft reference.
		edge.To("trips", Trip.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
func (CityVisit) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "region_code").Unique(), index.Fields("user_id", "province_code")}
}

// Trip is one visit to a city (F5). travel_date and note on city_visits are the
// frozen schema 1 copy: they are read to build "legacy-<region>" trips when an
// account is upgraded and never written afterwards. A trip has its own
// updated_at, so editing one trip does not touch the city or other trips.
type Trip struct{ ent.Schema }

func (Trip) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "city_trips"}}
}
func (Trip) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("visit_id").Immutable(),
		// The client-visible trip ID, unique per user.
		field.String("trip_id").Immutable().Validate(validTripID),
		// Denormalized from the city so media can check city ownership cheaply.
		field.String("region_code").NotEmpty().Immutable(),
		field.String("travel_date").Default("").Validate(validDate),
		field.String("note").Default("").Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 1000 {
				return fmt.Errorf("note exceeds 1000 characters")
			}
			return nil
		}),
	}, timestamps()...)
}
func (Trip) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("trips").Field("user_id").Unique().Required().Immutable(),
		edge.From("visit", CityVisit.Type).Ref("trips").Field("visit_id").Unique().Required().Immutable(),
	}
}
func (Trip) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "trip_id").Unique(), index.Fields("user_id", "region_code"), index.Fields("visit_id")}
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
		field.Enum("status").Values("pending", "ready", "deleting").Default("pending"),
		field.String("region_code").Default(""), field.String("filename").Default(""),
		field.String("upload_id").Default(""), field.String("request_hash").Default(""),
		field.String("upload_key").Default(""), field.String("thumbnail_key").Default(""),
		field.Time("upload_expires_at").Optional().Nillable(),
		field.Int("width").Default(0).NonNegative(), field.Int("height").Default(0).NonNegative(),
		// Soft reference to city_trips.trip_id; empty for city-level files. No foreign
		// key on purpose: deleting a trip or city must never delete a photo.
		field.String("trip_id").Default(""),
	}, timestamps()...)
}
func (MediaObject) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("media_objects").Field("user_id").Unique().Required().Immutable()}
}

func (MediaObject) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "region_code"), index.Fields("user_id", "trip_id"), index.Fields("user_id", "upload_id").Unique().Annotations(entsql.IndexWhere("upload_id <> ''"))}
}

// Upload cleanup outlives the owning account: a signed PUT can still finish
// after account deletion. No personal information is retained here.
type MediaCleanup struct{ ent.Schema }

func (MediaCleanup) Fields() []ent.Field {
	return []ent.Field{field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("object_key").Unique(), field.Time("delete_after")}
}
func (MediaCleanup) Indexes() []ent.Index { return []ent.Index{index.Fields("delete_after")} }

func validColor(v string) error {
	switch v {
	case "#4b937d", "#e2af5c", "#d88370", "#719fc6", "#a08cbd":
		return nil
	}
	return fmt.Errorf("unsupported footprint color")
}

var tripIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validTripID(v string) error {
	if !tripIDPattern.MatchString(v) {
		return fmt.Errorf("invalid trip id")
	}
	return nil
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

// Changes retain explicit delete tombstones and full upsert values.
type FootprintChange struct{ ent.Schema }

func (FootprintChange) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.Int64("revision").Positive().Immutable(), field.Bytes("payload").Immutable(), field.Time("created_at").Default(now).Immutable(),
	}
}
func (FootprintChange) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("footprint_changes").Field("user_id").Unique().Required().Immutable()}
}
func (FootprintChange) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "revision").Unique()}
}

type SyncReceipt struct{ ent.Schema }

func (SyncReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.String("mutation_id").NotEmpty().Immutable(), field.String("request_hash").Immutable(),
		field.Int64("revision").NonNegative().Immutable(), field.Int("changed_count").NonNegative().Immutable(), field.Time("created_at").Default(now).Immutable(),
		// Set by snapshot restores: the pre-restore snapshot, so a replay can answer with it.
		field.String("snapshot_id").Optional().Nillable().Immutable(),
	}
}
func (SyncReceipt) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("sync_receipts").Field("user_id").Unique().Required().Immutable()}
}
func (SyncReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "mutation_id").Unique()}
}

// Snapshot is an immutable copy of a user's footprints at one revision. The
// payload is a gzip-compressed tripmap.v1.SnapshotData. Snapshots are removed
// only by retention or when the account is deleted.
type Snapshot struct{ ent.Schema }

func (Snapshot) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable(),
		field.Enum("reason").Values("manual", "scheduled", "before_migration", "before_restore").Immutable(),
		field.Int64("revision").NonNegative().Immutable(),
		field.Int("visit_count").NonNegative().Immutable(), field.Int("mark_count").NonNegative().Immutable(),
		// Rows written before F5 have schema_version 1 and trip_count 0 (read as one
		// legacy trip per city). The stored payload is never rewritten.
		field.Int("schema_version").Default(1).Immutable(), field.Int("trip_count").Default(0).NonNegative().Immutable(),
		field.Bytes("payload").Immutable(), field.Time("created_at").Default(now).Immutable(),
	}
}
func (Snapshot) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("snapshots").Field("user_id").Unique().Required().Immutable()}
}
func (Snapshot) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at"), index.Fields("user_id", "revision")}
}

// Share is the account's single public sharing configuration (F6). The row
// outlives revocation as a private draft: token and payload are cleared, so
// nothing is reachable, while the chosen settings stay for the next publish. A
// token is never reused: every new publication draws a fresh one.
//
// payload is a gzip JSON snapshot written only by an explicit publish. Public
// reads never consult the live footprint tables, so later edits, notes or photos
// stay private until the owner publishes again. The token is stored in clear
// because the owner must be able to copy the link again; the database already
// holds every footprint in clear, unlike session tokens.
type Share struct{ ent.Schema }

func (Share) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shares"}}
}
func (Share) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(), field.String("user_id").Immutable().Unique(),
		field.String("token").Optional().Nillable().Unique().Sensitive(),
		field.Enum("visibility").Values("private", "link", "public").Default("private"),
		field.Bool("show_cities").Default(false), field.Bool("show_dates").Default(false),
		field.Bool("show_notes").Default(false), field.Bool("show_photos").Default(false),
		// Chosen media IDs, in display order. Only ready JPEG photos of the owner are accepted.
		field.JSON("photo_ids", []string{}).Optional(),
		// Bumped by every change; SaveShare is conditional on it.
		field.Int64("version").Default(0).NonNegative(),
		// users.revision the payload was built from (0 until published).
		field.Int64("published_revision").Default(0).NonNegative(),
		field.Time("published_at").Optional().Nillable(),
		field.Bytes("payload").Optional().Sensitive(),
	}, timestamps()...)
}
func (Share) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("share").Field("user_id").Unique().Required().Immutable()}
}

// Friendship is a mutual friend link between two users (F7). user_low_id and
// user_high_id store the ordered pair (min, max) so at most one row can exist
// between any two accounts regardless of who invited whom.
type Friendship struct{ ent.Schema }

func (Friendship) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "friendships"}}
}
func (Friendship) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("requester_id").Immutable(),
		field.String("addressee_id").Immutable(),
		field.String("user_low_id").NotEmpty().Immutable(),
		field.String("user_high_id").NotEmpty().Immutable(),
		field.Enum("status").Values("pending", "accepted").Default("pending"),
		field.Time("accepted_at").Optional().Nillable(),
	}, timestamps()...)
}
func (Friendship) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("requester", User.Type).Ref("sent_friendships").Field("requester_id").Unique().Required().Immutable(),
		edge.From("addressee", User.Type).Ref("received_friendships").Field("addressee_id").Unique().Required().Immutable(),
	}
}
func (Friendship) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_low_id", "user_high_id").Unique(),
		index.Fields("requester_id", "status"),
		index.Fields("addressee_id", "status"),
	}
}

// SharedTrip is a collaborative trip created by owner_id around one city trip (F7).
// Active members each hold a synchronized trip row in their own city_trips table
// so their personal map, statistics, exports and offline cache work naturally,
// while leaving or dissolving a shared trip never deletes another user's records.
type SharedTrip struct{ ent.Schema }

func (SharedTrip) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "shared_trips"}}
}
func (SharedTrip) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("owner_id").Immutable(),
		field.String("region_code").NotEmpty().Immutable(),
		field.String("trip_id").NotEmpty().Immutable().Validate(validTripID),
		field.String("travel_date").Default("").Validate(validDate),
		field.String("note").Default("").Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 1000 {
				return fmt.Errorf("note exceeds 1000 characters")
			}
			return nil
		}),
	}, timestamps()...)
}
func (SharedTrip) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).Ref("owned_shared_trips").Field("owner_id").Unique().Required().Immutable(),
		edge.To("members", TripMember.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
func (SharedTrip) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_id", "trip_id").Unique(),
		index.Fields("owner_id", "region_code"),
	}
}

// TripMember links a user to a SharedTrip with an explicit role and invitation state (F7).
type TripMember struct{ ent.Schema }

func (TripMember) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trip_members"}}
}
func (TripMember) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("shared_trip_id").Immutable(),
		field.String("user_id").Immutable(),
		// The trip_id inside user_id's city_trips table once status is active.
		field.String("member_trip_id").Default(""),
		field.Enum("role").Values("owner", "editor", "viewer").Default("editor"),
		field.Enum("status").Values("invited", "active").Default("invited"),
		field.String("invited_by").Default("").Immutable(),
		field.Time("joined_at").Optional().Nillable(),
	}, timestamps()...)
}
func (TripMember) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("shared_trip", SharedTrip.Type).Ref("members").Field("shared_trip_id").Unique().Required().Immutable(),
		edge.From("user", User.Type).Ref("trip_memberships").Field("user_id").Unique().Required().Immutable(),
	}
}
func (TripMember) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("shared_trip_id", "user_id").Unique(),
		index.Fields("user_id", "status"),
		index.Fields("user_id", "member_trip_id"),
	}
}

// ItineraryStopRecord is one ordered city stop persisted inside an Itinerary (F11).
type ItineraryStopRecord struct {
	ID         string `json:"id"`
	RegionCode string `json:"region_code"`
	Date       string `json:"date,omitempty"`
	Note       string `json:"note,omitempty"`
	Transport  string `json:"transport,omitempty"`
	TripID     string `json:"trip_id,omitempty"`
}

// Itinerary is a multi-city travel route owned by user_id (F11).
type Itinerary struct{ ent.Schema }

func (Itinerary) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "itineraries"}}
}
func (Itinerary) Fields() []ent.Field {
	return append([]ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("user_id").Immutable(),
		field.String("itinerary_id").NotEmpty().Immutable().Validate(validTripID),
		field.String("title").NotEmpty().Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 80 {
				return fmt.Errorf("title exceeds 80 characters")
			}
			return nil
		}),
		field.String("note").Default("").Validate(func(v string) error {
			if len(utf16.Encode([]rune(v))) > 1000 {
				return fmt.Errorf("note exceeds 1000 characters")
			}
			return nil
		}),
		field.String("color").Default("#4b937d").Validate(validColor),
		field.JSON("stops", []ItineraryStopRecord{}),
		field.Int64("version").Default(1).Positive(),
	}, timestamps()...)
}
func (Itinerary) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("itineraries").Field("user_id").Unique().Required().Immutable(),
	}
}
func (Itinerary) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "itinerary_id").Unique(),
		index.Fields("user_id", "updated_at"),
	}
}
