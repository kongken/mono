package application

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/trip"
)

// AuthorizeTripMediaWrite decides whether uid may attach new media to tripID in
// region. Personal rows are still scoped by user_id elsewhere; shared-trip
// collaboration rules (#38) extend read/list on top of this write gate.
func AuthorizeTripMediaWrite(ctx context.Context, tx *ent.Tx, uid, region, tripID string) error {
	return requireTrip(ctx, tx, uid, region, tripID)
}

func requireTrip(ctx context.Context, tx *ent.Tx, uid, region, tripID string) error {
	if _, err := checkSharedTripWriteAllowed(ctx, tx, uid, tripID); err != nil {
		return err
	}
	ok, err := tx.Trip.Query().Where(trip.UserID(uid), trip.TripID(tripID), trip.RegionCode(region)).Exist(ctx)
	if err != nil {
		return internalError(err)
	}
	if ok {
		return nil
	}
	if tripID == legacyTripID(region) {
		u, err := tx.User.Get(ctx, uid)
		if err != nil {
			return internalError(err)
		}
		if u.SchemaVersion < schemaTrips {
			ok, err = tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(region)).Exist(ctx)
			if err != nil {
				return internalError(err)
			}
			if ok {
				return nil
			}
		}
	}
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("trip not found: sync the trip before attaching files"))
}
