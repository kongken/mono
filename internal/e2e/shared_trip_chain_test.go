package e2e_test

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	bfftripmapv1 "github.com/kongken/mono/pkg/proto/bff/tripmap/v1"
)

func TestChainSharedTripViewerWriteDeniedAndSharedMedia(t *testing.T) {
	blobs := newChainMediaBlobs()
	h := NewChainHarness(t, blobs)
	ctx := context.Background()

	owner := h.FreshBrowser()
	ownerID, err := owner.Register(ctx, "st_owner", "password1", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	editor := h.FreshBrowser()
	editorID, err := editor.Register(ctx, "st_editor", "password1", "Editor")
	if err != nil {
		t.Fatal(err)
	}
	viewer := h.FreshBrowser()
	viewerID, err := viewer.Register(ctx, "st_viewer", "password1", "Viewer")
	if err != nil {
		t.Fatal(err)
	}

	becomeFriends(t, ctx, owner, editor, "st_editor")
	becomeFriends(t, ctx, owner, viewer, "st_viewer")

	csrf := owner.refreshCSRF(ctx)
	syncReq := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "owner-init", BaseRevision: 0,
		Mutations: []*bfftripmapv1.FootprintMutation{
			{Operation: &bfftripmapv1.FootprintMutation_UpsertCity{
				UpsertCity: &bfftripmapv1.Visit{Code: "330100", Name: "杭州市", Province: "330000", Color: "#4b937d"},
			}},
			{Operation: &bfftripmapv1.FootprintMutation_UpsertTrip{
				UpsertTrip: &bfftripmapv1.Trip{City: "330100", Id: "t-shared", Date: "2026-08-01", Note: "共游"},
			}},
		},
	})
	owner.writeHeaders(syncReq, csrf, owner.Origin)
	if _, err = owner.Foot.SyncChanges(ctx, syncReq); err != nil {
		t.Fatal(err)
	}

	inv, err := owner.InviteSharedTrip(ctx, "330100", "t-shared", editorID, "editor")
	if err != nil || inv.GetId() == "" {
		t.Fatalf("invite editor: %+v err=%v", inv, err)
	}
	stID := inv.GetId()
	if _, err = owner.InviteSharedTrip(ctx, "330100", "t-shared", viewerID, "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err = editor.RespondSharedTripInvite(ctx, stID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = viewer.RespondSharedTripInvite(ctx, stID, true); err != nil {
		t.Fatal(err)
	}

	_, err = viewer.UpdateSharedTrip(ctx, stID, "2026-08-02", "viewer edit", "v1")
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("viewer update shared trip: %v", err)
	}

	fpEditor, err := editor.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	csrfE := editor.refreshCSRF(ctx)
	syncEditor := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "editor-trip-write", BaseRevision: fpEditor.GetRevision(),
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertTrip{
				UpsertTrip: &bfftripmapv1.Trip{City: "330100", Id: "t-shared", Date: "2026-08-02", Note: "editor via sync"},
			},
		}},
	})
	editor.writeHeaders(syncEditor, csrfE, editor.Origin)
	if _, err = editor.Foot.SyncChanges(ctx, syncEditor); err != nil {
		t.Fatalf("editor sync upsert_trip: %v", err)
	}

	fpViewer, err := viewer.GetFootprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	csrfV := viewer.refreshCSRF(ctx)
	syncViewer := connect.NewRequest(&bfftripmapv1.SyncChangesRequest{
		MutationId: "viewer-trip-write", BaseRevision: fpViewer.GetRevision(),
		Mutations: []*bfftripmapv1.FootprintMutation{{
			Operation: &bfftripmapv1.FootprintMutation_UpsertTrip{
				UpsertTrip: &bfftripmapv1.Trip{City: "330100", Id: "t-shared", Date: "2026-08-03", Note: "blocked"},
			},
		}},
	})
	viewer.writeHeaders(syncViewer, csrfV, viewer.Origin)
	if _, err = viewer.Foot.SyncChanges(ctx, syncViewer); err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("viewer sync upsert_trip: %v", err)
	}

	ownerMedia := uploadChainTripPhoto(t, ctx, h, blobs, owner, "330100", "t-shared")
	list, err := viewer.ListSharedTripMedia(ctx, stID)
	if err != nil || len(list.GetMedia()) != 1 {
		t.Fatalf("viewer list shared media: %+v err=%v", list, err)
	}
	if _, err = viewer.ReadSharedTripMedia(ctx, stID, ownerMedia, true); err != nil {
		t.Fatalf("viewer read shared media: %v", err)
	}

	csrfO := owner.refreshCSRF(ctx)
	rmFriend := connect.NewRequest(&bfftripmapv1.RemoveFriendRequest{FriendshipId: findFriendship(t, ctx, owner, editorID)})
	owner.writeHeaders(rmFriend, csrfO, owner.Origin)
	if _, err = owner.Friends.RemoveFriend(ctx, rmFriend); err != nil {
		t.Fatal(err)
	}
	_, err = editor.ListSharedTripMedia(ctx, stID)
	if err == nil || connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("editor after unfriend list: %v", err)
	}
	_ = ownerID
}

