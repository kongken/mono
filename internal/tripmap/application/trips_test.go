package application_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/kongken/mono/internal/tripmap/application"
	entcity "github.com/kongken/mono/internal/tripmap/ent/cityvisit"
	entsnap "github.com/kongken/mono/internal/tripmap/ent/snapshot"
	enttrip "github.com/kongken/mono/internal/tripmap/ent/trip"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
)

// v2 marks a request as coming from a client that understands several trips per city.
func v2[T any](token string, msg *T) *connect.Request[T] {
	req := bearer(token, msg)
	req.Header().Set("X-Tripmap-Schema", "2")
	return req
}

func upCity(code, name, province, color string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertCity{UpsertCity: &tripmapv1.Visit{Code: code, Name: name, Province: province, Color: color}}}
}

func upTrip(code, id, date, note string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertTrip{UpsertTrip: &tripmapv1.Trip{Id: id, RegionCode: code, Date: date, Note: note}}}
}

func delTrip(code, id string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteTrip{DeleteTrip: &tripmapv1.DeleteTrip{RegionCode: code, Id: id}}}
}

func (e *env) sync2(token, id string, base int64, ms ...*tripmapv1.FootprintMutation) (*tripmapv1.SyncChangesResponse, error) {
	res, err := e.foot.SyncChanges(context.Background(), v2(token, &tripmapv1.SyncChangesRequest{MutationId: id, BaseRevision: base, Mutations: ms}))
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (e *env) mustSync2(token, id string, base int64, ms ...*tripmapv1.FootprintMutation) int64 {
	e.t.Helper()
	res, err := e.sync2(token, id, base, ms...)
	if err != nil {
		e.t.Fatalf("sync %s: %v", id, err)
	}
	return res.Revision
}

// state2 lists the schema 2 view as sorted "city:code:color" and "trip:id:region:date:note" lines.
func (e *env) state2(token string) ([]string, int64) {
	e.t.Helper()
	res, err := e.foot.GetFootprints(context.Background(), v2(token, &tripmapv1.GetFootprintsRequest{}))
	if err != nil {
		e.t.Fatal(err)
	}
	if res.Msg.SchemaVersion < 2 {
		e.t.Fatalf("schema_version %d", res.Msg.SchemaVersion)
	}
	var out []string
	for _, v := range res.Msg.Visits {
		if v.Date != "" || v.Note != "" {
			e.t.Fatalf("schema 2 city carries date or note: %v", v)
		}
		out = append(out, fmt.Sprintf("city:%s:%s", v.Code, v.Color))
	}
	for _, t := range res.Msg.Trips {
		out = append(out, fmt.Sprintf("trip:%s:%s:%s:%s", t.Id, t.RegionCode, t.Date, t.Note))
	}
	for _, m := range res.Msg.ProvinceMarks {
		out = append(out, "mark:"+m.Province)
	}
	sort.Strings(out)
	return out, res.Msg.Revision
}

func wantLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("state:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func accountSchema(t *testing.T, e *env, username string) int {
	t.Helper()
	return e.store.DB.User.Query().Where(user.Username(username)).OnlyX(context.Background()).SchemaVersion
}

func TestSchema1AccountUpgradesOnFirstSchema2WriteAndStaysIdempotent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("up_a")
	hz := putCity("330100", "杭州市")
	hz.GetUpsertVisit().Note = "西湖"
	nb := putCity("330200", "宁波市")
	nb.GetUpsertVisit().Date = ""
	rev := e.sync(a, "legacy-1", 0, hz, nb, putMark("330000"))

	// Reading as schema 2 synthesizes one trip per city, empty ones included, and writes nothing.
	got, now := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#4b937d", "mark:330000",
		"trip:legacy-330100:330100:2026-10-13:西湖", "trip:legacy-330200:330200::")
	if now != rev || accountSchema(t, e, "up_a") != 1 || e.store.DB.Trip.Query().CountX(ctx) != 0 {
		t.Fatalf("a read changed the account: rev %d schema %d", now, accountSchema(t, e, "up_a"))
	}

	// The first schema 2 write upgrades in place without touching the revision log.
	rev2 := e.mustSync2(a, "v2-1", rev, upTrip("330100", "t-second", "2026-11-01", "再访"))
	if rev2 != rev+1 || accountSchema(t, e, "up_a") != 2 || e.store.DB.Trip.Query().CountX(ctx) != 3 {
		t.Fatalf("after upgrade: rev %d schema %d trips %d", rev2, accountSchema(t, e, "up_a"), e.store.DB.Trip.Query().CountX(ctx))
	}
	got, _ = e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#4b937d", "mark:330000",
		"trip:legacy-330100:330100:2026-10-13:西湖", "trip:legacy-330200:330200::", "trip:t-second:330100:2026-11-01:再访")

	// A repeated upgrade (for instance after a partial failure) keeps edited trips and adds nothing.
	e.mustSync2(a, "v2-2", rev2, upTrip("330100", "legacy-330100", "2026-10-14", "改过"))
	e.store.DB.User.Update().Where(user.Username("up_a")).SetSchemaVersion(1).ExecX(ctx)
	e.mustSync2(a, "v2-3", rev2+1, upTrip("330200", "t-nb", "2026-12-01", ""))
	got, _ = e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#4b937d", "mark:330000",
		"trip:legacy-330100:330100:2026-10-14:改过", "trip:legacy-330200:330200::", "trip:t-second:330100:2026-11-01:再访", "trip:t-nb:330200:2026-12-01:")
	// The frozen schema 1 columns were never written after the upgrade.
	row := e.store.DB.CityVisit.Query().Where(entcity.RegionCode("330100")).OnlyX(ctx)
	if row.TravelDate != "2026-10-13" || row.Note != "西湖" {
		t.Fatalf("frozen columns changed: %+v", row)
	}
}

