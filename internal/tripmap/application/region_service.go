package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/platform/rpcauth"
	"github.com/kongken/mono/internal/tripmap/auth"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	entitinerary "github.com/kongken/mono/internal/tripmap/ent/itinerary"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	"github.com/kongken/mono/internal/tripmap/ent/schema"
	"github.com/kongken/mono/internal/tripmap/ent/sharedtrip"
	"github.com/kongken/mono/internal/tripmap/ent/snapshot"
	"github.com/kongken/mono/internal/tripmap/ent/syncreceipt"
	"github.com/kongken/mono/internal/tripmap/ent/tripmember"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

const regionVersionHeader = "X-Tripmap-Regions"

func checkRegionVersion(version string) error {
	if version != "" && version != regions.CurrentVersion() {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("region index version mismatch: reload the app or upgrade the server"))
	}
	return nil
}

func regionVersionFrom(ctx context.Context, header http.Header) string {
	if v := header.Get(regionVersionHeader); v != "" {
		return v
	}
	return rpcauth.CallFrom(ctx).TripmapRegions
}

// A canonical write must not create a second city alongside its retired ID.
// Call under the account row lock, after receipt replay, before ordinary writes.
func requireCurrentRegionIDs(ctx context.Context, query *ent.CityVisitQuery, index *regions.Index) error {
	rows, err := query.Select(cityvisit.FieldRegionCode, cityvisit.FieldRegionName, cityvisit.FieldProvinceCode).All(ctx)
	if err != nil {
		return internalError(err)
	}
	for _, row := range rows {
		ref, err := index.ResolveCity(row.RegionCode, row.RegionName, row.ProvinceCode)
		if err != nil || ref.Code != row.RegionCode {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade required before writes: preview and migrate old region IDs; no data changed"))
		}
	}
	return nil
}

type RegionService struct {
	src   StoreSource
	index *regions.Index
}

func NewRegionService(src StoreSource, idx *regions.Index) *RegionService {
	return &RegionService{src, idx}
}

func (s *RegionService) GetRegionIndex(ctx context.Context, req *connect.Request[tripmapv1.GetRegionIndexRequest]) (*connect.Response[tripmapv1.GetRegionIndexResponse], error) {
	if _, err := principal(ctx); err != nil {
		return nil, err
	}
	out := &tripmapv1.GetRegionIndexResponse{Version: s.index.Version(), NotModified: req.Msg.KnownVersion == s.index.Version(), SupportedVersions: s.index.SupportedVersions()}
	if !out.NotModified {
		out.IndexJson = s.index.JSON()
		for _, m := range s.index.Mappings() {
			out.Mappings = append(out.Mappings, &tripmapv1.RegionMapping{FromCode: m.FromCode, FromName: m.FromName, FromProvince: m.FromProvince, ToCode: m.ToCode})
		}
	}
	return connect.NewResponse(out), nil
}

type regionPlan struct {
	state   *footState
	preview *tripmapv1.PreviewRegionUpgradeResponse
	cities  map[string]regions.CityRef // keyed by original row ID
	codes   map[string]string          // ID-only references: trips and media
}

