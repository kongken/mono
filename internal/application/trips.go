package application

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/trip"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// F5: several trips per city.
//
// Accounts start at schema 1, where a city carries one date and note in
// city_visits. The first write from a client that sends clientSchemaHeader: 2
// upgrades the account in place (upgradeAccount); from then on trips live in
// city_trips and requests without the header are refused, so a client that only
// understands one trip per city can never overwrite or drop trips it cannot see.
const (
	schemaLegacy = 1
	schemaTrips  = 2

	// clientSchemaHeader is sent by every schema 2 client.
	clientSchemaHeader = "X-Tripmap-Schema"

	// maxSyncMutations bounds one atomic batch. It holds a whole migration: every
	// one of the 497 cities with several trips each, plus 34 province marks.
	// Larger changes are sent as consecutive batches by the client.
	maxSyncMutations = 2000

	// syncOpsPerWindow is the per-user budget of batched operations per minute. A
	// batch is charged by its size in one step, so a full batch fits several times.
	syncOpsPerWindow = 6000
)

var tripIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// legacyTripID is the stable ID of the trip schema 1 data becomes. Client and
// server derive the same ID, so independent upgrades of one record merge.
func legacyTripID(regionCode string) string { return "legacy-" + regionCode }

type (
	clientSchemaKey  struct{}
	accountSchemaKey struct{}
)

// withSchemas records what the caller declared and the schema its account had when
// the session was authenticated. Writes re-check under the user's row lock.
func withSchemas(ctx context.Context, header http.Header, account int) context.Context {
	client := schemaLegacy
	if n, err := strconv.Atoi(header.Get(clientSchemaHeader)); err == nil && n >= schemaTrips {
		client = n
	}
	ctx = context.WithValue(ctx, clientSchemaKey{}, client)
	return context.WithValue(ctx, accountSchemaKey{}, account)
}

func clientSchema(ctx context.Context) int {
	if n, ok := ctx.Value(clientSchemaKey{}).(int); ok {
		return n
	}
	return schemaLegacy
}

func accountSchemaFromCtx(ctx context.Context) int {
	if n, ok := ctx.Value(accountSchemaKey{}).(int); ok {
		return n
	}
	return schemaLegacy
}

func upgradeRequired() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("client upgrade required: this account records several trips per city, reload the app to continue"))
}

// gateRead refuses a client that cannot represent the account's data.
func gateRead(ctx context.Context, accountSchema int) error {
	if accountSchema >= schemaTrips && clientSchema(ctx) < schemaTrips {
		return upgradeRequired()
	}
	return nil
}

// gateWrite runs under the user's row lock. It upgrades a schema 1 account for a
// schema 2 client and returns the schema the write is applied under.
func gateWrite(ctx context.Context, tx *ent.Tx, u *ent.User) (int, error) {
	client := clientSchema(ctx)
	if u.SchemaVersion >= schemaTrips {
		if client < schemaTrips {
			return 0, upgradeRequired()
		}
		return schemaTrips, nil
	}
	if client < schemaTrips {
		return schemaLegacy, nil
	}
	if err := upgradeAccount(ctx, tx, u); err != nil {
		return 0, internalError(err)
	}
	return schemaTrips, nil
}

// upgradeAccount turns every city's date and note into its first trip, without
// changing the revision or the change log. It is idempotent: a trip that already
// exists is kept, and the schema 1 columns stay untouched as a frozen copy.
func upgradeAccount(ctx context.Context, tx *ent.Tx, u *ent.User) error {
	visits, err := tx.CityVisit.Query().Where(cityvisit.UserID(u.ID)).Order(ent.Asc(cityvisit.FieldRegionCode)).All(ctx)
	if err != nil {
		return err
	}
	ids, err := tx.Trip.Query().Where(trip.UserID(u.ID)).Select(trip.FieldTripID).Strings(ctx)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(ids))
	for _, id := range ids {
		have[id] = true
	}
	var create []*ent.TripCreate
	for _, v := range visits {
		id := legacyTripID(v.RegionCode)
		if have[id] {
			continue
		}
		create = append(create, tx.Trip.Create().SetUserID(u.ID).SetVisitID(v.ID).SetTripID(id).SetRegionCode(v.RegionCode).
			SetTravelDate(v.TravelDate).SetNote(v.Note).SetCreatedAt(v.UpdatedAt).SetUpdatedAt(v.UpdatedAt))
	}
	for len(create) > 0 {
		n := min(len(create), 500)
		if err = tx.Trip.CreateBulk(create[:n]...).Exec(ctx); err != nil {
			return err
		}
		create = create[n:]
	}
	if err = tx.User.UpdateOneID(u.ID).SetSchemaVersion(schemaTrips).Exec(ctx); err != nil {
		return err
	}
	u.SchemaVersion = schemaTrips
	return nil
}