func TestOldClientsAreRefusedOnceTheAccountHasTrips(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("gate_a")
	rev := e.sync(a, "g1", 0, putCity("330100", "杭州市"))
	e.snapshot(a, "manual")
	rev = e.mustSync2(a, "g2", rev, upTrip("330100", "t-2", "2026-11-01", "再访"))

	refused := func(name string, err error) {
		t.Helper()
		wantCode(t, err, connect.CodeFailedPrecondition)
		if !strings.Contains(err.Error(), "client upgrade required") {
			t.Fatalf("%s: unexpected message %v", name, err)
		}
	}
	_, err := e.foot.GetFootprints(ctx, bearer(a, &tripmapv1.GetFootprintsRequest{}))
	refused("get", err)
	_, err = e.foot.GetChanges(ctx, bearer(a, &tripmapv1.GetChangesRequest{}))
	refused("changes", err)
	_, err = e.foot.SyncChanges(ctx, bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "old-1", BaseRevision: rev, Mutations: []*tripmapv1.FootprintMutation{putCity("330100", "杭州市")}}))
	refused("sync", err)
	_, err = upsert(e, a, hangzhou(), rev)
	refused("upsert", err)
	_, err = e.foot.DeleteVisit(ctx, bearer(a, &tripmapv1.DeleteVisitRequest{Code: "330100"}))
	refused("delete", err)
	_, err = e.foot.UpsertProvinceMark(ctx, bearer(a, &tripmapv1.UpsertProvinceMarkRequest{Mark: &tripmapv1.ProvinceMark{Province: "330000", Color: "#4b937d"}}))
	refused("mark", err)
	_, err = e.snap.ListSnapshots(ctx, bearer(a, &tripmapv1.ListSnapshotsRequest{}))
	refused("list snapshots", err)
	list, err := e.snap.ListSnapshots(ctx, v2(a, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil || len(list.Msg.Snapshots) != 1 {
		t.Fatalf("v2 list %v %v", list, err)
	}
	_, err = e.snap.GetSnapshot(ctx, bearer(a, &tripmapv1.GetSnapshotRequest{Id: list.Msg.Snapshots[0].Id}))
	refused("get snapshot", err)
	_, err = e.snap.CreateSnapshot(ctx, bearer(a, &tripmapv1.CreateSnapshotRequest{Reason: "manual"}))
	refused("create snapshot", err)
	_, err = e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: list.Msg.Snapshots[0].Id, BaseRevision: rev, MutationId: "old-restore"}))
	refused("restore snapshot", err)

	// Nothing the old clients attempted changed the account.
	got, now := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "trip:legacy-330100:330100:2026-10-13:", "trip:t-2:330100:2026-11-01:再访")
	if now != rev {
		t.Fatalf("revision moved from %d to %d", rev, now)
	}
	// Another account is unaffected.
	b := e.register("gate_b")
	e.sync(b, "b1", 0, putCity("330100", "杭州市"))
}

func TestTripLifecycleCascadeAndValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("trip_a")
	rev := e.mustSync2(a, "t1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-a", "2026-10-01", "第一次"), upTrip("330100", "t-b", "2026-10-05", "第二次"))
	if rev != 3 {
		t.Fatalf("revision %d", rev)
	}
	got, _ := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "trip:t-a:330100:2026-10-01:第一次", "trip:t-b:330100:2026-10-05:第二次")

	// The same trip again changes nothing; an edit bumps one revision.
	res, err := e.sync2(a, "t2", rev, upTrip("330100", "t-a", "2026-10-01", "第一次"))
	if err != nil || res.ChangedCount != 0 || res.Revision != rev {
		t.Fatalf("no-op %v %v", res, err)
	}
	rev = e.mustSync2(a, "t3", rev, upTrip("330100", "t-b", "", "没写日期"))
	// Deleting the last trip keeps the city.
	rev = e.mustSync2(a, "t4", rev, delTrip("330100", "t-a"), delTrip("330100", "t-b"))
	got, _ = e.state2(a)
	wantLines(t, got, "city:330100:#4b937d")
	// Deleting an already deleted trip is a no-op.
	res, err = e.sync2(a, "t5", rev, delTrip("330100", "t-a"))
	if err != nil || res.ChangedCount != 0 {
		t.Fatalf("repeat delete %v %v", res, err)
	}
	// Deleting the city removes its trips with it.
	rev = e.mustSync2(a, "t6", rev, upTrip("330100", "t-c", "2026-10-09", "x"))
	e.mustSync2(a, "t7", rev, delCity("330100"))
	if n := e.store.DB.Trip.Query().CountX(ctx); n != 0 {
		t.Fatalf("%d trips outlived their city", n)
	}

	// Rules the server enforces for every client.
	rev = e.mustSync2(a, "t8", rev+1, upCity("330100", "杭州市", "330000", "#4b937d"), upCity("330200", "宁波市", "330000", "#e2af5c"), upTrip("330100", "t-d", "2026-10-01", ""))
	cases := []struct {
		name string
		m    *tripmapv1.FootprintMutation
		code connect.Code
	}{
		{"trip without city", upTrip("330300", "t-x", "2026-10-01", ""), connect.CodeFailedPrecondition},
		{"trip moved to another city", upTrip("330200", "t-d", "2026-10-01", ""), connect.CodeInvalidArgument},
		{"impossible date", upTrip("330100", "t-e", "2026-02-30", ""), connect.CodeInvalidArgument},
		{"malformed date", upTrip("330100", "t-e", "2026-2-3", ""), connect.CodeInvalidArgument},
		{"note too long", upTrip("330100", "t-e", "", strings.Repeat("a", 1001)), connect.CodeInvalidArgument},
		{"astral note counts UTF-16 units", upTrip("330100", "t-e", "", strings.Repeat("😀", 501)), connect.CodeInvalidArgument},
		{"slash in id", upTrip("330100", "a/b", "", ""), connect.CodeInvalidArgument},
		{"empty id", upTrip("330100", "", "", ""), connect.CodeInvalidArgument},
		{"long id", upTrip("330100", strings.Repeat("a", 65), "", ""), connect.CodeInvalidArgument},
		{"unknown region", upTrip("999999", "t-e", "", ""), connect.CodeInvalidArgument},
		{"city with a date", func() *tripmapv1.FootprintMutation {
			m := upCity("330100", "杭州市", "330000", "#4b937d")
			m.GetUpsertCity().Date = "2026-10-01"
			return m
		}(), connect.CodeInvalidArgument},
		{"bad color", upCity("330100", "杭州市", "330000", "#000000"), connect.CodeInvalidArgument},
		{"nil trip", &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertTrip{}}, connect.CodeInvalidArgument},
	}
	for _, c := range cases {
		_, err := e.sync2(a, "bad-"+strings.ReplaceAll(c.name, " ", "-"), rev, c.m)
		if codeOf(err) != c.code {
			t.Errorf("%s: %v, want %v", c.name, err, c.code)
		}
	}
	// A note of exactly 1000 UTF-16 units and a real leap day are fine.
	rev = e.mustSync2(a, "ok-edge", rev, upTrip("330100", "t-edge", "2024-02-29", strings.Repeat("😀", 500)))
	if _, now := e.state2(a); now != rev {
		t.Fatalf("revision %d want %d", now, rev)
	}
}

