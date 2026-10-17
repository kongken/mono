package application_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/kongken/mono/internal/tripmap/application"
	"github.com/kongken/mono/internal/tripmap/ent"
	entsnap "github.com/kongken/mono/internal/tripmap/ent/snapshot"
	"github.com/kongken/mono/internal/tripmap/ent/user"
	tripmapv1 "github.com/kongken/mono/pkg/proto/tripmap/v1"
	"google.golang.org/protobuf/proto"
)

func putMark(province string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertMark{UpsertMark: &tripmapv1.ProvinceMark{Province: province, Color: "#4b937d"}}}
}

func delCity(code string) *tripmapv1.FootprintMutation {
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: code}}
}

func recolor(m *tripmapv1.FootprintMutation, color, note string) *tripmapv1.FootprintMutation {
	v := proto.Clone(m.GetUpsertVisit()).(*tripmapv1.Visit)
	v.Color, v.Note = color, note
	return &tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_UpsertVisit{UpsertVisit: v}}
}

// sync applies a batch from base revision and returns the new revision.
func (e *env) sync(token, id string, base int64, ms ...*tripmapv1.FootprintMutation) int64 {
	e.t.Helper()
	res, err := e.foot.SyncChanges(context.Background(), bearer(token, &tripmapv1.SyncChangesRequest{MutationId: id, BaseRevision: base, Mutations: ms}))
	if err != nil {
		e.t.Fatalf("sync %s: %v", id, err)
	}
	return res.Msg.Revision
}

// state returns the user's records keyed like "v:code" / "m:province".
func (e *env) state(token string) (map[string]string, int64) {
	e.t.Helper()
	res, err := e.foot.GetFootprints(context.Background(), bearer(token, &tripmapv1.GetFootprintsRequest{}))
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]string{}
	for _, v := range res.Msg.Visits {
		out["v:"+v.Code] = fmt.Sprintf("%s|%s|%s|%s", v.Name, v.Color, v.Date, v.Note)
	}
	for _, m := range res.Msg.ProvinceMarks {
		out["m:"+m.Province] = m.Color
	}
	return out, res.Msg.Revision
}

func (e *env) snapshot(token string, reason string) *tripmapv1.CreateSnapshotResponse {
	e.t.Helper()
	res, err := e.snap.CreateSnapshot(context.Background(), bearer(token, &tripmapv1.CreateSnapshotRequest{Reason: reason}))
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Msg
}

