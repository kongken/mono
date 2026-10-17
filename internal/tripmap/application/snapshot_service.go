package application

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/config"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/provincemark"
	"github.com/kongken/mono/internal/tripmap/ent/snapshot"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Snapshot retention defaults. A snapshot is kept while it is among the newest
// snapshotKeepNewest or younger than snapshotMaxAge, and never beyond
// snapshotMaxCount. The newest ones are kept regardless of age so a long-idle
// account still has something to restore from.
const (
	snapshotMaxAge     = 30 * 24 * time.Hour
	snapshotKeepNewest = 10
	snapshotMaxCount   = 50
	// snapshotInterval is the minimum spacing of scheduled snapshots.
	snapshotInterval = 24 * time.Hour

	snapshotCreateLimit  = 30
	snapshotCreateWindow = time.Hour
	snapshotRestoreLimit = 6
	snapshotRestoreWin   = time.Minute
	// maxSnapshotBytes bounds a decompressed payload read from the database.
	maxSnapshotBytes = 64 << 20
)

// SnapshotService implements tripmap.v1.SnapshotService. Every query is scoped
// by the principal's user ID.
type SnapshotService struct {
	src StoreSource
	fp  *FootprintService
}

func NewSnapshotService(cfg *config.AppConfig, src StoreSource, idx *regions.Index) *SnapshotService {
	return &SnapshotService{src: src, fp: NewFootprintService(cfg, src, idx)}
}

// snapshotInfoColumns are the columns a list or receipt needs; payloads stay unloaded.
var snapshotInfoColumns = []string{snapshot.FieldID, snapshot.FieldReason, snapshot.FieldRevision, snapshot.FieldVisitCount, snapshot.FieldMarkCount,
	snapshot.FieldTripCount, snapshot.FieldSchemaVersion, snapshot.FieldCreatedAt}

func toSnapshotInfo(s *ent.Snapshot) *tripmapv1.SnapshotInfo {
	trips := s.TripCount
	if s.SchemaVersion < schemaTrips {
		// A schema 1 snapshot holds exactly one record per city.
		trips = s.VisitCount
	}
	return &tripmapv1.SnapshotInfo{
		Id: s.ID, Reason: string(s.Reason), CreatedAt: timestamppb.New(s.CreatedAt),
		VisitCount: int32(s.VisitCount), ProvinceMarkCount: int32(s.MarkCount), Revision: s.Revision,
		TripCount: int32(trips), SchemaVersion: int32(s.SchemaVersion),
	}
}

// footState is the user's records at one revision. Trips are loaded only for
// schema 2 accounts; a schema 1 account keeps date and note on its cities.
type footState struct {
	visits []*ent.CityVisit
	trips  []*ent.Trip
	marks  []*ent.ProvinceMark
}