func TestTripsAreIsolatedBetweenUsers(t *testing.T) {
	e := newEnv(t)
	a, b := e.register("iso_a"), e.register("iso_b")
	e.mustSync2(a, "a1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "shared-id", "2026-10-01", "A 的"))
	// B has no city yet, so cannot attach a trip, even with A's trip ID.
	_, err := e.sync2(b, "b0", 0, upTrip("330100", "shared-id", "", "B"))
	wantCode(t, err, connect.CodeFailedPrecondition)
	rbase := e.mustSync2(b, "b1", 0, upCity("330100", "杭州市", "330000", "#e2af5c"), upTrip("330100", "shared-id", "2026-12-01", "B 的"))
	// B deleting by A's ID only touches B's own record.
	e.mustSync2(b, "b2", rbase, delTrip("330100", "shared-id"))
	got, _ := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "trip:shared-id:330100:2026-10-01:A 的")
	got, _ = e.state2(b)
	wantLines(t, got, "city:330100:#e2af5c")
}

func TestChangeLogCarriesCitiesTripsAndLegacyUpserts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("log_a")
	rev := e.sync(a, "l1", 0, putCity("330100", "杭州市"))
	rev = e.mustSync2(a, "l2", rev, upTrip("330100", "t-1", "2026-11-01", "再访"), upCity("330200", "宁波市", "330000", "#e2af5c"), delTrip("330100", "t-1"))
	// A legacy batch from a schema 2 client lands as city plus its legacy trip.
	nb := putCity("330200", "宁波市")
	nb.GetUpsertVisit().Date, nb.GetUpsertVisit().Note = "2026-09-09", "老批次"
	e.mustSync2(a, "l3", rev, nb)

	res, err := e.foot.GetChanges(ctx, v2(a, &tripmapv1.GetChangesRequest{}))
	if err != nil || res.Msg.SchemaVersion < 2 || res.Msg.ResyncRequired {
		t.Fatalf("changes %v %v", res, err)
	}
	var kinds []string
	for _, c := range res.Msg.Changes {
		switch op := c.Mutation.Operation.(type) {
		case *tripmapv1.FootprintMutation_UpsertVisit:
			kinds = append(kinds, "visit:"+op.UpsertVisit.Code+":"+op.UpsertVisit.Date+":"+op.UpsertVisit.Note)
		case *tripmapv1.FootprintMutation_UpsertCity:
			kinds = append(kinds, "city:"+op.UpsertCity.Code)
		case *tripmapv1.FootprintMutation_UpsertTrip:
			kinds = append(kinds, "trip:"+op.UpsertTrip.Id+":"+op.UpsertTrip.Note)
		case *tripmapv1.FootprintMutation_DeleteTrip:
			kinds = append(kinds, "deltrip:"+op.DeleteTrip.Id)
		}
	}
	if want := "visit:330100:2026-10-13: trip:t-1:再访 city:330200 deltrip:t-1 visit:330200:2026-09-09:老批次"; strings.Join(kinds, " ") != want {
		t.Fatalf("change log %q, want %q", strings.Join(kinds, " "), want)
	}
	got, _ := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#4b937d", "trip:legacy-330100:330100:2026-10-13:", "trip:legacy-330200:330200:2026-09-09:老批次")
}

func TestLegacyBatchReplayAfterUpgradeReusesItsReceipt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("replay_a")
	hz := putCity("330100", "杭州市")
	hz.GetUpsertVisit().Note = "旧版"
	batch := &tripmapv1.SyncChangesRequest{MutationId: "pending-v1", BaseRevision: 0, Mutations: []*tripmapv1.FootprintMutation{hz}}
	first, err := e.foot.SyncChanges(ctx, bearer(a, batch))
	if err != nil || first.Msg.Replayed {
		t.Fatal(first, err)
	}
	// The response was lost; the new client upgrades the account with another change and then retries the old request.
	e.mustSync2(a, "v2-after", first.Msg.Revision, upTrip("330100", "t-2", "2026-11-01", "新版"))
	replay, err := e.foot.SyncChanges(ctx, v2(a, batch))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Revision != first.Msg.Revision {
		t.Fatalf("replay %v %v", replay, err)
	}
	// A request that was never applied is applied once, as a city and its legacy trip.
	other := &tripmapv1.SyncChangesRequest{MutationId: "pending-v1-b", BaseRevision: first.Msg.Revision + 1, Mutations: []*tripmapv1.FootprintMutation{putCity("330200", "宁波市")}}
	if _, err = e.foot.SyncChanges(ctx, v2(a, other)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#4b937d", "trip:legacy-330100:330100:2026-10-13:旧版", "trip:t-2:330100:2026-11-01:新版", "trip:legacy-330200:330200:2026-10-13:")
}

