package application_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/ent"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	"github.com/kongken/mono/internal/tripmap/ent/mediaobject"
	entsnapshot "github.com/kongken/mono/internal/tripmap/ent/snapshot"
	"github.com/kongken/mono/internal/tripmap/ent/trip"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	"github.com/kongken/mono/internal/tripmap/regions"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"github.com/kongken/mono/pkg/proto/tripmap/v1/tripmapv1connect"
	"google.golang.org/protobuf/proto"
)

func regionUser(t *testing.T, e *env, username string) (string, *ent.User) {
	t.Helper()
	token := e.register(username)
	u, err := e.store.DB.User.Query().Where(user.Username(username)).Only(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return token, u
}
func regionCity(t *testing.T, e *env, uid, code, name, province string) *ent.CityVisit {
	t.Helper()
	v, err := e.store.DB.CityVisit.Create().SetUserID(uid).SetRegionCode(code).SetRegionName(name).SetProvinceCode(province).SetColor(green).SetTravelDate("2026-10-14").SetNote("原旅行记忆 🏔️").Save(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row, err := e.store.DB.CityVisit.Get(t.Context(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func regionReq[T any](token string, msg *T) *connect.Request[T] {
	r := bearer(token, msg)
	r.Header().Set("X-Tripmap-Schema", "2")
	return r
}

// This fixture is deliberately not shipped in production. It exercises a real
// retired ID with the same reviewed rule in the frontend's compatibility tests.
func retiredIndex(t *testing.T) *regions.Index {
	t.Helper()
	idx, err := regions.Default()
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Mapping regions.Mapping `json:"mapping"`
	}
	raw, err := os.ReadFile("../../../packages/fixtures/region-upgrades.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	mappings := append(idx.Mappings(), fixture.Mapping)
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(idx.JSON())); err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal(mappings)
	canonical := append([]byte(`{"regions":`), compact.Bytes()...)
	canonical = append(canonical, []byte(`,"mappings":`)...)
	canonical = append(canonical, rules...)
	canonical = append(canonical, '}')
	version := fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
	manifest, _ := json.Marshal(regions.Catalog{Version: version, SupportedVersions: []string{"legacy", idx.Version(), version}, Mappings: mappings})
	out, err := idx.WithCatalog(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func serveRegionIndex(t *testing.T, e *env, idx *regions.Index) (tripmapv1connect.RegionServiceClient, tripmapv1connect.SnapshotServiceClient, tripmapv1connect.FootprintServiceClient) {
	t.Helper()
	src := storeSource{e.store}
	opts := connect.WithInterceptors(testRPCAuth())
	mux := http.NewServeMux()
	mux.Handle(tripmapv1connect.NewRegionServiceHandler(application.NewRegionService(src, idx), opts))
	mux.Handle(tripmapv1connect.NewSnapshotServiceHandler(application.NewSnapshotService(e.cfg, src, idx), opts))
	mux.Handle(tripmapv1connect.NewFootprintServiceHandler(application.NewFootprintService(e.cfg, src, idx), opts))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return tripmapv1connect.NewRegionServiceClient(server.Client(), server.URL), tripmapv1connect.NewSnapshotServiceClient(server.Client(), server.URL), tripmapv1connect.NewFootprintServiceClient(server.Client(), server.URL)
}

func TestRegionIndexAuthenticationConditionalReadAndMismatch(t *testing.T) {
	e := newEnv(t)
	token := e.register("region_index")
	ctx := t.Context()
	_, err := e.region.GetRegionIndex(ctx, connect.NewRequest(&tripmapv1.GetRegionIndexRequest{}))
	wantCode(t, err, connect.CodeUnauthenticated)
	full, err := e.region.GetRegionIndex(ctx, bearer(token, &tripmapv1.GetRegionIndexRequest{}))
	if err != nil || full.Msg.Version == "" || full.Msg.NotModified || len(full.Msg.Mappings) != 2 || full.Msg.IndexJson == "" {
		t.Fatalf("catalog: %v %v", full, err)
	}
	unchanged, err := e.region.GetRegionIndex(ctx, bearer(token, &tripmapv1.GetRegionIndexRequest{KnownVersion: full.Msg.Version}))
	if err != nil || !unchanged.Msg.NotModified || unchanged.Msg.IndexJson != "" || len(unchanged.Msg.Mappings) != 0 {
		t.Fatalf("conditional: %v %v", unchanged, err)
	}
	wrong := regionReq(token, &tripmapv1.GetFootprintsRequest{})
	wrong.Header().Set("X-Tripmap-Regions", "sha256:unknown-client")
	_, err = e.foot.GetFootprints(ctx, wrong)
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestRegionUpgradePreservesIslandContentAndOtherAccounts(t *testing.T) {
	e := newEnv(t)
	token, u := regionUser(t, e, "region_islands")
	other, v := regionUser(t, e, "region_other")
	ctx := t.Context()
	for code, name := range map[string]string{"tw-09020": "金门县", "tw-09007": "连江县"} {
		regionCity(t, e, u.ID, code, name, "710000")
	}
	regionCity(t, e, v.ID, "tw-09020", "金门县", "710000")
	if err := e.store.DB.ProvinceMark.Create().SetUserID(u.ID).SetProvinceCode("710000").SetColor(green).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := e.region.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	if err != nil || len(preview.Msg.Changes) != 2 || len(preview.Msg.Blockers) != 0 || preview.Msg.Revision != 0 {
		t.Fatalf("preview: %v %v", preview, err)
	}
	if count, _ := e.store.DB.Snapshot.Query().Count(ctx); count != 0 {
		t.Fatal("preview wrote a snapshot")
	}
	msg := &tripmapv1.ApplyRegionUpgradeRequest{Version: preview.Msg.Version, BaseRevision: 0, MutationId: "upgrade-islands"}
	applied, err := e.region.ApplyRegionUpgrade(ctx, regionReq(token, msg))
	if err != nil || applied.Msg.ChangedCount != 2 || applied.Msg.SnapshotId == "" || applied.Msg.Revision != 2 {
		t.Fatalf("apply: %v %v", applied, err)
	}
	for range 8 {
		replay, err := e.region.ApplyRegionUpgrade(ctx, regionReq(token, msg))
		if err != nil || !replay.Msg.Replayed || replay.Msg.SnapshotId != applied.Msg.SnapshotId || replay.Msg.Revision != applied.Msg.Revision {
			t.Fatalf("replay: %v %v", replay, err)
		}
	}
	rows, _ := e.store.DB.CityVisit.Query().Where(cityvisit.UserID(u.ID)).All(ctx)
	for _, row := range rows {
		if row.ProvinceCode != "350000" || row.TravelDate != "2026-10-14" || row.Note != "原旅行记忆 🏔️" || row.Color != green {
			t.Fatalf("changed content: %v", row)
		}
	}
	otherRows, _ := e.store.DB.CityVisit.Query().Where(cityvisit.UserID(v.ID)).All(ctx)
	if otherRows[0].ProvinceCode != "710000" {
		t.Fatal("changed another user's row")
	}
	if r, _ := e.region.PreviewRegionUpgrade(ctx, regionReq(other, &tripmapv1.PreviewRegionUpgradeRequest{})); len(r.Msg.Changes) != 1 {
		t.Fatal("cross-user preview")
	}
	row, _ := e.store.DB.Snapshot.Get(ctx, applied.Msg.SnapshotId)
	bytesBefore := bytes.Clone(row.Payload)
	detail, err := e.snap.GetSnapshot(ctx, regionReq(token, &tripmapv1.GetSnapshotRequest{Id: row.ID}))
	if err != nil || detail.Msg.Data.Visits[0].Province != "350000" || detail.Msg.Data.Trips[0].Note != "原旅行记忆 🏔️" {
		t.Fatalf("old snapshot projection: %v %v", detail, err)
	}
	row, _ = e.store.DB.Snapshot.Get(ctx, row.ID)
	if !bytes.Equal(row.Payload, bytesBefore) {
		t.Fatal("rewrote snapshot payload")
	}
	_, err = e.snap.GetSnapshot(ctx, regionReq(other, &tripmapv1.GetSnapshotRequest{Id: row.ID}))
	wantCode(t, err, connect.CodeNotFound)
}

func TestRetiredRegionUpgradePreservesTripIDsMediaAndSnapshotBytes(t *testing.T) {
	e := newEnv(t)
	token, u := regionUser(t, e, "retired_region")
	idx := retiredIndex(t)
	client, snapshots, footprints := serveRegionIndex(t, e, idx)
	ctx := t.Context()
	visit := regionCity(t, e, u.ID, "old-hangzhou", "旧杭州", "330000")
	media, err := e.store.DB.MediaObject.Create().SetUserID(u.ID).SetRegionCode(visit.RegionCode).SetTripID("legacy-old-hangzhou").SetObjectKey("region-test/" + u.ID).SetContentType("image/jpeg").SetSizeBytes(3).SetStatus(mediaobject.StatusReady).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := client.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	if err != nil || len(before.Msg.Changes) != 1 || len(before.Msg.Blockers) != 0 {
		t.Fatalf("preview: %v %v", before, err)
	}
	// Normal edits cannot create a duplicate current city before its old ID migrates.
	_, err = footprints.SyncChanges(ctx, regionReq(token, &tripmapv1.SyncChangesRequest{MutationId: "must-wait", Mutations: []*tripmapv1.FootprintMutation{{Operation: &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: &tripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: green}}}}}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	applied, err := client.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: idx.Version(), BaseRevision: before.Msg.Revision, MutationId: "retired-upgrade"}))
	if err != nil || applied.Msg.Revision != 3 || applied.Msg.ChangedCount != 1 {
		t.Fatalf("upgrade: %v %v", applied, err)
	}
	current, _ := e.store.DB.CityVisit.Get(ctx, visit.ID)
	if current.RegionCode != "330100" || current.RegionName != "杭州市" || current.Note != visit.Note || current.Color != visit.Color {
		t.Fatalf("city changed user content: %v", current)
	}
	journey, err := e.store.DB.Trip.Query().Where(trip.UserID(u.ID), trip.TripID("legacy-old-hangzhou")).Only(ctx)
	if err != nil || journey.RegionCode != "330100" || journey.VisitID != visit.ID || journey.Note != visit.Note || journey.TravelDate != visit.TravelDate || !journey.CreatedAt.Equal(visit.UpdatedAt) {
		t.Fatalf("trip: %v %v", journey, err)
	}
	file, _ := e.store.DB.MediaObject.Get(ctx, media.ID)
	if file.RegionCode != "330100" || file.TripID != media.TripID || file.ObjectKey != media.ObjectKey || file.UploadKey != media.UploadKey {
		t.Fatal("media references or blob keys changed")
	}
	old, _ := e.store.DB.Snapshot.Get(ctx, applied.Msg.SnapshotId)
	payload := bytes.Clone(old.Payload)
	detail, err := snapshots.GetSnapshot(ctx, regionReq(token, &tripmapv1.GetSnapshotRequest{Id: old.ID}))
	if err != nil || detail.Msg.Data.Visits[0].Code != "330100" || detail.Msg.Data.Trips[0].Id != "legacy-old-hangzhou" || detail.Msg.Data.Trips[0].Note != visit.Note {
		t.Fatalf("snapshot: %v %v", detail, err)
	}
	row, _ := e.store.DB.Snapshot.Get(ctx, old.ID)
	if !bytes.Equal(row.Payload, payload) {
		t.Fatal("changed historical snapshot bytes")
	}
	log, err := footprints.GetChanges(ctx, regionReq(token, &tripmapv1.GetChangesRequest{}))
	if err != nil || len(log.Msg.Changes) != 3 || log.Msg.Changes[0].Mutation.GetDeleteVisit() != "old-hangzhou" || log.Msg.Changes[2].Mutation.GetUpsertTrip().Id != journey.TripID {
		t.Fatalf("journal: %v %v", log, err)
	}
	preview, err := client.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	if err != nil || len(preview.Msg.Changes) != 0 {
		t.Fatalf("second preview: %v %v", preview, err)
	}
}

func TestRegionUpgradeBlockersAreAtomic(t *testing.T) {
	for _, scenario := range []string{"unknown", "collision", "shared"} {
		t.Run(scenario, func(t *testing.T) {
			e := newEnv(t)
			token, u := regionUser(t, e, "region_blocked")
			idx := retiredIndex(t)
			client, _, _ := serveRegionIndex(t, e, idx)
			ctx := t.Context()
			original := regionCity(t, e, u.ID, "old-hangzhou", "旧杭州", "330000")
			switch scenario {
			case "unknown":
				regionCity(t, e, u.ID, "unmapped-region", "未审核区域", "330000")
			case "collision":
				regionCity(t, e, u.ID, "330100", "杭州市", "330000")
			case "shared":
				if err := e.store.DB.SharedTrip.Create().SetOwnerID(u.ID).SetRegionCode(original.RegionCode).SetTripID("shared-old-trip").Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			preview, err := client.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
			if err != nil || len(preview.Msg.Blockers) == 0 {
				t.Fatalf("blockers: %v %v", preview, err)
			}
			_, err = client.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: idx.Version(), MutationId: "blocked"}))
			wantCode(t, err, connect.CodeFailedPrecondition)
			row, _ := e.store.DB.CityVisit.Get(ctx, original.ID)
			account, _ := e.store.DB.User.Get(ctx, u.ID)
			count, _ := e.store.DB.Snapshot.Query().Count(ctx)
			if row.RegionCode != original.RegionCode || row.Note != original.Note || account.Revision != 0 || account.SchemaVersion != 1 || count != 0 {
				t.Fatal("blocked upgrade partially changed data")
			}
		})
	}
}

func TestRegionUpgradeRevisionVersionNoopAndMutationIsolation(t *testing.T) {
	e := newEnv(t)
	token, u := regionUser(t, e, "region_revisions")
	ctx := t.Context()
	regionCity(t, e, u.ID, "tw-09020", "金门县", "710000")
	preview, _ := e.region.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	_, err := e.region.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: "future", MutationId: "wrong-version"}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if err := e.store.DB.User.UpdateOneID(u.ID).SetRevision(1).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = e.region.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: preview.Msg.Version, BaseRevision: 0, MutationId: "stale-preview"}))
	wantCode(t, err, connect.CodeAborted)
	applied, err := e.region.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: preview.Msg.Version, BaseRevision: 1, MutationId: "final-upgrade"}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.region.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: preview.Msg.Version, BaseRevision: 2, MutationId: "final-upgrade"}))
	wantCode(t, err, connect.CodeAlreadyExists)
	noop, err := e.region.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: preview.Msg.Version, BaseRevision: applied.Msg.Revision, MutationId: "current-noop"}))
	if err != nil || noop.Msg.ChangedCount != 0 || noop.Msg.Revision != applied.Msg.Revision || noop.Msg.SnapshotId != "" {
		t.Fatalf("noop: %v %v", noop, err)
	}
	count, _ := e.store.DB.Snapshot.Query().Where(entsnapshot.UserID(u.ID)).Count(ctx)
	if count != 1 {
		t.Fatal("no-op created another snapshot")
	}
}