func validateDateNote(date, note string) error {
	if date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || !datePattern.MatchString(date) || parsed.Format("2006-01-02") != date {
			return invalid("date must be a real YYYY-MM-DD date or empty")
		}
	}
	if !utf8.ValidString(note) {
		return invalid("note must be valid UTF-8")
	}
	if len(utf16.Encode([]rune(note))) > noteLimit {
		return invalid("note exceeds 1000 characters")
	}
	return nil
}

func (s *FootprintService) validateTrip(t *tripmapv1.Trip) error {
	if t == nil {
		return invalid("trip is required")
	}
	if !tripIDPattern.MatchString(t.GetId()) {
		return invalid("trip id must match [A-Za-z0-9_-]{1,64}")
	}
	if _, _, ok := s.regions.City(t.GetRegionCode()); !ok {
		return invalid("unknown region code")
	}
	return validateDateNote(t.GetDate(), t.GetNote())
}

// validateCity checks the city fields of a schema 2 record; trips carry date and note.
func (s *FootprintService) validateCity(v *tripmapv1.Visit) error {
	if v == nil {
		return invalid("city is required")
	}
	if v.GetDate() != "" || v.GetNote() != "" {
		return invalid("a city carries no date or note, record them as trips")
	}
	name, province, ok := s.regions.City(v.GetCode())
	if !ok {
		return invalid("unknown region code")
	}
	if v.GetName() != name || v.GetProvince() != province {
		return invalid("region name or province does not match the region index")
	}
	if !footprintColors[v.GetColor()] {
		return invalid("unsupported color")
	}
	return nil
}

// toCity is the schema 2 view of a city: no date or note.
func toCity(v *ent.CityVisit) *tripmapv1.Visit {
	ref := regions.CanonicalCity(v.RegionCode, v.RegionName, v.ProvinceCode)
	return &tripmapv1.Visit{Code: ref.Code, Name: ref.Name, Province: ref.Province, Color: v.Color, UpdatedAt: timestamppb.New(v.UpdatedAt)}
}

func toTrip(t *ent.Trip) *tripmapv1.Trip {
	return &tripmapv1.Trip{Id: t.TripID, RegionCode: regions.CanonicalCode(t.RegionCode), Date: t.TravelDate, Note: t.Note, CreatedAt: timestamppb.New(t.CreatedAt), UpdatedAt: timestamppb.New(t.UpdatedAt)}
}

// legacyTrip is the trip a schema 1 city is read as, without modifying it.
func legacyTrip(v *ent.CityVisit) *tripmapv1.Trip {
	return &tripmapv1.Trip{Id: legacyTripID(v.RegionCode), RegionCode: regions.CanonicalCode(v.RegionCode), Date: v.TravelDate, Note: v.Note, CreatedAt: timestamppb.New(v.CreatedAt), UpdatedAt: timestamppb.New(v.UpdatedAt)}
}

// upsertCityRow creates the city or updates its city fields. Under schema 2 the
// frozen date and note columns are never written.
func upsertCityRow(ctx context.Context, tx *ent.Tx, uid string, v *tripmapv1.Visit) (*ent.CityVisit, bool, error) {
	row, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(v.GetCode())).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		row, err = tx.CityVisit.Create().SetUserID(uid).SetRegionCode(v.GetCode()).SetRegionName(v.GetName()).SetProvinceCode(v.GetProvince()).SetColor(v.GetColor()).Save(ctx)
		return row, true, wrapWrite(err)
	case err != nil:
		return nil, false, internalError(err)
	}
	if row.RegionName == v.GetName() && regions.CanonicalProvince(row.RegionCode, row.ProvinceCode) == v.GetProvince() && row.Color == v.GetColor() {
		return row, false, nil
	}
	row, err = row.Update().SetRegionName(v.GetName()).SetProvinceCode(v.GetProvince()).SetColor(v.GetColor()).Save(ctx)
	return row, true, wrapWrite(err)
}

// upsertTripRow creates or updates one trip of city. A trip never moves between cities.
func upsertTripRow(ctx context.Context, tx *ent.Tx, uid string, city *ent.CityVisit, id, date, note string) (bool, error) {
	row, err := tx.Trip.Query().Where(trip.UserID(uid), trip.TripID(id)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		_, err = tx.Trip.Create().SetUserID(uid).SetVisitID(city.ID).SetTripID(id).SetRegionCode(city.RegionCode).SetTravelDate(date).SetNote(note).Save(ctx)
		return true, wrapWrite(err)
	case err != nil:
		return false, internalError(err)
	}
	if row.RegionCode != city.RegionCode {
		return false, invalid("trip belongs to another city")
	}
	if row.TravelDate == date && row.Note == note {
		return false, nil
	}
	_, err = row.Update().SetTravelDate(date).SetNote(note).Save(ctx)
	return true, wrapWrite(err)
}

func loadTrips(ctx context.Context, tx *ent.Tx, uid string) ([]*ent.Trip, error) {
	return tx.Trip.Query().Where(trip.UserID(uid)).Order(ent.Asc(trip.FieldRegionCode), ent.Asc(trip.FieldTripID)).All(ctx)
}
