package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
)

func TestChainFootprintSyncAndIsolation(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	a := h.FreshBrowser()
	if _, err := a.Register(ctx, "sync_a", "password1", "A"); err != nil {
		t.Fatal(err)
	}
	csrf := a.refreshCSRF(ctx)
	syncReq := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "batch-1", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	a.writeHeaders(syncReq, csrf, a.Origin)
	res, err := a.Foot.SyncChanges(ctx, syncReq)
	if err != nil || res.Msg.GetRevision() < 1 || res.Msg.GetChangedCount() != 1 {
		t.Fatalf("sync: rev=%d count=%d err=%v", res.Msg.GetRevision(), res.Msg.GetChangedCount(), err)
	}
	replay, err := a.Foot.SyncChanges(ctx, syncReq)
	if err != nil || !replay.Msg.GetReplayed() || replay.Msg.GetRevision() != res.Msg.GetRevision() {
		t.Fatalf("replay: %+v err=%v", replay.Msg, err)
	}
	fp, err := a.GetFootprints(ctx)
	if err != nil || len(fp.GetVisits()) != 1 || fp.GetVisits()[0].GetCode() != "330100" {
		t.Fatalf("get footprints: %+v err=%v", fp, err)
	}

	b := h.FreshBrowser()
	if _, err := b.Register(ctx, "sync_b", "password1", "B"); err != nil {
		t.Fatal(err)
	}
	bFoot, err := b.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range bFoot.GetVisits() {
		if v.GetCode() == "330100" {
			t.Fatal("user B must not see user A city")
		}
	}

	deltaReq := connect.NewRequest(&bfftripmapv1.GetChangesRequest{SinceRevision: 0, Limit: 50})
	a.withTripmapHeaders(deltaReq)
	page, err := a.Foot.GetChanges(ctx, deltaReq)
	if err != nil || len(page.Msg.GetChanges()) == 0 || page.Msg.GetCurrentRevision() != res.Msg.GetRevision() {
		t.Fatalf("get changes: %+v err=%v", page.Msg, err)
	}
}

func TestChainFootprintRevisionConflict(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "conflict", "password1", "C"); err != nil {
		t.Fatal(err)
	}
	csrf := u.refreshCSRF(ctx)
	first := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "c-1", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	u.writeHeaders(first, csrf, u.Origin)
	r1, err := u.Foot.SyncChanges(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	stale := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "c-2", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330200", Name: "宁波市", Province: "330000", Color: "#4b937d"},
			},
		}},
	})
	u.writeHeaders(stale, csrf, u.Origin)
	_, err = u.Foot.SyncChanges(ctx, stale)
	if err == nil || connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("expected aborted conflict, got %v", err)
	}
	fp, err := u.GetFootprints(ctx)
	if err != nil || fp.GetRevision() != r1.Msg.GetRevision() {
		t.Fatalf("revision unchanged after conflict: %v", fp)
	}
}