func sameState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestSnapshotCreateListGetAndDedup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("snap_a")
	rev := e.sync(a, "s1", 0, putCity("330100", "杭州市"), putCity("330200", "宁波市"), putMark("330000"))

	first := e.snapshot(a, "manual")
	if !first.Created || first.Snapshot.VisitCount != 2 || first.Snapshot.ProvinceMarkCount != 1 || first.Snapshot.Revision != rev ||
		first.Snapshot.Reason != "manual" {
		t.Fatalf("snapshot %v", first)
	}
	// Same revision, same content: the existing snapshot is returned.
	again := e.snapshot(a, "before_migration")
	if again.Created || again.Snapshot.Id != first.Snapshot.Id {
		t.Fatalf("expected the existing snapshot, got %v", again)
	}
	e.sync(a, "s2", rev, putCity("330300", "温州市"))
	second := e.snapshot(a, "before_migration")
	if !second.Created || second.Snapshot.VisitCount != 3 {
		t.Fatalf("second %v", second)
	}

	list, err := e.snap.ListSnapshots(ctx, bearer(a, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil || len(list.Msg.Snapshots) != 2 || list.Msg.Snapshots[0].Id != second.Snapshot.Id {
		t.Fatalf("list %v %v", list, err)
	}
	got, err := e.snap.GetSnapshot(ctx, bearer(a, &tripmapv1.GetSnapshotRequest{Id: first.Snapshot.Id}))
	if err != nil || len(got.Msg.Data.Visits) != 2 || len(got.Msg.Data.ProvinceMarks) != 1 || got.Msg.Data.Visits[0].Code != "330100" {
		t.Fatalf("get %v %v", got, err)
	}

	for _, reason := range []string{"", "scheduled", "before_restore"} {
		_, err = e.snap.CreateSnapshot(ctx, bearer(a, &tripmapv1.CreateSnapshotRequest{Reason: reason}))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	_, err = e.snap.CreateSnapshot(ctx, connect.NewRequest(&tripmapv1.CreateSnapshotRequest{Reason: "manual"}))
	wantCode(t, err, connect.CodeUnauthenticated)
}

func TestSnapshotsAreIsolatedBetweenUsers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("iso_a"), e.register("iso_b")
	e.sync(a, "a1", 0, putCity("330100", "杭州市"))
	snap := e.snapshot(a, "manual")
	e.sync(b, "b1", 0, putCity("330200", "宁波市"))

	_, err := e.snap.GetSnapshot(ctx, bearer(b, &tripmapv1.GetSnapshotRequest{Id: snap.Snapshot.Id}))
	wantCode(t, err, connect.CodeNotFound)
	_, err = e.snap.RestoreSnapshot(ctx, bearer(b, &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Snapshot.Id, BaseRevision: 1, MutationId: "steal"}))
	wantCode(t, err, connect.CodeNotFound)
	list, err := e.snap.ListSnapshots(ctx, bearer(b, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil || len(list.Msg.Snapshots) != 0 {
		t.Fatalf("user B sees snapshots: %v %v", list, err)
	}
	if st, _ := e.state(b); len(st) != 1 || st["v:330200"] == "" {
		t.Fatalf("user B changed: %v", st)
	}
}

func TestRestoreReplacesStateSavesUndoAndIsUndoable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("restore_a")
	hz, nb := putCity("330100", "杭州市"), putCity("330200", "宁波市")
	rev := e.sync(a, "r1", 0, hz, nb, putMark("330000"), putMark("310000"))
	original, _ := e.state(a)
	snap := e.snapshot(a, "manual")

	// A bad import: remove one city, rewrite another, add a third, drop one mark.
	rev = e.sync(a, "r2", rev, delCity("330100"), recolor(nb, "#e2af5c", "改了"), putCity("330300", "温州市"),
		&tripmapv1.FootprintMutation{Operation: &tripmapv1.FootprintMutation_DeleteMark{DeleteMark: "310000"}})
	damaged, _ := e.state(a)
	if sameState(original, damaged) {
		t.Fatal("setup did not change anything")
	}

	res, err := e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Snapshot.Id, BaseRevision: rev, MutationId: "restore-1"}))
	if err != nil {
		t.Fatal(err)
	}
	// Re-add one city, revert one, remove one, re-add one mark.
	if res.Msg.ChangedCount != 4 || res.Msg.Revision != rev+4 || res.Msg.Replayed {
		t.Fatalf("restore %v", res.Msg)
	}
	restored, now := e.state(a)
	if !sameState(restored, original) || now != res.Msg.Revision {
		t.Fatalf("restored %v (rev %d), want %v", restored, now, original)
	}
	undo := res.Msg.UndoSnapshot
	if undo == nil || undo.Reason != "before_restore" || undo.Revision != rev || undo.VisitCount != 2 {
		t.Fatalf("undo snapshot %v", undo)
	}

	// Other devices follow the restore through the ordinary change feed.
	changes, err := e.foot.GetChanges(ctx, bearer(a, &tripmapv1.GetChangesRequest{SinceRevision: rev}))
	if err != nil || len(changes.Msg.Changes) != 4 || changes.Msg.ResyncRequired || changes.Msg.CurrentRevision != now {
		t.Fatalf("changes %v %v", changes, err)
	}

	// Restoring the pre-restore snapshot undoes the restore.
	back, err := e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: undo.Id, BaseRevision: now, MutationId: "restore-undo"}))
	if err != nil {
		t.Fatal(err)
	}
	undone, _ := e.state(a)
	if !sameState(undone, damaged) || back.Msg.UndoSnapshot == nil {
		t.Fatalf("undo gave %v, want %v", undone, damaged)
	}
	if back.Msg.UndoSnapshot.Reason != "before_restore" || back.Msg.UndoSnapshot.Revision != now {
		t.Fatalf("redo snapshot %v", back.Msg.UndoSnapshot)
	}
	// Snapshots are immutable: the original still holds its records.
	got, err := e.snap.GetSnapshot(ctx, bearer(a, &tripmapv1.GetSnapshotRequest{Id: snap.Snapshot.Id}))
	if err != nil || len(got.Msg.Data.Visits) != 2 {
		t.Fatalf("original snapshot changed: %v %v", got, err)
	}
}