// encodeSnapshot stores the state in the account's own schema. A schema 1
// snapshot is never rewritten later; readers convert it.
func encodeSnapshot(st *footState, schema int) ([]byte, error) {
	data := &tripmapv1.SnapshotData{RegionVersion: regions.CurrentVersion()}
	if schema >= schemaTrips {
		data.SchemaVersion = schemaTrips
		for _, v := range st.visits {
			city := toCity(v)
			city.Code, city.Name, city.Province = v.RegionCode, v.RegionName, v.ProvinceCode
			data.Visits = append(data.Visits, city)
		}
		for _, t := range st.trips {
			record := toTrip(t)
			record.RegionCode = t.RegionCode
			data.Trips = append(data.Trips, record)
		}
	} else {
		for _, v := range st.visits {
			visit := toVisit(v)
			visit.Code, visit.Name, visit.Province = v.RegionCode, v.RegionName, v.ProvinceCode
			data.Visits = append(data.Visits, visit)
		}
	}
	for _, m := range st.marks {
		data.ProvinceMarks = append(data.ProvinceMarks, toMark(m))
	}
	raw, err := proto.Marshal(data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err = zw.Write(raw); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// asSchema2 returns the snapshot as cities plus trips. A schema 1 snapshot
// becomes one legacy trip per city, empty ones included, so a restore keeps what
// the city carried. The stored snapshot is not modified.
func asSchema2(data *tripmapv1.SnapshotData) *tripmapv1.SnapshotData {
	if data.SchemaVersion >= schemaTrips {
		return data
	}
	out := &tripmapv1.SnapshotData{SchemaVersion: schemaTrips, ProvinceMarks: data.ProvinceMarks, RegionVersion: data.RegionVersion}
	for _, v := range data.Visits {
		city := proto.Clone(v).(*tripmapv1.Visit)
		city.Date, city.Note = "", ""
		out.Visits = append(out.Visits, city)
		out.Trips = append(out.Trips, &tripmapv1.Trip{Id: legacyTripID(v.Code), RegionCode: v.Code, Date: v.Date, Note: v.Note, CreatedAt: v.UpdatedAt, UpdatedAt: v.UpdatedAt})
	}
	return out
}

func decodeSnapshot(payload []byte) (*tripmapv1.SnapshotData, error) {
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxSnapshotBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSnapshotBytes {
		return nil, errors.New("snapshot payload too large")
	}
	data := &tripmapv1.SnapshotData{}
	if err = proto.Unmarshal(raw, data); err != nil {
		return nil, err
	}
	return data, nil
}

func loadState(ctx context.Context, tx *ent.Tx, uid string, schema int) (*footState, error) {
	visits, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid)).Order(ent.Asc(cityvisit.FieldRegionCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	marks, err := tx.ProvinceMark.Query().Where(provincemark.UserID(uid)).Order(ent.Asc(provincemark.FieldProvinceCode)).All(ctx)
	if err != nil {
		return nil, err
	}
	st := &footState{visits: visits, marks: marks}
	if schema >= schemaTrips {
		if st.trips, err = loadTrips(ctx, tx, uid); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// captureSnapshot stores the user's current footprints. The caller must hold
// the user's row lock in tx so the revision matches the records read. An
// existing snapshot of the same revision (same content) is reused rather than
// duplicated; created reports whether a new row was written. The snapshot is
// written in the account's schema, u.SchemaVersion.
//
// keep names a snapshot retention must not remove while it is still in use.
func captureSnapshot(ctx context.Context, tx *ent.Tx, u *ent.User, reason snapshot.Reason, st *footState, keep ...string) (row *ent.Snapshot, created bool, err error) {
	existing, err := tx.Snapshot.Query().Where(snapshot.UserID(u.ID), snapshot.RevisionEQ(u.Revision)).Order(ent.Desc(snapshot.FieldCreatedAt)).First(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, err
	}
	payload, err := encodeSnapshot(st, u.SchemaVersion)
	if err != nil {
		return nil, false, err
	}
	create := tx.Snapshot.Create().SetUserID(u.ID).SetReason(reason).SetRevision(u.Revision).
		SetVisitCount(len(st.visits)).SetMarkCount(len(st.marks)).SetPayload(payload)
	if u.SchemaVersion >= schemaTrips {
		create.SetSchemaVersion(schemaTrips).SetTripCount(len(st.trips))
	}
	row, err = create.Save(ctx)
	if err != nil {
		return nil, false, err
	}
	if err = pruneSnapshots(ctx, tx.Snapshot, u.ID, time.Now().UTC(), keep...); err != nil {
		return nil, false, err
	}
	return row, true, nil
}

// pruneSnapshots applies the retention rules to one user's snapshots, except
// those whose IDs are listed in keep.
func pruneSnapshots(ctx context.Context, snaps *ent.SnapshotClient, uid string, now time.Time, keep ...string) error {
	rows, err := snaps.Query().Where(snapshot.UserID(uid)).Order(ent.Desc(snapshot.FieldCreatedAt, snapshot.FieldID)).
		Select(snapshot.FieldID, snapshot.FieldCreatedAt).All(ctx)
	if err != nil {
		return err
	}
	var drop []string
	for i, row := range rows {
		if i < snapshotKeepNewest || slices.Contains(keep, row.ID) {
			continue
		}
		if i >= snapshotMaxCount || row.CreatedAt.Before(now.Add(-snapshotMaxAge)) {
			drop = append(drop, row.ID)
		}
	}
	if len(drop) == 0 {
		return nil
	}
	_, err = snaps.Delete().Where(snapshot.UserID(uid), snapshot.IDIn(drop...)).Exec(ctx)
	return err
}

// lockUser takes the user's row lock, the same one footprint writes use.
func lockUser(ctx context.Context, tx *ent.Tx, uid string) (*ent.User, error) {
	u, err := tx.User.Query().Where(user.ID(uid)).ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	return u, nil
}

func (s *SnapshotService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
	if err != nil {
		slog.Error("rate limiter unavailable", "error", err)
		return connect.NewError(connect.CodeUnavailable, errors.New("service temporarily unavailable"))
	}
	if !ok {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("too many snapshot requests, slow down"))
	}
	return nil
}

func (s *SnapshotService) ListSnapshots(ctx context.Context, _ *connect.Request[tripmapv1.ListSnapshotsRequest]) (*connect.Response[tripmapv1.ListSnapshotsResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	owner, err := st.DB.User.Get(ctx, p.UserID)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	ctx = withSchemasFromRPC(ctx, owner.SchemaVersion)
	if err = gateRead(ctx, owner.SchemaVersion); err != nil {
		return nil, err
	}
	// Payloads are not loaded: the list only needs the metadata columns.
	rows, err := st.DB.Snapshot.Query().Where(snapshot.UserID(p.UserID)).Order(ent.Desc(snapshot.FieldCreatedAt, snapshot.FieldID)).
		Select(snapshotInfoColumns...).All(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	out := &tripmapv1.ListSnapshotsResponse{}
	for _, row := range rows {
		out.Snapshots = append(out.Snapshots, toSnapshotInfo(row))
	}
	return connect.NewResponse(out), nil
}

func (s *SnapshotService) CreateSnapshot(ctx context.Context, req *connect.Request[tripmapv1.CreateSnapshotRequest]) (*connect.Response[tripmapv1.CreateSnapshotResponse], error) {
	var reason snapshot.Reason
	switch snapshot.Reason(req.Msg.GetReason()) {
	case snapshot.ReasonManual:
		reason = snapshot.ReasonManual
	case snapshot.ReasonBeforeMigration:
		reason = snapshot.ReasonBeforeMigration
	default:
		return nil, invalid(`reason must be "manual" or "before_migration"`)
	}
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if err = s.allow(ctx, st, "snapshot:"+p.UserID, snapshotCreateLimit, snapshotCreateWindow); err != nil {
		return nil, err
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, p.UserID)
	if err != nil {
		return nil, err
	}
	ctx = withSchemasFromRPC(ctx, u.SchemaVersion)
	schema, err := gateWrite(ctx, tx, u)
	if err != nil {
		return nil, err
	}
	state, err := loadState(ctx, tx, p.UserID, schema)
	if err != nil {
		return nil, internalError(err)
	}
	row, created, err := captureSnapshot(ctx, tx, u, reason, state)
	if err != nil {
		slog.Error("snapshot capture", "error", err)
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.CreateSnapshotResponse{Snapshot: toSnapshotInfo(row), Created: created}), nil
}

func (s *SnapshotService) GetSnapshot(ctx context.Context, req *connect.Request[tripmapv1.GetSnapshotRequest]) (*connect.Response[tripmapv1.GetSnapshotResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	if req.Msg.GetId() == "" {
		return nil, invalid("snapshot id is required")
	}
	owner, err := st.DB.User.Get(ctx, p.UserID)
	if ent.IsNotFound(err) {
		return nil, errUnauthenticated
	}
	if err != nil {
		return nil, internalError(err)
	}
	ctx = withSchemasFromRPC(ctx, owner.SchemaVersion)
	if err = gateRead(ctx, owner.SchemaVersion); err != nil {
		return nil, err
	}
	row, err := st.DB.Snapshot.Query().Where(snapshot.ID(req.Msg.GetId()), snapshot.UserID(p.UserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("snapshot not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	data, err := decodeSnapshot(row.Payload)
	if err != nil {
		slog.Error("snapshot decode", "error", err)
		return nil, internalError(err)
	}
	if clientSchema(ctx) >= schemaTrips {
		data = asSchema2(data)
	}
	data, err = s.normalizeSnapshot(data)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&tripmapv1.GetSnapshotResponse{Snapshot: toSnapshotInfo(row), Data: data}), nil
}

// restoreMutations is the minimal ordered change list that turns the current
// records into the snapshot's: removals first, then new or changed records.
// Snapshot records are validated against the current region index, which may
// have changed since the snapshot was taken.
func (s *SnapshotService) restoreMutations(data *tripmapv1.SnapshotData, st *footState, schema int) ([]*tripmapv1.FootprintMutation, error) {
	if schema >= schemaTrips {
		data = asSchema2(data)
	}
	data, err := s.normalizeSnapshot(data)
	if err != nil {
		return nil, err
	}
	if schema >= schemaTrips {
		return s.restoreMutationsV2(data, st)
	}
	if data.SchemaVersion >= schemaTrips {
		return nil, upgradeRequired()
	}
	visits, marks := st.visits, st.marks
	var targetVisits []*tripmapv1.Visit
	var targetMarks []*tripmapv1.ProvinceMark
	wantVisit := map[string]*tripmapv1.Visit{}
	wantMark := map[string]*tripmapv1.ProvinceMark{}
	for _, v := range data.Visits {
		m, err := s.fp.normalizeMutation(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: v}})
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot contains regions the current region index no longer accepts"))
		}
		nv := m.GetUpsertVisit()
		targetVisits = append(targetVisits, nv)
		wantVisit[nv.Code] = nv
	}
	for _, mk := range data.ProvinceMarks {
		m, err := s.fp.normalizeMutation(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: mk}})
		if err != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot contains regions the current region index no longer accepts"))
		}
		nm := m.GetUpsertMark()
		targetMarks = append(targetMarks, nm)
		wantMark[nm.Province] = nm
	}
	if len(wantVisit) != len(targetVisits) || len(wantMark) != len(targetMarks) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("snapshot contains duplicate records"))
	}
	var ops []*tripmapv1.FootprintMutation
	haveVisit := map[string]*ent.CityVisit{}
	for _, v := range visits {
		haveVisit[v.RegionCode] = v
		if _, ok := wantVisit[v.RegionCode]; !ok {
			ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: v.RegionCode}})
		}
	}
	haveMark := map[string]*ent.ProvinceMark{}
	for _, m := range marks {
		haveMark[m.ProvinceCode] = m
		if _, ok := wantMark[m.ProvinceCode]; !ok {
			ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteMark{DeleteMark: m.ProvinceCode}})
		}
	}
	for _, v := range targetVisits {
		cur, ok := haveVisit[v.Code]
		if ok && cur.RegionName == v.Name && regions.CanonicalProvince(cur.RegionCode, cur.ProvinceCode) == v.Province && cur.Color == v.Color && cur.TravelDate == v.Date && cur.Note == v.Note {
			continue
		}
		ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: v}})
	}
	for _, m := range targetMarks {
		if cur, ok := haveMark[m.Province]; ok && cur.Color == m.Color {
			continue
		}
		ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: m}})
	}
	return ops, nil
}

