package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"connectrpc.com/connect"
	"go.orx.me/apps/trip-map/internal/auth"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/footprintchange"
	"go.orx.me/apps/trip-map/internal/ent/provincemark"
	"go.orx.me/apps/trip-map/internal/ent/syncreceipt"
	"go.orx.me/apps/trip-map/internal/ent/trip"
	"go.orx.me/apps/trip-map/internal/ent/user"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

func recordChange(ctx context.Context, tx *ent.Tx, uid string, revision int64, mutation *tripmapv1.FootprintMutation, schema int) error {
	copied := proto.Clone(mutation).(*tripmapv1.FootprintMutation)
	m := &tripmapv1.FootprintMutation{Operation: copied.Operation}
	switch op := m.Operation.(type) {
	case *tripmapv1.FootprintMutation_UpsertVisit:
		row, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(op.UpsertVisit.Code)).Only(ctx)
		if err != nil {
			return err
		}
		op.UpsertVisit = toVisit(row)
		if schema >= schemaTrips {
			// The date and note of a legacy upsert live in its legacy trip.
			t, err := tx.Trip.Query().Where(trip.UserID(uid), trip.TripID(legacyTripID(row.RegionCode))).Only(ctx)
			if err != nil {
				return err
			}
			op.UpsertVisit.Date, op.UpsertVisit.Note = t.TravelDate, t.Note
		}
	case *tripmapv1.FootprintMutation_UpsertCity:
		row, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(op.UpsertCity.Code)).Only(ctx)
		if err != nil {
			return err
		}
		op.UpsertCity = toCity(row)
	case *tripmapv1.FootprintMutation_UpsertTrip:
		row, err := tx.Trip.Query().Where(trip.UserID(uid), trip.TripID(op.UpsertTrip.Id)).Only(ctx)
		if err != nil {
			return err
		}
		op.UpsertTrip = toTrip(row)
	case *tripmapv1.FootprintMutation_UpsertMark:
		row, err := tx.ProvinceMark.Query().Where(provincemark.UserID(uid), provincemark.ProvinceCode(op.UpsertMark.Province)).Only(ctx)
		if err != nil {
			return err
		}
		op.UpsertMark = toMark(row)
	}
	payload, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	return tx.FootprintChange.Create().SetUserID(uid).SetRevision(revision).SetPayload(payload).Exec(ctx)
}

func (s *FootprintService) normalizeMutation(input *tripmapv1.FootprintMutation) (*tripmapv1.FootprintMutation, error) {
	if input == nil {
		return nil, invalid("mutation is required")
	}
	m := proto.Clone(input).(*tripmapv1.FootprintMutation)
	switch op := m.Operation.(type) {
	case *tripmapv1.FootprintMutation_UpsertVisit:
		if op.UpsertVisit == nil {
			return nil, invalid("visit is required")
		}
		ref, err := s.regions.ResolveCity(op.UpsertVisit.Code, op.UpsertVisit.Name, op.UpsertVisit.Province)
		if err != nil {
			return nil, invalid("unmapped region record")
		}
		if ref.Code != op.UpsertVisit.Code {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade blocked: legacy ID-changing batch must be exported before upgrade"))
		}
		op.UpsertVisit.Code, op.UpsertVisit.Name, op.UpsertVisit.Province = ref.Code, ref.Name, ref.Province
		op.UpsertVisit.UpdatedAt = nil
		if err := s.validateVisit(op.UpsertVisit); err != nil {
			return nil, err
		}
	case *tripmapv1.FootprintMutation_UpsertCity:
		if op.UpsertCity == nil {
			return nil, invalid("city is required")
		}
		ref, err := s.regions.ResolveCity(op.UpsertCity.Code, op.UpsertCity.Name, op.UpsertCity.Province)
		if err != nil {
			return nil, invalid("unmapped region record")
		}
		op.UpsertCity.Code, op.UpsertCity.Name, op.UpsertCity.Province = ref.Code, ref.Name, ref.Province
		op.UpsertCity.UpdatedAt = nil
		if err := s.validateCity(op.UpsertCity); err != nil {
			return nil, err
		}
	case *tripmapv1.FootprintMutation_UpsertTrip:
		if op.UpsertTrip == nil {
			return nil, invalid("trip is required")
		}
		code, ok := s.regions.ResolveCode(op.UpsertTrip.RegionCode)
		if !ok {
			return nil, invalid("unknown region code")
		}
		op.UpsertTrip.RegionCode = code
		op.UpsertTrip.CreatedAt, op.UpsertTrip.UpdatedAt = nil, nil
		if err := s.validateTrip(op.UpsertTrip); err != nil {
			return nil, err
		}
	case *tripmapv1.FootprintMutation_DeleteTrip:
		if op.DeleteTrip == nil || !tripIDPattern.MatchString(op.DeleteTrip.GetId()) {
			return nil, invalid("trip id must match [A-Za-z0-9_-]{1,64}")
		}
		code, ok := s.regions.ResolveCode(op.DeleteTrip.GetRegionCode())
		if !ok {
			return nil, invalid("unknown region code")
		}
		op.DeleteTrip.RegionCode = code
	case *tripmapv1.FootprintMutation_DeleteVisit:
		code, ok := s.regions.ResolveCode(op.DeleteVisit)
		if !ok {
			return nil, invalid("unknown region code")
		}
		op.DeleteVisit = code
	case *tripmapv1.FootprintMutation_UpsertMark:
		if err := s.validateMark(op.UpsertMark); err != nil {
			return nil, err
		}
		op.UpsertMark.UpdatedAt = nil
	case *tripmapv1.FootprintMutation_DeleteMark:
		if !s.regions.HasProvince(op.DeleteMark) {
			return nil, invalid("unknown province code")
		}
	default:
		return nil, invalid("mutation operation is required")
	}
	return m, nil
}