func TestRetiredPendingReceiptStillReplaysWithMismatchedVersion(t *testing.T) {
	e := newEnv(t)
	token, u := regionUser(t, e, "retired_receipt")
	ctx := t.Context()
	// Simulate a v2 request committed by an older region index whose response was lost.
	original := &tripmapv1.SyncChangesRequest{MutationId: "receipt-from-old-index", Mutations: []*tripmapv1.FootprintMutation{{Operation: &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: &tripmapv1.Visit{Code: "old-hangzhou", Name: "旧杭州", Province: "330000", Color: green}}}}}
	raw, _ := (proto.MarshalOptions{Deterministic: true}).Marshal(original)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if err := e.store.DB.SyncReceipt.Create().SetUserID(u.ID).SetMutationID(original.MutationId).SetRequestHash(hash).SetRevision(0).SetChangedCount(1).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	req := regionReq(token, original)
	req.Header().Set("X-Tripmap-Regions", "sha256:previous-index")
	replayed, err := e.foot.SyncChanges(ctx, req)
	if err != nil || !replayed.Msg.Replayed || replayed.Msg.ChangedCount != 1 {
		t.Fatalf("receipt replay: %v %v", replayed, err)
	}
	changed := proto.Clone(original).(*tripmapv1.SyncChangesRequest)
	changed.Mutations[0].GetUpsertCity().Color = "#e2af5c"
	_, err = e.foot.SyncChanges(ctx, regionReq(token, changed))
	wantCode(t, err, connect.CodeAlreadyExists)
	rows, _ := e.store.DB.CityVisit.Query().Where(cityvisit.UserID(u.ID)).All(ctx)
	if len(rows) != 0 {
		t.Fatal("receipt replay wrote another city")
	}
}