// restoreMutationsV2 diffs cities, trips and marks. Trips of a city that is
// removed go with it; a trip whose city changed is deleted before it is
// created again, since a trip never moves between cities.
func (s *SnapshotService) restoreMutationsV2(data *tripmapv1.SnapshotData, st *footState) ([]*tripmapv1.FootprintMutation, error) {
	refused := func(msg string) error { return connect.NewError(connect.CodeFailedPrecondition, errors.New(msg)) }
	const staleIndex = "snapshot contains regions the current region index no longer accepts"
	var wantCities []*tripmapv1.Visit
	var wantTrips []*tripmapv1.Trip
	var wantMarks []*tripmapv1.ProvinceMark
	city := map[string]*tripmapv1.Visit{}
	tripByID := map[string]*tripmapv1.Trip{}
	mark := map[string]*tripmapv1.ProvinceMark{}
	for _, v := range data.Visits {
		m, err := s.fp.normalizeMutation(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: v}})
		if err != nil {
			return nil, refused(staleIndex)
		}
		wantCities = append(wantCities, m.GetUpsertCity())
		city[v.Code] = m.GetUpsertCity()
	}
	for _, t := range data.Trips {
		m, err := s.fp.normalizeMutation(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: t}})
		if err != nil {
			return nil, refused(staleIndex)
		}
		wantTrips = append(wantTrips, m.GetUpsertTrip())
		tripByID[t.Id] = m.GetUpsertTrip()
	}
	for _, mk := range data.ProvinceMarks {
		m, err := s.fp.normalizeMutation(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: mk}})
		if err != nil {
			return nil, refused(staleIndex)
		}
		wantMarks = append(wantMarks, m.GetUpsertMark())
		mark[mk.Province] = m.GetUpsertMark()
	}
	if len(city) != len(wantCities) || len(tripByID) != len(wantTrips) || len(mark) != len(wantMarks) {
		return nil, refused("snapshot contains duplicate records")
	}
	for _, t := range wantTrips {
		if city[t.RegionCode] == nil {
			return nil, refused("snapshot contains a trip without its city")
		}
	}

	var ops []*tripmapv1.FootprintMutation
	haveCity := map[string]*ent.CityVisit{}
	for _, v := range st.visits {
		haveCity[v.RegionCode] = v
	}
	haveTrip := map[string]*ent.Trip{}
	for _, t := range st.trips {
		haveTrip[t.TripID] = t
		want := tripByID[t.TripID]
		if city[t.RegionCode] != nil && (want == nil || want.RegionCode != t.RegionCode) {
			ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteTrip{DeleteTrip: &tripmapv1.DeleteTrip{RegionCode: t.RegionCode, Id: t.TripID}}})
		}
	}
	for _, v := range st.visits {
		if city[v.RegionCode] == nil {
			ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: v.RegionCode}})
		}
	}
	haveMark := map[string]*ent.ProvinceMark{}
	for _, m := range st.marks {
		haveMark[m.ProvinceCode] = m
		if mark[m.ProvinceCode] == nil {
			ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteMark{DeleteMark: m.ProvinceCode}})
		}
	}
	for _, v := range wantCities {
		cur := haveCity[v.Code]
		if cur != nil && cur.RegionName == v.Name && regions.CanonicalProvince(cur.RegionCode, cur.ProvinceCode) == v.Province && cur.Color == v.Color {
			continue
		}
		ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: v}})
	}
	for _, t := range wantTrips {
		cur := haveTrip[t.Id]
		if cur != nil && cur.RegionCode == t.RegionCode && cur.TravelDate == t.Date && cur.Note == t.Note {
			continue
		}
		ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: t}})
	}
	for _, m := range wantMarks {
		if cur, ok := haveMark[m.Province]; ok && cur.Color == m.Color {
			continue
		}
		ops = append(ops, &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: m}})
	}
	return ops, nil
}

