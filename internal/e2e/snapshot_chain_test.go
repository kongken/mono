package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
)

func TestChainSnapshotCreateRestoreAndIsolation(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	if _, err := a.Register(ctx, "snap_a", "password1", "A"); err != nil {
		t.Fatal(err)
	}
	csrf := a.refreshCSRF(ctx)
	syncReq := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "snap-seed", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	a.writeHeaders(syncReq, csrf, a.Origin)
	syncRes, err := a.Foot.SyncChanges(ctx, syncReq)
	if err != nil {
		t.Fatal(err)
	}
	rev := syncRes.Msg.GetRevision()

	createReq := connect.NewRequest(&bfftripmapv1.CreateSnapshotRequest{Reason: "manual"})
	a.writeHeaders(createReq, csrf, a.Origin)
	created, err := a.Snap.CreateSnapshot(ctx, createReq)
	if err != nil || !created.Msg.GetCreated() || created.Msg.GetSnapshot().GetRevision() != rev {
		t.Fatalf("create snapshot: %+v err=%v", created.Msg, err)
	}
	snapID := created.Msg.GetSnapshot().GetId()

	dupReq := connect.NewRequest(&bfftripmapv1.CreateSnapshotRequest{Reason: "before_migration"})
	a.writeHeaders(dupReq, csrf, a.Origin)
	dup, err := a.Snap.CreateSnapshot(ctx, dupReq)
	if err != nil || dup.Msg.GetCreated() || dup.Msg.GetSnapshot().GetId() != snapID {
		t.Fatalf("dedup snapshot: %+v err=%v", dup.Msg, err)
	}

	delReq := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "snap-del", BaseRevision: rev,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_DeleteVisit{DeleteVisit: "330100"},
		}},
	})
	a.writeHeaders(delReq, csrf, a.Origin)
	delRes, err := a.Foot.SyncChanges(ctx, delReq)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := a.GetFootprints(ctx)
	if err != nil || len(fp.GetVisits()) != 0 {
		t.Fatalf("city deleted before restore: %+v err=%v", fp, err)
	}

	restoreReq := connect.NewRequest(&bfftripmapv1.RestoreSnapshotRequest{
		SnapshotId: snapID, BaseRevision: delRes.Msg.GetRevision(), MutationId: "restore-1",
	})
	a.writeHeaders(restoreReq, csrf, a.Origin)
	restored, err := a.Snap.RestoreSnapshot(ctx, restoreReq)
	if err != nil || restored.Msg.GetChangedCount() < 1 || restored.Msg.GetUndoSnapshot().GetReason() != "before_restore" {
		t.Fatalf("restore: %+v err=%v", restored.Msg, err)
	}
	fp, err = a.GetFootprints(ctx)
	if err != nil || len(fp.GetVisits()) != 1 || fp.GetVisits()[0].GetCode() != "330100" {
		t.Fatalf("after restore: %+v err=%v", fp, err)
	}

	replayReq := connect.NewRequest(&bfftripmapv1.RestoreSnapshotRequest{
		SnapshotId: snapID, BaseRevision: delRes.Msg.GetRevision(), MutationId: "restore-1",
	})
	a.writeHeaders(replayReq, csrf, a.Origin)
	replay, err := a.Snap.RestoreSnapshot(ctx, replayReq)
	if err != nil || !replay.Msg.GetReplayed() {
		t.Fatalf("restore replay: %+v err=%v", replay.Msg, err)
	}

	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "snap_b", "password1", "B"); err != nil {
		t.Fatal(err)
	}
	getReq := connect.NewRequest(&bfftripmapv1.GetSnapshotRequest{Id: snapID})
	b.withTripmapHeaders(getReq)
	_, err = b.Snap.GetSnapshot(ctx, getReq)
	if err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("user B must not read A snapshot, got %v", err)
	}
}