// becomeFriends has a send b a friend request with b's invite code (friends are
// never found by name) and b accept it.
func becomeFriends(t *testing.T, ctx context.Context, a, b *Browser, _ string) {
	t.Helper()
	ov, err := b.GetCollaborationOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	csrf := a.refreshCSRF(ctx)
	send := connect.NewRequest(&bfftripmapv1.SendFriendRequestRequest{Target: ov.GetMyFriendCode()})
	a.writeHeaders(send, csrf, a.Origin)
	sendRes, err := a.Friends.SendFriendRequest(ctx, send)
	if err != nil {
		t.Fatal(err)
	}
	csrfB := b.refreshCSRF(ctx)
	acc := connect.NewRequest(&bfftripmapv1.RespondFriendRequestRequest{
		FriendshipId: sendRes.Msg.GetFriendship().GetId(), Accept: true,
	})
	b.writeHeaders(acc, csrfB, b.Origin)
	if _, err = b.Friends.RespondFriendRequest(ctx, acc); err != nil {
		t.Fatal(err)
	}
}

func findFriendship(t *testing.T, ctx context.Context, b *Browser, friendUserID string) string {
	t.Helper()
	ov, err := b.GetCollaborationOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range ov.GetFriends() {
		if f.GetFriend().GetId() == friendUserID && f.GetStatus() == "accepted" {
			return f.GetId()
		}
	}
	t.Fatalf("friendship not found for %s", friendUserID)
	return ""
}

func uploadChainTripPhoto(t *testing.T, ctx context.Context, h *ChainHarness, blobs *chainMediaBlobs, b *Browser, region, tripID string) string {
	t.Helper()
	data := photoPNG(t)
	uploadID := uuid.NewString()
	prep, err := b.PrepareUpload(ctx, &bfftripmapv1.PrepareUploadRequest{
		RegionCode: region, TripId: tripID, Filename: "shot.png", ContentType: "image/png",
		SizeBytes: int64(len(data)), UploadId: uploadID,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := prep.GetMedia().GetId()
	m := h.TripmapDB.MediaObject.GetX(ctx, id)
	if err = blobs.Put(ctx, m.UploadKey, bytes.NewReader(data), int64(len(data)), "image/png"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.CompleteUpload(ctx, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (b *Browser) InviteSharedTrip(ctx context.Context, region, tripID, friendUserID, role string) (*bfftripmapv1.SharedTripInfo, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.InviteToSharedTripRequest{
		RegionCode: region, TripId: tripID, FriendUserId: friendUserID, Role: role,
	})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.SharedTrip.InviteToSharedTrip(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetSharedTrip(), nil
}

func (b *Browser) RespondSharedTripInvite(ctx context.Context, sharedTripID string, accept bool) (*bfftripmapv1.RespondSharedTripInviteResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.RespondSharedTripInviteRequest{SharedTripId: sharedTripID, Accept: accept})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.SharedTrip.RespondSharedTripInvite(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) UpdateSharedTrip(ctx context.Context, sharedTripID, date, note, mutationID string) (*bfftripmapv1.UpdateSharedTripResponse, error) {
	csrf := b.refreshCSRF(ctx)
	req := connect.NewRequest(&bfftripmapv1.UpdateSharedTripRequest{
		SharedTripId: sharedTripID, Date: date, Note: note, MutationId: mutationID,
	})
	b.writeHeaders(req, csrf, b.Origin)
	res, err := b.SharedTrip.UpdateSharedTrip(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ListSharedTripMedia(ctx context.Context, sharedTripID string) (*bfftripmapv1.ListSharedTripMediaResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.ListSharedTripMediaRequest{SharedTripId: sharedTripID})
	b.withTripmapHeaders(req)
	res, err := b.SharedTrip.ListSharedTripMedia(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}

func (b *Browser) ReadSharedTripMedia(ctx context.Context, sharedTripID, mediaID string, thumb bool) (*bfftripmapv1.ReadSharedTripMediaResponse, error) {
	req := connect.NewRequest(&bfftripmapv1.ReadSharedTripMediaRequest{
		SharedTripId: sharedTripID, MediaId: mediaID, Thumbnail: thumb,
	})
	b.withTripmapHeaders(req)
	res, err := b.SharedTrip.ReadSharedTripMedia(ctx, req)
	if err != nil {
		return nil, err
	}
	return res.Msg, nil
}
