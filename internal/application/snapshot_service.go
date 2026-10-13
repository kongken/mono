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
	"go.orx.me/apps/trip-map/internal/auth"
	"go.orx.me/apps/trip-map/internal/config"
	"go.orx.me/apps/trip-map/internal/ent"
	"go.orx.me/apps/trip-map/internal/ent/cityvisit"
	"go.orx.me/apps/trip-map/internal/ent/provincemark"
	"go.orx.me/apps/trip-map/internal/ent/snapshot"
	"go.orx.me/apps/trip-map/internal/ent/syncreceipt"
	"go.orx.me/apps/trip-map/internal/ent/user"
	"go.orx.me/apps/trip-map/internal/regions"
	tripmapv1 "go.orx.me/apps/trip-map/pkg/proto/tripmap/v1"
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

func toSnapshotInfo(s *ent.Snapshot) *tripmapv1.SnapshotInfo {
	return &tripmapv1.SnapshotInfo{
		Id: s.ID, Reason: string(s.Reason), CreatedAt: timestamppb.New(s.CreatedAt),
		VisitCount: int32(s.VisitCount), ProvinceMarkCount: int32(s.MarkCount), Revision: s.Revision,
	}
}

func encodeSnapshot(visits []*ent.CityVisit, marks []*ent.ProvinceMark) ([]byte, error) {
	data := &tripmapv1.SnapshotData{}
	for _, v := range visits {
		data.Visits = append(data.Visits, toVisit(v))
	}
	for _, m := range marks {
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

func loadState(ctx context.Context, tx *ent.Tx, uid string) ([]*ent.CityVisit, []*ent.ProvinceMark, error) {
	visits, err := tx.CityVisit.Query().Where(cityvisit.UserID(uid)).Order(ent.Asc(cityvisit.FieldRegionCode)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	marks, err := tx.ProvinceMark.Query().Where(provincemark.UserID(uid)).Order(ent.Asc(provincemark.FieldProvinceCode)).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	return visits, marks, nil
}

// captureSnapshot stores the user's current footprints. The caller must hold
// the user's row lock in tx so the revision matches the records read. An
// existing snapshot of the same revision (same content) is reused rather than
// duplicated; created reports whether a new row was written.
//
// keep names a snapshot retention must not remove while it is still in use.
func captureSnapshot(ctx context.Context, tx *ent.Tx, u *ent.User, reason snapshot.Reason, visits []*ent.CityVisit, marks []*ent.ProvinceMark, keep ...string) (row *ent.Snapshot, created bool, err error) {
	existing, err := tx.Snapshot.Query().Where(snapshot.UserID(u.ID), snapshot.RevisionEQ(u.Revision)).Order(ent.Desc(snapshot.FieldCreatedAt)).First(ctx)
	if err == nil {
		return existing, false, nil
	}
	if !ent.IsNotFound(err) {
		return nil, false, err
	}
	payload, err := encodeSnapshot(visits, marks)
	if err != nil {
		return nil, false, err
	}
	row, err = tx.Snapshot.Create().SetUserID(u.ID).SetReason(reason).SetRevision(u.Revision).
		SetVisitCount(len(visits)).SetMarkCount(len(marks)).SetPayload(payload).Save(ctx)
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
	if u.Disabled {
		return nil, errUnauthenticated
	}
	return u, nil
}

func (s *SnapshotService) allow(ctx context.Context, st *Store, key string, limit int64, window time.Duration) error {
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, key, limit, window)
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
	// Payloads are not loaded: the list only needs the metadata columns.
	rows, err := st.DB.Snapshot.Query().Where(snapshot.UserID(p.UserID)).Order(ent.Desc(snapshot.FieldCreatedAt, snapshot.FieldID)).
		Select(snapshot.FieldID, snapshot.FieldReason, snapshot.FieldRevision, snapshot.FieldVisitCount, snapshot.FieldMarkCount, snapshot.FieldCreatedAt).All(ctx)
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
	visits, marks, err := loadState(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	row, created, err := captureSnapshot(ctx, tx, u, reason, visits, marks)
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
	return connect.NewResponse(&tripmapv1.GetSnapshotResponse{Snapshot: toSnapshotInfo(row), Data: data}), nil
}

// restoreMutations is the minimal ordered change list that turns the current
// records into the snapshot's: removals first, then new or changed records.
// Snapshot records are validated against the current region index, which may
// have changed since the snapshot was taken.
func (s *SnapshotService) restoreMutations(data *tripmapv1.SnapshotData, visits []*ent.CityVisit, marks []*ent.ProvinceMark) ([]*tripmapv1.FootprintMutation, error) {
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
				Select(snapshot.FieldID, snapshot.FieldReason, snapshot.FieldRevision, snapshot.FieldVisitCount, snapshot.FieldMarkCount, snapshot.FieldCreatedAt).Only(ctx); err == nil {
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
	visits, marks, err := loadState(ctx, tx, p.UserID)
	if err != nil {
		return nil, internalError(err)
	}
	ops, err := s.restoreMutations(data, visits, marks)
	if err != nil {
		return nil, err
	}
	// Save the state being replaced first, so the restore itself can be undone.
	undo, _, err := captureSnapshot(ctx, tx, u, snapshot.ReasonBeforeRestore, visits, marks, target.ID)
	if err != nil {
		slog.Error("snapshot capture", "error", err)
		return nil, internalError(err)
	}
	revision, count := u.Revision, int32(0)
	for _, op := range ops {
		changed, err := applyMutation(ctx, tx, p.UserID, op)
		if err != nil {
			return nil, err
		}
		if !changed {
			continue
		}
		revision++
		count++
		if err = recordChange(ctx, tx, p.UserID, revision, op); err != nil {
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