// applyMutation applies one validated mutation under the schema the account is
// served with. It reports whether anything changed.
func (s *FootprintService) applyMutation(ctx context.Context, tx *ent.Tx, uid string, m *tripmapv1.FootprintMutation, schema int) (bool, error) {
	switch op := m.Operation.(type) {
	case *tripmapv1.FootprintMutation_UpsertVisit:
		v := op.UpsertVisit
		if schema >= schemaTrips {
			// A legacy record is a city plus the trip it always implied.
			city, cityChanged, err := upsertCityRow(ctx, tx, uid, v)
			if err != nil {
				return false, err
			}
			tripChanged, err := upsertTripRow(ctx, tx, uid, city, legacyTripID(v.Code), v.Date, v.Note)
			return cityChanged || tripChanged, err
		}
		row, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(v.Code)).Only(ctx)
		if ent.IsNotFound(err) {
			err = tx.CityVisit.Create().SetUserID(uid).SetRegionCode(v.Code).SetRegionName(v.Name).SetProvinceCode(v.Province).SetColor(v.Color).SetTravelDate(v.Date).SetNote(v.Note).Exec(ctx)
			return true, wrapWrite(err)
		}
		if err != nil {
			return false, internalError(err)
		}
		if row.RegionName == v.Name && regions.CanonicalProvince(row.RegionCode, row.ProvinceCode) == v.Province && row.Color == v.Color && row.TravelDate == v.Date && row.Note == v.Note {
			return false, nil
		}
		err = row.Update().SetRegionName(v.Name).SetProvinceCode(v.Province).SetColor(v.Color).SetTravelDate(v.Date).SetNote(v.Note).Exec(ctx)
		return true, wrapWrite(err)
	case *tripmapv1.FootprintMutation_UpsertCity:
		if schema < schemaTrips {
			return false, invalid("this operation needs a client that supports several trips per city")
		}
		_, changed, err := upsertCityRow(ctx, tx, uid, op.UpsertCity)
		return changed, err
	case *tripmapv1.FootprintMutation_UpsertTrip:
		if schema < schemaTrips {
			return false, invalid("this operation needs a client that supports several trips per city")
		}
		t := op.UpsertTrip
		tm, err := checkSharedTripWriteAllowed(ctx, tx, uid, t.Id)
		if err != nil {
			return false, err
		}
		city, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid), cityvisit.RegionCode(t.RegionCode)).Only(ctx)
		if ent.IsNotFound(err) {
			return false, connect.NewError(connect.CodeFailedPrecondition, errors.New("trip city does not exist"))
		}
		if err != nil {
			return false, internalError(err)
		}
		changed, err := upsertTripRow(ctx, tx, uid, city, t.Id, t.Date, t.Note)
		if err != nil {
			return false, err
		}
		if changed && tm != nil {
			if err = propagateSharedTripUpsert(ctx, tx, s.regions, tm, uid, t.Date, t.Note); err != nil {
				return false, err
			}
		}
		return changed, nil
	case *tripmapv1.FootprintMutation_DeleteTrip:
		if schema < schemaTrips {
			return false, invalid("this operation needs a client that supports several trips per city")
		}
		if err := handleSharedTripDelete(ctx, tx, uid, op.DeleteTrip.Id); err != nil {
			return false, err
		}
		n, err := tx.Trip.Delete().Where(trip.UserID(uid), trip.TripID(op.DeleteTrip.Id), trip.RegionCode(op.DeleteTrip.RegionCode)).Exec(ctx)
		return n > 0, wrapWrite(err)
	case *tripmapv1.FootprintMutation_DeleteVisit:
		if err := handleSharedCityDelete(ctx, tx, uid, op.DeleteVisit); err != nil {
			return false, err
		}
		// The trips of a city are removed with it by the foreign key.
		count, err := tx.CityVisit.Delete().Where(cityvisit.UserID(uid), cityvisit.RegionCode(op.DeleteVisit)).Exec(ctx)
		return count > 0, wrapWrite(err)
	case *tripmapv1.FootprintMutation_UpsertMark:
		m := op.UpsertMark
		row, err := tx.ProvinceMark.Query().Where(provincemark.UserID(uid), provincemark.ProvinceCode(m.Province)).Only(ctx)
		if ent.IsNotFound(err) {
			err = tx.ProvinceMark.Create().SetUserID(uid).SetProvinceCode(m.Province).SetColor(m.Color).Exec(ctx)
			return true, wrapWrite(err)
		}
		if err != nil {
			return false, internalError(err)
		}
		if row.Color == m.Color {
			return false, nil
		}
		return true, wrapWrite(row.Update().SetColor(m.Color).Exec(ctx))
	case *tripmapv1.FootprintMutation_DeleteMark:
		count, err := tx.ProvinceMark.Delete().Where(provincemark.UserID(uid), provincemark.ProvinceCode(op.DeleteMark)).Exec(ctx)
		return count > 0, wrapWrite(err)
	}
	return false, invalid("unsupported operation")
}

var mutationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// preRegionSyncHash freezes the F9 receipt contract. Only server-owned
// timestamps and the already-supported island province aliases were removed.
func preRegionSyncHash(input *tripmapv1.SyncChangesRequest) (string, error) {
	m := proto.Clone(input).(*tripmapv1.SyncChangesRequest)
	for _, mutation := range m.Mutations {
		if mutation == nil {
			continue
		}
		switch op := mutation.Operation.(type) {
		case *tripmapv1.FootprintMutation_UpsertVisit:
			if op.UpsertVisit != nil {
				op.UpsertVisit.Province = regions.CanonicalProvince(op.UpsertVisit.Code, op.UpsertVisit.Province)
				op.UpsertVisit.UpdatedAt = nil
			}
		case *tripmapv1.FootprintMutation_UpsertCity:
			if op.UpsertCity != nil {
				op.UpsertCity.Province = regions.CanonicalProvince(op.UpsertCity.Code, op.UpsertCity.Province)
				op.UpsertCity.UpdatedAt = nil
			}
		case *tripmapv1.FootprintMutation_UpsertTrip:
			if op.UpsertTrip != nil {
				op.UpsertTrip.CreatedAt, op.UpsertTrip.UpdatedAt = nil, nil
			}
		case *tripmapv1.FootprintMutation_UpsertMark:
			if op.UpsertMark != nil {
				op.UpsertMark.UpdatedAt = nil
			}
		}
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	return fmt.Sprintf("%x", sha256.Sum256(raw)), err
}

func (s *FootprintService) SyncChanges(ctx context.Context, req *connect.Request[tripmapv1.SyncChangesRequest]) (*connect.Response[tripmapv1.SyncChangesResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if !mutationIDPattern.MatchString(req.Msg.MutationId) || req.Msg.BaseRevision < 0 || len(req.Msg.Mutations) < 1 || len(req.Msg.Mutations) > maxSyncMutations {
		return nil, invalid("mutation_id, non-negative revision and 1-2000 mutations are required")
	}
	// A retired ID may no longer pass current validation. Answer an exact
	// pre-F10 receipt first, using its original normalization, without rewriting
	// the pending batch or allocating a replacement mutation ID.
	legacyHash, err := preRegionSyncHash(req.Msg)
	if err != nil {
		return nil, internalError(err)
	}
	if receipt, err := st.DB.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(req.Msg.MutationId)).Only(ctx); err == nil && receipt.RequestHash == legacyHash {
		return connect.NewResponse(&tripmapv1.SyncChangesResponse{Revision: receipt.Revision, ChangedCount: int32(receipt.ChangedCount), Replayed: true}), nil
	} else if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	} else if err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used for another request"))
	}
	normalized := proto.Clone(req.Msg).(*tripmapv1.SyncChangesRequest)
	for i, m := range normalized.Mutations {
		normalized.Mutations[i], err = s.normalizeMutation(m)
		if err != nil {
			return nil, err
		}
	}
	// Freeze the receipt hash to the original wire contract. Hashing the current
	// regional projection would change it again after the next catalog upgrade.
	hash := legacyHash
	if err := checkRegionVersion(req.Header().Get(regionVersionHeader)); err != nil {
		return nil, err
	}
	// A batch is charged per operation in one step, from a budget of its own so a
	// full migration batch never competes with single writes.
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).AllowN(ctx, "sync:"+p.UserID, int64(len(normalized.Mutations)), syncOpsPerWindow, writeWindow)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many writes"))
	}
	peerIDs, err := sharedTripPeerUserIDs(ctx, st.DB, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	if len(peerIDs) > 1 {
		if _, err = lockUsers(ctx, tx, peerIDs...); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
			return nil, err
		}
	}
	u, err := tx.User.Query().Where(user.ID(p.UserID)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	if u.Disabled {
		return nil, errUnauthenticated
	}
	receipt, err := tx.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(normalized.MutationId)).Only(ctx)
	if err == nil {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used for another request"))
		}
		return connect.NewResponse(&tripmapv1.SyncChangesResponse{Revision: receipt.Revision, ChangedCount: int32(receipt.ChangedCount), Replayed: true}), nil
	}
	if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if err := requireCurrentRegionIDs(ctx, tx.CityVisit.Query().Where(cityvisit.UserID(u.ID)), s.regions); err != nil {
		return nil, err
	}
	schema, err := gateWrite(ctx, tx, u)
	if err != nil {
		return nil, err
	}
	revision := u.Revision
	count := int32(0)
	for _, m := range normalized.Mutations {
		changed, err := s.applyMutation(ctx, tx, p.UserID, m, schema)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		revision++
		count++
		if err = recordChange(ctx, tx, p.UserID, revision, m, schema); err != nil {
			return nil, internalError(err)
		}
	}
	if count > 0 && normalized.BaseRevision != u.Revision {
		conflict := connect.NewError(connect.CodeAborted, errors.New("footprints changed since base_revision"))
		if d, err := connect.NewErrorDetail(&tripmapv1.RevisionConflict{CurrentRevision: u.Revision}); err == nil {
			conflict.AddDetail(d)
		}
		return nil, conflict
	}
	if count > 0 {
		if err = tx.User.UpdateOneID(p.UserID).SetRevision(revision).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.SyncReceipt.Create().SetUserID(p.UserID).SetMutationID(normalized.MutationId).SetRequestHash(hash).SetRevision(revision).SetChangedCount(int(count)).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.SyncChangesResponse{Revision: revision, ChangedCount: count}), nil
}