func TestBatchesAreAtomicIdempotentAndBounded(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("batch_a")
	// One bad operation rolls back the whole batch, including the account upgrade.
	_, err := e.sync2(a, "bad", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-1", "", ""), upTrip("330300", "t-2", "", ""))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if e.store.DB.CityVisit.Query().CountX(ctx) != 0 || e.store.DB.Trip.Query().CountX(ctx) != 0 || accountSchema(t, e, "batch_a") != 1 {
		t.Fatal("a failed batch left writes behind")
	}
	// A stale base revision aborts without writes.
	e.mustSync2(a, "first", 0, upCity("330100", "杭州市", "330000", "#4b937d"))
	_, err = e.sync2(a, "stale", 0, upTrip("330100", "t-1", "", ""))
	wantCode(t, err, connect.CodeAborted)
	got, rev := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d")

	// A batch of the maximum size: 400 cities with 4 trips each (one city already exists).
	var big []*tripmapv1.FootprintMutation
	for _, c := range cityCodes(t) {
		big = append(big, upCity(c.code, c.name, c.province, "#4b937d"))
		for n := 0; n < 4 && len(big) < 2000; n++ {
			big = append(big, upTrip(c.code, fmt.Sprintf("big-%s-%d", c.code, n), "2026-10-01", "批量"))
		}
		if len(big) >= 2000 {
			break
		}
	}
	if len(big) != 2000 {
		t.Fatalf("test fixture has %d operations", len(big))
	}
	res, err := e.sync2(a, "big-1", rev, big...)
	if err != nil || res.ChangedCount < 1990 || res.Revision != rev+int64(res.ChangedCount) {
		t.Fatalf("2000-operation batch: %v %v", res, err)
	}
	again, err := e.sync2(a, "big-1", rev, big...)
	if err != nil || !again.Replayed || again.Revision != res.Revision {
		t.Fatalf("replay %v %v", again, err)
	}
	_, err = e.sync2(a, "big-2", res.Revision, append(big, upCity("330100", "杭州市", "330000", "#4b937d"))...)
	wantCode(t, err, connect.CodeInvalidArgument)
}

type cityRef struct{ code, name, province string }

func cityCodes(t *testing.T) []cityRef {
	t.Helper()
	var out []cityRef
	for _, m := range everyFootprint(t) {
		if v := m.GetUpsertVisit(); v != nil {
			out = append(out, cityRef{v.Code, v.Name, v.Province})
		}
	}
	return out
}

func TestBatchRateLimitChargesPerOperation(t *testing.T) {
	e := newEnv(t)
	a := e.register("rate_a")
	op := upCity("330100", "杭州市", "330000", "#4b937d")
	var batch []*tripmapv1.FootprintMutation
	for range 1500 {
		batch = append(batch, op)
	}
	rev := int64(0)
	for i := 0; i < 4; i++ {
		res, err := e.sync2(a, fmt.Sprintf("rate-%d", i), rev, batch...)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		rev = res.Revision
	}
	_, err := e.sync2(a, "rate-4", rev, batch...)
	wantCode(t, err, connect.CodeResourceExhausted)
	// A replay of a finished batch is answered from its receipt and costs nothing.
	if res, err := e.sync2(a, "rate-0", 0, batch...); err != nil || !res.Replayed {
		t.Fatalf("replay under the limit: %v %v", res, err)
	}
	// Another account has its own budget.
	b := e.register("rate_b")
	if _, err = e.sync2(b, "rate-b", 0, batch...); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentOldAndNewClientsNeverLoseData(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("race_%d", i)
		a := e.register(name)
		hz := putCity("330100", "杭州市")
		hz.GetUpsertVisit().Note = "原始"
		rev := e.sync(a, "r0", 0, hz)
		oldWrite := putCity("330100", "杭州市")
		oldWrite.GetUpsertVisit().Note = "旧版改的"
		var wg sync.WaitGroup
		var oldErr, newErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, oldErr = e.foot.SyncChanges(context.Background(), bearer(a, &tripmapv1.SyncChangesRequest{MutationId: "old", BaseRevision: rev, Mutations: []*tripmapv1.FootprintMutation{oldWrite}}))
		}()
		go func() {
			defer wg.Done()
			_, newErr = e.sync2(a, "new", rev, upTrip("330100", "t-new", "2026-11-01", "新版加的"))
		}()
		wg.Wait()
		// The writes share a base revision, so at most one of them is accepted.
		if oldErr != nil && newErr != nil {
			t.Fatalf("both writes failed: %v / %v", oldErr, newErr)
		}
		got, _ := e.state2(a)
		legacy := "trip:legacy-330100:330100:2026-10-13:原始"
		if oldErr == nil {
			legacy = "trip:legacy-330100:330100:2026-10-13:旧版改的"
		}
		if newErr == nil {
			wantLines(t, got, "city:330100:#4b937d", legacy, "trip:t-new:330100:2026-11-01:新版加的")
		} else {
			wantLines(t, got, "city:330100:#4b937d", legacy)
		}
	}
}

