package e2e_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	bffv1 "github.com/kongken/mono/pkg/proto/bff/v1"
	"github.com/kongken/mono/pkg/proto/bff/v1/bffv1connect"
)

func assertWriteRejected(t *testing.T, err error) {
	t.Helper()
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("want permission denied, got %v", err)
	}
}

func TestChainBusinessWritesRequireCSRFAndOrigin(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "csrf_w", "password1", "W"); err != nil {
		t.Fatal(err)
	}
	csrf := u.refreshCSRF(ctx)

	syncReq := connect.NewRequest(&bffv1.SyncChangesRequest{MutationId: "m1", BaseRevision: 0})
	u.withTripmapHeaders(syncReq)
	syncReq.Header().Set("Origin", u.Origin)
	_, err := u.Foot.SyncChanges(ctx, syncReq)
	assertWriteRejected(t, err)

	createReq := connect.NewRequest(&bffv1.CreateSnapshotRequest{Reason: "manual"})
	u.writeHeaders(createReq, csrf, "https://evil.example")
	_, err = u.Snap.CreateSnapshot(ctx, createReq)
	assertWriteRejected(t, err)

	share := bffv1connect.NewShareServiceClient(u.HTTP, u.APIURL)
	saveReq := connect.NewRequest(&bffv1.SaveShareRequest{})
	u.writeHeaders(saveReq, csrf, "https://evil.example")
	_, err = share.SaveShare(ctx, saveReq)
	assertWriteRejected(t, err)

	notify := bffv1connect.NewNotificationServiceClient(u.HTTP, u.APIURL)
	upd := connect.NewRequest(&bffv1.UpdateNotificationSettingsRequest{})
	u.writeHeaders(upd, "", u.Origin)
	_, err = notify.UpdateNotificationSettings(ctx, upd)
	assertWriteRejected(t, err)

	delMedia := connect.NewRequest(&bffv1.DeleteMediaRequest{Id: "missing"})
	u.writeHeaders(delMedia, csrf, "https://evil.example")
	_, err = u.Media().DeleteMedia(ctx, delMedia)
	assertWriteRejected(t, err)

	stWrite := connect.NewRequest(&bffv1.UpdateSharedTripRequest{SharedTripId: "x", MutationId: "m"})
	u.writeHeaders(stWrite, "", u.Origin)
	_, err = u.SharedTrip.UpdateSharedTrip(ctx, stWrite)
	assertWriteRejected(t, err)

	regReq := connect.NewRequest(&bffv1.ApplyRegionUpgradeRequest{Version: u.Region, MutationId: "m"})
	u.writeHeaders(regReq, csrf, "https://evil.example")
	_, err = u.RegionSvc.ApplyRegionUpgrade(ctx, regReq)
	assertWriteRejected(t, err)

	delAcct := connect.NewRequest(&bffv1.DeleteAccountRequest{Confirmation: "DELETE", Password: "password1"})
	u.writeHeaders(delAcct, csrf, "https://evil.example")
	_, err = u.Auth.DeleteAccount(ctx, delAcct)
	assertWriteRejected(t, err)

	rev := connect.NewRequest(&bffv1.RevokeSessionRequest{Id: "other"})
	u.writeHeaders(rev, csrf, "https://evil.example")
	_, err = u.Auth.RevokeSession(ctx, rev)
	assertWriteRejected(t, err)
}

func TestChainReadsWorkWithoutCSRF(t *testing.T) {
	h := NewChainHarness(t)
	ctx := context.Background()
	u := h.FreshBrowser()
	if _, err := u.Register(ctx, "csrf_r", "password1", "R"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Me(ctx); err != nil {
		t.Fatalf("Me without csrf: %v", err)
	}
	if _, err := u.GetFootprints(ctx); err != nil {
		t.Fatalf("GetFootprints without csrf: %v", err)
	}
}