func (s *RegionService) plan(ctx context.Context, tx *ent.Tx, u *ent.User) (*regionPlan, error) {
	state, err := loadState(ctx, tx, u.ID, u.SchemaVersion)
	if err != nil {
		return nil, err
	}
	p := &regionPlan{state: state, preview: &tripmapv1.PreviewRegionUpgradeResponse{Version: s.index.Version(), Revision: u.Revision}, cities: map[string]regions.CityRef{}, codes: map[string]string{}}
	occupied := map[string]string{}
	for _, v := range state.visits {
		ref, err := s.index.ResolveCity(v.RegionCode, v.RegionName, v.ProvinceCode)
		if err != nil {
			p.preview.Blockers = append(p.preview.Blockers, "unmapped region: "+v.RegionCode)
			continue
		}
		if previous, exists := occupied[ref.Code]; exists {
			p.preview.Blockers = append(p.preview.Blockers, "region collision: "+previous+" / "+v.RegionCode)
			continue
		}
		occupied[ref.Code] = v.RegionCode
		if ref.Code != v.RegionCode || ref.Name != v.RegionName || ref.Province != v.ProvinceCode {
			p.cities[v.ID] = ref
			p.preview.Changes = append(p.preview.Changes, &tripmapv1.RegionUpgradeChange{FromCode: v.RegionCode, ToCode: ref.Code, FromName: v.RegionName, ToName: ref.Name, FromProvince: v.ProvinceCode, ToProvince: ref.Province})
		}
		if ref.Code != v.RegionCode {
			p.codes[v.RegionCode] = ref.Code
		}
	}
	for _, m := range state.marks {
		if !s.index.HasProvince(m.ProvinceCode) {
			p.preview.Blockers = append(p.preview.Blockers, "unmapped province mark: "+m.ProvinceCode)
		}
	}
	media, err := tx.MediaObject.Query().Where(mediaobject.UserID(u.ID)).Select(mediaobject.FieldRegionCode).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range media {
		code, ok := s.index.ResolveCode(m.RegionCode)
		if !ok {
			p.preview.Blockers = append(p.preview.Blockers, "unmapped media region: "+m.RegionCode)
		} else if code != m.RegionCode {
			if _, exists := p.codes[m.RegionCode]; !exists {
				name, province, _ := s.index.City(code)
				change := &tripmapv1.RegionUpgradeChange{FromCode: m.RegionCode, ToCode: code, FromName: m.RegionCode, ToName: name, ToProvince: province, MediaOnly: true}
				for _, mapping := range s.index.Mappings() {
					if mapping.FromCode == m.RegionCode {
						change.FromName, change.FromProvince = mapping.FromName, mapping.FromProvince
						break
					}
				}
				p.preview.Changes = append(p.preview.Changes, change)
			}
			p.codes[m.RegionCode] = code
		}
	}
	itineraries, err := tx.Itinerary.Query().Where(entitinerary.UserID(u.ID)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, it := range itineraries {
		for _, stop := range it.Stops {
			code, ok := s.index.ResolveCode(stop.RegionCode)
			if !ok {
				p.preview.Blockers = append(p.preview.Blockers, "unmapped itinerary region: "+stop.RegionCode)
			} else if code != stop.RegionCode {
				if _, exists := p.codes[stop.RegionCode]; !exists {
					name, province, _ := s.index.City(code)
					change := &tripmapv1.RegionUpgradeChange{FromCode: stop.RegionCode, ToCode: code, FromName: stop.RegionCode, ToName: name, ToProvince: province, MediaOnly: true}
					for _, mapping := range s.index.Mappings() {
						if mapping.FromCode == stop.RegionCode {
							change.FromName, change.FromProvince = mapping.FromName, mapping.FromProvince
							break
						}
					}
					p.preview.Changes = append(p.preview.Changes, change)
				}
				p.codes[stop.RegionCode] = code
			}
		}
	}
	for _, t := range state.trips {
		// Never remap an unrelated trip, even if its ID-only field is recognised.
		v := slices.IndexFunc(state.visits, func(v *ent.CityVisit) bool { return v.ID == t.VisitID && v.RegionCode == t.RegionCode })
		if v < 0 {
			p.preview.Blockers = append(p.preview.Blockers, "trip city mismatch: "+t.TripID)
		}
	}
	for old := range p.codes {
		// A code change of an active/pending shared trip needs a coordinated
		// multi-account plan. Fail closed instead of editing only one member.
		shared, err := tx.SharedTrip.Query().Where(sharedtrip.RegionCode(old), sharedtrip.Or(sharedtrip.OwnerID(u.ID), sharedtrip.HasMembersWith(tripmember.UserID(u.ID)))).Exist(ctx)
		if err != nil {
			return nil, err
		}
		if shared {
			p.preview.Blockers = append(p.preview.Blockers, "shared trip requires coordinated region upgrade: "+old)
		}
	}
	slices.Sort(p.preview.Blockers)
	p.preview.Blockers = slices.Compact(p.preview.Blockers)
	return p, nil
}

func (s *RegionService) PreviewRegionUpgrade(ctx context.Context, _ *connect.Request[tripmapv1.PreviewRegionUpgradeRequest]) (*connect.Response[tripmapv1.PreviewRegionUpgradeResponse], error) {
	p, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, "region-preview:"+p.UserID, 60, time.Minute)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("region preview temporarily unavailable"))
	}
	if !ok {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many region previews"))
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
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
	plan, err := s.plan(ctx, tx, u)
	if err != nil {
		return nil, internalError(err)
	}
	out := connect.NewResponse(plan.preview)
	out.Header().Set("Cache-Control", "no-store")
	return out, nil
}