func TestOldSnapshotsConvertOnReadAndRestoreWithoutBeingRewritten(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("snap2_a")
	hz := putCity("330100", "杭州市")
	hz.GetUpsertVisit().Note = "西湖"
	nb := putCity("330200", "宁波市")
	nb.GetUpsertVisit().Date = ""
	rev := e.sync(a, "s1", 0, hz, nb, putMark("330000"))
	old := e.snapshot(a, "manual").Snapshot
	if old.SchemaVersion != 1 || old.TripCount != 2 {
		t.Fatalf("schema 1 snapshot info %v", old)
	}
	before := e.store.DB.Snapshot.GetX(ctx, old.Id).Payload

	rev = e.mustSync2(a, "s2", rev, upTrip("330100", "t-2", "2026-11-01", "再访"), upTrip("330100", "legacy-330100", "2026-10-13", "改了"), delCity("330200"))
	newSnap, err := e.snap.CreateSnapshot(ctx, v2(a, &tripmapv1.CreateSnapshotRequest{Reason: "manual"}))
	if err != nil {
		t.Fatal(err)
	}
	if newSnap.Msg.Snapshot.SchemaVersion != 2 || newSnap.Msg.Snapshot.TripCount != 2 || newSnap.Msg.Snapshot.VisitCount != 1 {
		t.Fatalf("schema 2 snapshot info %v", newSnap.Msg.Snapshot)
	}

	// The old snapshot reads as cities plus one legacy trip each, empty ones included.
	got, err := e.snap.GetSnapshot(ctx, v2(a, &tripmapv1.GetSnapshotRequest{Id: old.Id}))
	if err != nil || got.Msg.Data.SchemaVersion != 2 || len(got.Msg.Data.Visits) != 2 || len(got.Msg.Data.Trips) != 2 {
		t.Fatalf("converted snapshot %v %v", got, err)
	}
	for _, v := range got.Msg.Data.Visits {
		if v.Date != "" || v.Note != "" {
			t.Fatalf("converted city keeps date or note: %v", v)
		}
	}
	list, err := e.snap.ListSnapshots(ctx, v2(a, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list.Msg.Snapshots {
		if s.Id == old.Id && (s.TripCount != 2 || s.VisitCount != 2) {
			t.Fatalf("old snapshot counts %v", s)
		}
	}

	// Restoring it brings back both cities with their single legacy trips and removes the later trip.
	res, err := e.snap.RestoreSnapshot(ctx, v2(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: old.Id, BaseRevision: rev, MutationId: "restore-old"}))
	if err != nil {
		t.Fatal(err)
	}
	state, _ := e.state2(a)
	wantLines(t, state, "city:330100:#4b937d", "city:330200:#4b937d", "mark:330000", "trip:legacy-330100:330100:2026-10-13:西湖", "trip:legacy-330200:330200::")
	if res.Msg.UndoSnapshot == nil || res.Msg.UndoSnapshot.SchemaVersion != 2 {
		t.Fatalf("undo %v", res.Msg.UndoSnapshot)
	}
	// Undoing the restore brings the multi-trip state back.
	_, err = e.snap.RestoreSnapshot(ctx, v2(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: res.Msg.UndoSnapshot.Id, BaseRevision: res.Msg.Revision, MutationId: "restore-undo"}))
	if err != nil {
		t.Fatal(err)
	}
	state, _ = e.state2(a)
	wantLines(t, state, "city:330100:#4b937d", "mark:330000", "trip:legacy-330100:330100:2026-10-13:改了", "trip:t-2:330100:2026-11-01:再访")
	// The stored old snapshot was never rewritten.
	if after := e.store.DB.Snapshot.GetX(ctx, old.Id).Payload; !bytes.Equal(before, after) {
		t.Fatal("the schema 1 snapshot payload changed")
	}
	row := e.store.DB.Snapshot.GetX(ctx, old.Id)
	if row.SchemaVersion != 1 {
		t.Fatalf("schema_version rewritten to %d", row.SchemaVersion)
	}
	// Restoring the schema 2 snapshot (taken after the edits) is also accepted.
	back, err := e.snap.RestoreSnapshot(ctx, v2(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: newSnap.Msg.Snapshot.Id, BaseRevision: mustRev(t, e, a), MutationId: "restore-new"}))
	if err != nil || back.Msg.Replayed {
		t.Fatal(back, err)
	}
}

