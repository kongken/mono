package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/internal/tripmap/ent/cityvisit"
)

func TestChainRegionUpgradeIslandMigrationAndIsolation(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()

	alice := h.FreshBrowser()
	aliceID, err := alice.Register(ctx, "alice_region", "password1", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	bob := h.FreshBrowser()
	bobID, err := bob.Register(ctx, "bob_region", "password1", "Bob")
	if err != nil {
		t.Fatal(err)
	}

	seedIsland := func(userID string) {
		t.Helper()
		if err := h.TripmapDB.CityVisit.Create().
			SetUserID(userID).SetRegionCode("tw-09020").SetRegionName("金门县").
			SetProvinceCode("710000").SetColor("#4b937d").SetTravelDate("2026-10-14").SetNote("原旅行记忆 🏔️").
			Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	seedIsland(aliceID)
	seedIsland(bobID)

	idx, err := alice.GetRegionIndex(ctx)
	if err != nil || idx.GetVersion() == "" || idx.GetNotModified() {
		t.Fatalf("index: %v %+v", err, idx)
	}
	preview, err := alice.PreviewRegionUpgrade(ctx)
	if err != nil || len(preview.GetChanges()) != 1 || len(preview.GetBlockers()) != 0 {
		t.Fatalf("preview alice: %v %+v", err, preview)
	}
	bobPreview, err := bob.PreviewRegionUpgrade(ctx)
	if err != nil || len(bobPreview.GetChanges()) != 1 {
		t.Fatalf("preview bob: %v %+v", err, bobPreview)
	}

	req := connect.NewRequest(&bffv1.ApplyRegionUpgradeRequest{
		Version: preview.GetVersion(), BaseRevision: preview.GetRevision(), MutationId: "chain-island",
	})
	alice.withTripmapHeaders(req)
	if _, err = alice.RegionSvc.ApplyRegionUpgrade(ctx, req); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("apply without csrf: %v", err)
	}

	applied, err := alice.ApplyRegionUpgrade(ctx, preview.GetVersion(), preview.GetRevision(), "chain-island")
	if err != nil || applied.GetChangedCount() != 1 || applied.GetSnapshotId() == "" || applied.GetRevision() != 1 {
		t.Fatalf("apply alice: %v %+v", err, applied)
	}
	replay, err := alice.ApplyRegionUpgrade(ctx, preview.GetVersion(), preview.GetRevision(), "chain-island")
	if err != nil || !replay.GetReplayed() || replay.GetSnapshotId() != applied.GetSnapshotId() {
		t.Fatalf("replay: %v %+v", err, replay)
	}

	rows, err := h.TripmapDB.CityVisit.Query().Where(cityvisit.UserID(aliceID)).All(ctx)
	if err != nil || len(rows) != 1 || rows[0].ProvinceCode != "350000" || rows[0].Note != "原旅行记忆 🏔️" {
		t.Fatalf("alice migrated: %v %+v", err, rows)
	}
	bobRows, err := h.TripmapDB.CityVisit.Query().Where(cityvisit.UserID(bobID)).All(ctx)
	if err != nil || len(bobRows) != 1 || bobRows[0].ProvinceCode != "710000" {
		t.Fatalf("bob unchanged: %v %+v", err, bobRows)
	}
}

func TestChainRegionUpgradeBlockers(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	b := h.FreshBrowser()
	uid, err := b.Register(ctx, "blocked_region", "password1", "Blocked")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.TripmapDB.CityVisit.Create().
		SetUserID(uid).SetRegionCode("unmapped-region").SetRegionName("未审核区域").
		SetProvinceCode("330000").SetColor("#4b937d").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := b.PreviewRegionUpgrade(ctx)
	if err != nil || len(preview.GetBlockers()) == 0 {
		t.Fatalf("expected blockers: %v %+v", err, preview)
	}
	_, err = b.ApplyRegionUpgrade(ctx, preview.GetVersion(), preview.GetRevision(), "blocked")
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("blocked apply: %v", err)
	}
	count, err := h.TripmapDB.CityVisit.Query().Where(cityvisit.UserID(uid)).Count(ctx)
	if err != nil || count != 1 {
		t.Fatalf("data unchanged: %v %d", err, count)
	}
}