func (s *RegionService) ApplyRegionUpgrade(ctx context.Context, req *connect.Request[tripmapv1.ApplyRegionUpgradeRequest]) (*connect.Response[tripmapv1.ApplyRegionUpgradeResponse], error) {
	principal, err := principal(ctx)
	if err != nil {
		return nil, err
	}
	m := req.Msg
	if !mutationIDPattern.MatchString(m.MutationId) || m.BaseRevision < 0 {
		return nil, invalid("invalid region upgrade request")
	}
	st, err := storeFrom(s.src)
	if err != nil {
		return nil, err
	}
	raw, err := proto.Marshal(m)
	if err != nil {
		return nil, internalError(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(append([]byte("region-upgrade:v1:"), raw...)))
	replay := func(receipt *ent.SyncReceipt) (*connect.Response[tripmapv1.ApplyRegionUpgradeResponse], error) {
		if receipt.RequestHash != hash {
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("mutation_id was already used"))
		}
		out := &tripmapv1.ApplyRegionUpgradeResponse{Version: m.Version, Revision: receipt.Revision, ChangedCount: int32(receipt.ChangedCount), Replayed: true}
		if receipt.SnapshotID != nil {
			out.SnapshotId = *receipt.SnapshotID
		}
		return connect.NewResponse(out), nil
	}
	// A committed retry needs neither write budget nor Redis availability.
	if receipt, err := st.DB.SyncReceipt.Query().Where(syncreceipt.UserID(principal.UserID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		return replay(receipt)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	ok, err := auth.NewLimiter(st.Redis, st.prefix()).Allow(ctx, "region-upgrade:"+principal.UserID, 6, time.Minute)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("region upgrade temporarily unavailable"))
	}
	if !ok {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("too many region upgrades"))
	}
	tx, err := st.DB.Tx(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	defer func() { _ = tx.Rollback() }()
	u, err := lockUser(ctx, tx, principal.UserID)
	if err != nil {
		return nil, err
	}
	if receipt, err := tx.SyncReceipt.Query().Where(syncreceipt.UserID(u.ID), syncreceipt.MutationIDEQ(m.MutationId)).Only(ctx); err == nil {
		return replay(receipt)
	} else if !ent.IsNotFound(err) {
		return nil, internalError(err)
	}
	if m.Version != s.index.Version() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("region index version mismatch"))
	}
	if m.BaseRevision != u.Revision {
		return nil, connect.NewError(connect.CodeAborted, errors.New("footprints changed since region preview"))
	}
	plan, err := s.plan(ctx, tx, u)
	if err != nil {
		return nil, internalError(err)
	}
	if len(plan.preview.Blockers) > 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("region upgrade blocked: unmapped, colliding or shared records; no data changed"))
	}
	out := &tripmapv1.ApplyRegionUpgradeResponse{Version: s.index.Version(), Revision: u.Revision, ChangedCount: int32(len(plan.preview.Changes))}
	if len(plan.cities) > 0 || len(plan.codes) > 0 {
		before, _, err := captureSnapshot(ctx, tx, u, snapshot.ReasonBeforeMigration, plan.state)
		if err != nil {
			return nil, internalError(err)
		}
		out.SnapshotId = before.ID
	}
	if u.SchemaVersion < schemaTrips && slices.ContainsFunc(plan.preview.Changes, func(c *tripmapv1.RegionUpgradeChange) bool { return !c.MediaOnly && c.FromCode != c.ToCode }) {
		// Materialise legacy trip IDs before renaming their city IDs. Otherwise
		// legacy-<city> would change identity merely by reading the migrated row.
		if clientSchema(ctx) < schemaTrips {
			return nil, upgradeRequired()
		}
		if err := upgradeAccount(ctx, tx, u); err != nil {
			return nil, internalError(err)
		}
		plan, err = s.plan(ctx, tx, u)
		if err != nil {
			return nil, internalError(err)
		}
	}
	journal := func(m *tripmapv1.FootprintMutation) error {
		out.Revision++
		return recordChange(ctx, tx, u.ID, out.Revision, m, u.SchemaVersion)
	}
	for _, v := range plan.state.visits {
		ref, changed := plan.cities[v.ID]
		if !changed {
			continue
		}
		if err := tx.CityVisit.UpdateOneID(v.ID).SetRegionCode(ref.Code).SetRegionName(ref.Name).SetProvinceCode(ref.Province).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
		if ref.Code != v.RegionCode {
			// RegionCode is immutable for ordinary edits. A reviewed ID rename
			// replaces the row atomically while retaining every stable ID and timestamp.
			for _, t := range plan.state.trips {
				if t.VisitID != v.ID {
					continue
				}
				if err := tx.Trip.DeleteOneID(t.ID).Exec(ctx); err != nil {
					return nil, internalError(err)
				}
				if err := tx.Trip.Create().SetID(t.ID).SetUserID(u.ID).SetVisitID(v.ID).SetTripID(t.TripID).SetRegionCode(ref.Code).SetTravelDate(t.TravelDate).SetNote(t.Note).SetCreatedAt(t.CreatedAt).SetUpdatedAt(t.UpdatedAt).Exec(ctx); err != nil {
					return nil, internalError(err)
				}
			}
			if err := journal(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: v.RegionCode}}); err != nil {
				return nil, internalError(err)
			}
		}
		row, err := tx.CityVisit.Get(ctx, v.ID)
		if err != nil {
			return nil, internalError(err)
		}
		mutation := &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: toVisit(row)}}
		if u.SchemaVersion >= schemaTrips {
			mutation.Operation = &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: toCity(row)}
		}
		if err := journal(mutation); err != nil {
			return nil, internalError(err)
		}
		if ref.Code != v.RegionCode && u.SchemaVersion >= schemaTrips {
			for _, t := range plan.state.trips {
				if t.VisitID == v.ID {
					copy := *t
					copy.RegionCode = ref.Code
					if err := journal(&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: toTrip(&copy)}}); err != nil {
						return nil, internalError(err)
					}
				}
			}
		}
	}
	for old, code := range plan.codes {
		if _, err := tx.MediaObject.Update().Where(mediaobject.UserID(u.ID), mediaobject.RegionCode(old)).SetRegionCode(code).Save(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	if len(plan.codes) > 0 {
		itineraries, err := tx.Itinerary.Query().Where(entitinerary.UserID(u.ID)).All(ctx)
		if err != nil {
			return nil, internalError(err)
		}
		for _, it := range itineraries {
			changed := false
			stops := make([]schema.ItineraryStopRecord, len(it.Stops))
			for i, stop := range it.Stops {
				stops[i] = stop
				if mapped, ok := plan.codes[stop.RegionCode]; ok && mapped != stop.RegionCode {
					stops[i].RegionCode = mapped
					changed = true
				}
			}
			if changed {
				if err := tx.Itinerary.UpdateOneID(it.ID).SetStops(stops).SetVersion(it.Version + 1).Exec(ctx); err != nil {
					return nil, internalError(err)
				}
			}
		}
	}
	if out.Revision != u.Revision {
		if err := tx.User.UpdateOneID(u.ID).SetRevision(out.Revision).Exec(ctx); err != nil {
			return nil, internalError(err)
		}
	}
	receipt := tx.SyncReceipt.Create().SetUserID(u.ID).SetMutationID(m.MutationId).SetRequestHash(hash).SetRevision(out.Revision).SetChangedCount(int(out.ChangedCount))
	if out.SnapshotId != "" {
		receipt.SetSnapshotID(out.SnapshotId)
	}
	if err := receipt.Exec(ctx); err != nil {
		return nil, internalError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, internalError(err)
	}
	return connect.NewResponse(out), nil
}