func (s *SnapshotService) RestoreSnapshot(ctx context.Context, req *connect.Request[tripmapv1.RestoreSnapshotRequest]) (*connect.Response[tripmapv1.RestoreSnapshotResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if m.SnapshotId == "" || !mutationIDPattern.MatchString(m.MutationId) || m.BaseRevision < 0 {
		return nil, invalid("snapshot_id, mutation_id and a non-negative base_revision are required")
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("restore\x00%s\x00%d", m.SnapshotId, m.BaseRevision))))
	// A retry of a finished restore is answered from its receipt without
	// spending budget; the transaction below repeats the check under the lock.
	replay := func(receipt *ent.SyncReceipt) (*connect.Response[tripmapv1.RestoreSnapshotResponse], error) {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used for another request"))
		}
		out := &tripmapv1.RestoreSnapshotResponse{Revision: receipt.Revision, ChangedCount: int32(receipt.ChangedCount), Replayed: true}
		if receipt.SnapshotID != nil {
			if undo, err := st.DB.Snapshot.Query().Where(snapshot.ID(*receipt.SnapshotID), snapshot.UserID(p.UserID)).
				Select(snapshotInfoColumns...).Only(ctx); err == nil {
				out.UndoSnapshot = toSnapshotInfo(undo)
			}
		}
		return connect.NewResponse(out), nil
	}
	if receipt, err := st.DB.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		return replay(receipt)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if err = s.allow(ctx, st, "snapshot-restore:"+p.UserID, snapshotRestoreLimit, snapshotRestoreWin); err != nil {
		return nil, err
	}

	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, p.UserID)
	if err != nil {
		return nil, err
	}
	if receipt, err := tx.SyncReceipt.Query().Where(syncreceipt.UserID(p.UserID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		return replay(receipt)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if m.BaseRevision != u.Revision {
		conflict := connect.NewError(connect.CodeAborted, errors.New("footprints changed since base_revision"))
		if d, derr := connect.NewErrorDetail(&tripmapv1.RevisionConflict{CurrentRevision: u.Revision}); derr == nil {
			conflict.AddDetail(d)
		}
		return nil, conflict
	}
	target, err := tx.Snapshot.Query().Where(snapshot.ID(m.SnapshotId), snapshot.UserID(p.UserID)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("snapshot not found"))
	}
	if err != nil {
		return nil, internalError(err)
	}
	data, err := decodeSnapshot(target.Payload)
	if err != nil {
		slog.Error("snapshot decode", "error", err)
		return nil, internalError(err)
	}
	if err := requireCurrentRegionIDs(ctx, tx.CityVisit.Query().Where(cityvisit.UserID(u.ID)), s.fp.regions); err != nil {
		return nil, err
	}
	ctx = withSchemasFromRPC(ctx, u.SchemaVersion)
	schema, err := gateWrite(ctx, tx, u)
	if err != nil {
		return nil, err
	}
	state, err := loadState(ctx, tx, p.UserID, schema)
	if err != nil {
		return nil, internalError(err)
	}
	ops, err := s.restoreMutations(data, state, schema)
	if err != nil {
		return nil, err
	}
	// Save the state being replaced first, so the restore itself can be undone.
	undo, _, err := captureSnapshot(ctx, tx, u, snapshot.ReasonBeforeRestore, state, target.ID)
	if err != nil {
		slog.Error("snapshot capture", "error", err)
		return nil, internalError(err)
	}
	revision, count := u.Revision, int32(0)
	for _, op := range ops {
		changed, err := s.fp.applyMutation(ctx, tx, p.UserID, op, schema)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		revision++
		count++
		if err = recordChange(ctx, tx, p.UserID, revision, op, schema); err != nil {
			return nil, internalError(err)
		}
	}
	if count > 0 {
		if err = tx.User.UpdateOneID(p.UserID).SetRevision(revision).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if err = tx.SyncReceipt.Create().SetUserID(p.UserID).SetMutationID(m.MutationId).SetRequestHash(hash).SetRevision(revision).SetChangedCount(int(count)).SetSnapshotID(undo.ID).Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(&tripmapv1.RestoreSnapshotResponse{Revision: revision, ChangedCount: count, UndoSnapshot: toSnapshotInfo(undo)}), nil
}