func mustRev(t *testing.T, e *env, token string) int64 {
	t.Helper()
	_, rev := e.state2(token)
	return rev
}

func TestSnapshotRestoreMovesTripsBetweenCities(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("snap2_move")
	rev := e.mustSync2(a, "m1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upCity("330200", "宁波市", "330000", "#e2af5c"), upTrip("330100", "same-id", "2026-10-01", "杭州"))
	snap, err := e.snap.CreateSnapshot(ctx, v2(a, &tripmapv1.CreateSnapshotRequest{Reason: "manual"}))
	if err != nil {
		t.Fatal(err)
	}
	// The ID is reused in another city after the first one lost it.
	rev = e.mustSync2(a, "m2", rev, delTrip("330100", "same-id"), upTrip("330200", "same-id", "2026-12-01", "宁波"))
	if _, err = e.snap.RestoreSnapshot(ctx, v2(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Msg.Snapshot.Id, BaseRevision: rev, MutationId: "m-restore"})); err != nil {
		t.Fatal(err)
	}
	got, _ := e.state2(a)
	wantLines(t, got, "city:330100:#4b937d", "city:330200:#e2af5c", "trip:same-id:330100:2026-10-01:杭州")
}

func TestScheduledSnapshotsUseTheAccountSchema(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("sched2_a")
	e.mustSync2(a, "x1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-1", "", ""), upTrip("330100", "t-2", "", ""))
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	row := e.store.DB.Snapshot.Query().Where(entsnap.ReasonEQ(entsnap.ReasonScheduled)).OnlyX(ctx)
	if row.SchemaVersion != 2 || row.TripCount != 2 || row.VisitCount != 1 {
		t.Fatalf("scheduled snapshot %+v", row)
	}
}

func TestMediaTripLinksAreValidatedAndSurviveTripAndCityDeletion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("mtrip_a"), e.register("mtrip_b")
	e.store.Blobs = &mediaMemory{files: map[string][]byte{}}
	data := photo(t)
	prepare := func(token, region, trip string) (*tripmapv1.PrepareUploadResponse, error) {
		r := preparePhoto(data)
		r.RegionCode, r.TripId = region, trip
		res, err := e.media.PrepareUpload(ctx, bearer(token, r))
		if err != nil {
			return nil, err
		}
		return res.Msg, nil
	}
	rev := e.mustSync2(a, "p1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upCity("330200", "宁波市", "330000", "#4b937d"), upTrip("330100", "t-hz", "2026-10-01", ""))
	e.mustSync2(b, "p2", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-b", "2026-10-01", ""))

	for name, c := range map[string]struct{ token, region, trip string }{
		"unknown trip":         {a, "330100", "nope"},
		"another city's trip":  {a, "330200", "t-hz"},
		"another user's trip":  {a, "330100", "t-b"},
		"user b with a's trip": {b, "330100", "t-hz"},
	} {
		if _, err := prepare(c.token, c.region, c.trip); codeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := prepare(a, "330100", "bad/id"); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("malformed trip id: %v", err)
	}
	linked, err := prepare(a, "330100", "t-hz")
	if err != nil || linked.Media.TripId != "t-hz" {
		t.Fatalf("linked upload %v %v", linked, err)
	}
	plain, err := prepare(a, "330100", "")
	if err != nil || plain.Media.TripId != "" {
		t.Fatalf("city upload %v %v", plain, err)
	}
	// The same upload ID cannot change its trip link on replay.
	r := preparePhoto(data)
	r.UploadId = uuid.NewString()
	r.TripId = "t-hz"
	if _, err = e.media.PrepareUpload(ctx, bearer(a, r)); err != nil {
		t.Fatal(err)
	}
	r.TripId = ""
	_, err = e.media.PrepareUpload(ctx, bearer(a, r))
	wantCode(t, err, connect.CodeAlreadyExists)

	list := func(token string, region, trip string) int {
		res, err := e.media.ListMedia(ctx, bearer(token, &tripmapv1.ListMediaRequest{RegionCode: region, TripId: trip}))
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Msg.Media)
	}
	if list(a, "330100", "") != 3 || list(a, "330100", "t-hz") != 2 || list(b, "330100", "t-hz") != 0 {
		t.Fatalf("list counts %d %d %d", list(a, "330100", ""), list(a, "330100", "t-hz"), list(b, "330100", "t-hz"))
	}
	// Deleting the trip, then the city, never removes the files; they stay under the city.
	rev = e.mustSync2(a, "p3", rev, delTrip("330100", "t-hz"))
	if list(a, "330100", "") != 3 {
		t.Fatal("deleting a trip removed media")
	}
	e.mustSync2(a, "p4", rev, delCity("330100"))
	if list(a, "330100", "") != 3 || list(a, "330100", "t-hz") != 2 {
		t.Fatal("cancelling the city removed media")
	}
	// A deleted trip can no longer receive uploads.
	if _, err = prepare(a, "330100", "t-hz"); codeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("upload to deleted trip: %v", err)
	}
	// Each file stays deletable on its own.
	res, err := e.media.ListMedia(ctx, bearer(a, &tripmapv1.ListMediaRequest{RegionCode: "330100", TripId: "t-hz"}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.media.DeleteMedia(ctx, bearer(a, &tripmapv1.DeleteMediaRequest{Id: res.Msg.Media[0].Id})); err != nil {
		t.Fatal(err)
	}
	if list(a, "330100", "") != 2 {
		t.Fatal("single delete failed")
	}
}

func TestMediaAcceptsTheLegacyTripOfAnUnupgradedAccount(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("mlegacy_a")
	e.store.Blobs = &mediaMemory{files: map[string][]byte{}}
	e.sync(a, "ml1", 0, putCity("330100", "杭州市"))
	r := preparePhoto(photo(t))
	r.TripId = "legacy-330100"
	if _, err := e.media.PrepareUpload(ctx, bearer(a, r)); err != nil {
		t.Fatalf("legacy trip link: %v", err)
	}
	r = preparePhoto(photo(t))
	r.TripId = "legacy-330200"
	_, err := e.media.PrepareUpload(ctx, bearer(a, r))
	wantCode(t, err, connect.CodeFailedPrecondition)
}

func TestAccountDeletionRemovesTripsOfThatAccountOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("tripdel_a"), e.register("tripdel_b")
	e.mustSync2(a, "d1", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-1", "", ""), upTrip("330100", "t-2", "", ""))
	e.mustSync2(b, "d2", 0, upCity("330100", "杭州市", "330000", "#4b937d"), upTrip("330100", "t-1", "", ""))
	if _, err := e.auth.DeleteAccount(ctx, bearer(a, &tripmapv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "correct horse battery"})); err != nil {
		t.Fatal(err)
	}
	if n := e.store.DB.Trip.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d trips remain, want only the other account's", n)
	}
	got, _ := e.state2(b)
	wantLines(t, got, "city:330100:#4b937d", "trip:t-1:330100::")
}