func TestRegionUpgradeOrphanMediaReferences(t *testing.T) {
	e := newEnv(t)
	token, u := regionUser(t, e, "orphan_region")
	idx := retiredIndex(t)
	client, _, _ := serveRegionIndex(t, e, idx)
	ctx := t.Context()
	media, err := e.store.DB.MediaObject.Create().SetUserID(u.ID).SetRegionCode("old-hangzhou").SetObjectKey("orphan-region/" + u.ID).SetContentType("application/pdf").SetSizeBytes(2).Save(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.PreviewRegionUpgrade(ctx, regionReq(token, &tripmapv1.PreviewRegionUpgradeRequest{}))
	if err != nil || len(preview.Msg.Changes) != 1 || !preview.Msg.Changes[0].MediaOnly {
		t.Fatalf("orphan preview: %v %v", preview, err)
	}
	result, err := client.ApplyRegionUpgrade(ctx, regionReq(token, &tripmapv1.ApplyRegionUpgradeRequest{Version: idx.Version(), MutationId: "orphan-upgrade"}))
	if err != nil || result.Msg.ChangedCount != 1 || result.Msg.Revision != 0 {
		t.Fatalf("orphan upgrade: %v %v", result, err)
	}
	file, _ := e.store.DB.MediaObject.Get(ctx, media.ID)
	if file.RegionCode != "330100" || file.ObjectKey != media.ObjectKey {
		t.Fatal("lost orphan media")
	}
	account, _ := e.store.DB.User.Get(ctx, u.ID)
	if account.SchemaVersion != 1 {
		t.Fatal("media-only upgrade changed footprint schema")
	}
	if !slices.Contains(idx.SupportedVersions(), regions.CurrentVersion()) {
		t.Fatal("fixture must accept snapshots stamped with the bundled version")
	}
}