func TestRestoreIsConditionalIdempotentAndAtomic(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("restore_cond")
	rev := e.sync(a, "c1", 0, putCity("330100", "杭州市"))
	snap := e.snapshot(a, "manual")
	rev = e.sync(a, "c2", rev, putCity("330200", "宁波市"))
	before, _ := e.state(a)

	// A stale preview must not restore, and must not leave a snapshot behind.
	_, err := e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Snapshot.Id, BaseRevision: rev - 1, MutationId: "stale"}))
	wantCode(t, err, connect.CodeAborted)
	if after, r := e.state(a); !sameState(before, after) || r != rev {
		t.Fatalf("stale restore changed data: %v rev %d", after, r)
	}
	if n := e.store.DB.Snapshot.Query().CountX(ctx); n != 1 {
		t.Fatalf("stale restore left %d snapshots", n)
	}

	for _, bad := range []*tripmapv1.RestoreSnapshotRequest{
		{SnapshotId: "", BaseRevision: rev, MutationId: "x"},
		{SnapshotId: snap.Snapshot.Id, BaseRevision: rev, MutationId: ""},
		{SnapshotId: snap.Snapshot.Id, BaseRevision: rev, MutationId: "bad id!"},
		{SnapshotId: snap.Snapshot.Id, BaseRevision: -1, MutationId: "x"},
	} {
		_, err = e.snap.RestoreSnapshot(ctx, bearer(a, bad))
		wantCode(t, err, connect.CodeInvalidArgument)
	}
	_, err = e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: "missing", BaseRevision: rev, MutationId: "nf"}))
	wantCode(t, err, connect.CodeNotFound)

	req := &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Snapshot.Id, BaseRevision: rev, MutationId: "restore-once"}
	first, err := e.snap.RestoreSnapshot(ctx, bearer(a, req))
	if err != nil || first.Msg.ChangedCount != 1 {
		t.Fatalf("restore %v %v", first, err)
	}
	// A lost response is retried with the same ID: answered, not applied again.
	again, err := e.snap.RestoreSnapshot(ctx, bearer(a, req))
	if err != nil || !again.Msg.Replayed || again.Msg.Revision != first.Msg.Revision || again.Msg.UndoSnapshot.GetId() != first.Msg.UndoSnapshot.Id {
		t.Fatalf("replay %v %v", again, err)
	}
	if _, r := e.state(a); r != first.Msg.Revision {
		t.Fatalf("replay wrote again, revision %d", r)
	}
	// The same ID for a different request is refused.
	other := &tripmapv1.RestoreSnapshotRequest{SnapshotId: first.Msg.UndoSnapshot.Id, BaseRevision: first.Msg.Revision, MutationId: "restore-once"}
	_, err = e.snap.RestoreSnapshot(ctx, bearer(a, other))
	wantCode(t, err, connect.CodeAlreadyExists)

	// Restoring what is already current changes nothing.
	cur, r := e.state(a)
	noop, err := e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: snap.Snapshot.Id, BaseRevision: r, MutationId: "noop"}))
	if err != nil || noop.Msg.ChangedCount != 0 || noop.Msg.Revision != r {
		t.Fatalf("noop restore %v %v", noop, err)
	}
	if after, _ := e.state(a); !sameState(cur, after) {
		t.Fatal("noop restore changed data")
	}
}