func (s *FootprintService) GetChanges(ctx context.Context, req *connect.Request[tripmapv1.GetChangesRequest]) (*connect.Response[tripmapv1.GetChangesResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	since := req.Msg.SinceRevision
	limit := int(req.Msg.Limit)
	if since < 0 || limit < 0 || limit > 500 {
		return nil, invalid("invalid revision or limit")
	}
	if limit == 0 {
		limit = 200
	}
	tx, err := st.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := tx.User.Get(ctx, p.UserID)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	if u.Disabled {
		return nil, errUnauthenticated
	}
	if err = gateRead(ctx, u.SchemaVersion); err != nil {
		return nil, err
	}
	out := &tripmapv1.GetChangesResponse{CurrentRevision: u.Revision, NextRevision: since, SchemaVersion: schemaTrips, RegionVersion: s.regions.Version()}
	first, err := tx.FootprintChange.Query().Where(footprintchange.UserID(p.UserID)).Order(ent.Asc(footprintchange.FieldRevision)).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if since > u.Revision || (ent.IsNotFound(err) && since < u.Revision) || (err == nil && since < first.Revision-1) {
		out.ResyncRequired = true
		return connect.NewResponse(out), nil
	}
	rows, err := tx.FootprintChange.Query().Where(footprintchange.UserID(p.UserID), footprintchange.RevisionGT(since)).Order(ent.Asc(footprintchange.FieldRevision)).Limit(limit + 1).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	out.HasMore = len(rows) > limit
	if out.HasMore {
		rows = rows[:limit]
	}
	for _, row := range rows {
		m := new(tripmapv1.FootprintMutation)
		if err = proto.Unmarshal(row.Payload, m); err != nil {
			return nil, internalError(err)
		}
		if v := m.GetUpsertVisit(); v != nil {
			v.Province = regions.CanonicalProvince(v.Code, v.Province)
		}
		out.Changes = append(out.Changes, &tripmapv1.FootprintChange{Revision: row.Revision, Mutation: m})
		out.NextRevision = row.Revision
	}
	if !out.HasMore {
		out.NextRevision = u.Revision
	}
	return connect.NewResponse(out), nil
}