func TestUpgradeBackfillIsExactForTheStoredLegacyColumns(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("exact_a")
	hz := putCity("330100", "杭州市")
	hz.GetUpsertVisit().Note = strings.Repeat("😀", 500)
	e.sync(a, "x1", 0, hz)
	e.mustSync2(a, "x2", 1, upCity("330200", "宁波市", "330000", "#e2af5c"))
	tr := e.store.DB.Trip.Query().Where(enttrip.TripID("legacy-330100")).OnlyX(ctx)
	city := e.store.DB.CityVisit.Query().Where(entcity.RegionCode("330100")).OnlyX(ctx)
	if tr.Note != city.Note || tr.TravelDate != city.TravelDate || tr.VisitID != city.ID || tr.RegionCode != "330100" {
		t.Fatalf("backfilled trip %+v from %+v", tr, city)
	}
}

// The frontend (apps/web/src/lib/trips.ts isRealDate) is checked against the same file in
// apps/web/scripts/trips.test.mjs: both sides must accept and reject exactly the same dates.
func TestTripDatesMatchTheSharedFrontendFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../../packages/fixtures/trip-dates.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Valid   []string `json:"valid"`
		Invalid []string `json:"invalid"`
	}
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	a := e.register("date_fixture")
	rev := e.mustSync2(a, "d0", 0, upCity("330100", "杭州市", "330000", "#4b937d"))
	for i, date := range fixtures.Valid {
		res, err := e.sync2(a, fmt.Sprintf("d-ok-%d", i), rev, upTrip("330100", fmt.Sprintf("t-ok-%d", i), date, ""))
		if err != nil {
			t.Errorf("valid date %q was rejected: %v", date, err)
			continue
		}
		rev = res.Revision
	}
	for i, date := range fixtures.Invalid {
		_, err := e.sync2(a, fmt.Sprintf("d-bad-%d", i), rev, upTrip("330100", fmt.Sprintf("t-bad-%d", i), date, ""))
		if codeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("invalid date %q gave %v, want invalid_argument", date, err)
		}
	}
}