func TestRestoreRefusesSnapshotsTheRegionIndexNoLongerAccepts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("restore_index")
	rev := e.sync(a, "i1", 0, putCity("330100", "杭州市"))
	u := e.store.DB.User.Query().Where(user.UsernameEQ("restore_index")).OnlyX(ctx)
	data, err := proto.Marshal(&tripmapv1.SnapshotData{Visits: []*tripmapv1.Visit{{Code: "999999", Name: "已撤销区划", Province: "330000", Color: "#4b937d"}}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(data)
	_ = zw.Close()
	old := e.store.DB.Snapshot.Create().SetUserID(u.ID).SetReason(entsnap.ReasonManual).SetRevision(0).SetVisitCount(1).SetMarkCount(0).SetPayload(buf.Bytes()).SaveX(ctx)

	_, err = e.snap.RestoreSnapshot(ctx, bearer(a, &tripmapv1.RestoreSnapshotRequest{SnapshotId: old.ID, BaseRevision: rev, MutationId: "bad-index"}))
	wantCode(t, err, connect.CodeFailedPrecondition)
	if st, r := e.state(a); len(st) != 1 || r != rev {
		t.Fatalf("refused restore changed data: %v rev %d", st, r)
	}
	if n := e.store.DB.Snapshot.Query().Where(entsnap.ReasonEQ(entsnap.ReasonBeforeRestore)).CountX(ctx); n != 0 {
		t.Fatalf("refused restore left %d pre-restore snapshots", n)
	}
}

func TestRetentionKeepsNewestAndExpiresOldOnes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("retain_a")
	e.sync(a, "t1", 0, putCity("330100", "杭州市"))
	u := e.store.DB.User.Query().Where(user.UsernameEQ("retain_a")).OnlyX(ctx)
	now := time.Now().UTC()
	seed := func(age time.Duration, revision int64) {
		e.store.DB.Snapshot.Create().SetUserID(u.ID).SetReason(entsnap.ReasonScheduled).SetRevision(revision).SetVisitCount(0).SetMarkCount(0).
			SetPayload([]byte("x")).SetCreatedAt(now.Add(-age)).ExecX(ctx)
	}
	// 12 snapshots, all older than the 30-day limit: only the newest 10 stay.
	for i := 0; i < 12; i++ {
		seed(time.Duration(40+i)*24*time.Hour, 0)
	}
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now); err != nil {
		t.Fatal(err)
	}
	var old int
	for _, s := range e.store.DB.Snapshot.Query().AllX(ctx) {
		if s.Reason == entsnap.ReasonScheduled && s.Revision == 0 {
			old++
		}
	}
	// The same run also took a scheduled snapshot for the changed account, which
	// is now the newest; with it, 9 of the old ones fit in the newest 10.
	if old != 9 {
		t.Fatalf("kept %d old snapshots, want 9", old)
	}
	if total := e.store.DB.Snapshot.Query().CountX(ctx); total != 10 {
		t.Fatalf("total %d, want 10", total)
	}

	// Recent snapshots are capped at 50 when a new one is stored.
	for i := 0; i < 55; i++ {
		seed(time.Duration(i+1)*time.Minute, 0)
	}
	rev := e.sync(a, "t2", 1, putCity("330200", "宁波市"))
	res := e.snapshot(a, "manual")
	if !res.Created || res.Snapshot.Revision != rev {
		t.Fatalf("capture %v", res)
	}
	if total := e.store.DB.Snapshot.Query().CountX(ctx); total != 50 {
		t.Fatalf("total %d, want cap of 50", total)
	}
	list, err := e.snap.ListSnapshots(ctx, bearer(a, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil || list.Msg.Snapshots[0].Id != res.Snapshot.Id {
		t.Fatalf("newest snapshot was pruned: %v %v", list, err)
	}
}

func TestScheduledSnapshotsOnlyWhenChangedAndDue(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("sched_a")
	e.register("sched_idle")
	now := time.Now().UTC()
	count := func() int { return e.store.DB.Snapshot.Query().CountX(ctx) }

	// An account without footprints never gets a snapshot.
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now); err != nil || count() != 0 {
		t.Fatalf("empty accounts snapshotted: %d %v", count(), err)
	}
	rev := e.sync(a, "m1", 0, putCity("330100", "杭州市"))
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now); err != nil || count() != 1 {
		t.Fatalf("first run: %d %v", count(), err)
	}
	first := e.store.DB.Snapshot.Query().OnlyX(ctx)
	if first.Reason != entsnap.ReasonScheduled || first.Revision != rev || first.VisitCount != 1 {
		t.Fatalf("scheduled snapshot %+v", first)
	}
	// Running again, even on several instances, does not duplicate it.
	for i := 0; i < 3; i++ {
		if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now); err != nil || count() != 1 {
			t.Fatalf("repeat run %d: %d %v", i, count(), err)
		}
	}
	// A change within the interval waits for the next due time.
	rev = e.sync(a, "m2", rev, putCity("330200", "宁波市"))
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now.Add(time.Hour)); err != nil || count() != 1 {
		t.Fatalf("snapshotted before the interval: %d %v", count(), err)
	}
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now.Add(25*time.Hour)); err != nil || count() != 2 {
		t.Fatalf("due run: %d %v", count(), err)
	}
	// Unchanged since, so a later run adds nothing.
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now.Add(60*time.Hour)); err != nil || count() != 2 {
		t.Fatalf("unchanged account snapshotted: %d %v", count(), err)
	}
	if newest := e.store.DB.Snapshot.Query().Order(ent.Desc(entsnap.FieldCreatedAt)).FirstX(ctx); newest.Revision != rev || newest.VisitCount != 2 {
		t.Fatalf("newest scheduled snapshot %+v, want revision %d", newest, rev)
	}
}

func TestAccountDeletionRemovesSnapshots(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.register("snapdel_a"), e.register("snapdel_b")
	e.sync(a, "d1", 0, putCity("330100", "杭州市"))
	e.snapshot(a, "manual")
	e.sync(b, "d2", 0, putCity("330200", "宁波市"))
	e.snapshot(b, "manual")
	e.mustCleanupAccount(ctx, a)
	if n := e.store.DB.Snapshot.Query().CountX(ctx); n != 1 {
		t.Fatalf("%d snapshots remain, want only the other account's", n)
	}
	list, err := e.snap.ListSnapshots(ctx, bearer(b, &tripmapv1.ListSnapshotsRequest{}))
	if err != nil || len(list.Msg.Snapshots) != 1 {
		t.Fatalf("other account affected: %v %v", list, err)
	}
}

func TestMaintenanceExpiresOldSnapshotsOfIdleAccounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.register("idle_retain")
	rev := e.sync(a, "i1", 0, putCity("330100", "杭州市"))
	u := e.store.DB.User.Query().Where(user.UsernameEQ("idle_retain")).OnlyX(ctx)
	now := time.Now().UTC()
	// The account is unchanged since its newest snapshot, so no new one is due.
	for i := 0; i < 13; i++ {
		e.store.DB.Snapshot.Create().SetUserID(u.ID).SetReason(entsnap.ReasonManual).SetRevision(rev).SetVisitCount(1).SetMarkCount(0).
			SetPayload([]byte("x")).SetCreatedAt(now.Add(-time.Duration(31+i) * 24 * time.Hour)).ExecX(ctx)
	}
	if err := application.RunSnapshotMaintenance(ctx, e.store.DB, now); err != nil {
		t.Fatal(err)
	}
	if n := e.store.DB.Snapshot.Query().CountX(ctx); n != 10 {
		t.Fatalf("%d snapshots, want the newest 10", n)
	}
	// The ten kept are the newest ones.
	oldest := e.store.DB.Snapshot.Query().Order(ent.Asc(entsnap.FieldCreatedAt)).FirstX(ctx)
	if oldest.CreatedAt.Before(now.Add(-41 * 24 * time.Hour)) {
		t.Fatalf("kept an older snapshot than the newest 10: %v", oldest.CreatedAt)
	}
}
